package agenttest_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubParallelMatchers(t *testing.T) {
	tool := echoTool()
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(
			agenttest.RawToolCall(tool.Name, rawjson.Message(`{"value":"two"}`)),
			agenttest.RawToolCall(tool.Name, rawjson.Message(`{"value":"one"}`))),
			agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(tool, "first"))))
	stub := agenttest.Stub(h, tool)
	// Both Handle callbacks must start before either completes, proving parallel dispatch.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-entered
		<-entered
		close(release)
	})
	for i, value := range []string{"one", "two"} {
		response := []string{"first", "second"}[i]
		stub.Handle(func(ctx context.Context, _ any) (any, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return response, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}).When(agenttest.ArgsEqual[any, any](tool, map[string]any{"value": value}))
	}
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	wg.Wait()
	calls := stub.Calls()
	require.Len(t, calls, 2)
	for _, call := range calls {
		value := call.Payload.(map[string]any)["value"].(string)
		assert.Equal(t, map[string]string{"one": "first", "two": "second"}[value], call.Result)
		assert.Equal(t, map[string]int{"one": 1, "two": 2}[value], call.Response)
		require.NoError(t, call.Err)
	}
	// Snapshots own their raw bytes and slice slots.
	calls[0].Raw[0] = 'x'
	assert.NotEqual(t, calls[0].Raw, stub.Calls()[0].Raw)
	global := h.ToolCalls()
	global[0].Raw[0] = 'x'
	assert.NotEqual(t, global[0].Raw, h.ToolCalls()[0].Raw)
}

func TestStubUnmatchedDiagnostics(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":"other"}`))), agenttest.Respond(agenttest.Text("done"))))
	require.NoError(t, err)
	stub := agenttest.Stub(h, echoTool())
	stub.Return("one").When(agenttest.ArgsWhere("must be one", func(p any) bool {
		return p.(map[string]any)["value"] == "one"
	}), func(any) error {
		return errors.New("second matcher should not run")
	})
	stub.Return("two").When(agenttest.ArgsEqual[any, any](echoTool(), map[string]any{"value": "two"}))
	result, err := h.Run(t.Context(), "root", nil)
	var failure *agenttest.StubError
	require.ErrorAs(t, err, &failure)
	require.NotNil(t, result)
	require.NoError(t, result.RunErr)
	assert.Contains(t, failure.Reason, "response 1: must be one")
	assert.Contains(t, failure.Reason, "response 2: ArgsEqual")
	assert.NotContains(t, failure.Reason, "second matcher should not run")
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Failed(echoTool(), nil, nil))
	require.Len(t, stub.Calls(), 1)
	require.ErrorAs(t, stub.Calls()[0].Err, &failure)
	assert.ErrorContains(t, h.Close(), failure.Error())
}

func TestArgsEqualCanonicalCodec(t *testing.T) {
	calls := 0
	codec := tools.JSONCodec[string]{ToJSON: func(v string) ([]byte, error) {
		calls++
		switch v {
		case "want":
			return []byte(` { "b":2, "a":1 } `), nil
		case "received":
			return []byte(`{"a":1,"b":2}`), nil
		case "bad":
			return nil, errors.New("codec broke")
		default:
			return []byte(`{"a":3}`), nil
		}
	}}
	tool := tools.TypedTool[string, string]{Name: "codec.test", Payload: codec}
	matcher := agenttest.ArgsEqual(tool, "want")
	assert.Equal(t, 1, calls)
	require.NoError(t, matcher("received"))
	assert.Equal(t, 2, calls)
	require.ErrorContains(t, matcher("other"), "ArgsEqual")
	require.ErrorContains(t, matcher("bad"), "encode received: codec broke")
	bad := agenttest.ArgsEqual(tool, "bad")
	before := calls
	require.ErrorContains(t, bad("received"), "ArgsEqual: encode want: codec broke")
	require.ErrorContains(t, bad("received"), "ArgsEqual: encode want: codec broke")
	assert.Equal(t, before, calls)
}

