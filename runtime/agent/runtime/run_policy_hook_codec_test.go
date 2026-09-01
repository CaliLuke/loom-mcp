package runtime

import (
	"context"
	"testing"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/require"
)

func TestRunWithMaxToolCallsOverrideCompletesWithHooks(t *testing.T) {
	ctx := context.Background()
	rt := New(
		WithEngine(engineinmem.New()),
		WithHooks(hooks.NewBus()),
		WithRunEventStore(runloginmem.New()),
		WithLogger(telemetry.NoopLogger{}),
		WithMetrics(telemetry.NoopMetrics{}),
		WithTracer(telemetry.NoopTracer{}),
	)
	pl := &stubPlanner{
		start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
			return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "done"}},
			}}}, nil
		},
	}
	require.NoError(t, rt.RegisterAgent(ctx, AgentRegistration{
		ID:      "svc.agent",
		Planner: pl,
		Workflow: engine.WorkflowDefinition{
			Name:    "svc.agent.workflow",
			Handler: rt.ExecuteWorkflow,
		},
		PlanActivityName:    "svc.agent.plan",
		ResumeActivityName:  "svc.agent.resume",
		ExecuteToolActivity: "svc.agent.execute_tool",
	}))

	_, err := rt.CreateSession(ctx, "session-1")
	require.NoError(t, err)
	output, err := rt.MustClient(agent.Ident("svc.agent")).Run(
		ctx,
		"session-1",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}},
		WithRunID("run-1"),
		WithRunMaxToolCalls(1),
	)
	require.NoError(t, err)
	require.Equal(t, "done", output.Final.Parts[0].(model.TextPart).Text)
}
