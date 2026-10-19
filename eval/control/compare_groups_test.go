package control_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlComparisonGroupsPoolIndependentTrials(t *testing.T) {
	cfg := config(t)
	baseline := cfg.Candidates.Discover().BaselineDigest
	candidate := comparisonCandidate(t, cfg)
	jobs := &jobSet{jobs: make(map[string]control.JobRecord)}
	for _, trial := range []struct {
		id, digest, item string
		verdict          eval.Verdict
	}{
		{"a1", baseline, "private-case-a", eval.VerdictPassed},
		{"a2", baseline, "private-case-a", eval.VerdictFailed},
		{"b1", baseline, "private-case-b", eval.VerdictPending},
		{"b2", baseline, "private-case-b", eval.VerdictError},
		{"c1", candidate, "private-case-a", eval.VerdictPassed},
		{"c2", candidate, "private-case-a", eval.VerdictPassed},
		{"d1", candidate, "private-case-b", eval.VerdictPassed},
		{"d2", candidate, "private-case-b", eval.VerdictFailed},
	} {
		jobs.jobs[trial.id] = savedComparisonTrial(t, cfg, trial.id, trial.digest, trial.item, trial.verdict, func(receipt *eval.PreparedReceipt, submission *control.Submission) {
			receipt.TaskSnapshot = "private-task-snapshot-" + trial.item
			submission.TaskSnapshot = receipt.TaskSnapshot
			submission.TaskID = "private-task-" + trial.item
			submission.EvaluationPlan = "private-evaluation-plan-" + trial.item
		})
	}
	interrupted := jobs.jobs["b2"]
	interrupted.State = control.Interrupted
	jobs.jobs["b2"] = interrupted
	cfg.Executor = jobs
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	before := comparisonStoreBytes(t, cfg.Experiments.Dir())

	got, err := service(t, cfg).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a1", "b1", "a2", "b2"}, CandidateJobIDs: []string{"d2", "c2", "d1", "c1"}})
	require.NoError(t, err)
	assert.Equal(t, "comparable", got.Status)
	assert.Equal(t, 4, got.Baseline.Attempts)
	assert.Equal(t, 4, got.Baseline.Ran)
	assert.Equal(t, 4, got.Candidate.Attempts)
	assert.Equal(t, &control.QualityCounts{Passed: 1, Failed: 1, Pending: 1, Errored: 1}, got.Baseline.Quality)
	assert.Equal(t, &control.QualityCounts{Passed: 3, Failed: 1}, got.Candidate.Quality)
	require.Len(t, got.Groups, 2)
	groupA := comparisonGroup(t, got, "a1")
	groupB := comparisonGroup(t, got, "b1")
	assert.ElementsMatch(t, []control.ComparisonJob{{JobID: "a1", State: control.Succeeded}, {JobID: "a2", State: control.Succeeded}}, groupA.BaselineJobs)
	assert.ElementsMatch(t, []control.ComparisonJob{{JobID: "c1", State: control.Succeeded}, {JobID: "c2", State: control.Succeeded}}, groupA.CandidateJobs)
	assert.Equal(t, &control.QualityCounts{Passed: 1, Failed: 1}, groupA.Baseline.Quality)
	assert.Equal(t, &control.QualityCounts{Passed: 2}, groupA.Candidate.Quality)
	assert.ElementsMatch(t, []control.ComparisonJob{{JobID: "b1", State: control.Succeeded}, {JobID: "b2", State: control.Interrupted}}, groupB.BaselineJobs)
	assert.ElementsMatch(t, []control.ComparisonJob{{JobID: "d1", State: control.Succeeded}, {JobID: "d2", State: control.Succeeded}}, groupB.CandidateJobs)
	assert.Equal(t, &control.QualityCounts{Pending: 1, Errored: 1}, groupB.Baseline.Quality)
	assert.Empty(t, got.OnlyBaseline)
	assert.Empty(t, got.OnlyCandidate)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	for _, hidden := range []string{"private-", "task-v1", "evaluation-v1", baseline, candidate, cfg.Experiments.Dir()} {
		assert.NotContains(t, string(encoded), hidden)
	}
	assert.Equal(t, before, comparisonStoreBytes(t, cfg.Experiments.Dir()))
	for _, job := range jobs.jobs {
		stored, err := cfg.Experiments.Load(job.ExperimentID)
		require.NoError(t, err)
		require.Len(t, stored.Attempts, 1)
		assert.Equal(t, 1, stored.Attempts[0].Repeat)
		assert.Equal(t, job.Attempt, stored.Attempts[0].AttemptKey)
	}
}

