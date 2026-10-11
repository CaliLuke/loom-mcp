package trajectory_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	interceptionPlanner struct{}
	interceptionModel   struct{}
)

func TestBuildWithDroppingBeforeEvent(t *testing.T) {
	ctx := t.Context()
	eng := engineinmem.New()
	rt := runtime.New(runtime.WithEngine(eng), runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{
		BeforeEventFunc: func(_ context.Context, input *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
			assert.False(t, hooks.IsRecordEvent(input.Event.Type()))
			return &runtime.BeforeEventDecision{Drop: true}, nil
		},
	}))
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
	require.NoError(t, rt.RegisterAgent(ctx, runtime.AgentRegistration{
		ID: "agent", Planner: interceptionPlanner{}, Specs: []tools.ToolSpec{spec},
		Workflow:         engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
	}))
	_, err := rt.CreateSession(ctx, "session")
	require.NoError(t, err)
	_, err = rt.MustClient("agent").Run(ctx, "session", nil, runtime.WithRunID("run"))
	require.NoError(t, err)
	rec, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "run", "run")
	require.NoError(t, err)
	assert.False(t, slices.ContainsFunc(rec.Runs[0].Events, func(event *runlog.Event) bool {
		return event.Type == hooks.RunCompleted || event.Type == hooks.ToolCallScheduled || event.Type == hooks.ToolResultReceived || event.Type == hooks.PlannerNote
	}))
	got, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: true})
	require.NoError(t, err)
	steps := recordSteps(got)
	require.Len(t, steps, 3)
	require.Equal(t, []trajectory.StepKind{trajectory.StepModel, trajectory.StepTool, trajectory.StepModel}, stepKinds(steps))
	assert.Equal(t, []int{1, 1, 2}, []int{steps[0].Step, steps[1].Step, steps[2].Step})
	assert.Equal(t, 2, got.Totals.ModelCalls)
	assert.Equal(t, 2, got.Totals.AdoptedModelCalls)
	assert.Equal(t, 1, got.Totals.ToolCalls)
	assert.Equal(t, 6, got.Totals.TokensByModel["provider"].TotalTokens)
	assert.JSONEq(t, `{"whole":true}`, string(steps[1].Tool.Result))
	// Add arbitrary observed endpoints around the same immutable evidence. Neither
	// the call steps nor any total can change, even with misleading observed usage.
	rec.Runs[0].Events = append([]*runlog.Event{{Type: hooks.RunStarted, Timestamp: time.Unix(1, 0)}}, rec.Runs[0].Events...)
	rec.Runs[0].Events = append(rec.Runs[0].Events,
		&runlog.Event{Type: hooks.Usage, Payload: rawjson.Message(`{"TotalTokens":999999}`)},
		&runlog.Event{Type: hooks.RunCompleted, Payload: rawjson.Message(`{"status":"success"}`), Timestamp: time.Unix(9_999_999_999, 0)},
	)
	withObserved, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: true})
	require.NoError(t, err)
	assert.Equal(t, steps, recordSteps(withObserved))
	assert.Equal(t, got.Totals, withObserved.Totals)
}

func (interceptionPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("registered")
	if !ok {
		return nil, fmt.Errorf("missing registered model")
	}
	response, err := client.Complete(ctx, &model.Request{Model: "provider", Tools: []*model.ToolDefinition{{Name: "test.read", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		return nil, err
	}
	input.Events.PlannerThought(ctx, "observed", nil)
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: response.ToolCalls[0].Name, ToolCallID: "call", Payload: response.ToolCalls[0].Payload}}}, nil
}

func (interceptionPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("registered")
	if !ok {
		return nil, fmt.Errorf("missing registered model")
	}
	_, err := client.Complete(ctx, &model.Request{Model: "provider", Tools: []*model.ToolDefinition{{Name: "test.read", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}}, nil
}

func (interceptionModel) Complete(context.Context, *model.Request) (*model.Response, error) {
	return &model.Response{StopReason: "tool_use", ToolCalls: []model.ToolCall{{Name: "test.read", ID: "call", Payload: rawjson.Message(`{}`)}}, Usage: model.TokenUsage{Model: "provider", InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}, nil
}

func (interceptionModel) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}
