package runtime

import (
	"context"
	"errors"
	"testing"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type panickingExecutionUnwrapError struct{}

type panickingExecutionAsError struct{}

func (*panickingExecutionUnwrapError) Error() string {
	return "panicking unwrap"
}

func (*panickingExecutionUnwrapError) Unwrap() error {
	panic("broken unwrap")
}

func (*panickingExecutionAsError) Error() string {
	return "panicking custom As"
}

func (*panickingExecutionAsError) As(any) bool {
	panic("broken As")
}

func TestToolCollectorsPropagateCancellationGraphs(t *testing.T) {
	t.Parallel()

	cleanup := errors.New("cleanup failed")
	tests := []struct {
		name       string
		err        error
		wantLeaves []error
	}{
		{name: "cancellation", err: context.Canceled, wantLeaves: []error{context.Canceled}},
		{name: "joined cancellations", err: errors.Join(context.Canceled, context.Canceled), wantLeaves: []error{context.Canceled}},
		{name: "mixed cancellation and failure", err: errors.Join(context.Canceled, cleanup), wantLeaves: []error{context.Canceled, cleanup}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ready := make(chan struct{})
			close(ready)
			call := planner.ToolRequest{
				Name:       "svc.tools.call",
				RunID:      "run-1",
				SessionID:  "session-1",
				TurnID:     "turn-1",
				ToolCallID: "call-1",
			}
			exec := &toolBatchExec{}
			wfCtx := &testWorkflowContext{ctx: context.Background()}

			activityResult, activityErr := exec.collectActivityExecution(wfCtx, context.Background(), futureInfo{
				future: &controlledToolFuture{ready: ready, err: test.err},
				call:   call,
			})
			require.Nil(t, activityResult)
			for _, leaf := range test.wantLeaves {
				require.ErrorIs(t, activityErr, leaf)
			}

			childResult, childErr := exec.collectChildResult(wfCtx, context.Background(), agentChildFutureInfo{
				handle: &controlledChildHandle{ready: ready, err: test.err},
				call:   call,
			})
			require.Nil(t, childResult)
			for _, leaf := range test.wantLeaves {
				require.ErrorIs(t, childErr, leaf)
			}
		})
	}
}

func TestToolCollectorsBoundHostileExecutionErrors(t *testing.T) {
	t.Parallel()

	cycle := &cyclicCompletionError{}
	cycle.next = cycle
	tests := []struct {
		name          string
		err           error
		wantCause     string
		wantRetryHint bool
	}{
		{name: "cycle", err: cycle, wantCause: cancellation.ErrInvalidErrorGraph.Error()},
		{name: "panicking error text", err: &panickingInterceptorError{}, wantCause: cancellation.ErrInvalidErrorGraph.Error()},
		{name: "panicking unwrap", err: &panickingExecutionUnwrapError{}, wantCause: cancellation.ErrInvalidErrorGraph.Error()},
		{name: "panicking custom As", err: &panickingExecutionAsError{}, wantCause: "panicking custom As"},
		{
			name:          "service unavailable",
			err:           &loom.ServiceError{Name: "service_unavailable", Message: "provider unavailable"},
			wantCause:     "provider unavailable",
			wantRetryHint: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ready := make(chan struct{})
			close(ready)
			call := planner.ToolRequest{
				Name:       "svc.tools.call",
				RunID:      "run-1",
				SessionID:  "session-1",
				TurnID:     "turn-1",
				ToolCallID: "call-1",
			}
			rt := New()
			rt.toolsets["svc.tools"] = ToolsetRegistration{Name: "svc.tools"}
			rt.toolSpecs[call.Name] = tools.ToolSpec{
				Name:    call.Name,
				Toolset: "svc.tools",
				Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec},
				Result:  tools.TypeSpec{Codec: tools.AnyJSONCodec},
			}
			exec := &toolBatchExec{
				r:         rt,
				runID:     call.RunID,
				sessionID: call.SessionID,
				turnID:    call.TurnID,
			}
			wfCtx := &testWorkflowContext{ctx: context.Background()}

			activityResult, activityErr := exec.collectActivityExecution(wfCtx, context.Background(), futureInfo{
				future: &controlledToolFuture{ready: ready, err: test.err},
				call:   call,
			})
			require.NoError(t, activityErr)
			require.NotNil(t, activityResult)
			assertCollectedExecutionFailure(t, activityResult.ToolResult, test.wantCause, test.wantRetryHint)

			childResult, childErr := exec.collectChildResult(wfCtx, context.Background(), agentChildFutureInfo{
				handle: &controlledChildHandle{ready: ready, err: test.err},
				call:   call,
			})
			require.NoError(t, childErr)
			assertCollectedExecutionFailure(t, childResult, test.wantCause, test.wantRetryHint)
		})
	}
}

func assertCollectedExecutionFailure(t *testing.T, result *planner.ToolResult, wantCause string, wantRetryHint bool) {
	t.Helper()
	require.NotNil(t, result)
	require.NotNil(t, result.Error)
	require.NotNil(t, result.Error.Cause)
	require.Equal(t, wantCause, result.Error.Cause.Message)
	if wantRetryHint {
		require.NotNil(t, result.RetryHint)
		require.Equal(t, planner.RetryReasonToolUnavailable, result.RetryHint.Reason)
		return
	}
	require.Nil(t, result.RetryHint)
}
