package agentfeatures_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type unsupportedMultimodalPlanner struct{ attempts atomic.Int32 }

func testMultimodalPlannerRetry(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	provider, err := model.NewClient(&openai.Client{})
	require.NoError(t, err)
	require.NoError(t, fx.rt.RegisterModel("unsupported", provider))
	p := &unsupportedMultimodalPlanner{}
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: p}))
	_, err = fx.rt.CreateSession(ctx, "unsupported-session")
	require.NoError(t, err)
	seed := []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}}}},
	}
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "unsupported-session", seed, agentsruntime.WithRunID("unsupported-multimodal-run"))
	require.NoError(t, err)
	_, err = handle.Wait(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model.unsupported_part")
	assert.Equal(t, int32(1), p.attempts.Load())
}
func (p *unsupportedMultimodalPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	p.attempts.Add(1)
	provider, ok := in.Agent.ModelClient("unsupported")
	if !ok {
		return nil, fmt.Errorf("missing unsupported model")
	}
	_, err := provider.Complete(ctx, &model.Request{Model: "test-model", Messages: in.Messages})
	return nil, err
}
func (*unsupportedMultimodalPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, fmt.Errorf("unexpected resume")
}
