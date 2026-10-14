package suite

import (
	"fmt"
	"reflect"
	"slices"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// VerifyCalibration requires complete production counts and bound v2 inputs.
func VerifyCalibration(artifact CalibrationArtifact, cfg gate.CalibrationConfig, source SourceManifest, validationSHA string) error {
	if artifact.Version != 2 || validateConfigurationName(artifact.Configuration) != nil || len(artifact.Report.Cells) != 0 {
		return fmt.Errorf("calibration requires unambiguous v2 candidate evidence")
	}
	if !sourceHex(validationSHA, 32) || !sourceHex(source.Identity.SHA256, 32) || !sourceHex(source.BundleSHA256, 32) || artifact.SourceSHA256 != source.Identity.SHA256 || artifact.SourceBundleSHA256 != source.BundleSHA256 || artifact.ValidationSHA256 != validationSHA {
		return fmt.Errorf("calibration provenance mismatch")
	}
	if err := cfg.Gate.Policy.Validate([]string{"receipt_only", "hybrid"}); err != nil {
		return err
	}
	if cfg.Seed != 190019 || cfg.Gate.Policy.Bootstrap.Seed != BootstrapSeed || !slices.Equal(cfg.Gate.Scenarios, env.GatedScenarios(artifact.Configuration)) || !slices.Equal(cfg.Gate.Variants, []string{"receipt_only", "hybrid"}) {
		return fmt.Errorf("calibration configuration differs from the fixed design")
	}
	expected, actual := cfg, artifact.Config
	expected.Workers, actual.Workers = 0, 0
	if !reflect.DeepEqual(expected, actual) || (artifact.Configuration != "offline" && cfg.Pilot == nil) {
		return fmt.Errorf("calibration inputs differ or lack a live pilot")
	}
	if (cfg.Pilot == nil) != (artifact.Pilot == nil) {
		return fmt.Errorf("calibration rate source lacks its pilot provenance")
	}
	if len(artifact.Candidates) != 3 {
		return fmt.Errorf("calibration requires all three candidates")
	}
	grid := gate.CalibrationGrid()
	sensitivity := []gate.Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: 0}, {Delta: .2, Rho: .5, Recall: .9, Kappa: .8}}
	for i, candidate := range artifact.Candidates {
		if candidate.Margin != []float64{.02, .03, .05}[i] {
			return fmt.Errorf("calibration candidate order or margin differs")
		}
		candidateConfig := cfg
		policy := gate.ProposedPolicy(map[string]float64{"receipt_only": candidate.Margin, "hybrid": candidate.Margin}, BootstrapSeed)
		candidateConfig.Gate.Policy = &policy
		if err := gate.VerifyCalibrationReport(candidate.Report, candidateConfig); err != nil {
			return err
		}
		if err := verifyCalibrationRows(candidate.Report.Cells, grid, candidate.Margin, 0); err != nil {
			return err
		}
		if err := verifyCalibrationRows(candidate.Report.Sensitivity, sensitivity, candidate.Margin, 0); err != nil {
			return err
		}
	}
	if err := gate.VerifyCalibrationReport(artifact.Checks, cfg); err != nil {
		return err
	}
	checks := []gate.Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, {DY: -.05, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}
	if err := verifyCalibrationRows(artifact.Checks.Cells, checks, .02, 200); err != nil {
		return err
	}
	selections, err := gate.SelectCandidates(artifact.Candidates, cfg.Gate.Variants)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(selections, artifact.Selections) {
		return fmt.Errorf("calibration selections differ from measured candidates")
	}
	return nil
}

func verifyCalibrationRows(rows []gate.CellReport, expected []gate.Cell, margin float64, fixedCount int) error {
	if len(rows) != len(expected) {
		return fmt.Errorf("calibration is missing required cells")
	}
	for i, row := range rows {
		count := fixedCount
		if count == 0 {
			count = 400
			if expected[i].CandidateDecisionCell(margin) {
				count = 2000
			}
		}
		if row.Infeasible != "" {
			count = 0
		}
		if row.Cell != expected[i] || row.Simulations != count {
			return fmt.Errorf("calibration cell %d has mismatched population or simulation count", i)
		}
	}
	return nil
}
