package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type awaitContextProbe struct {
	Elapsed  time.Duration
	Err      string
	ErrType  string
	Canceled bool
}

type awaitContextKey struct{}

func TestTemporalAwaitRejectsNativeGoCancellationContext(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wf := func(tctx workflow.Context) (awaitContextProbe, error) {
		adapted := newTemporalWorkflowContext(workflowContextTestEngine(), tctx)
		goCtx, cancel := context.WithCancel(adapted.Context())
		started := workflow.Now(tctx)
		ready := false
		workflow.Go(tctx, func(tctx workflow.Context) {
			if err := workflow.NewTimer(tctx, time.Second).Get(tctx, nil); err != nil {
				panic(err)
			}
			cancel()
		})
		workflow.Go(tctx, func(tctx workflow.Context) {
			if err := workflow.NewTimer(tctx, 3*time.Second).Get(tctx, nil); err != nil {
				panic(err)
			}
			ready = true
		})

		err := adapted.Await(goCtx, func() bool {
			return ready
		})
		probe := awaitContextProbe{Elapsed: workflow.Now(tctx).Sub(started)}
		if err != nil {
			probe.Err = err.Error()
			probe.ErrType = fmt.Sprintf("%T", err)
			probe.Canceled = cancellation.Only(err)
		}
		if err := workflow.Await(tctx, func() bool {
			return ready
		}); err != nil {
			return probe, err
		}
		return probe, nil
	}

	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
	var probe awaitContextProbe
	require.NoError(t, env.GetWorkflowResult(&probe))
	require.Contains(t, probe.Err, "WithCancel().Context()")
	require.Zero(t, probe.Elapsed, "unsupported Go cancellation must be rejected before waiting")
}

func TestTemporalAwaitUsesWorkflowScopeCancellation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wf := func(tctx workflow.Context) (awaitContextProbe, error) {
		adapted := newTemporalWorkflowContext(workflowContextTestEngine(), tctx)
		cancelable, cancel := adapted.WithCancel()
		started := workflow.Now(tctx)
		workflow.Go(tctx, func(tctx workflow.Context) {
			if err := workflow.NewTimer(tctx, time.Second).Get(tctx, nil); err != nil {
				panic(err)
			}
			cancel()
		})
		err := cancelable.Await(cancelable.Context(), func() bool {
			return false
		})
		probe := awaitContextProbe{Elapsed: workflow.Now(tctx).Sub(started)}
		if err != nil {
			probe.Err = err.Error()
			probe.ErrType = fmt.Sprintf("%T", err)
			probe.Canceled = cancellation.Only(err)
		}
		return probe, nil
	}

	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
	var probe awaitContextProbe
	require.NoError(t, env.GetWorkflowResult(&probe))
	require.True(t, probe.Canceled, "workflow-scope cancellation classification (%s: %s)", probe.ErrType, probe.Err)
	require.Equal(t, time.Second, probe.Elapsed)
}

func TestTemporalAwaitRejectsContextsOutsideWorkflowScope(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wf := func(tctx workflow.Context) ([]string, error) {
		adapted := newTemporalWorkflowContext(workflowContextTestEngine(), tctx)
		uncancelableValue := context.WithValue(adapted.Context(), awaitContextKey{}, "value")
		if err := adapted.Await(uncancelableValue, func() bool {
			return true
		}); err != nil {
			return nil, err
		}
		foreign, cancelForeign := adapted.WithCancel()
		defer cancelForeign()
		cancelableGo, cancelGo := context.WithCancel(adapted.Context())
		defer cancelGo()
		preCanceled, cancelPreCanceled := context.WithCancel(adapted.Context())
		cancelPreCanceled()
		if err := adapted.Await(preCanceled, func() bool {
			return false
		}); !errors.Is(err, context.Canceled) {
			return nil, errors.New("pre-canceled context did not preserve context.Canceled")
		}
		for _, ctx := range []context.Context{context.Background(), foreign.Context(), cancelableGo} {
			err := adapted.Await(ctx, func() bool {
				return true
			})
			if err == nil {
				return nil, errors.New("expected unsupported await context")
			}
			if !strings.Contains(err.Error(), "WithCancel().Context()") {
				return nil, err
			}
		}
		return []string{"accepted value scope", "preserved pre-cancel", "rejected unscoped", "rejected foreign scope", "rejected native cancel"}, nil
	}

	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
	var outcomes []string
	require.NoError(t, env.GetWorkflowResult(&outcomes))
	require.Equal(t, []string{"accepted value scope", "preserved pre-cancel", "rejected unscoped", "rejected foreign scope", "rejected native cancel"}, outcomes)
}
