package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/agentfeatures/gen/features/agents/compactor"
	"example.com/agentfeatures/gen/features/toolsets/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextFixturePlanner struct {
	resumed chan []*model.Message
}

const contextKeptResult = "whole evidence: " + "ORDER-1234; exact value retained."

func TestGeneratedContextConfig(t *testing.T) {
	cfg := compactor.CompactorAgentConfig{Planner: &contextFixturePlanner{}}
	require.EqualError(t, cfg.Validate(), "context model is required when Summarize is configured")
	cfg.ContextModel = &agentsruntime.ContextModel{ID: "summary"}
	require.NoError(t, cfg.Validate())
	rt := agentsruntime.New(agentsruntime.WithContextCompaction(compactor.AgentID, agentsruntime.ContextCompaction{}))
	assert.ErrorContains(t, compactor.RegisterCompactorAgent(t.Context(), rt, cfg), "RunPolicy.ContextCompaction and WithContextCompaction")
}

func TestGeneratedContextCompaction(t *testing.T) {
	fx := newFeatureRuntime(t, agentsruntime.WithContextLimits(map[string]model.ContextLimits{
		"history": {ContextWindow: 6000}, "selected-summary": {ContextWindow: 200000},
	}))
	var summaryCalls atomic.Int32
	summaryRequests := make(chan *model.Request, 10)
	require.NoError(t, fx.rt.RegisterModel("history", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, fx.rt.RegisterModel("selected-summary", modelClientFunc(func(_ context.Context, req *model.Request) (*model.Response, error) {
		summaryCalls.Add(1)
		summaryRequests <- req
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: historySummaryText}}}}, StopReason: "end_turn"}, nil
	})))
	plan := &contextFixturePlanner{resumed: make(chan []*model.Message, 1)}
	require.NoError(t, compactor.RegisterUsedToolsets(t.Context(), fx.rt, compactor.WithEvidenceExecutor(agentsruntime.ToolCallExecutorFunc(
		func(context.Context, *agentsruntime.ToolCallMeta, *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
			return agentsruntime.Executed(&planner.ToolResult{Result: contextKeptResult}), nil
		},
	))))
	require.NoError(t, compactor.RegisterCompactorAgent(t.Context(), fx.rt, compactor.CompactorAgentConfig{
		Planner: plan, ContextModel: &agentsruntime.ContextModel{ID: "selected-summary"},
	}))
	_, err := fx.rt.CreateSession(t.Context(), "context-session")
	require.NoError(t, err)
	out, err := compactor.NewClient(fx.rt).Run(t.Context(), "context-session", []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember the task"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("recorded evidence ", 500)}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}},
	}, agentsruntime.WithRunID("context-run"))
	require.NoError(t, err)
	assert.Equal(t, "context complete", messageText(out.Final))
	assert.Positive(t, fx.recorder.count(hooks.ContextCompacted))
	require.Positive(t, summaryCalls.Load())
	request := <-summaryRequests
	data, err := json.Marshal(request.Messages)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Keep every order ID verbatim.")
	view := <-plan.resumed
	data, err = json.Marshal(view)
	require.NoError(t, err)
	assert.Contains(t, string(data), contextKeptResult)
	record, err := runrecord.Read(t.Context(), fx.rt.RunStore, fx.rt.RunEventStore, out.RunID, out.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	settings := record.Runs[0].ContextSettings
	require.NotNil(t, settings)
	assert.Equal(t, contextmeasure.Rate{Tokens: 1, Bytes: 100}, settings.TriggerAt)
	assert.Equal(t, 30, settings.KeepRecentTokens)
	assert.True(t, settings.Threshold)
	assert.True(t, settings.Summary.Enabled)
	assert.Equal(t, "selected-summary", settings.Summary.Model.ID)
	assert.Equal(t, "Keep every order ID verbatim.", settings.Summary.Settings.Instructions)
	assert.Contains(t, record.Runs[0].KeepResultTools, evidence.Keep)
	assert.True(t, evidence.SpecKeep.KeepResultInContext)
}

func (p *contextFixturePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: evidence.Keep, ToolCallID: "keep-evidence", Payload: []byte(`"read"`)}}}, nil
}

func (p *contextFixturePlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	p.resumed <- in.Messages
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "context complete"}}}}}, nil
}
