package runstoretest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type invalidCallCase struct {
	name   string
	change func(*runstore.CallRecord)
}

type callTransitionCase struct {
	name   string
	before runstore.CallState
	next   runstore.CallRecord
	want   error
}

func runCallTransitions(t *testing.T, newStore func(*testing.T) runstore.Store) {
	t.Helper()
	for _, tc := range callTransitions() {
		t.Run("transition/"+tc.name, func(t *testing.T) {
			s := newStore(t)
			var want []runstore.CallRecord
			if tc.before != "" {
				r := call(runstore.CallStarted, "request", "")
				require.NoError(t, s.PutCall(t.Context(), "a", r))
				if tc.before != runstore.CallStarted {
					r = call(tc.before, "request", "response")
					require.NoError(t, s.PutCall(t.Context(), "a", r))
				}
				want = []runstore.CallRecord{r}
			}
			err := s.PutCall(t.Context(), "a", tc.next)
			if tc.want == nil {
				require.NoError(t, err)
				want = []runstore.CallRecord{tc.next}
			} else {
				require.ErrorIs(t, err, tc.want)
			}
			got, err := s.ListCalls(t.Context(), "a", "run")
			require.NoError(t, err)
			assert.Len(t, got, len(want))
			if len(want) > 0 {
				assert.Equal(t, want, got)
			}
		})
	}
}

func runInvalidCalls(t *testing.T, newStore func(*testing.T) runstore.Store) {
	t.Helper()
	for _, tc := range invalidCalls() {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			s := newStore(t)
			r := call(runstore.CallStarted, "request", "")
			tc.change(&r)
			err := s.PutCall(t.Context(), "a", r)
			require.Error(t, err)
			require.NotErrorIs(t, err, runstore.ErrCallConflict)
			require.NotErrorIs(t, err, runstore.ErrCallTransition)
			got, err := s.ListCalls(t.Context(), "a", r.Key.RunID)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func callTransitions() []callTransitionCase {
	return []callTransitionCase{
		{"insert_started", "", call(runstore.CallStarted, "request", ""), nil},
		{"repeat_started", runstore.CallStarted, call(runstore.CallStarted, "request", ""), nil},
		{"conflicting_started", runstore.CallStarted, call(runstore.CallStarted, "different", ""), runstore.ErrCallConflict},
		{"complete", runstore.CallStarted, call(runstore.CallCompleted, "request", "response"), nil},
		{"fail", runstore.CallStarted, call(runstore.CallFailed, "request", "response"), nil},
		{"complete_different_request", runstore.CallStarted, call(runstore.CallCompleted, "different", "response"), runstore.ErrCallConflict},
		{"fail_different_request", runstore.CallStarted, call(runstore.CallFailed, "different", "response"), runstore.ErrCallConflict},
		{"repeat_completed", runstore.CallCompleted, call(runstore.CallCompleted, "request", "response"), nil},
		{"repeat_failed", runstore.CallFailed, call(runstore.CallFailed, "request", "response"), nil},
		{"failed_over_completed", runstore.CallCompleted, call(runstore.CallFailed, "request", "response"), runstore.ErrCallConflict},
		{"completed_over_failed", runstore.CallFailed, call(runstore.CallCompleted, "request", "response"), runstore.ErrCallConflict},
		{"completed_different_response", runstore.CallCompleted, call(runstore.CallCompleted, "request", "different"), runstore.ErrCallConflict},
		{"failed_different_response", runstore.CallFailed, call(runstore.CallFailed, "request", "different"), runstore.ErrCallConflict},
		{"completed_different_request", runstore.CallCompleted, call(runstore.CallCompleted, "different", "response"), runstore.ErrCallConflict},
		{"failed_different_request", runstore.CallFailed, call(runstore.CallFailed, "different", "response"), runstore.ErrCallConflict},
		{"started_over_completed", runstore.CallCompleted, call(runstore.CallStarted, "request", ""), runstore.ErrCallTransition},
		{"started_over_failed", runstore.CallFailed, call(runstore.CallStarted, "request", ""), runstore.ErrCallTransition},
		{"different_started_over_completed", runstore.CallCompleted, call(runstore.CallStarted, "different", ""), runstore.ErrCallTransition},
		{"different_started_over_failed", runstore.CallFailed, call(runstore.CallStarted, "different", ""), runstore.ErrCallTransition},
		{"completed_without_started", "", call(runstore.CallCompleted, "request", "response"), runstore.ErrCallTransition},
		{"failed_without_started", "", call(runstore.CallFailed, "request", "response"), runstore.ErrCallTransition},
	}
}

func invalidCalls() []invalidCallCase {
	return []invalidCallCase{
		{"empty_run", func(r *runstore.CallRecord) {
			r.Key.RunID = ""
		}},
		{"zero_seq", func(r *runstore.CallRecord) {
			r.Key.Seq = 0
		}},
		{"negative_seq", func(r *runstore.CallRecord) {
			r.Key.Seq = -1
		}},
		{"zero_attempt", func(r *runstore.CallRecord) {
			r.Key.Attempt = 0
		}},
		{"negative_attempt", func(r *runstore.CallRecord) {
			r.Key.Attempt = -1
		}},
		{"unknown_state", func(r *runstore.CallRecord) {
			r.State = "unknown"
		}},
		{"empty_state", func(r *runstore.CallRecord) {
			r.State = ""
		}},
		{"started_missing_body", func(r *runstore.CallRecord) {
			r.Started = nil
		}},
		{"started_empty_body", func(r *runstore.CallRecord) {
			r.Started = []byte{}
		}},
		{"started_with_completion", func(r *runstore.CallRecord) {
			r.Completion = []byte("response")
		}},
		{"completed_missing_completion", func(r *runstore.CallRecord) {
			r.State = runstore.CallCompleted
		}},
		{"failed_missing_completion", func(r *runstore.CallRecord) {
			r.State = runstore.CallFailed
		}},
		{"completed_missing_started", func(r *runstore.CallRecord) {
			r.State = runstore.CallCompleted
			r.Started = nil
			r.Completion = []byte("response")
		}},
		{"failed_missing_started", func(r *runstore.CallRecord) {
			r.State = runstore.CallFailed
			r.Started = nil
			r.Completion = []byte("response")
		}},
	}
}