func TestStubMatchersNeverRunAtDeclaration(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	require.NoError(t, err)
	tool := echoTool()
	tool.Payload.ToJSON = func(any) ([]byte, error) {
		return nil, errors.New("want rejected")
	}
	matched := 0
	stub := agenttest.Stub(h, tool)
	stub.Return("unused").When(func(any) error {
		matched++
		return nil
	}, agenttest.ArgsEqual[any, any](tool, "bad"))
	assert.Zero(t, matched)
	result, err := h.Run(t.Context(), "root", nil)
	require.NotNil(t, result)
	var failure *agenttest.StubError
	require.ErrorAs(t, err, &failure)
	assert.Contains(t, failure.Reason, "ArgsEqual: encode want: want rejected")
	assert.Equal(t, 1, matched)
	assert.ErrorContains(t, h.Close(), "want rejected")
}

func TestStubTimesAndAlwaysCleanup(t *testing.T) {
	tb := &recordingTB{TB: t, ctx: t.Context()}
	h := agenttest.New(tb, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	stub := agenttest.Stub(h, echoTool())
	stub.Return("twice").Times(2)
	stub.Return("forever").Always()
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	for _, cleanup := range tb.cleanups {
		cleanup()
	}
	require.Len(t, tb.errors, 1)
	assert.Contains(t, tb.errors[0], "response 1: 1 unused calls")
	assert.NotContains(t, tb.errors[0], "response 2")
}

func TestStubCounts(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model",
			agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))),
			agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))),
			agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))),
			agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))),
			agenttest.Respond(agenttest.Text("done"))))
	stub := agenttest.Stub(h, echoTool())
	stub.Return("finite").Times(2)
	stub.Return("infinite").Always()
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	responses := make([]int, 0, 4)
	for _, call := range stub.Calls() {
		responses = append(responses, call.Response)
	}
	assert.Equal(t, []int{1, 1, 2, 2}, responses)
}

func TestStubPassthrough(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "named", true: "all"}[all], func(t *testing.T) {
			var opts []agenttest.Option
			if all {
				opts = append(opts, agenttest.PassthroughAll())
			}
			opts = append(opts, agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(echoTool(), "echoed"))))
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}), opts...)
			if !all {
				h.Passthrough(echoTool())
			}
			result, err := h.Run(t.Context(), "root", nil)
			require.NoError(t, err)
			require.NoError(t, result.RunErr)
			require.Len(t, h.ToolCalls(), 1)
			assert.True(t, h.ToolCalls()[0].Passthrough)
			assert.Equal(t, result.RunID, h.ToolCalls()[0].RunID)
		})
	}
}

func TestRawStubTypedRegistry(t *testing.T) {
	spec := echoSpec()
	type result struct {
		Value string `json:"value"`
	}
	spec.Result.Codec = tools.JSONCodec[any]{FromJSON: func(data []byte) (any, error) {
		if string(data) != `{"value":"raw"}` {
			return nil, fmt.Errorf("unexpected result: %s", data)
		}
		return &result{Value: "raw"}, nil
	}, ToJSON: func(v any) ([]byte, error) {
		typed, ok := v.(*result)
		if !ok {
			return nil, fmt.Errorf("typed result required: %T", v)
		}
		return []byte(fmt.Sprintf(`{"value":%q}`, typed.Value)), nil
	}}
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{spec}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(echoTool(), "raw"))))
	stub := agenttest.RawStub(h, "echo.echo")
	stub.Return(rawjson.Message(`{"value":"raw"}`))
	resultRun, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.NoError(t, resultRun.RunErr)
	require.Len(t, stub.Calls(), 1)
	assert.JSONEq(t, `{}`, string(stub.Calls()[0].Payload))
	assert.JSONEq(t, `{"value":"raw"}`, string(stub.Calls()[0].Result))
}

