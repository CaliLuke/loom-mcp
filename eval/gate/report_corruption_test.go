package gate

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoredCalibrationRejectsCorruptOutcomeAccounting(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 190019, Workers: 1, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	// Four deterministic samples provide a valid small report. The test checks
	// stored-report integrity; it does not perform calibration qualification.
	report, err := calibrate(t.Context(), cfg, []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}, 4, Evaluate)
	require.NoError(t, err)
	require.NoError(t, VerifyCalibrationReport(report, cfg))
	original, err := json.Marshal(report)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		edit func(*CellReport)
	}{
		{"pass outside probability", func(r *CellReport) {
			r.Pass = 2
		}},
		{"not passed outside probability", func(r *CellReport) {
			r.NotPassed = -1
		}},
		{"combined uncertainty", func(r *CellReport) {
			r.SE = math.NaN()
		}},
		{"combined look pass", func(r *CellReport) {
			r.LookPass[0] = -1
		}},
		{"combined look fail", func(r *CellReport) {
			r.LookFail[0] = 2
		}},
		{"truncated probability", func(r *CellReport) {
			r.TruncatedPass[0] = 2
		}},
		{"combined outcome sum", func(r *CellReport) {
			r.LookPass = [3]float64{1, 1, 1}
			r.TruncatedPass = [2]float64{1, 2}
		}},
		{"unknown variant", func(r *CellReport) {
			r.VariantPass["unknown"] = 0
		}},
		{"missing named variant", func(r *CellReport) {
			v := r.Variants["hybrid"]
			delete(r.Variants, "hybrid")
			r.Variants["unknown"] = v
		}},
		{"variant pass mismatch", func(r *CellReport) {
			r.VariantPass["hybrid"] = 2
		}},
		{"unknown criterion status", func(r *CellReport) {
			r.Final["hybrid/success"] = map[Status]float64{"unknown": 1}
		}},
		{"criterion probability", func(r *CellReport) {
			r.Final["hybrid/success"] = map[Status]float64{Pass: 2}
		}},
		{"criterion missing mass", func(r *CellReport) {
			r.Final["hybrid/success"] = map[Status]float64{Pass: .5}
		}},
		{"look cost", func(r *CellReport) {
			r.ExpectedLooks = 100
		}},
		{"item cost", func(r *CellReport) {
			r.ExpectedItems = -1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered CalibrationReport
			require.NoError(t, json.Unmarshal(original, &altered))
			tc.edit(&altered.Cells[0])
			require.Error(t, VerifyCalibrationReport(altered, cfg))
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*VariantSimulation)
	}{
		{"negative look", func(v *VariantSimulation) {
			v.LookPass[0] = -1
		}},
		{"large look failure", func(v *VariantSimulation) {
			v.LookFail[0] = 2
		}},
		{"incomplete outcomes", func(v *VariantSimulation) {
			v.Pass = 0
			v.SE = 0
			v.NotPassed = 0
			v.LookPass = [3]float64{}
			v.LookFail = [3]float64{}
		}},
		{"unsorted causes", func(v *VariantSimulation) {
			v.FailureMasks = map[string]float64{"success+guard": 1}
		}},
		{"duplicate cause", func(v *VariantSimulation) {
			v.FailureMasks = map[string]float64{"guard+guard": 1}
		}},
		{"unknown cause", func(v *VariantSimulation) {
			v.FailureMasks = map[string]float64{"unknown": 1}
		}},
		{"invalid mask probability", func(v *VariantSimulation) {
			v.FailureMasks = map[string]float64{"guard": 2}
		}},
		{"guard accounting", func(v *VariantSimulation) {
			v.FailureMasks = map[string]float64{"guard": 1}
			v.GuardStop = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered CalibrationReport
			require.NoError(t, json.Unmarshal(original, &altered))
			v := VariantSimulation{LookFail: [3]float64{1}, GuardStop: 1, FailureMasks: map[string]float64{"guard": 1}}
			tc.edit(&v)
			altered.Cells[0].Variants["hybrid"] = v
			require.Error(t, VerifyCalibrationReport(altered, cfg))
		})
	}
}
