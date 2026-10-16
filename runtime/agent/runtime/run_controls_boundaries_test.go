package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statusFailureEngine struct {
	*stubEngine
	statusErr error
}

func TestRunControlsRejectMissingInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*Runtime) error
		want   string
	}{
		{"nil pause", func(r *Runtime) error {
			return r.PauseRun(t.Context(), nil)
		}, "pause request"},
		{"empty pause run", func(r *Runtime) error {
			return r.PauseRun(t.Context(), &api.PauseRequest{})
		}, "run id"},
		{"nil resume", func(r *Runtime) error {
			return r.ResumeRun(t.Context(), nil)
		}, "resume request"},
		{"empty resume run", func(r *Runtime) error {
			return r.ResumeRun(t.Context(), &api.ResumeRequest{})
		}, "run id"},
		{"nil clarification", func(r *Runtime) error {
			return r.ProvideClarification(t.Context(), nil)
		}, "clarification answer"},
		{"empty clarification run", func(r *Runtime) error {
			return r.ProvideClarification(t.Context(), &api.ClarificationAnswer{})
		}, "run id"},
		{"nil results", func(r *Runtime) error {
			return r.ProvideToolResults(t.Context(), nil)
		}, "tool results"},
		{"empty results run", func(r *Runtime) error {
			return r.ProvideToolResults(t.Context(), &api.ToolResultsSet{})
		}, "run id"},
		{"nil confirmation", func(r *Runtime) error {
			return r.ProvideConfirmation(t.Context(), nil)
		}, "confirmation decision"},
		{"blank confirmation run", func(r *Runtime) error {
			return r.ProvideConfirmation(t.Context(), &api.ConfirmationDecision{RunID: "  "})
		}, "run id"},
		{"nil typed input", func(r *Runtime) error {
			return r.ProvideTypedInput(t.Context(), nil)
		}, "typed input"},
		{"blank typed run", func(r *Runtime) error {
			return r.ProvideTypedInput(t.Context(), &api.TypedInputAnswer{RunID: "  "})
		}, "run id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An engine without initialized effects also proves validation precedes I/O.
			runtime := New(WithEngine(&stubEngine{}))
			require.ErrorContains(t, tc.invoke(runtime), tc.want)
		})
	}
}

func TestRunConfirmationClassifiesUnavailableRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     engine.RunStatus
		queryErr   error
		wantReason RunNotAwaitableReason
		wantErr    error
	}{
		{"unknown", "", engine.ErrWorkflowNotFound, RunNotAwaitableUnknownRun, engine.ErrWorkflowNotFound},
		{"completed", engine.RunStatusCompleted, nil, RunNotAwaitableCompletedRun, engine.ErrWorkflowNotFound},
		{"active", engine.RunStatusRunning, nil, "", engine.ErrWorkflowNotFound},
		{"status lookup failed", "", assert.AnError, "", assert.AnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := New(WithEngine(&statusFailureEngine{stubEngine: &stubEngine{runStatus: tc.status}, statusErr: tc.queryErr}))
			err := runtime.ProvideConfirmation(t.Context(), &api.ConfirmationDecision{RunID: "run", ID: "confirm", Approved: true})
			require.ErrorIs(t, err, tc.wantErr)
			typed, ok := AsRunNotAwaitable(err)
			if tc.wantReason == "" {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tc.wantReason, typed.Reason)
			assert.Equal(t, "run", typed.RunID)
		})
	}
}

func TestRunControlsReportUnsupportedOrFailedCancellation(t *testing.T) {
	runtime := New(WithEngine(&stubEngine{}))
	require.ErrorContains(t, runtime.CancelRun(t.Context(), "run"), "does not support cancel")
	require.ErrorContains(t, runtime.PauseRun(t.Context(), &api.PauseRequest{RunID: "run"}), "not found")
	engine := &recordingCancelByIDEngine{Engine: &stubEngine{}, err: assert.AnError}
	runtime = New(WithEngine(engine))
	require.ErrorIs(t, runtime.CancelRun(t.Context(), "run"), assert.AnError)
	assert.Equal(t, []string{"run"}, engine.snapshot())
}

func (e *statusFailureEngine) QueryRunStatus(ctx context.Context, id string) (engine.RunStatus, error) {
	if e.statusErr != nil {
		return "", e.statusErr
	}
	return e.stubEngine.QueryRunStatus(ctx, id)
}
