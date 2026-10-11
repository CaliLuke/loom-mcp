package agentfeatures_test

import (
	"context"
	"strings"
	"testing"

	"example.com/agentfeatures/gen/features/agents/compactor"
	"example.com/agentfeatures/gen/features/toolsets/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextKeepPlanner struct {
	view chan []*model.Message
}

func TestGeneratedContextKeepsOldResult(t *testing.T) {
	fx := newFeatureRuntime(t, agentsruntime.WithContextLimits(map[string]model.ContextLimits{
		"history": {ContextWindow: 21500},
	}))
	require.NoError(t, fx.rt.RegisterModel("history", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	})))
	// Each result exceeds both the recent-result protection budget and the
	// minimum saving. The third result makes both earlier results clearable.
	kept := strings.Repeat("kept evidence; ", 1200)
	control := strings.Repeat("control evidence; ", 1000)
	recent := strings.Repeat("recent evidence; ", 1200)
	results := map[string]string{"old-kept": kept, "old-control": control, "recent-control": recent}
	require.NoError(t, compactor.RegisterUsedToolsets(t.Context(), fx.rt, compactor.WithEvidenceExecutor(agentsruntime.ToolCallExecutorFunc(
		func(_ context.Context, _ *agentsruntime.ToolCallMeta, req *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
			return agentsruntime.Executed(&planner.ToolResult{Result: results[req.ToolCallID]}), nil
		},
	))))
	plan := &contextKeepPlanner{view: make(chan []*model.Message, 1)}
	require.NoError(t, compactor.RegisterCompactorAgent(t.Context(), fx.rt, compactor.CompactorAgentConfig{
		Planner: plan, ContextModel: &agentsruntime.ContextModel{ID: "history"},
	}))
	_, err := compactor.NewClient(fx.rt).OneShotRun(t.Context(), []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "collect evidence"}}},
	}, agentsruntime.WithRunID("context-keep-run"))
	require.NoError(t, err)
	contents := make(map[string]any)
	for _, msg := range <-plan.view {
		for _, part := range msg.Parts {
			if result, ok := part.(model.ToolResultPart); ok {
				contents[result.ToolUseID] = result.Content
			}
		}
	}
	assert.Equal(t, kept, contents["old-kept"], "old flagged result must remain whole")
	assert.Equal(t, checkpoint.ClearedResult(len(control)+2, "old-control", ""), contents["old-control"], "old unflagged control must be cleared")
	assert.Equal(t, recent, contents["recent-control"], "latest result remains protected by recency")
}

func (p *contextKeepPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	// One atomic batch prevents whole-unit eviction from masking clearing.
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
		{Name: evidence.Keep, ToolCallID: "old-kept", Payload: []byte(`"read"`)},
		{Name: evidence.Clear, ToolCallID: "old-control", Payload: []byte(`"read"`)},
		{Name: evidence.Clear, ToolCallID: "recent-control", Payload: []byte(`"read"`)},
	}}, nil
}

func (p *contextKeepPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	p.view <- in.Messages
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "evidence complete"}}}}}, nil
}
