package suite

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/require"
)

func TestK19cContractCalibrationRequiresCompleteV2Evidence(t *testing.T) {
	artifact, source := calibrationFixture(t)
	require.NoError(t, VerifyCalibration(artifact, artifact.Config, source, artifact.ValidationSHA256))
	for _, tc := range []struct {
		name   string
		change func(*CalibrationArtifact)
	}{
		{"missing result", func(a *CalibrationArtifact) { a.Candidates[0].Report.Sensitivity[0].Variants = nil }},
		{"false infeasible", func(a *CalibrationArtifact) {
			a.Candidates[0].Report.Cells[0].Infeasible = "skip"
			a.Candidates[0].Report.Cells[0].Simulations = 0
		}},
		{"legacy", func(a *CalibrationArtifact) { a.Version = 1 }},
		{"missing candidate", func(a *CalibrationArtifact) { a.Candidates = a.Candidates[:2] }},
		{"missing grid cell", func(a *CalibrationArtifact) { a.Candidates[0].Report.Cells = a.Candidates[0].Report.Cells[1:] }},
		{"duplicate grid cell", func(a *CalibrationArtifact) { a.Candidates[0].Report.Cells[0] = a.Candidates[0].Report.Cells[1] }},
		{"reduced simulations", func(a *CalibrationArtifact) { a.Candidates[0].Report.Cells[0].Simulations = 1 }},
		{"missing sensitivity", func(a *CalibrationArtifact) { a.Candidates[0].Report.Sensitivity = nil }},
		{"reduced bootstrap", func(a *CalibrationArtifact) { a.Config.Gate.Policy.Bootstrap.Resamples = 10 }},
		{"source drift", func(a *CalibrationArtifact) { a.SourceSHA256 = strings.Repeat("b", 64) }},
		{"validation drift", func(a *CalibrationArtifact) { a.ValidationSHA256 = strings.Repeat("b", 64) }},
		{"forged selection", func(a *CalibrationArtifact) { a.Selections[0].Margin = .05 }},
		{"missing check cells", func(a *CalibrationArtifact) { a.Checks.Cells = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(artifact)
			require.NoError(t, err)
			var changed CalibrationArtifact
			require.NoError(t, json.Unmarshal(data, &changed))
			tc.change(&changed)
			require.Error(t, VerifyCalibration(changed, artifact.Config, source, artifact.ValidationSHA256))
		})
	}
}

// This synthetic manifest tests structural acceptance; it is never persisted or
// represented as measured production calibration.
func calibrationFixture(t *testing.T) (CalibrationArtifact, SourceManifest) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	source := SourceManifest{Identity: SourceIdentity{SHA256: digest}, BundleSHA256: digest}
	cfg := CalibrationConfig("offline", Validation{Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}}, 4)
	for _, variant := range cfg.Gate.Variants {
		for _, scenario := range cfg.Gate.Scenarios {
			cfg.Qualifying[variant][scenario] = 1
		}
	}
	a := CalibrationArtifact{Version: 2, Configuration: "offline", Config: cfg, SourceSHA256: digest, SourceBundleSHA256: digest, ValidationSHA256: digest, QualificationSource: "offline structural validation; assumed baseline rates"}
	for _, margin := range []float64{.02, .03, .05} {
		report := gate.CalibrationReport{Version: gate.SimulatorVersion, Seed: cfg.Seed, RateSource: "assumed defaults", Qualifying: cfg.Qualifying, Baselines: map[string]gate.Rates{}}
		for _, scenario := range cfg.Gate.Scenarios {
			report.Baselines[scenario] = gate.Rates{Success: .85, Unsupported: .05, Evidence: .9}
		}
		for _, cell := range gate.CalibrationGrid() {
			count, pass := 400, .2
			if cell.CandidateDecisionCell(margin) {
				count, pass = 2000, .01
			}
			if cell.DY == 0 && cell.DU == 0 && cell.DE == 0 && cell.Recall == .9 {
				pass = .9
			}
			report.Cells = append(report.Cells, calibrationResult(cell, count, pass, cfg))
		}
		for _, kappa := range []float64{0, .8} {
			report.Sensitivity = append(report.Sensitivity, calibrationResult(gate.Cell{Delta: .2, Rho: .5, Recall: .9, Kappa: kappa}, 2000, .2, cfg))
		}
		policy := gate.ProposedPolicy(map[string]float64{"receipt_only": margin, "hybrid": margin}, BootstrapSeed)
		var err error
		report.Targets, err = gate.CalibrationTargets(report.Cells, &policy)
		require.NoError(t, err)
		a.Candidates = append(a.Candidates, gate.CandidateReport{Margin: margin, Report: report})
	}
	var err error
	a.Selections, err = gate.SelectCandidates(a.Candidates, cfg.Gate.Variants)
	require.NoError(t, err)
	for _, dy := range []float64{0, -.05} {
		a.Checks.Cells = append(a.Checks.Cells, calibrationResult(gate.Cell{DY: dy, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, 200, .2, cfg))
	}
	a.Checks.Version, a.Checks.Seed, a.Checks.Qualifying = gate.SimulatorVersion, cfg.Seed, cfg.Qualifying
	a.Checks.RateSource = "assumed defaults"
	a.Checks.Baselines = a.Candidates[0].Report.Baselines
	a.Checks.Targets, err = gate.CalibrationTargets(a.Checks.Cells, cfg.Gate.Policy)
	require.NoError(t, err)
	return a, source
}

func calibrationResult(cell gate.Cell, count int, pass float64, cfg gate.CalibrationConfig) gate.CellReport {
	se := math.Sqrt(pass * (1 - pass) / float64(count))
	items := float64(len(cfg.Gate.Scenarios)) * (48 - 36*pass)
	row := gate.CellReport{Cell: cell, Simulations: count, Pass: pass, NotPassed: 1 - pass, SE: se, ExpectedLooks: 3 - 2*pass, ExpectedItems: items, ExpectedAttempts: items * 6, LookPass: [3]float64{pass}, TruncatedPass: [2]float64{pass, pass}, Variants: map[string]gate.VariantSimulation{}, VariantPass: map[string]float64{}, Final: map[string]map[gate.Status]float64{}}
	for _, v := range cfg.Gate.Variants {
		row.Variants[v] = gate.VariantSimulation{Pass: pass, NotPassed: 1 - pass, SE: se, LookPass: [3]float64{pass}, FailureMasks: map[string]float64{}}
		row.VariantPass[v] = pass
		for _, measure := range []string{"success", "unsupported", "evidence", "recall"} {
			row.Final[v+"/"+measure] = map[gate.Status]float64{gate.Pass: pass, gate.Inconclusive: 1 - pass}
		}
	}
	return row
}
