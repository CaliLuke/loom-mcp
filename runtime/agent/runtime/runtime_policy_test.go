package runtime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type captureModelClient struct {
	tools []string
}

func (c *captureModelClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	c.tools = nil
	for _, tool := range req.Tools {
		c.tools = append(c.tools, tool.Name)
	}
	return &model.Response{
		Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "done"}},
		}},
	}, nil
}

func (c *captureModelClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, io.EOF
}

type modelToolPolicyPlanner struct{}

func (p *modelToolPolicyPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("default")
	if !ok {
		return nil, errors.New("default model not configured")
	}
	resp, err := client.Complete(ctx, &model.Request{
		Tools: []*model.ToolDefinition{
			{Name: "allowed", InputSchema: map[string]any{"type": "object"}},
			{Name: "blocked", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeAny},
	})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{
		FinalResponse: &planner.FinalResponse{Message: &resp.Content[0]},
	}, nil
}

func (p *modelToolPolicyPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, nil
}

func TestPolicyAllowlistTrimsToolExecution(t *testing.T) {
	recorder := &recordingHooks{}
	rt := &Runtime{
		Bus:           recorder,
		Policy:        &stubPolicyEngine{decision: policy.Decision{AllowedTools: []tools.Ident{tools.Ident("allowed")}}},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}
	rt.toolsets = map[string]ToolsetRegistration{"svc.tools": {
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*planner.ToolResult, error) {
			return &planner.ToolResult{
				Name:   call.Name,
				Result: map[string]any{"ok": true},
			}, nil
		}}}
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"allowed": newAnyJSONSpec("allowed", "svc.tools"), "blocked": newAnyJSONSpec("blocked", "svc.tools")}
	wfCtx := &testWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		asyncResult: ToolOutput{Payload: []byte("null")},
		planResult: &planner.PlanResult{
			FinalResponse: &planner.FinalResponse{
				Message: &model.Message{
					Role:  "assistant",
					Parts: []model.Part{model.TextPart{Text: "done"}},
				},
			},
		},
		hasPlanResult: true,
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}, Agent: newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID})}
	initial := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tools.Ident("allowed")}, {Name: tools.Ident("blocked")}}}
	out, err := rt.runLoop(wfCtx, AgentRegistration{
		ID:                  input.AgentID,
		Planner:             &stubPlanner{},
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}, input, base, initial, nil, model.TokenUsage{}, policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 5}, time.Time{}, time.Time{}, 2, "turn-1", nil, nil, 0)
	require.NoError(t, err)
	require.Len(t, out.ToolEvents, 1)
	require.Equal(t, tools.Ident("allowed"), out.ToolEvents[0].Name)
	var scheduled []tools.Ident
	for _, evt := range recorder.events {
		if e, ok := evt.(*hooks.ToolCallScheduledEvent); ok {
			scheduled = append(scheduled, e.ToolName)
		}
	}
	require.Equal(t, []tools.Ident{tools.Ident("allowed")}, scheduled)
}

func TestPlanStartActivityFiltersModelVisibleTools(t *testing.T) {
	modelClient := &captureModelClient{}
	rt := &Runtime{
		agents: map[agent.Ident]AgentRegistration{
			"svc.agent": {
				ID:      "svc.agent",
				Planner: &modelToolPolicyPlanner{},
			},
		},
		models: map[string]model.Client{
			"default": modelClient,
		},
		Bus:           hooks.NewBus(),
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-1",
		RunContext:       run.Context{RunID: "run-1"},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"allowed"},
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, []string{"allowed"}, modelClient.tools)
	require.True(t, out.ToolPolicyActive)
	require.Equal(t, []tools.Ident{tools.Ident("allowed")}, out.AllowedTools)
}
