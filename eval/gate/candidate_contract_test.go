package gate

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"slices"
	"testing"
)

func TestK19cContractCandidateGridAndCommonOutcomes(t *testing.T) {
	grid := CalibrationGrid()
	require.Len(t, grid, 1620)
	for _, u := range []float64{0, .01, .02, .03, .05} {
		assert.True(t, slices.ContainsFunc(grid, func(c Cell) bool { return c.DU == u }))
	}
	for _, margin := range []float64{.02, .03, .05} {
		own := Cell{DU: margin, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}
		assert.True(t, own.CandidateDecisionCell(margin))
		other := own
		other.DU = .01
		assert.False(t, other.CandidateDecisionCell(margin))
	}
	cfg := CalibrationConfig{Gate: policyConfig(), Seed: 10, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	cell := Cell{DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}
	seed := splitmix(cfg.Seed ^ cellSeed(cell) ^ splitmix(7+0x100000000))
	a, err := synthetic(cfg, cell, seed)
	require.NoError(t, err)
	cfg.Gate.Policy.Unsupported = map[string]float64{"receipt_only": .05, "hybrid": .05}
	b, err := synthetic(cfg, cell, seed)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	other := cell
	other.DU = .05
	assert.NotEqual(t, cellSeed(cell), cellSeed(other))
}
func TestK19cContractSimulatorUsesVariantSchedule(t *testing.T) {
	cfg := policyConfig()
	out := CellReport{VariantPass: map[string]float64{}, Final: map[string]map[Status]float64{}}
	calls := []Config{}
	evaluator := func(_ Table, c Config, look int) (Evaluation, error) {
		calls = append(calls, c)
		e := decisionEvidence(t)
		e.Look = look
		if look == 1 {
			e.Variants[0].Success.Status = Fail
			e.Variants[1].Recall.Status = Inconclusive
		} else {
			e.Variants = e.Variants[1:]
		}
		return e, nil
	}
	err := simulateLooks(testTable(48), cfg, &out, evaluator)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"hybrid"}, calls[1].Variants)
	assert.InDelta(t, 1.0, out.Variants["hybrid"].Pass, 1e-12)
	assert.InDelta(t, 1.0, out.Variants["receipt_only"].LookFail[0], 1e-12)
	// Two scenarios, two repeats: first12*(baseline+both), next12*(baseline+hybrid).
	assert.InDelta(t, float64((12*3+12*2)*2*2), out.ExpectedAttempts, 1e-12)
	assert.Zero(t, out.Pass, "partial success never becomes combined PASS")
}
func candidateFixture(margin float64) CandidateReport {
	candidate := CandidateReport{Margin: margin}
	for _, delta := range []float64{.1, .2, .3} {
		for _, rho := range []float64{.3, .5, .7} {
			for _, axis := range []string{measureNull, measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
				c := Cell{Delta: delta, Rho: rho, Recall: .9, Kappa: .5}
				p := .04
				switch axis {
				case measureNull:
					p = .85
				case measureSuccess:
					c.DY = -.05
				case measureUnsupported:
					c.DU = margin
				case measureEvidence:
					c.DE = -.05
				case measureRecall:
					c.Recall = .8
				}
				candidate.Report.Cells = append(candidate.Report.Cells, CellReport{Cell: c, Simulations: 2000, Variants: map[string]VariantSimulation{"receipt_only": {Pass: p}, "hybrid": {Pass: p}}})
			}
		}
	}
	return candidate
}
func TestK19cContractCandidateSelection(t *testing.T) {
	a, b, c := candidateFixture(.02), candidateFixture(.03), candidateFixture(.05)
	for i := range a.Report.Cells {
		v := a.Report.Cells[i].Variants["hybrid"]
		v.Pass = .5
		a.Report.Cells[i].Variants["hybrid"] = v
	}
	selected, err := SelectCandidates([]CandidateReport{c, a, b}, []string{"receipt_only", "hybrid"})
	require.NoError(t, err)
	require.Len(t, selected, 2)
	assert.InDelta(t, .02, selected[0].Margin, 1e-12)
	assert.InDelta(t, .03, selected[1].Margin, 1e-12)
	for i := range b.Report.Cells {
		b.Report.Cells[i].Infeasible = "boundary unavailable"
	}
	for i := range c.Report.Cells {
		v := c.Report.Cells[i].Variants["hybrid"]
		v.Pass = .5
		c.Report.Cells[i].Variants["hybrid"] = v
	}
	selected, err = SelectCandidates([]CandidateReport{a, b, c}, []string{"receipt_only", "hybrid"})
	require.NoError(t, err)
	assert.Equal(t, CalibrationIneligible, selected[1].Status)
	a.Report.Cells = a.Report.Cells[:len(a.Report.Cells)-1]
	_, err = SelectCandidates([]CandidateReport{a, b, c}, []string{"receipt_only", "hybrid"})
	require.Error(t, err, "missing required cell is malformed evidence")
}
