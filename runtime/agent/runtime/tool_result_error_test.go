package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedToolResultNonRetryable(t *testing.T) {
	err := nonRetryableProviderError(&model.UnsupportedPartError{Provider: "openai", Placement: "tool_result", Kind: "image", Reason: "provider"})
	var app *engine.ApplicationError
	require.ErrorAs(t, err, &app)
	assert.True(t, app.NonRetryable)
	assert.ErrorIs(t, err, model.ErrUnsupportedPart)
}

func TestMultimodalUnsupportedPlannerOneAttempt(t *testing.T) {
	rt := New(WithEngine(inmem.New()))
	client, err := model.NewClient(&openai.Client{})
	require.NoError(t, err)
	require.NoError(t, rt.RegisterModel("unsupported", client))
	attempts := 0
	p := &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		attempts++
		client, ok := in.Agent.ModelClient("unsupported")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Model: "test-model", Messages: in.Messages})
		return nil, err
	}}
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "content.agent", Planner: p, Workflow: engine.WorkflowDefinition{Name: "content.workflow", TaskQueue: "content.queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "content.plan", ResumeActivityName: "content.resume", ExecuteToolActivity: "content.tool", PlanActivityOptions: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Millisecond}}}))
	_, err = rt.CreateSession(t.Context(), "session")
	require.NoError(t, err)
	seed := []*model.Message{
		contentMessage("read"),
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}}}},
	}
	_, err = rt.MustClient("content.agent").Run(t.Context(), "session", seed)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
	assert.Equal(t, 1, attempts)
}
