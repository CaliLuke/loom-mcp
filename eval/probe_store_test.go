package eval

import (
	"bytes"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storedProbeFixture() *Experiment {
	r := preparedTestReceipt()
	a := &StoredAttempt{AttemptKey: AttemptKey{Variant: "base", Scenario: "chat", Item: "one", Repeat: 1}, ExecutionKind: ExecutionProbe, Prepared: &r, Probe: &ProbeStatus{Execution: "succeeded", Delivery: "not_declared", Evidence: "complete", Quality: "not_assessed"}, Verdict: VerdictUnassessed}
	a.Key = a.AttemptKey.String()
	return &Experiment{Header: ExperimentHeader{Format: experimentFormat, Version: 2, ID: reportFixtureID, ExecutionKind: ExecutionProbe, Status: statusComplete, Variants: []StoredVariant{{ID: "base"}}, Scenarios: []ScenarioInfo{{ID: "chat", Items: []string{"one"}}}, Planned: 1}, Attempts: []*StoredAttempt{a}}
}

func TestProbeStoredProjections(t *testing.T) {
	e := storedProbeFixture()
	quality := CheckQualityStrict(e)
	assert.False(t, quality.Passed)
	assert.Contains(t, quality.Reasons, "quality_not_assessed")
	_, err := e.RerunSelection()
	require.ErrorIs(t, err, ErrProbeRerunUnsupported)
	for _, render := range []struct {
		name string
		call func(*bytes.Buffer) error
	}{{"summary", func(b *bytes.Buffer) error {
		return Summary(b, e)
	}}, {"summary_json", func(b *bytes.Buffer) error {
		return SummaryJSON(b, e)
	}}, {"show", func(b *bytes.Buffer) error {
		return Show(b, e, e.Attempts[0], ShowOptions{})
	}}, {"show_json", func(b *bytes.Buffer) error {
		return ShowJSON(b, e, e.Attempts[0], ShowOptions{})
	}}} {
		t.Run(render.name, func(t *testing.T) {
			var b bytes.Buffer
			require.NoError(t, render.call(&b))
			assert.Contains(t, b.String(), "not_assessed")
			assert.NotContains(t, b.String(), "0/1 passed")
			if strings.HasSuffix(render.name, "json") {
				assert.Contains(t, b.String(), `"version":2`)
			}
			if render.name == "summary_json" {
				assert.Contains(t, b.String(), `"failing_attempts":[]`)
			}
		})
	}
	// Empty/running probes must still refuse quality assessment.
	e.Attempts, e.Header.Status = nil, statusRunning
	assert.Contains(t, CheckQualityStrict(e).Reasons, "quality_not_assessed")
}

func TestProbeStoredComparison(t *testing.T) {
	probe := storedProbeFixture()
	evaluation := storedProbeFixture()
	evaluation.Header.ID = "0199d4a0-0000-7000-8000-000000000015"
	evaluation.Header.Version, evaluation.Header.ExecutionKind = 1, ""
	evaluation.Attempts[0].ExecutionKind, evaluation.Attempts[0].Probe = "", nil
	evaluation.Attempts[0].Verdict = VerdictPassed
	evaluation.Attempts[0].Checks = []expect.Check{{Name: "quality", Passed: true}}
	for _, sides := range [][2]*Experiment{{evaluation, probe}, {probe, evaluation}} {
		c, err := Compare(Side{Experiment: sides[0]}, Side{Experiment: sides[1]}, CompareOptions{})
		require.NoError(t, err)
		require.Len(t, c.Groups, 1)
		assert.Equal(t, statusNotComparable, c.Groups[0].Status)
		assert.Empty(t, c.Groups[0].Checks)
		assert.Empty(t, c.Groups[0].Claims)
		assert.Empty(t, c.Groups[0].Scores)
		var b bytes.Buffer
		require.NoError(t, WriteComparisonsJSON(&b, []*Comparison{c}))
		assert.Contains(t, b.String(), `"version":2`)
		_, err = Compare(Side{Experiment: sides[0]}, Side{Experiment: sides[1]}, CompareOptions{Parent: evaluation})
		require.Error(t, err)
	}
	stats := groupStats(probe.Attempts)
	assert.Equal(t, 1, stats.Probes)
	assert.Equal(t, 1, stats.Unassessed)
}

func TestProbeStoredEmptyComparison(t *testing.T) {
	base := storedProbeFixture()
	cand := storedProbeFixture()
	cand.Header.ID = "0199d4a0-0000-7000-8000-000000000015"
	base.Attempts, cand.Attempts = nil, nil
	base.Header.Status, cand.Header.Status = statusRunning, statusRunning
	comparison, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
	require.NoError(t, err)
	var text, data bytes.Buffer
	require.NoError(t, WriteComparisons(&text, []*Comparison{comparison}))
	assert.Contains(t, text.String(), "quality not_assessed")
	assert.NotContains(t, text.String(), "0/0 passed")
	require.NoError(t, WriteComparisonsJSON(&data, []*Comparison{comparison}))
	assert.Contains(t, data.String(), `"version":2`)
	assert.Contains(t, data.String(), `"quality":"not_assessed"`)
	text.Reset()
	data.Reset()
	require.NoError(t, Summary(&text, cand))
	assert.Contains(t, text.String(), "not_assessed")
	require.NoError(t, SummaryJSON(&data, cand))
	assert.Contains(t, data.String(), `"quality":"not_assessed"`)
}

func TestProbeStoreRoundTrip(t *testing.T) {
	s, w, plan := preparedTestWriter(t, ExecutionProbe)
	_, err := w.reservePrepared(plan.Keys[0])
	require.NoError(t, err)
	receipt := preparedTestReceipt()
	require.NoError(t, w.writePreparedReceipt(plan.Keys[0], receipt))
	r := &AttemptRecord{AttemptKey: plan.Keys[0], ExecutionKind: ExecutionProbe, Prepared: &receipt, Probe: &ProbeStatus{Execution: probeExecutionSucceeded, Delivery: probeDeliveryNotDeclared, Evidence: probeStatusComplete, Quality: probeQualityNotAssessed}, Outcome: &Outcome{}}
	require.NoError(t, w.Append(r))
	require.NoError(t, w.Finish(&Report{ExecutionKind: ExecutionProbe, Attempts: []*AttemptRecord{r}}, nil))
	e, err := s.Load(w.ID())
	require.NoError(t, err)
	assert.Equal(t, ExecutionProbe, e.Header.ExecutionKind)
	assert.Equal(t, VerdictUnassessed, e.Attempts[0].Verdict)
	assert.Equal(t, r.Probe, e.Attempts[0].Probe)
	assert.Contains(t, CheckQualityStrict(e).Reasons, "quality_not_assessed")
}
