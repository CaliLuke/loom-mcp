package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealRecordActivityContract(t *testing.T) {
	e := New().(*eng)
	input := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", EventKey: "run_sealed:run", Timestamp: time.Unix(42, 0)}
	want := &api.SealRecordOutput{IndexHead: "sealed"}
	handler := func(ctx context.Context, got *api.SealRecordInput) (*api.SealRecordOutput, error) {
		assert.Same(t, input, got)
		require.NoError(t, ctx.Err())
		deadline, ok := ctx.Deadline()
		assert.True(t, ok)
		assert.LessOrEqual(t, time.Until(deadline), time.Second)
		return want, nil
	}
	require.ErrorContains(t, e.RegisterSealRecordActivity(t.Context(), "", engine.ActivityOptions{}, handler), "name is required")
	require.ErrorContains(t, e.RegisterSealRecordActivity(t.Context(), "seal", engine.ActivityOptions{}, nil), "handler is required")
	require.NoError(t, e.RegisterSealRecordActivity(t.Context(), "seal", engine.ActivityOptions{StartToCloseTimeout: time.Second}, handler))
	require.ErrorContains(t, e.RegisterSealRecordActivity(t.Context(), "seal", engine.ActivityOptions{}, handler), "already registered")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	wf := e.newWorkflowContext(ctx, "run").Detached()
	out, err := wf.ExecuteSealRecordActivity(wf.Context(), engine.SealRecordActivityCall{Name: "seal", Input: input})
	require.NoError(t, err)
	assert.Same(t, want, out)
	for _, call := range []engine.SealRecordActivityCall{{}, {Name: "seal"}, {Name: "missing", Input: input}} {
		_, err := wf.ExecuteSealRecordActivity(wf.Context(), call)
		require.Error(t, err)
	}
	require.NoError(t, e.RegisterSealRecordActivity(t.Context(), "slow", engine.ActivityOptions{StartToCloseTimeout: time.Hour}, func(ctx context.Context, _ *api.SealRecordInput) (*api.SealRecordOutput, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	_, err = wf.ExecuteSealRecordActivity(wf.Context(), engine.SealRecordActivityCall{Name: "slow", Input: input, Options: engine.ActivityOptions{StartToCloseTimeout: time.Millisecond}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	failure := errors.New("storage unavailable")
	require.NoError(t, e.RegisterSealRecordActivity(t.Context(), "fail", engine.ActivityOptions{}, func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error) {
		return nil, failure
	}))
	_, err = wf.ExecuteSealRecordActivity(wf.Context(), engine.SealRecordActivityCall{Name: "fail", Input: input})
	assert.ErrorIs(t, err, failure)
}
