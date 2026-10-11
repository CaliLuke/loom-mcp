package eval

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareIntersectionAndDeclarationOrder(t *testing.T) {
	base := comparisonExperiment("baseline", "base", "a", "b")
	cand := comparisonExperiment("candidate", "changed", "b", "c")
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, []Pair{{Scenario: "chat", Item: "a"}}, c.OnlyBaseline)
	assert.Equal(t, []Pair{{Scenario: "chat", Item: "c"}}, c.OnlyCandidate)
	require.Len(t, c.Groups, 1)
	assert.Equal(t, "b", c.Groups[0].Item)
	assert.Equal(t, 1, c.Totals.Baseline.Ran)
	assert.Equal(t, 1, c.Totals.Candidate.Ran)
	before := comparisonJSON(t, []*Comparison{c})
	slices.Reverse(base.Attempts)
	slices.Reverse(cand.Attempts)
	again, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, before, comparisonJSON(t, []*Comparison{again}))
}

func TestCompareVerdictsAndMedians(t *testing.T) {
	for _, tc := range []struct {
		name       string
		base, cand []Verdict
		status     string
	}{
		{"improved", []Verdict{VerdictPassed, VerdictPassed, VerdictPassed, VerdictFailed, VerdictFailed}, []Verdict{VerdictPassed, VerdictPassed, VerdictPassed, VerdictPassed, VerdictPassed}, "improved"},
		{"regressed", []Verdict{VerdictPassed, VerdictPassed}, []Verdict{VerdictPassed, VerdictPassed, VerdictPassed, VerdictFailed}, "regressed"},
		{"unchanged", []Verdict{VerdictPassed, VerdictFailed}, []Verdict{VerdictPassed, VerdictPassed, VerdictFailed, VerdictFailed}, "unchanged"},
		{"not run", []Verdict{VerdictNotRun}, []Verdict{VerdictPassed}, "not_comparable"},
		{"pending", []Verdict{VerdictPassed}, []Verdict{VerdictPending}, "regressed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cand := repeatedExperiment("baseline", tc.base), repeatedExperiment("candidate", tc.cand)
			c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.status, c.Groups[0].Status)
		})
	}
	e := repeatedExperiment("baseline", []Verdict{VerdictPassed, VerdictPassed, VerdictNotRun})
	e.Attempts[0].Tokens, e.Attempts[1].Tokens, e.Attempts[2].Tokens = 3, 4, 100
	e.Attempts[0].Duration, e.Attempts[1].Duration = 3*time.Nanosecond, 4*time.Nanosecond
	c, err := Compare(Side{Experiment: e}, Side{Experiment: comparisonExperiment("candidate", "base", "one")}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, 3, c.Groups[0].Baseline.MedianTokens)
	assert.Equal(t, 3*time.Nanosecond, c.Totals.Baseline.MedianDuration)
}

func TestCompareDeltas(t *testing.T) {
	base := repeatedExperiment("baseline", []Verdict{VerdictPassed, VerdictPassed})
	cand := repeatedExperiment("candidate", []Verdict{VerdictFailed, VerdictFailed})
	for _, a := range base.Attempts {
		a.Checks = []expect.Check{{Name: "correct", Passed: true}}
		a.Claims = []StoredClaim{{ID: "args", Subject: "tool_args", Tool: "lookup"}}
		a.Judgments = []StoredJudgment{{ClaimID: "args", Subject: "tool_args", Label: "entailed", Step: &StoredJudgedStep{Tool: "lookup"}}, {ClaimID: "args", Subject: "tool_args", Label: "entailed", Step: &StoredJudgedStep{Index: 1, Tool: "lookup"}}}
		a.Scores = []expect.Score{{Name: "quality", Value: 1}, {Name: "same", Value: 0.5}}
	}
	threshold := 0.8
	for _, a := range cand.Attempts {
		a.Checks = []expect.Check{{Name: "correct", Diagnostic: "wrong arguments"}}
		a.Claims = base.Attempts[0].Claims
		a.Judgments = []StoredJudgment{{ClaimID: "args", Subject: "tool_args", Label: "contradicted", Rationale: "missing id", Truncated: true, Uncalibrated: true, Step: &StoredJudgedStep{Tool: "lookup"}}, {ClaimID: "args", Subject: "tool_args", Label: "entailed", Step: &StoredJudgedStep{Index: 1, Tool: "lookup"}}}
		a.Scores = []expect.Score{{Name: "quality", Value: 0.5, Threshold: &threshold}, {Name: "same", Value: 0.5}}
	}
	// Reversed storage order must still sample the lowest repeat.
	cand.Attempts[1].Checks[0].Diagnostic = "later failure"
	slices.Reverse(cand.Attempts)
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	g := c.Groups[0]
	assert.True(t, g.Uncalibrated)
	assert.True(t, g.Truncated)
	require.Len(t, g.Checks, 1)
	assert.Equal(t, CheckCounts{Passed: 2, Total: 2}, g.Checks[0].Baseline)
	assert.Equal(t, CheckCounts{Total: 2}, g.Checks[0].Candidate)
	assert.Equal(t, "wrong arguments", g.Checks[0].Diagnostic)
	require.Len(t, g.Claims, 1)
	assert.Equal(t, 4, g.Claims[0].Baseline.Entailed)
	assert.Equal(t, 2, g.Claims[0].Candidate.Entailed)
	assert.Equal(t, 4, g.Claims[0].Candidate.Total)
	assert.Equal(t, 2, g.Claims[0].Candidate.Truncated)
	assert.Equal(t, "missing id", g.Claims[0].Rationale)
	require.Len(t, g.Scores, 1)
	assert.Equal(t, "quality", g.Scores[0].Name)
	assert.InDelta(t, 0.5, *g.Scores[0].Candidate, 0)
	var text bytes.Buffer
	require.NoError(t, WriteComparisons(&text, []*Comparison{c}))
	reportGolden(t, "compare", text.String())
	first := comparisonJSON(t, []*Comparison{c})
	second := comparisonJSON(t, []*Comparison{c})
	assert.Equal(t, first, second)
	improved, err := Compare(Side{Experiment: cand}, Side{Experiment: base}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, "wrong arguments", improved.Groups[0].Checks[0].Diagnostic)
}