func TestControlComparisonGroupsUseCaseIntersection(t *testing.T) {
	cfg := config(t)
	baseline := cfg.Candidates.Discover().BaselineDigest
	candidate := comparisonCandidate(t, cfg)
	jobs := &jobSet{jobs: make(map[string]control.JobRecord)}
	jobs.jobs["a1"] = savedComparisonTrial(t, cfg, "a1", baseline, "shared", eval.VerdictPassed)
	jobs.jobs["a2"] = savedComparisonTrial(t, cfg, "a2", baseline, "shared", eval.VerdictFailed)
	jobs.jobs["b1"] = savedComparisonTrial(t, cfg, "b1", baseline, "baseline-only", eval.VerdictError)
	jobs.jobs["c1"] = savedComparisonTrial(t, cfg, "c1", candidate, "shared", eval.VerdictPassed)
	jobs.jobs["d1"] = savedComparisonTrial(t, cfg, "d1", candidate, "candidate-only", eval.VerdictPending)
	cfg.Executor = jobs
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	got, err := service(t, cfg).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a1", "a2", "b1"}, CandidateJobIDs: []string{"c1", "d1"}})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Baseline.Attempts)
	assert.Equal(t, 1, got.Candidate.Attempts)
	assert.Equal(t, &control.QualityCounts{Passed: 1, Failed: 1}, got.Baseline.Quality)
	assert.Equal(t, &control.QualityCounts{Passed: 1}, got.Candidate.Quality)
	require.Len(t, got.Groups, 1)
	assert.ElementsMatch(t, []control.ComparisonJob{{JobID: "a1", State: control.Succeeded}, {JobID: "a2", State: control.Succeeded}}, got.Groups[0].BaselineJobs)
	assert.Equal(t, []control.ComparisonJob{{JobID: "c1", State: control.Succeeded}}, got.Groups[0].CandidateJobs)
	assert.Equal(t, []control.ComparisonJob{{JobID: "b1", State: control.Succeeded}}, got.OnlyBaseline)
	assert.Equal(t, []control.ComparisonJob{{JobID: "d1", State: control.Succeeded}}, got.OnlyCandidate)
}

func TestControlComparisonGroupsNeverInferProbeQuality(t *testing.T) {
	for _, candidateVerdict := range []eval.Verdict{eval.VerdictUnassessed, eval.VerdictPassed} {
		t.Run(string(candidateVerdict), func(t *testing.T) {
			cfg := config(t)
			baseline := cfg.Candidates.Discover().BaselineDigest
			candidate := comparisonCandidate(t, cfg)
			jobs := &jobSet{jobs: make(map[string]control.JobRecord)}
			jobs.jobs["a1"] = savedComparisonTrial(t, cfg, "a1", baseline, "shared", eval.VerdictUnassessed)
			jobs.jobs["a2"] = savedComparisonTrial(t, cfg, "a2", baseline, "shared", eval.VerdictUnassessed)
			jobs.jobs["b1"] = savedComparisonTrial(t, cfg, "b1", candidate, "shared", candidateVerdict)
			cfg.Executor = jobs
			cfg.Operations = append(cfg.Operations, control.CompareOperation)
			got, err := service(t, cfg).Compare(t.Context(), control.CompareRequest{BaselineJobIDs: []string{"a1", "a2"}, CandidateJobIDs: []string{"b1"}})
			require.NoError(t, err)
			assert.Equal(t, "not_comparable", got.Status)
			assert.Nil(t, got.Baseline.Quality)
			assert.Nil(t, got.Candidate.Quality)
			assert.Equal(t, 2, got.Baseline.Ran)
			assert.Equal(t, 1, got.Candidate.Ran)
			require.Len(t, got.Groups, 1)
			assert.Nil(t, got.Groups[0].Baseline.Quality)
			assert.Nil(t, got.Groups[0].Candidate.Quality)
		})
	}
}
