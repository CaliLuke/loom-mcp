package eval_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
)

func TestCheckQualityStrict(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		planned   int
		attempts  []*eval.StoredAttempt
		truncated bool
		passed    bool
		reason    string
		counts    eval.QualityCounts
	}{
		{name: "all passed", status: "complete", planned: 2, attempts: qualityAttempts(eval.VerdictPassed, eval.VerdictPassed), passed: true, counts: eval.QualityCounts{Passed: 2}},
		{name: "failed", status: "complete", planned: 1, attempts: qualityAttempts(eval.VerdictFailed), reason: "failed_verdict", counts: eval.QualityCounts{Failed: 1}},
		{name: "error", status: "complete", planned: 1, attempts: qualityAttempts(eval.VerdictError), reason: "error_verdict", counts: eval.QualityCounts{Error: 1}},
		{name: "pending", status: "complete", planned: 1, attempts: qualityAttempts(eval.VerdictPending), reason: "pending_verdict", counts: eval.QualityCounts{Pending: 1}},
		{name: "not run", status: "complete", planned: 1, attempts: qualityAttempts(eval.VerdictNotRun), reason: "not_run_verdict", counts: eval.QualityCounts{NotRun: 1}},
		{name: "running", status: "running", planned: 1, attempts: qualityAttempts(eval.VerdictPassed), reason: "incomplete_status", counts: eval.QualityCounts{Passed: 1}},
		{name: "stopped", status: "stopped", planned: 1, attempts: qualityAttempts(eval.VerdictPassed), reason: "incomplete_status", counts: eval.QualityCounts{Passed: 1}},
		{name: "empty", status: "complete", planned: 0, reason: "no_attempts"},
		{name: "missing planned row", status: "complete", planned: 2, attempts: qualityAttempts(eval.VerdictPassed), reason: "incomplete_plan", counts: eval.QualityCounts{Passed: 1}},
		{name: "index gap", status: "complete", planned: 2, attempts: []*eval.StoredAttempt{{Index: 0, Verdict: eval.VerdictPassed}, {Index: 2, Verdict: eval.VerdictPassed}}, reason: "incomplete_plan", counts: eval.QualityCounts{Passed: 2}},
		{name: "torn tail", status: "complete", planned: 1, attempts: qualityAttempts(eval.VerdictPassed), truncated: true, reason: "truncated_tail", counts: eval.QualityCounts{Passed: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &eval.Experiment{Header: eval.ExperimentHeader{Status: tc.status, Planned: tc.planned}, Attempts: tc.attempts, TruncatedTail: tc.truncated}
			result := eval.CheckQualityStrict(e)
			assert.Equal(t, tc.passed, result.Passed)
			assert.Equal(t, tc.counts, result.Counts)
			assert.Equal(t, len(tc.attempts), result.Attempts)
			if tc.reason == "" {
				assert.Empty(t, result.Reasons)
			} else {
				assert.Contains(t, result.Reasons, tc.reason)
			}
		})
	}
}

func qualityAttempts(verdicts ...eval.Verdict) []*eval.StoredAttempt {
	attempts := make([]*eval.StoredAttempt, len(verdicts))
	for i, verdict := range verdicts {
		attempts[i] = &eval.StoredAttempt{Index: i, Verdict: verdict}
	}
	return attempts
}
