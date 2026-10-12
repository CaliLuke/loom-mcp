package eval

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareVariantTotalsUseEachIntersection(t *testing.T) {
	e := comparisonExperiment("variants", "base", "one", "two")
	e.Attempts[1].Verdict = VerdictFailed
	e.Attempts[0].Tokens, e.Attempts[1].Tokens = 10, 30
	addReportVariant(e, "first", VerdictFailed)
	addReportVariant(e, "second", VerdictPassed)
	e.Attempts[3].Item = "two"
	cs, err := CompareVariants(e, []string{"base", "first", "second"})
	require.NoError(t, err)
	assert.Equal(t, 1, cs[0].Totals.Baseline.Passed)
	assert.Equal(t, 0, cs[1].Totals.Baseline.Passed)
	var out bytes.Buffer
	require.NoError(t, WriteComparisons(&out, cs))
	assert.Contains(t, out.String(), "base (vs first) 1/1 passed; median tokens 10")
	assert.Contains(t, out.String(), "base (vs second) 0/1 passed; median tokens 30")
}

func TestCompareCalibrationFlagsStayWithJudgments(t *testing.T) {
	base := comparisonExperiment("baseline", "base", "unjudged", "calibrated", "uncalibrated")
	cand := comparisonExperiment("candidate", "base", "unjudged", "calibrated", "uncalibrated")
	cand.Header.Judge = &JudgeInfo{Calibrated: false}
	for i := 1; i < 3; i++ {
		base.Attempts[i].Claims = []StoredClaim{{ID: "truth"}}
		cand.Attempts[i].Claims = []StoredClaim{{ID: "truth"}}
		base.Attempts[i].Judgments = []StoredJudgment{{ClaimID: "truth", Label: "entailed"}}
		cand.Attempts[i].Judgments = []StoredJudgment{{ClaimID: "truth", Label: "contradicted", Uncalibrated: i == 2}}
		cand.Attempts[i].Verdict = VerdictFailed
	}
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, countWarnings(c, "judge_uncalibrated"))
	assert.False(t, c.Groups[0].Uncalibrated)
	assert.False(t, c.Groups[1].Uncalibrated)
	assert.False(t, c.Groups[1].Claims[0].Uncalibrated)
	assert.True(t, c.Groups[2].Uncalibrated)
	assert.True(t, c.Groups[2].Claims[0].Uncalibrated)
}

func TestCompareMergeParentIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		torn     bool
		warnings int
	}{
		{"complete", statusComplete, false, 0},
		{"running", statusRunning, false, 1},
		{"torn tail", statusComplete, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := comparisonExperiment("baseline", "base", "one", "two")
			parent := comparisonExperiment("parent", "base", "one", "two")
			parent.Header.Status, parent.TruncatedTail = tc.status, tc.torn
			cand := comparisonExperiment("candidate", "base", "one")
			cand.Header.Parent = parent.Header.ID
			c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{Parent: parent})
			require.NoError(t, err)
			assert.Equal(t, tc.warnings, countWarnings(c, "incomplete"))
			assert.True(t, c.Candidate.Complete, "candidate identity describes the standalone rerun")
			standalone, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
			require.NoError(t, err)
			assert.Zero(t, countWarnings(standalone, "incomplete"))
		})
	}
}
