package control_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlComparisonGroupsRejectInvalidAssociations(t *testing.T) {
	cfg := config(t)
	digest := cfg.Candidates.Discover().BaselineDigest
	a := savedComparisonTrial(t, cfg, "a", digest, "shared", eval.VerdictPassed)
	b := savedComparisonTrial(t, cfg, "b", digest, "shared", eval.VerdictPassed)
	probe := savedComparisonTrial(t, cfg, "probe", digest, "shared", eval.VerdictUnassessed)
	other := savedComparisonTrial(t, cfg, "other", comparisonCandidate(t, cfg), "shared", eval.VerdictPassed)
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	for _, tt := range []struct {
		name   string
		change func(*control.JobRecord)
		want   control.Code
	}{
		{"cross_scope", func(r *control.JobRecord) {
			r.Submission.Scope = "private-other-scope"
		}, control.NotFound},
		{"running", func(r *control.JobRecord) {
			r.State = control.Running
		}, control.EvidenceNotReady},
		{"cancel_requested", func(r *control.JobRecord) {
			r.State = control.CancelRequested
		}, control.EvidenceNotReady},
		{"missing_evidence", func(r *control.JobRecord) {
			r.ExperimentID = ""
		}, control.EvidenceNotReady},
		{"missing_experiment", func(r *control.JobRecord) {
			r.ExperimentID = "01996bbc-e5e5-7000-8000-000000000000"
		}, control.EvidenceNotReady},
		{"wrong_attempt", func(r *control.JobRecord) {
			r.Attempt.Repeat = 2
		}, control.Unsupported},
		{"receipt_candidate", func(r *control.JobRecord) {
			r.Submission.Candidate = "private-wrong-candidate"
		}, control.Unsupported},
		{"receipt_task", func(r *control.JobRecord) {
			r.Submission.TaskSnapshot = "private-wrong-task"
		}, control.Unsupported},
		{"receipt_kind", func(r *control.JobRecord) {
			r.Submission.Kind = eval.ExecutionProbe
		}, control.Unsupported},
		{"case_task", func(r *control.JobRecord) {
			r.Submission.TaskID = "private-other-task"
		}, control.Unsupported},
		{"case_plan", func(r *control.JobRecord) {
			r.Submission.EvaluationPlan = "private-other-plan"
		}, control.Unsupported},
		{"case_host_binding", func(r *control.JobRecord) {
			r.Submission.HostBinding.Version = "private-other-version"
		}, control.Unsupported},
		{"alias", func(r *control.JobRecord) {
			r.ExperimentID, r.Attempt = a.ExperimentID, a.Attempt
		}, control.InvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := b
			tt.change(&changed)
			local := cfg
			local.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": changed}}
			got, err := service(t, local).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
			code(t, err, tt.want)
			assert.Empty(t, got)
			assert.NotContains(t, err.Error(), "private-")
		})
	}
	for _, record := range []control.JobRecord{probe, other} {
		t.Run("mixed_side_"+record.ID, func(t *testing.T) {
			local := cfg
			local.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": b, record.ID: record}}
			_, err := service(t, local).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a", record.ID}, CandidateJobIDs: []string{"b"}})
			code(t, err, control.Unsupported)
		})
	}
	cfg.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": b}}
	cfg.Authorize = func(context.Context, control.Operation) error {
		return errors.New("private-denial")
	}
	_, err := service(t, cfg).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
	code(t, err, control.NotFound)
}

func TestControlComparisonGroupsRequireCommittedEvidence(t *testing.T) {
	cfg := config(t)
	a := savedProbe(t, cfg, "a")
	writer, err := cfg.Experiments.BeginProbe(t.Context(), eval.ProbeConfig{Key: a.Attempt, Item: eval.Item{ID: a.Attempt.Item, Input: "input"}, Variant: eval.Variant{ID: a.Attempt.Variant}, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return nil, nil
	}}, eval.ExperimentMeta{})
	require.NoError(t, err)
	b := a
	b.ID, b.ExperimentID = "b", writer.ID()
	cfg.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": b}}
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	s := service(t, cfg)
	_, err = s.Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
	code(t, err, control.EvidenceNotReady)
	_, err = s.Compare(t.Context(), control.CompareRequest{BaselineJobID: "a", CandidateJobID: "b"})
	code(t, err, control.Unsupported)
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Experiments.Dir(), b.ExperimentID, "experiment.json"), []byte("corrupt-private-data"), 0600))
	_, err = s.Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
	code(t, err, control.OperationFailed)
	assert.NotContains(t, err.Error(), "private")
}

func TestControlComparisonGroupsNoOverlapAndTerminalStates(t *testing.T) {
	cfg := config(t)
	digest := cfg.Candidates.Discover().BaselineDigest
	a := savedComparisonTrial(t, cfg, "a", digest, "one", eval.VerdictError)
	b := savedComparisonTrial(t, cfg, "b", digest, "two", eval.VerdictPending)
	a.State, b.State = control.Failed, control.Canceled
	cfg.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": b}}
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	got, err := service(t, cfg).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
	require.NoError(t, err)
	assert.Equal(t, "not_comparable", got.Status)
	assert.Zero(t, got.Baseline.Attempts)
	assert.Zero(t, got.Candidate.Attempts)
	assert.Empty(t, got.Groups)
	assert.Equal(t, []control.ComparisonJob{{JobID: "a", State: control.Failed}}, got.OnlyBaseline)
	assert.Equal(t, []control.ComparisonJob{{JobID: "b", State: control.Canceled}}, got.OnlyCandidate)
}

func TestControlComparisonGroupsRejectIncompatibleReceipts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*eval.PreparedReceipt, *control.Submission)
	}{
		{"task_snapshot", func(receipt *eval.PreparedReceipt, sub *control.Submission) {
			receipt.TaskSnapshot, sub.TaskSnapshot = "another-snapshot", "another-snapshot"
		}},
		{"surface", func(receipt *eval.PreparedReceipt, _ *control.Submission) {
			receipt.SurfaceID = "another-surface"
		}},
		{"binding", func(receipt *eval.PreparedReceipt, _ *control.Submission) {
			receipt.Bindings[0].Version = "2"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config(t)
			digest := cfg.Candidates.Discover().BaselineDigest
			a := savedComparisonTrial(t, cfg, "a", digest, "shared", eval.VerdictPassed)
			b := savedComparisonTrial(t, cfg, "b", digest, "shared", eval.VerdictPassed, tt.change)
			c := savedComparisonTrial(t, cfg, "c", digest, "shared", eval.VerdictPassed)
			cfg.Executor = &jobSet{jobs: map[string]control.JobRecord{"a": a, "b": b, "c": c}}
			cfg.Operations = append(cfg.Operations, control.CompareOperation)
			s := service(t, cfg)
			_, err := s.Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a"}, CandidateJobIDs: []string{"b"}})
			code(t, err, control.Unsupported)
			_, err = s.Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a", "b"}, CandidateJobIDs: []string{"c"}})
			code(t, err, control.Unsupported)
		})
	}
}
