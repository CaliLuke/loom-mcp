package enginetest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/require"
)

// testApplicationError exercises every registered activity boundary without
// requiring retries from engines that execute activities only once.
func testApplicationError(t *testing.T, f Fixture, a activityCase) error {
	opts := activityOptions(f)
	opts.RetryPolicy.MaxAttempts = 3
	var attempts atomic.Int32
	details := map[string]any{"reason": "invalid request"}
	require.NoError(t, a.register(f.ctx, f.Engine, opts, func(context.Context, any) (any, error) {
		attempts.Add(1)
		return nil, engine.NewNonRetryableApplicationError(applicationFailure, "enginetest.invalid", details, nil)
	}))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		_, err := a.invoke(w, a.input, engine.ActivityOptions{})
		var application *engine.ApplicationError
		if !errors.As(err, &application) {
			return nil, fmt.Errorf("activity did not return engine.ApplicationError: %w", err)
		}
		if application.Type != "enginetest.invalid" || !application.NonRetryable {
			return nil, fmt.Errorf("application metadata changed: %w", err)
		}
		return success(), checkEqual(details, application.Details)
	})
	if err := waitSuccess(f.ctx, start(t, f, f.ctx)); err != nil {
		return err
	}
	return checkEqual(int32(1), attempts.Load())
}
