package inmem

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecallActivity(t *testing.T) {
	e := New()
	input := &api.RecallActivityInput{RunID: "run", Scope: "root", PageBytes: 512, ScanBytes: 8_000_000}
	require.Error(t, e.RegisterRecallActivity(t.Context(), "", engine.ActivityOptions{}, nil))
	require.NoError(t, e.RegisterRecallActivity(t.Context(), "recall", engine.ActivityOptions{}, func(_ context.Context, in *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
		assert.Equal(t, input, in)
		return &api.RecallActivityOutput{Result: &api.ToolOutput{Preview: "page"}}, nil
	}))
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "test", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		out, err := wf.ExecuteRecallActivity(wf.Context(), engine.RecallActivityCall{Name: "recall", Input: input})
		if err != nil {
			return nil, err
		}
		assert.Equal(t, "page", out.Result.Preview)
		return &api.RunOutput{}, nil
	}}))
	handle, err := e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "test", Input: &api.RunInput{RootRunID: "root"}})
	require.NoError(t, err)
	_, err = handle.Wait(t.Context())
	require.NoError(t, err)
}
