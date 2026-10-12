package gate

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapGoldenAndEqualWeights(t *testing.T) {
	cfg := testConfig()
	rows := slices.DeleteFunc(testTable(4), func(r Row) bool {
		return r.Scenario == "a" && (r.Item == "2" || r.Item == "3")
	})
	for i := range rows {
		r := &rows[i]
		if r.Variant == "baseline" {
			r.Success = r.Item != "3"
		} else {
			r.Success = r.Scenario == "b" || r.Item != "0" || r.Repeat == 2
			r.Evidence = r.Success
			r.Unsupported = !r.Success
		}
	}
	e, err := Evaluate(rows, cfg, 1)
	require.NoError(t, err)
	// D_a = (-.5 + 0)/2 = -.25; D_b = (0 + 0 + 0 + 1)/4 = .25.
	// Equal scenario weight gives 0; item weight would give 1/12.
	assert.Equal(t, map[string]float64{"a": -.25, "b": .25}, e.Variants[0].Scenarios)
	assert.InDelta(t, 0, e.Variants[0].Success.Estimate, 1e-12)
	assert.InDelta(t, -.25, e.Variants[0].Success.Lo, 1e-12)
	assert.InDelta(t, .375, e.Variants[0].Success.Hi, 1e-12)
	assert.Equal(t, e.Variants[0].Success, e.Variants[1].Success)
	duplicated := append(Table(nil), rows...)
	for _, r := range rows {
		if r.Scenario == "a" {
			r.Item += "copy"
			duplicated = append(duplicated, r)
		}
	}
	d, err := Evaluate(duplicated, cfg, 1)
	require.NoError(t, err)
	assert.InDelta(t, e.Variants[0].Success.Estimate, d.Variants[0].Success.Estimate, 1e-12)
	reverse := append(Table(nil), rows...)
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	same, err := Evaluate(reverse, cfg, 1)
	require.NoError(t, err)
	assert.Equal(t, e, same)
}
func TestGuardBoundaryAndMissingRepeat(t *testing.T) {
	for _, failed := range []int{10, 11} {
		rows := testTable(100)
		for i := range rows {
			r := &rows[i]
			var id int
			n, err := fmt.Sscan(r.Item, &id)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			if r.Scenario == "a" && r.Variant != "baseline" && id < failed {
				r.Success = false
			}
		}
		cfg := testConfig()
		cfg.Scenarios = []string{"a", "b", "c", "d"}
		for _, row := range append(Table(nil), rows...) {
			if row.Scenario == "b" {
				row.Scenario = "c"
				rows = append(rows, row)
				row.Scenario = "d"
				rows = append(rows, row)
			}
		}
		e, err := Evaluate(rows, cfg, 1)
		require.NoError(t, err)
		assert.Equal(t, Pass, e.Variants[0].Success.Status)
		if failed == 10 {
			assert.Empty(t, e.GuardFailures)
		} else {
			assert.Len(t, e.GuardFailures, 2)
			d, err := Decide(e, nil)
			require.NoError(t, err)
			assert.Equal(t, Fail, d.Status)
		}
	}
	rows := testTable(2)
	missing := rows[1].Key
	rows = append(rows[:1], rows[2:]...)
	e, err := Evaluate(rows, testConfig(), 1)
	require.NoError(t, err)
	assert.Contains(t, e.Missing, missing)
}
func TestRecallAgreementClamp(t *testing.T) {
	rows := testTable(12)
	for i := range rows {
		rows[i].Recalled = rows[i].Repeat == 1
	}
	r := Recall(rows, "hybrid", []string{"a"}, .05/3)
	assert.Zero(t, r.Rho)
	assert.InDelta(t, 1, r.Deff, 1e-12)
	assert.InDelta(t, .28045, r.Lo, .001)
	assert.InDelta(t, .71955, r.Hi, .001)
}

func TestBootstrapPreservesScenarioStrata(t *testing.T) {
	rows := testTable(4)
	for i := range rows {
		r := &rows[i]
		r.Success = (r.Variant == "baseline") == (r.Scenario == "a")
		r.Evidence = r.Success
		r.Unsupported = !r.Success
	}
	// Every item has delta -1 in a and +1 in b (opposite for unsupported).
	// A within-scenario draw is therefore exactly zero every time. Mixing the
	// strata introduces variability even though the point estimate remains zero.
	got, err := Evaluate(rows, testConfig(), 1)
	require.NoError(t, err)
	for _, v := range got.Variants {
		for _, interval := range []Interval{v.Success.Interval, v.Evidence.Interval, v.Unsupported.Interval} {
			assert.Equal(t, Interval{Estimate: 0, Lo: 0, Hi: 0}, interval)
		}
	}
}
