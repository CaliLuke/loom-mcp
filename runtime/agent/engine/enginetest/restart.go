package enginetest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

func testRestart(t *testing.T, f Fixture) error {
	require.NotNil(t, f.Restart, "Durable=true requires a real backend restart hook")
	ctx := f.ctx
	var completed atomic.Int32
	checkpoint := make(chan struct{}, 4)
	register := func(e engine.Engine) {
		fixture := f
		fixture.Engine = e
		require.NoError(t, e.RegisterExecuteToolActivity(ctx, "once", activityOptions(f), func(_ context.Context, _ *api.ToolInput) (*api.ToolOutput, error) {
			completed.Add(1)
			return &api.ToolOutput{Preview: "recorded"}, nil
		}))
		require.NoError(t, e.RegisterHookActivity(ctx, activityName, activityOptions(f), func(_ context.Context, _ *api.HookActivityInput) error {
			checkpoint <- struct{}{}
			return nil
		}))
		registerWorkflow(t, fixture, workflowName, restartWorkflow)
	}
	register(f.Engine)
	h := start(t, f, ctx)
	receive(t, ctx, checkpoint)
	if err := checkEqual(int32(1), completed.Load()); err != nil {
		return err
	}
	restarted := f.Restart(t, register)
	require.NotNil(t, restarted)
	require.NoError(t, h.Signal(ctx, api.SignalResume, &api.ResumeRequest{Notes: "after restart"}))
	if err := waitSuccess(ctx, h); err != nil {
		return err
	}
	if err := checkEqual(int32(1), completed.Load()); err != nil {
		return fmt.Errorf("completed activity must replay without executing again: %w", err)
	}
	status, err := restarted.QueryRunStatus(ctx, request(f).ID)
	if err != nil {
		return err
	}
	return checkEqual(engine.RunStatusCompleted, status)
}

func restartWorkflow(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
	got, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: "once", Input: &api.ToolInput{ToolCallID: "once"}})
	if err != nil {
		return nil, err
	}
	if err := checkEqual(&api.ToolOutput{Preview: "recorded"}, got); err != nil {
		return nil, err
	}
	// The next activity cannot start until the first completion is recorded.
	if err := announce(w); err != nil {
		return nil, err
	}
	signal, err := w.ResumeRequests().Receive(w.Context())
	if err != nil {
		return nil, err
	}
	return success(), checkEqual(&api.ResumeRequest{Notes: "after restart"}, signal)
}
