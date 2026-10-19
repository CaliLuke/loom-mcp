package control_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type jobSet struct{ jobs map[string]control.JobRecord }

func (j *jobSet) Accept(context.Context, control.Submission) (control.JobRecord, error) {
	return control.JobRecord{}, control.NewError(control.Unsupported, "", "", "")
}
func (j *jobSet) Status(_ context.Context, scope, id string) (control.JobRecord, error) {
	r, ok := j.jobs[id]
	if !ok || r.Submission.Scope != scope {
		return control.JobRecord{}, control.NewError(control.NotFound, "", "", "")
	}
	return r, nil
}
func (j *jobSet) Cancel(ctx context.Context, scope, id string) (control.JobRecord, error) {
	return j.Status(ctx, scope, id)
}

func savedProbe(t *testing.T, cfg control.Config, id string) control.JobRecord {
	t.Helper()
	candidate := cfg.Candidates.Discover().BaselineDigest
	key := eval.AttemptKey{Scenario: "task", Item: "item", Variant: "variant", Repeat: 1}
	pc := eval.ProbeConfig{Key: key, Item: eval.Item{ID: key.Item, Input: "input"}, Variant: eval.Variant{ID: key.Variant}, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return &eval.PreparedAttempt{Harness: eval.AttemptHarnessConfig{Register: func(context.Context, *agentsruntime.Runtime) error { return nil }}, Drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
			return &agenttest.SessionResult{}, nil
		}, Receipt: eval.PreparedReceipt{CandidateDigest: candidate, SurfaceID: "sample", SurfaceVersion: "1", Bindings: []eval.PreparedBinding{{ID: "files", Version: "1"}}, TaskSnapshot: "task-v1", SourceSnapshot: "source", InputSnapshot: "input", DriverVersion: "driver-v1", ModelConfig: "none", Environment: eval.EnvironmentIdentity{Mode: "fixture", SnapshotID: "empty"}}, Quiesce: func(context.Context) error { return nil }, Collect: func(context.Context, eval.CollectionInput) ([]eval.CollectedArtifact, error) { return nil, nil }, Release: func(context.Context) error { return nil }}, nil
	}}
	writer, err := cfg.Experiments.BeginProbe(t.Context(), pc, eval.ExperimentMeta{})
	require.NoError(t, err)
	pc.PreparedWriter = writer
	report, runErr := eval.Probe(t.Context(), pc)
	require.NoError(t, runErr)
	require.NoError(t, writer.Finish(report, runErr))
	return control.JobRecord{ID: id, Submission: control.Submission{Scope: cfg.Scope, Candidate: candidate, TaskID: "task", TaskSnapshot: "task-v1", Kind: eval.ExecutionProbe}, State: control.Succeeded, ExperimentID: writer.ID(), Attempt: key}
}
func TestControlComparisonProbesNeverGainQuality(t *testing.T) {
	cfg := config(t)
	jobs := &jobSet{jobs: map[string]control.JobRecord{}}
	jobs.jobs["a"] = savedProbe(t, cfg, "a")
	jobs.jobs["b"] = savedProbe(t, cfg, "b")
	cfg.Executor = jobs
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	s := service(t, cfg)
	got, err := s.Compare(t.Context(), control.CompareRequest{BaselineJobID: "a", CandidateJobID: "b"})
	require.NoError(t, err)
	assert.Equal(t, "not_comparable", got.Status)
	assert.Nil(t, got.Baseline.Quality)
	assert.Nil(t, got.Candidate.Quality)
	assert.Equal(t, 1, got.Baseline.Attempts)
	record := jobs.jobs["b"]
	record.Attempt.Item = "wrong"
	jobs.jobs["b"] = record
	_, err = s.Compare(t.Context(), control.CompareRequest{BaselineJobID: "a", CandidateJobID: "b"})
	code(t, err, control.Unsupported)
}