func TestCompareVariants(t *testing.T) {
	e := comparisonExperiment("variants", "a", "one")
	addReportVariant(e, "b", VerdictFailed)
	addReportVariant(e, "c", VerdictPending)
	cs, err := CompareVariants(e, []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, cs, 2)
	assert.Equal(t, "b", cs[0].Candidate.Variant)
	assert.Equal(t, "c", cs[1].Candidate.Variant)
	var text bytes.Buffer
	require.NoError(t, WriteComparisons(&text, cs))
	reportGolden(t, "variants", text.String())
	other := comparisonExperiment("other", "b", "one")
	addReportVariant(other, "c", VerdictPassed)
	e.Header.Variants = e.Header.Variants[:2]
	cs, err = CompareExperiments(e, other, CompareOptions{})
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, "b", cs[0].Baseline.Variant)
	assert.Equal(t, 2, countWarnings(cs[0], "variant_unmatched"))
	_, err = CompareExperiments(e, e, CompareOptions{})
	require.Error(t, err)
	_, err = Compare(Side{Experiment: e, Variant: "a"}, Side{Experiment: e, Variant: "b"}, CompareOptions{})
	require.Error(t, err)
	_, err = CompareVariants(e, []string{"a"})
	require.Error(t, err)
	_, err = CompareVariants(e, []string{"a", "a"})
	require.Error(t, err)
	_, err = CompareVariants(e, []string{"a", "missing"})
	require.Error(t, err)
}

func TestCompareMergeParent(t *testing.T) {
	parent := comparisonExperiment("parent", "base", "x", "y")
	rerun := comparisonExperiment("rerun", "base", "x")
	rerun.Header.Parent = "parent"
	rerun.Attempts[0].Verdict = VerdictFailed
	base := comparisonExperiment("baseline", "base", "x", "y")
	c, err := Compare(Side{Experiment: base}, Side{Experiment: rerun}, CompareOptions{Parent: parent})
	require.NoError(t, err)
	require.Len(t, c.Groups, 2)
	assert.Equal(t, 0, c.Groups[0].Candidate.Passed)
	assert.Equal(t, 1, c.Groups[1].Candidate.Passed)
	assert.Len(t, rerun.Attempts, 1)
	assert.Equal(t, VerdictPassed, parent.Attempts[0].Verdict)
	_, err = Compare(Side{Experiment: base}, Side{Experiment: rerun}, CompareOptions{Parent: base})
	require.Error(t, err)
	standalone, err := Compare(Side{Experiment: base}, Side{Experiment: rerun}, CompareOptions{})
	require.NoError(t, err)
	assert.Len(t, standalone.Groups, 1)
}

func TestCompareUnavailableTools(t *testing.T) {
	base := comparisonExperiment("baseline", "base", "one")
	cand := comparisonExperiment("candidate", "base", "one")
	cand.Attempts[0].StorageError = "omitted"
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, c.Groups[0].Candidate.ToolsUnavailable)
	assert.Nil(t, c.Groups[0].Tools)
	require.Error(t, WriteComparisons(failedReportWriter{}, []*Comparison{c}))
	require.Error(t, WriteComparisonsJSON(failedReportWriter{}, []*Comparison{c}))
}

