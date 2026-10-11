package eval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreListResolveLast(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	ids := []string{"0199d770-0000-7000-8000-000000000001", "0199d770-0000-7000-8000-000000000002", "0199d771-0000-7000-8000-000000000003"}
	cfg := storeConfig()
	for i, id := range ids {
		s.newID = func() (uuid.UUID, error) {
			return uuid.Parse(id)
		}
		s.now = func() time.Time {
			return time.Date(2026, 10, 11, 12, i, 0, 0, time.UTC)
		}
		w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
		require.NoError(t, err)
		if i < 2 {
			require.NoError(t, w.Finish(storeReport(t, cfg), nil))
		}
	}
	require.NoError(t, os.Mkdir(filepath.Join(s.dir, "_calibration"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, ids[0], ".experiment.json.tmp-leftover"), []byte("invalid"), 0600))
	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, []string{ids[2], ids[1], ids[0]}, []string{list[0].ID, list[1].ID, list[2].ID})
	got, err := s.Resolve("0199d771")
	require.NoError(t, err)
	assert.Equal(t, ids[2], got)
	got, err = s.Resolve(ids[0])
	require.NoError(t, err)
	assert.Equal(t, ids[0], got)
	got, err = s.Resolve("last")
	require.NoError(t, err)
	assert.Equal(t, ids[1], got)
	for _, ref := range []string{"0199d770", "unknownxx", "0199", "../outside"} {
		_, err := s.Resolve(ref)
		require.Error(t, err)
	}
	h, err := s.Last([][2]string{{"chat", "one"}}, ids[1])
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, ids[0], h.ID)
	h, err = s.Last([][2]string{{"chat", "missing"}}, "")
	require.NoError(t, err)
	assert.Nil(t, h)
	e, err := s.Load(ids[0])
	require.NoError(t, err)
	assert.True(t, e.Complete())
	found, err := e.Find("#0")
	require.NoError(t, err)
	assert.Equal(t, "one", found.Item)
	found, err = e.Find("default:chat:one:1")
	require.NoError(t, err)
	assert.Equal(t, 0, found.Index)
	_, err = e.Find("chat:one:1")
	require.NoError(t, err)
	_, err = e.Find("missing")
	require.Error(t, err)
}

func TestStoreParentAndRerun(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	_, err = s.Begin(t.Context(), storeConfig(), ExperimentMeta{Parent: "0199d770-0000-7000-8000-000000000001"})
	require.Error(t, err)
	w, err := s.Begin(t.Context(), storeConfig(), ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	_, err = s.Begin(t.Context(), storeConfig(), ExperimentMeta{Parent: w.ID(), Identity: &Identity{}})
	require.NoError(t, err)
	e := &Experiment{}
	for i, v := range []Verdict{VerdictPassed, VerdictFailed, VerdictError, VerdictNotRun, VerdictPending} {
		e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: AttemptKey{Variant: "base", Scenario: "chat", Item: []string{"passed", "failed", "error", "not-run", "pending"}[i], Repeat: 1}, Verdict: v})
	}
	sel, err := e.RerunSelection()
	require.NoError(t, err)
	assert.Equal(t, []string{"base"}, sel.Variants)
	assert.Equal(t, []string{"chat/error", "chat/failed", "chat/not-run"}, sel.Items)
	e.Attempts = e.Attempts[:1]
	_, err = e.RerunSelection()
	require.ErrorIs(t, err, ErrNothingToRerun)
	e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: AttemptKey{Variant: "other", Scenario: "chat", Item: "passed", Repeat: 1}})
	_, err = e.Find("chat:passed:1")
	require.Error(t, err)
}

func TestStoreLoadTornTail(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	require.NoError(t, w.Append(storeReport(t, cfg).Attempts[0]))
	require.NoError(t, appendFile(filepath.Join(s.dir, w.ID(), "attempts.jsonl"), []byte(`{"index":`), false))
	e, err := s.Load(w.ID())
	require.NoError(t, err)
	assert.True(t, e.TruncatedTail)
	assert.False(t, e.Complete())
	assert.Len(t, e.Attempts, 1)
}
