package trajectory_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clarificationPlanner struct {
	interceptionPlanner
}

func TestBuildRealRuntimeClarificationPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	bus := hooks.NewBus()
	awaiting := make(chan *hooks.AwaitClarificationEvent, 1)
	subscription, err := bus.Register(hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		if await, ok := event.(*hooks.AwaitClarificationEvent); ok {
			select {
			case awaiting <- await:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, subscription.Close())
	})
	rt := runtime.New(runtime.WithEngine(engineinmem.New()), runtime.WithHooks(bus))
	registerClarificationAgent(t, rt)
	_, err = rt.CreateSession(ctx, "session")
	require.NoError(t, err)
	handle, err := rt.MustClient("agent").Start(ctx, "session", nil, runtime.WithRunID("run"))
	require.NoError(t, err)
	select {
	case await := <-awaiting:
		require.NoError(t, rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run", ID: await.ID, Answer: "clarified"}))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = handle.Wait(ctx)
	require.NoError(t, err)
	rec, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "run", "run")
	require.NoError(t, err)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"run_phase_changed:0", "run_phase_changed:0",
		"model:1", "tool:1", "planner_note:1", "thinking_block:1", "run_phase_changed:1", "await_clarification:1",
		"model:2", "answer:2", "planner_note:2", "thinking_block:2",
		"model:3", "planner_note:3", "thinking_block:3", "run_completed:3",
	}, describeSteps(got.Steps))
	for _, step := range got.Steps {
		if step.Event == nil {
			continue
		}
		var metadata struct {
			PlannerStep *int `json:"planner_step,omitempty"`
		}
		require.NoError(t, json.Unmarshal(step.Event.Payload, &metadata))
		if step.Event.Type == hooks.PlannerNote || step.Event.Type == hooks.ThinkingBlock {
			require.NotNil(t, metadata.PlannerStep)
			assert.Equal(t, step.Step, *metadata.PlannerStep)
		} else {
			assert.Nil(t, metadata.PlannerStep, "workflow event %s", step.Event.Type)
		}
	}
}

// registerClarificationAgent uses the real model recorder, tool executor and await workflow.
func registerClarificationAgent(t *testing.T, rt *runtime.Runtime) {
	t.Helper()
	codec := tools.JSONCodec[any]{
		ToJSON: func(value any) ([]byte, error) {
			return json.Marshal(value)
		},
		FromJSON: func(data []byte) (any, error) {
			var value any
			err := json.Unmarshal(data, &value)
			return value, err
		},
	}
	spec := tools.ToolSpec{Name: "test.read", Toolset: "test", Payload: tools.TypeSpec{Name: "payload", Codec: codec}, Result: tools.TypeSpec{Name: "result", Codec: codec}}
	require.NoError(t, rt.RegisterModel("registered", interceptionModel{}))
	require.NoError(t, rt.RegisterToolset(runtime.ToolsetRegistration{Name: "test", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"whole": true}}), nil
	}}))
	require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
		ID: "agent", Planner: clarificationPlanner{}, Specs: []tools.ToolSpec{spec},
		Workflow:         engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
	}))
}

func (p clarificationPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.interceptionPlanner.PlanStart(ctx, input)
	if err != nil {
		return nil, err
	}
	input.Events.PlannerThinkingBlock(ctx, model.ThinkingPart{Text: "first"})
	result.Await = planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "clarify", Question: "which value?"}))
	return result, nil
}

func (p clarificationPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	input.Events.PlannerThought(ctx, "resume note", nil)
	input.Events.PlannerThinkingBlock(ctx, model.ThinkingPart{Text: "second"})
	return p.interceptionPlanner.PlanResume(ctx, input)
}

// describeSteps includes every trajectory event, including notes and phase changes.
func describeSteps(steps []trajectory.Step) []string {
	labels := make([]string, len(steps))
	for i, step := range steps {
		kind := string(step.Kind)
		if step.Event != nil {
			kind = string(step.Event.Type)
		}
		labels[i] = fmt.Sprintf("%s:%d", kind, step.Step)
	}
	return labels
}