func comparisonExperiment(id, variant string, items ...string) *Experiment {
	e := &Experiment{Header: ExperimentHeader{ID: id, Label: id, Status: statusComplete, Planned: len(items), Variants: []StoredVariant{{ID: variant}}, Scenarios: []ScenarioInfo{{ID: "chat", DatasetHash: "same", Items: items}}, Fingerprints: map[string]Fingerprint{variant: {SystemHashes: []string{"system"}}}}}
	for i, item := range items {
		key := AttemptKey{Variant: variant, Scenario: "chat", Item: item, Repeat: 1}
		e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: key, Key: key.String(), Index: i, Verdict: VerdictPassed})
	}
	return e
}
func repeatedExperiment(id string, verdicts []Verdict) *Experiment {
	e := comparisonExperiment(id, "base")
	e.Header.Scenarios[0].Items = []string{"one"}
	e.Header.Planned = len(verdicts)
	for i, verdict := range verdicts {
		key := AttemptKey{Variant: "base", Scenario: "chat", Item: "one", Repeat: i + 1}
		e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: key, Key: key.String(), Index: i, Verdict: verdict})
	}
	return e
}
func addReportVariant(e *Experiment, variant string, verdict Verdict) {
	e.Header.Variants = append(e.Header.Variants, StoredVariant{ID: variant})
	key := AttemptKey{Variant: variant, Scenario: "chat", Item: "one", Repeat: 1}
	e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: key, Key: key.String(), Index: len(e.Attempts), Verdict: verdict})
	e.Header.Planned++
}
func comparisonJSON(t *testing.T, cs []*Comparison) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, WriteComparisonsJSON(&out, cs))
	return out.String()
}
func countWarnings(c *Comparison, code string) int {
	n := 0
	for _, w := range c.Warnings {
		if w.Code == code {
			n++
		}
	}
	return n
}

func TestCompareStoredToolSequences(t *testing.T) {
	fixture := loadReportFixture(t)
	require.Len(t, fixture.Attempts, 4)
	base := comparisonExperiment("baseline", "base", "one")
	cand := comparisonExperiment("candidate", "base", "one")
	base.Attempts[0].Runs = fixture.Attempts[1].Runs
	cand.Attempts[0].Runs = fixture.Attempts[2].Runs
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	require.NotNil(t, c.Groups[0].Tools)
	assert.Equal(t, []ToolEdit{{Op: "-", Name: "report.refund"}}, c.Groups[0].Tools.Edits)
	assert.Equal(t, ToolSequence{Sequence: []string{"report.lookup", "report.refund"}, Count: 1, Total: 1}, c.Groups[0].Tools.Baseline)
	session, err := fixture.Attempts[3].Session()
	require.NoError(t, err)
	assert.Equal(t, []string{"delegate.child", "report.lookup"}, toolSequence(session))
}

func TestCompareEqualFractionsAndLegacySubjects(t *testing.T) {
	base := repeatedExperiment("baseline", []Verdict{VerdictFailed})
	cand := repeatedExperiment("candidate", []Verdict{VerdictFailed, VerdictFailed})
	for _, e := range []*Experiment{base, cand} {
		for _, a := range e.Attempts {
			a.Checks = []expect.Check{{Name: "same", Passed: true}}
			a.Claims = []StoredClaim{{ID: "truth"}}
			a.Judgments = []StoredJudgment{{ClaimID: "truth", Label: "entailed"}, {ClaimID: "truth", Error: "offline"}}
		}
	}
	c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	assert.Empty(t, c.Groups[0].Checks)
	assert.Empty(t, c.Groups[0].Claims)
	cand.Attempts[0].Judgments[0].Label = "contradicted"
	c, err = Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	require.Len(t, c.Groups[0].Claims, 1)
	assert.Equal(t, "answer", c.Groups[0].Claims[0].Subject)
	assert.Equal(t, 1, c.Groups[0].Claims[0].Baseline.Errors)
	assert.Equal(t, 2, c.Groups[0].Claims[0].Candidate.Errors)
}

func TestCompareGroupsPermutation(t *testing.T) {
	base := comparisonExperiment("baseline", "base", "a", "b", "c")
	cand := comparisonExperiment("candidate", "base", "c", "a", "b")
	initial, err := CompareExperiments(base, cand, CompareOptions{})
	require.NoError(t, err)
	require.Len(t, initial, 1)
	assert.Equal(t, []string{"c", "a", "b"}, []string{initial[0].Groups[0].Item, initial[0].Groups[1].Item, initial[0].Groups[2].Item})
	before := comparisonJSON(t, initial)
	slices.Reverse(base.Attempts)
	slices.Reverse(cand.Attempts)
	after, err := CompareExperiments(base, cand, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, before, comparisonJSON(t, after))
}
