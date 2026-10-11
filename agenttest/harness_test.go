package agenttest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	runstoreinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type harnessPlanner struct {
	id      string
	specs   []tools.ToolSpec
	await   *planner.Await
	block   bool
	entered chan<- struct{}
}

type recordingTB struct {
	testing.TB
	ctx      context.Context
	cleanups []func()
	errors   []string
}

func TestHarnessActivityRun(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":"hello"}`))), agenttest.Respond(agenttest.Text("done"))))
	result, err := h.Run(t.Context(), "root", agenttest.User("hello"), runtime.WithLabels(map[string]string{"test": "yes"}))
	require.NoError(t, err)
	assert.NoError(t, result.RunErr)
	require.NotNil(t, result.Output.Final)
	assert.Equal(t, "done", result.Output.Final.Parts[0].(model.TextPart).Text)
	assert.True(t, result.Record.Runs[0].Sealed)
	assert.Equal(t, []trajectory.StepKind{trajectory.StepModel, trajectory.StepTool, trajectory.StepModel}, recordedKinds(result.Trajectory))
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed(), expect.AllAttemptsSuccessful(tools.Ident("echo.echo")))
	assert.Equal(t, h.Script("model").Client(), h.Model("model"))
	assert.Equal(t, "agenttest-r1", result.RunID)
	assert.Equal(t, "agenttest-s1", result.SessionID)
}

func TestHarnessFailedRun(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", agenttest.Fail(errors.New("model broke"))))
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.ErrorContains(t, result.RunErr, "model broke")
	assert.NotNil(t, result.Trajectory)
}

func TestHarnessOwnedOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option runtime.RuntimeOption
	}{
		{"Engine", runtime.WithEngine(engineinmem.New())},
		{"RunStore", runtime.WithRunStore(runstoreinmem.New())},
		{"RunEventStore", runtime.WithRunEventStore(runloginmem.New())},
		{"SessionStore", runtime.WithSessionStore(sessioninmem.New())},
		{"Hooks", runtime.WithHooks(hooks.NewBus())},
		{"Tracer", runtime.WithTracer(telemetry.NewNoopTracer())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := agenttest.NewHarness(t.Context(), func(context.Context, *runtime.Runtime) error {
				return nil
			}, agenttest.WithRuntimeOptions(tc.option))
			assert.Nil(t, h)
			require.ErrorContains(t, err, tc.name)
		})
	}
}

func TestHarnessUnverified(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))),
		agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
			return nil, nil
		}})))
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.False(t, result.Trajectory.StatusVerified)
	for _, step := range result.Trajectory.Steps {
		if step.Model != nil {
			assert.True(t, step.Verified)
		}
	}
}

func TestHarnessCleanupRecordingTB(t *testing.T) {
	tb := &recordingTB{TB: t, ctx: t.Context()}
	agenttest.New(tb, func(context.Context, *runtime.Runtime) error {
		return nil
	}, agenttest.ScriptedModel("unused-model", agenttest.Respond(agenttest.Text("unused"))))
	for _, cleanup := range tb.cleanups {
		cleanup()
	}
	require.Len(t, tb.errors, 1)
	assert.Contains(t, tb.errors[0], "unused-model")
	assert.Contains(t, tb.errors[0], "unconsumed turn 1")
}

func TestHarnessTimeout(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{block: true}), agenttest.WithRunTimeout(20*time.Millisecond),
		agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
			return nil, nil
		}})))
	require.NoError(t, err)
	result, err := h.Run(t.Context(), "root", nil)
	assert.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "BeforeEvent")
	require.NoError(t, h.Close())
}

func TestHarnessConcurrentRuns(t *testing.T) {
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		if err := registerHarnessAgent("one", harnessPlanner{id: "one"})(ctx, rt); err != nil {
			return err
		}
		return registerHarnessAgent("two", harnessPlanner{id: "two"})(ctx, rt)
	}
	h := agenttest.New(t, register, agenttest.ScriptedModel("one", agenttest.Respond(agenttest.Text("one"))), agenttest.ScriptedModel("two", agenttest.Respond(agenttest.Text("two"))))
	var wg sync.WaitGroup
	for _, id := range []agent.Ident{"one", "two"} {
		wg.Go(func() {
			result, err := h.Run(t.Context(), id, agenttest.User(string(id)))
			if assert.NoError(t, err) {
				assert.Equal(t, string(id), result.Output.Final.Parts[0].(model.TextPart).Text)
				assert.Equal(t, id, result.Trajectory.AgentID)
			}
		})
	}
	wg.Wait()
}

func TestHarnessRunIDReserved(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{block: true}))
	for _, id := range []string{"custom", ""} {
		_, err := h.Run(t.Context(), "root", nil, runtime.WithRunID(id))
		require.ErrorContains(t, err, "RunID")
	}
}

func TestHarnessConstructionFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		register agenttest.Register
		options  []agenttest.Option
	}{
		{name: "registration", register: func(context.Context, *runtime.Runtime) error {
			return errors.New("registration failed")
		}},
		{name: "builder", register: func(context.Context, *runtime.Runtime) error {
			return nil
		}, options: []agenttest.Option{agenttest.ScriptedModel("bad", agenttest.Respond(agenttest.Part{}))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := agenttest.NewHarness(t.Context(), tc.register, tc.options...)
			assert.Nil(t, h)
			require.Error(t, err)
		})
	}
}

func echoSpec() tools.ToolSpec {
	return tools.ToolSpec{Name: "echo.echo", Toolset: "echo", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}}
}

func registerHarnessAgent(id agent.Ident, p harnessPlanner) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if len(p.specs) > 0 {
			if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: p.specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
				return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"value": "echoed"}}), nil
			}}); err != nil {
				return err
			}
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: id, Planner: p, Specs: p.specs,
			Workflow:         engine.WorkflowDefinition{Name: string(id) + ".workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
			PlanActivityName: string(id) + ".plan", ResumeActivityName: string(id) + ".resume", ExecuteToolActivity: string(id) + ".tool"})
	}
}

func recordedKinds(tr *trajectory.Trajectory) []trajectory.StepKind {
	var kinds []trajectory.StepKind
	for _, step := range tr.Steps {
		if step.Model != nil || step.Tool != nil {
			kinds = append(kinds, step.Kind)
		}
	}
	return kinds
}

func (p harnessPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if p.block {
		if p.entered != nil {
			p.entered <- struct{}{}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.await != nil {
		return &planner.PlanResult{Await: p.await}, nil
	}
	return p.complete(ctx, in.Agent, in.Messages)
}
func (p harnessPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}
func (p harnessPlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient(p.id)
	if !ok {
		return nil, fmt.Errorf("model %s missing", p.id)
	}
	definitions := make([]*model.ToolDefinition, len(p.specs))
	for i, spec := range p.specs {
		definitions[i] = &model.ToolDefinition{Name: spec.Name.String(), InputSchema: map[string]any{"type": "object"}}
	}
	response, err := client.Complete(ctx, &model.Request{Model: "scripted", Messages: messages, Tools: definitions})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}

func (t *recordingTB) Context() context.Context {
	return t.ctx
}
func (t *recordingTB) Cleanup(fn func()) {
	t.cleanups = append(t.cleanups, fn)
}
func (t *recordingTB) Error(args ...any) {
	t.errors = append(t.errors, fmt.Sprint(args...))
}
