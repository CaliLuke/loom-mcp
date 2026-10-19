package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These contract tests require the new CompareTrials API before package execution.
func TestCompareTrialsPoolsIndependentAttempts(t *testing.T) {
	b1 := comparisonExperiment("base-1", "first", "a")
	b2 := comparisonExperiment("base-2", "second", "a")
	c1 := comparisonExperiment("cand-1", "third", "a")
	c2 := comparisonExperiment("cand-2", "fourth", "b")
	b1.Attempts[0].Tokens = 10
	b2.Attempts[0].Tokens = 30
	b2.Attempts[0].Verdict = VerdictPending
	before := *b2.Attempts[0]
	got, err := CompareTrials([]Side{{Experiment: b1}, {Experiment: b2}}, []Side{{Experiment: c1}, {Experiment: c2}})
	require.NoError(t, err)
	require.Len(t, got.Groups, 1)
	assert.Equal(t, 2, got.Totals.Baseline.Attempts)
	assert.Equal(t, 1, got.Totals.Baseline.Pending)
	assert.Equal(t, 20, got.Totals.Baseline.MedianTokens)
	assert.Equal(t, 1, got.Totals.Candidate.Attempts)
	assert.Equal(t, []Pair{{Scenario: "chat", Item: "b"}}, got.OnlyCandidate)
	assert.Equal(t, before, *b2.Attempts[0])
	assert.Equal(t, 1, b1.Attempts[0].Repeat)
	assert.Equal(t, 1, b2.Attempts[0].Repeat)
}

func TestCompareTrialsPreservesWarningsFromEverySource(t *testing.T) {
	b := comparisonExperiment("base", "base", "a")
	c1 := comparisonExperiment("cand-1", "first", "a")
	c2 := comparisonExperiment("cand-2", "second", "a")
	c2.Header.Judge = &JudgeInfo{ModelID: "changed", Calibrated: false}
	c2.Header.Variants[0].Policy = nil
	c2.Header.Fingerprints = map[string]Fingerprint{"second": {SystemHashes: []string{"different"}}}
	c2.Attempts[0].Judgments = []StoredJudgment{{ClaimID: "claim", Truncated: true}}
	got, err := CompareTrials([]Side{{Experiment: b}}, []Side{{Experiment: c1}, {Experiment: c2}})
	require.NoError(t, err)
	codes := make([]string, 0, len(got.Warnings))
	for _, w := range got.Warnings {
		codes = append(codes, w.Code)
	}
	assert.Contains(t, codes, "judge_changed")
	assert.Contains(t, codes, "judge_uncalibrated")
	assert.Contains(t, codes, "judgments_truncated")
	assert.NotContains(t, codes, "no_agent_change")
}

func TestCompareTrialsRejectsRepeatedSource(t *testing.T) {
	b := comparisonExperiment("base", "base", "a")
	c := comparisonExperiment("candidate", "candidate", "a")
	_, err := CompareTrials([]Side{{Experiment: b}, {Experiment: b}}, []Side{{Experiment: c}})
	require.Error(t, err)
}
