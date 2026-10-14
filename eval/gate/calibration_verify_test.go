package gate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Contract test: stored calibration must retain complete, internally consistent
// measured results and the actual population metadata. No simulations are rerun
// by the verifier itself.
func TestK19cCalibrationReportIntegrity(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 190019, Workers: 1, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	report, err := calibrate(t.Context(), cfg, []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}, 4, Evaluate)
	require.NoError(t, err)
	require.NoError(t, VerifyCalibrationReport(report, cfg))
	for _, tc := range []struct {
		name   string
		change func(*CalibrationReport)
	}{
		{"baseline", func(r *CalibrationReport) { r.Baselines = nil }},
		{"rate source", func(r *CalibrationReport) { r.RateSource = "pilot" }},
		{"qualifying", func(r *CalibrationReport) { r.Qualifying = nil }},
		{"targets", func(r *CalibrationReport) { r.Targets = nil }},
		{"variants", func(r *CalibrationReport) { r.Cells[0].Variants = nil }},
		{"missing uncertainty", func(r *CalibrationReport) {
			v := r.Cells[0].Variants["hybrid"]
			v.SE = 99
			r.Cells[0].Variants["hybrid"] = v
		}},
		{"missing cost", func(r *CalibrationReport) { r.Cells[0].ExpectedAttempts = 0 }},
		{"missing terminal", func(r *CalibrationReport) { r.Cells[0].Final = nil }},
		{"false infeasible", func(r *CalibrationReport) { r.Cells[0].Infeasible = "skip"; r.Cells[0].Simulations = 0 }},
		{"cause mismatch", func(r *CalibrationReport) {
			v := r.Cells[0].Variants["hybrid"]
			v.GuardStop = 1
			v.CriterionStop = 1
			v.FailureMasks = map[string]float64{}
			r.Cells[0].Variants["hybrid"] = v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(report)
			require.NoError(t, err)
			var changed CalibrationReport
			require.NoError(t, json.Unmarshal(data, &changed))
			tc.change(&changed)
			require.Error(t, VerifyCalibrationReport(changed, cfg))
		})
	}
}
