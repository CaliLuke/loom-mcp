package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preparedTestReceipt() PreparedReceipt {
	return PreparedReceipt{CandidateDigest: strings.Repeat("a", 64), SurfaceID: "surface", SurfaceVersion: "1", Bindings: []PreparedBinding{{ID: "host", Version: "1"}}, TaskSnapshot: "task", SourceSnapshot: "source", InputSnapshot: "input", ModelConfig: "scripted", Environment: EnvironmentIdentity{Mode: "fixture", SnapshotID: "snapshot"}}
}

func preparedTestWriter(t *testing.T, kind ExecutionKind) (*Store, *Writer, preparedPlan) {
	t.Helper()
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	key := AttemptKey{Variant: "base", Scenario: "task", Item: "item", Repeat: 1}
	plan := preparedPlan{Kind: kind, Keys: []AttemptKey{key}, Variants: []StoredVariant{{ID: "base"}}, Scenarios: []ScenarioInfo{{ID: "task", Items: []string{"item"}}}, Config: StoredConfig{Repeats: 1, Concurrency: 1, PrepareTimeout: time.Second, CleanupTimeout: time.Second}}
	h := ExperimentHeader{Format: experimentFormat, Version: 2, ExecutionKind: kind, ID: reportFixtureID, Status: statusRunning, Planned: 1, Variants: plan.Variants, Scenarios: plan.Scenarios, Config: plan.Config}
	dir := filepath.Join(s.dir, h.ID)
	require.NoError(t, os.Mkdir(dir, 0700))
	require.NoError(t, s.writeHeader(dir, h))
	data, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: 2, Experiment: h.ID})
	require.NoError(t, err)
	require.NoError(t, appendFile(filepath.Join(dir, "attempts.jsonl"), append(data, '\n'), true))
	return s, &Writer{store: s, dir: dir, header: h, keys: plan.Keys, written: make(map[int]Fingerprint)}, plan
}

func TestPreparedStoreRetention(t *testing.T) {
	s, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	require.NoError(t, w.validatePreparedPlan(plan))
	dir, err := w.reservePrepared(plan.Keys[0])
	require.NoError(t, err)
	require.DirExists(t, dir)
	r := preparedTestReceipt()
	require.NoError(t, w.writePreparedReceipt(plan.Keys[0], r))
	artifacts, err := w.retainPreparedArtifacts(plan.Keys[0], []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain", Data: []byte("answer")}, {Name: "empty", MediaType: "text/plain"}}, true)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	assert.True(t, artifacts[0].Provisional)
	a := &AttemptRecord{AttemptKey: plan.Keys[0], ExecutionKind: ExecutionEvaluation, Prepared: &r, Artifacts: artifacts, Outcome: &Outcome{}}
	require.NoError(t, w.Append(a))
	require.NoError(t, w.Finish(&Report{ExecutionKind: ExecutionEvaluation, Attempts: []*AttemptRecord{a}}, nil))
	loaded, err := s.Load(w.ID())
	require.NoError(t, err)
	assert.Equal(t, VerdictPassed, loaded.Attempts[0].Verdict)
	assert.True(t, loaded.Attempts[0].Artifacts[0].Provisional)
	bytes, err := s.ReadArtifact(context.Background(), w.ID(), plan.Keys[0], "answer.txt")
	require.NoError(t, err)
	assert.Equal(t, "answer", string(bytes))
	bytes, err = s.ReadArtifact(context.Background(), w.ID(), plan.Keys[0], "empty")
	require.NoError(t, err)
	assert.Empty(t, bytes)
	assert.Equal(t, r, *loaded.Attempts[0].Prepared)
}

func TestPreparedStoreAdmission(t *testing.T) {
	_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	wrong := plan
	wrong.Kind = ExecutionProbe
	require.Error(t, w.validatePreparedPlan(wrong))
	require.NoError(t, w.validatePreparedPlan(plan))
	_, err := w.reservePrepared(plan.Keys[0])
	require.NoError(t, err)
	_, err = w.reservePrepared(plan.Keys[0])
	require.Error(t, err)
	require.Error(t, w.Err())
}

func TestPreparedStoreRejectsSubstitutedEvidence(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*AttemptRecord)
	}{
		{"receipt", func(r *AttemptRecord) {
			r.Prepared.InputSnapshot = "different"
		}},
		{"drop_receipt", func(r *AttemptRecord) {
			r.Prepared = nil
		}},
		{"artifacts", func(r *AttemptRecord) {
			r.Artifacts = []StoredArtifact{{Name: "forged", Digest: strings.Repeat("a", 64), MediaType: "text/plain"}}
		}},
		{"kind", func(r *AttemptRecord) {
			r.ExecutionKind = ExecutionProbe
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
			_, err := w.reservePrepared(plan.Keys[0])
			require.NoError(t, err)
			receipt := preparedTestReceipt()
			require.NoError(t, w.writePreparedReceipt(plan.Keys[0], receipt))
			r := &AttemptRecord{AttemptKey: plan.Keys[0], ExecutionKind: ExecutionEvaluation, Prepared: &receipt, Outcome: &Outcome{}}
			change.apply(r)
			require.Error(t, w.Append(r))
			require.NoError(t, w.Err())
		})
	}
}

