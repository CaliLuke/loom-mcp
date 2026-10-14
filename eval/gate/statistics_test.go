package gate

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	policy := ProposedPolicy(map[string]float64{"receipt_only": .02, "hybrid": .02}, 42)
	return Config{Policy: &policy, Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Scenarios: []string{"a", "b"}, Repeats: 2}
}
func testTable(n int) Table {
	var out Table
	for _, s := range []string{"a", "b"} {
		for i := 0; i < n; i++ {
			for r := 1; r <= 2; r++ {
				for _, v := range []string{"baseline", "receipt_only", "hybrid"} {
					out = append(out, Row{Key: Key{v, s, fmtItem(i), r}, Success: true, Evidence: true, Qualifying: v != "baseline", Recalled: true})
				}
			}
		}
	}
	return out
}
func fmtItem(i int) string {
	return strconv.Itoa(i)
}
func TestBootstrapAndPairing(t *testing.T) {
	lo, hi := PercentileIndices(10000, 0.05/3)
	assert.Equal(t, 83, lo)
	assert.Equal(t, 9916, hi)
	cfg := testConfig()
	rows := testTable(12)
	got, err := Evaluate(rows, cfg, 1)
	require.NoError(t, err)
	assert.Empty(t, got.Missing)
	assert.Equal(t, Interval{Estimate: 0, Lo: 0, Hi: 0}, got.Variants[0].Success.Interval)
	rows[1].Infrastructure = true
	got, err = Evaluate(rows, cfg, 1)
	require.NoError(t, err)
	assert.Equal(t, []Key{rows[1].Key}, got.Missing)
	rows[1].Infrastructure = false
	rows[1].RunFailed = true
	got, err = Evaluate(rows, cfg, 1)
	require.NoError(t, err)
	assert.Empty(t, got.Missing)
	assert.Less(t, got.Variants[0].Success.Estimate, 0.0)
	rows[1].Repeat = 3
	_, err = Evaluate(rows, cfg, 1)
	require.Error(t, err)
}
func TestWilsonDeff(t *testing.T) {
	rows := testTable(12)
	got := Recall(rows, "hybrid", []string{"a"}, 0.05/3)
	assert.Equal(t, 24, got.N)
	assert.InDelta(t, 2.0, got.Deff, 1e-12)
	assert.InDelta(t, 12.0, got.EffectiveN, 1e-12)
	assert.InDelta(t, 0.6767, got.Lo, 0.001)
	empty := Recall(nil, "hybrid", []string{"a"}, 0.05/3)
	assert.Equal(t, "no qualifying runs", empty.Reason)
	assert.False(t, math.IsNaN(empty.Lo))
}
func TestCriteriaBoundaries(t *testing.T) {
	rows := testTable(12)
	for i := range rows {
		if rows[i].Variant != "baseline" {
			rows[i].Success = false
			rows[i].Evidence = false
			rows[i].Unsupported = true
			rows[i].Recalled = false
		}
	}
	evaluated, err := Evaluate(rows, testConfig(), 1)
	require.NoError(t, err)
	v := evaluated.Variants[0]
	for _, tc := range []struct {
		name   string
		result CriterionResult
	}{
		{"success", v.Success},
		{"unsupported", v.Unsupported},
		{"evidence", v.Evidence},
		{"recall", v.Recall.CriterionResult},
	} {
		t.Run(tc.name+"_interval_fail", func(t *testing.T) {
			assert.Equal(t, Fail, tc.result.Status, "interval: %+v", tc.result.Interval)
		})
	}
	for _, higher := range []bool{true, false} {
		for _, bound := range []float64{-0.05, 0.02, 0.8} {
			assert.Equal(t, Inconclusive, Criterion(Interval{Lo: bound, Hi: bound}, bound, higher).Status)
			if higher {
				assert.Equal(t, Pass, Criterion(Interval{Lo: bound + 0.01, Hi: bound + 0.02}, bound, higher).Status)
			} else {
				assert.Equal(t, Pass, Criterion(Interval{Lo: bound - 0.02, Hi: bound - 0.01}, bound, higher).Status)
			}
		}
	}
}
func TestLooks(t *testing.T) {
	cfg := testConfig()
	e, err := Evaluate(testTable(48), cfg, 2)
	require.NoError(t, err)
	d, err := DecideMode(e, nil, Offline)
	require.NoError(t, err)
	assert.Equal(t, Complete, d.Status)
	for _, v := range d.Variants {
		assert.Equal(t, Pass, v.Status)
	}
	_, err = Decide(e, []Decision{d})
	require.Error(t, err)
	e.Variants[0].Success.Status = Inconclusive
	e.Look = 3
	d, err = DecideMode(e, nil, Offline)
	require.NoError(t, err)
	assert.Equal(t, NotPassed, d.Variants[0].Status)
	assert.Contains(t, d.Variants[0].Reason, "precision")
	missing := Key{Variant: "hybrid", Scenario: "a", Item: "missing", Repeat: 1}
	e.Variants[1].Missing = []Key{missing}
	d, err = DecideMode(e, nil, Offline)
	require.NoError(t, err)
	assert.Equal(t, Incomplete, d.Status)
	assert.Equal(t, []Key{missing}, d.Rerun)
	e.Variants[1].Missing = nil
	e.Variants[1].ScenarioIntervals["a"] = Interval{Estimate: -.3, Lo: -.4, Hi: -.2}
	e.Look = 1
	d, err = Decide(e, nil)
	require.NoError(t, err)
	assert.Equal(t, Fail, d.Variants[1].Status)
	assert.Equal(t, Continue, d.Variants[0].Status)
	assert.Equal(t, Continue, d.Status)
}