func TestStubBounds(t *testing.T) {
	spec := echoSpec()
	spec.Bounds = &tools.BoundsSpec{}
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{spec}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	total := 5
	agenttest.Stub(h, echoTool()).Return(map[string]any{"value": "bounded"}).Bounds(agent.Bounds{Returned: 1, Total: &total, Truncated: true, RefinementHint: "narrow"})
	total = 100
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Bounds(echoTool(), func(b *agent.Bounds) error {
		assert.Equal(t, 1, b.Returned)
		require.NotNil(t, b.Total)
		assert.Equal(t, 5, *b.Total)
		assert.True(t, b.Truncated)
		return nil
	}))
}

func TestStubAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	after := runtime.ToolInterceptorFuncs{AfterToolFunc: func(_ context.Context, in *runtime.AfterToolInput) (*runtime.AfterToolDecision, error) {
		assert.ErrorIs(t, in.Err, context.Canceled)
		return nil, nil
	}}
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.WithRuntimeOptions(runtime.WithInterceptors(after)), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("unused"))))
	// Execute the activity directly so cancellation is observed without a run timeout.
	stub := agenttest.Stub(h, echoTool())
	stub.Handle(func(context.Context, any) (any, error) {
		t.Error("handler ran after cancellation")
		return nil, nil
	}).When(func(any) error {
		close(started)
		return nil
	}).After(time.Hour)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-started
		cancel()
	})
	_, err := h.Runtime().ExecuteToolActivity(ctx, &runtime.ToolInput{ToolName: "echo.echo", Payload: rawjson.Message(`{}`)})
	require.ErrorIs(t, err, context.Canceled)
	wg.Wait()
	require.Len(t, stub.Calls(), 1)
	require.ErrorIs(t, stub.Calls()[0].Err, context.Canceled)
	// Consume the unrelated model turn so cleanup proves delay is not an unused stub.
	result, runErr := h.Run(t.Context(), "root", nil)
	require.NoError(t, runErr)
	require.NoError(t, result.RunErr)
}

func TestStubAfterToolChain(t *testing.T) {
	observed := false
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		spec := echoSpec()
		if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			return nil, errors.New("executor should not run")
		}}); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "root", Planner: harnessPlanner{id: "model", specs: []tools.ToolSpec{spec}}, Specs: []tools.ToolSpec{spec},
			Interceptors: []runtime.Interceptor{runtime.NewRetryAndReflectInterceptor(runtime.RetryAndReflectConfig{MaxRetries: 2}), runtime.ToolInterceptorFuncs{AfterToolFunc: func(_ context.Context, in *runtime.AfterToolInput) (*runtime.AfterToolDecision, error) {
				observed = true
				if in.Err != nil {
					t.Errorf("AfterTool received an unexpected error: %v", in.Err)
					return nil, in.Err
				}
				if !assert.NotNil(t, in.Execution) {
					return nil, errors.New("AfterTool execution missing")
				}
				assert.Equal(t, "stub", in.Execution.ToolResult.Result)
				return &runtime.AfterToolDecision{Result: &planner.ToolResult{Name: in.Call.Name, ToolCallID: in.Call.ToolCallID, Result: "after"}}, nil
			}}},
			Workflow: engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool"})
	}
	h := agenttest.New(t, register, agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(echoTool(), "after"))))
	agenttest.Stub(h, echoTool()).Return("stub")
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	assert.True(t, observed)
}

