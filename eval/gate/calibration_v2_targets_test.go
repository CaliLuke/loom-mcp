package gate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cContractCandidateTargetsUseOwnBoundaryAndVariant(t *testing.T) {
	for _, margin := range []float64{.03, .05} {
		candidate := candidateFixture(margin)
		for i := range candidate.Report.Cells {
			row := &candidate.Report.Cells[i]
			a := row.Variants["receipt_only"]
			a.SE = .004
			a.GuardStop = .12
			a.CriterionStop = .08
			a.FailureMasks = map[string]float64{"guard:a+success": .03}
			row.Variants["receipt_only"] = a
			b := row.Variants["hybrid"]
			b.Pass = .50
			row.Variants["hybrid"] = b
		}
		// The old .02 boundary is deliberately adverse and must not be used.
		candidate.Report.Cells = append(candidate.Report.Cells, CellReport{Cell: Cell{DU: .02, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, Simulations: 400, Variants: map[string]VariantSimulation{"receipt_only": {Pass: .99}, "hybrid": {Pass: .99}}})
		p := ProposedPolicy(map[string]float64{"receipt_only": margin, "hybrid": margin}, 42)
		lines, err := CalibrationTargets(candidate.Report.Cells, &p)
		require.NoError(t, err)
		got := strings.Join(lines, "\n")
		assert.Contains(t, got, "receipt_only unsupported margin PASS <= 0.0500: measured 0.0400")
		assert.Contains(t, got, "hybrid unsupported margin PASS <= 0.0500: measured 0.5000")
		assert.Contains(t, got, "receipt_only null PASS >= 0.8000: measured 0.8500")
		assert.Contains(t, got, "hybrid null PASS >= 0.8000: measured 0.5000")
		assert.Contains(t, got, "MC SE 0.0040..0.0040")
		assert.Contains(t, got, "measured guard stop 0.1200..0.1200")
		assert.Contains(t, got, "criterion stop 0.0800..0.0800")
		assert.Contains(t, got, "guard:a+success=0.0300..0.0300")
		assert.NotContains(t, got, "lower-bound")
		assert.NotContains(t, got, "whole-gate")
	}
}