func TestPreparedStorePlanIdentity(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*preparedPlan)
	}{
		{"keys", func(p *preparedPlan) {
			p.Keys = []AttemptKey{{Variant: "other", Scenario: "task", Item: "item", Repeat: 1}}
		}},
		{"settings", func(p *preparedPlan) {
			p.Config.CleanupTimeout++
		}},
		{"dataset", func(p *preparedPlan) {
			p.Scenarios = []ScenarioInfo{{ID: "task", DatasetHash: "different", Items: []string{"item"}}}
		}},
		{"variant", func(p *preparedPlan) {
			p.Variants = []StoredVariant{{ID: "base", Labels: map[string]string{"changed": "yes"}}}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
			change.apply(&plan)
			require.Error(t, w.validatePreparedPlan(plan))
			require.NoError(t, w.Err())
		})
	}
	var missing *Writer
	require.Error(t, missing.validatePreparedPlan(preparedPlan{}))
	_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	w.finished = true
	require.Error(t, w.validatePreparedPlan(plan))
}

func TestPreparedArtifactIntegrity(t *testing.T) {
	s, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	_, err := w.reservePrepared(plan.Keys[0])
	require.NoError(t, err)
	r := preparedTestReceipt()
	require.NoError(t, w.writePreparedReceipt(plan.Keys[0], r))
	artifacts, err := w.retainPreparedArtifacts(plan.Keys[0], []CollectedArtifact{{Name: "answer", MediaType: "text/plain", Data: []byte("value")}, {Name: "same", MediaType: "text/plain", Data: []byte("value")}}, false)
	require.NoError(t, err)
	a := &AttemptRecord{AttemptKey: plan.Keys[0], ExecutionKind: ExecutionEvaluation, Prepared: &r, Artifacts: artifacts, Outcome: &Outcome{}}
	require.NoError(t, w.Append(a))
	_, err = s.ReadArtifact(t.Context(), w.ID(), plan.Keys[0], "missing")
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.ReadArtifact(ctx, w.ID(), plan.Keys[0], "answer")
	require.ErrorIs(t, err, context.Canceled)
	blob := filepath.Join(w.dir, "evidence", "0", "artifacts", artifacts[0].Digest)
	require.NoError(t, os.WriteFile(blob, []byte("other"), 0600))
	_, err = s.ReadArtifact(t.Context(), w.ID(), plan.Keys[0], "answer")
	require.ErrorContains(t, err, "digest or size")
}

func TestPreparedReceiptPublicationIsImmutable(t *testing.T) {
	_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	_, err := w.reservePrepared(plan.Keys[0])
	require.NoError(t, err)
	r := preparedTestReceipt()
	require.NoError(t, w.writePreparedReceipt(plan.Keys[0], r))
	r.InputSnapshot = "different"
	require.Error(t, w.writePreparedReceipt(plan.Keys[0], r))
	require.Error(t, w.Err())
}

func TestPreparedArtifactFailureIsSticky(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     func(*testing.T, *Writer)
		artifacts []CollectedArtifact
	}{
		{"duplicate", nil, []CollectedArtifact{{Name: "answer", MediaType: "text/plain"}, {Name: "answer", MediaType: "text/plain"}}},
		{"invalid_path", nil, []CollectedArtifact{{Name: "../escape", MediaType: "text/plain"}}},
		{"unwritable_directory", func(t *testing.T, w *Writer) {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(w.dir, "evidence", "0", "artifacts"), []byte("file"), 0600))
		}, []CollectedArtifact{{Name: "answer", MediaType: "text/plain"}}},
		{"conflicting_blob", func(t *testing.T, w *Writer) {
			t.Helper()
			dir := filepath.Join(w.dir, "evidence", "0", "artifacts")
			require.NoError(t, os.Mkdir(dir, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, artifactDigest([]byte("value"))), []byte("other"), 0600))
		}, []CollectedArtifact{{Name: "answer", MediaType: "text/plain", Data: []byte("value")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
			_, err := w.reservePrepared(plan.Keys[0])
			require.NoError(t, err)
			if tc.setup != nil {
				tc.setup(t, w)
			}
			called := 0
			w.store.options.onError = func(err error) {
				called++
				require.ErrorIs(t, w.Err(), err)
			}
			_, err = w.retainPreparedArtifacts(plan.Keys[0], tc.artifacts, false)
			require.Error(t, err)
			require.ErrorIs(t, w.Err(), err)
			assert.Equal(t, 1, called)
			_, retryErr := w.reservePrepared(plan.Keys[0])
			require.ErrorIs(t, retryErr, err)
			assert.Equal(t, 1, called)
		})
	}
}

func TestPreparedWriterRequiresReservation(t *testing.T) {
	_, w, plan := preparedTestWriter(t, ExecutionEvaluation)
	require.Error(t, w.writePreparedReceipt(plan.Keys[0], preparedTestReceipt()))
	_, err := w.retainPreparedArtifacts(plan.Keys[0], nil, false)
	require.Error(t, err)
	var missing *Writer
	_, err = missing.reservePrepared(plan.Keys[0])
	require.Error(t, err)
}

func TestPreparedLegacyGroupJSON(t *testing.T) {
	report := &Report{Attempts: []*AttemptRecord{{AttemptKey: AttemptKey{Variant: "base", Scenario: "task", Item: "one", Repeat: 1}, Outcome: &Outcome{}}}}
	data, err := json.Marshal(report.Groups())
	require.NoError(t, err)
	assert.NotContains(t, string(data), "Probes")
	assert.NotContains(t, string(data), "Unassessed")
	assert.NotContains(t, string(data), "probes")
	assert.NotContains(t, string(data), "unassessed")
}
