package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealRecordActivityOnDetachedContext(t *testing.T) {
	env, eng := newStorageTestEnvironment(t)
	input := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", EventKey: "run_sealed:run", Timestamp: time.Unix(42, 0).UTC()}
	called := false
	require.NoError(t, eng.RegisterSealRecordActivity(t.Context(), "seal", engine.ActivityOptions{StartToCloseTimeout: time.Second}, func(ctx context.Context, got *api.SealRecordInput) (*api.SealRecordOutput, error) {
		called = true
		assert.Equal(t, input, got)
		require.NoError(t, ctx.Err())
		return &api.SealRecordOutput{IndexHead: "sealed"}, nil
	}))
	require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		scope, cancel := wf.WithCancel()
		cancel()
		detached := scope.Detached()
		out, err := detached.ExecuteSealRecordActivity(detached.Context(), engine.SealRecordActivityCall{Name: "seal", Input: input})
		if err != nil {
			return nil, err
		}
		assert.Equal(t, "sealed", string(out.IndexHead))
		return &api.RunOutput{RunID: "run"}, nil
	}}))
	env.ExecuteWorkflow("workflow", &api.RunInput{RunID: "run"})
	require.NoError(t, env.GetWorkflowError())
	assert.True(t, called)
}