func TestStubFailureJoinsAndRunIsolation(t *testing.T) {
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		if err := registerHarnessAgent("one", harnessPlanner{id: "one", specs: []tools.ToolSpec{echoSpec()}})(ctx, rt); err != nil {
			return err
		}
		return registerHarnessAgent("two", harnessPlanner{id: "two"})(ctx, rt)
	}
	h, err := agenttest.NewHarness(t.Context(), register,
		agenttest.ScriptedModel("one", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":1}`)), agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":2}`))), agenttest.Respond(agenttest.Text("done"))),
		agenttest.ScriptedModel("two", agenttest.Respond(agenttest.Text("unaffected"))))
	require.NoError(t, err)
	var wg sync.WaitGroup
	var otherResult *agenttest.Result
	var otherErr error
	wg.Go(func() {
		otherResult, otherErr = h.Run(t.Context(), "two", nil)
	})
	result, runErr := h.Run(t.Context(), "one", nil)
	require.NotNil(t, result)
	var failure *agenttest.StubError
	require.ErrorAs(t, runErr, &failure)
	require.NoError(t, result.RunErr)
	wg.Wait()
	require.NoError(t, otherErr)
	assert.NotNil(t, otherResult)
	require.Len(t, h.ToolCalls(), 2)
	for _, call := range h.ToolCalls() {
		var each *agenttest.StubError
		require.ErrorAs(t, call.Err, &each)
		require.ErrorIs(t, runErr, each)
		assert.Equal(t, result.RunID, each.RunID)
	}
	assert.ErrorContains(t, h.Close(), "unstubbed tool")
}

func TestStubRunSessionRetainsFailedEvidence(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	require.NoError(t, err)
	session, runErr := h.RunSession(t.Context(), "root", nil, nil)
	var failure *agenttest.StubError
	require.ErrorAs(t, runErr, &failure)
	require.NotNil(t, session)
	require.Len(t, session.Runs, 1)
	require.Len(t, session.Trajectory, 1)
	assert.Equal(t, failure.RunID, session.Runs[0].RunID)
	assert.ErrorContains(t, h.Close(), "unstubbed tool")
}

func TestPassthroughAllToolsAndStubPrecedence(t *testing.T) {
	second := echoSpec()
	second.Name = "echo.second"
	specs := []tools.ToolSpec{echoSpec(), second}
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: specs}), agenttest.PassthroughAll(),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`)), agenttest.RawToolCall(second.Name, rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done")),
			agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(echoTool(), "stub wins"))))
	real, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.NoError(t, real.RunErr)
	require.Len(t, h.ToolCalls(), 2)
	for _, call := range h.ToolCalls() {
		assert.True(t, call.Passthrough)
	}
	agenttest.Stub(h, echoTool()).Return("stub wins")
	stub, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.NoError(t, stub.RunErr)
	calls := h.ToolCalls()
	require.Len(t, calls, 3)
	assert.False(t, calls[2].Passthrough)
}

func TestStubNewDeclarationReportsToTest(t *testing.T) {
	tb := &recordingTB{TB: t, ctx: t.Context()}
	h := agenttest.New(tb, func(context.Context, *runtime.Runtime) error {
		return nil
	})
	agenttest.Stub(h, echoTool())
	require.Len(t, tb.errors, 1)
	assert.Contains(t, tb.errors[0], "not registered")
	for _, cleanup := range tb.cleanups {
		cleanup()
	}
	require.Len(t, tb.errors, 2)
}

func TestStubPayloadPreparationAndInterceptorOrder(t *testing.T) {
	adapterCalls, decoderCalls, laterBefore := 0, 0, 0
	spec := echoSpec()
	spec.Payload.Codec.FromJSON = func(data []byte) (any, error) {
		decoderCalls++
		if string(data) != `{"value":"adapted"}` {
			return nil, errors.New("payload not adapted")
		}
		return map[string]any{"value": "adapted"}, nil
	}
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{spec}})(ctx, rt); err != nil {
			return err
		}
		return rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			return nil, errors.New("must not execute")
		},
			PayloadAdapter: func(context.Context, runtime.ToolCallMeta, tools.Ident, jsontext.Value) (jsontext.Value, error) {
				adapterCalls++
				return jsontext.Value(`{"value":"adapted"}`), nil
			}})
	}, agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.ToolInterceptorFuncs{BeforeToolFunc: func(context.Context, *runtime.BeforeToolInput) (*runtime.BeforeToolDecision, error) {
		laterBefore++
		return nil, nil
	}})), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	stub := agenttest.Stub(h, echoTool())
	stub.Handle(func(_ context.Context, payload any) (any, error) {
		return payload, nil
	})
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	assert.Equal(t, 1, adapterCalls)
	assert.Positive(t, decoderCalls)
	assert.Zero(t, laterBefore)
	assert.JSONEq(t, `{"value":"adapted"}`, string(stub.Calls()[0].Raw))
}
