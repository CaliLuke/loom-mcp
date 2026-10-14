package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// CalibrationArtifact binds production results and the independently reproducible check cells.
type CalibrationArtifact struct {
	Pilot               *PilotEvidence            `json:"pilot_evidence,omitempty"`
	Version             int                       `json:"version"`
	Configuration       string                    `json:"configuration"`
	Candidates          []gate.CandidateReport    `json:"candidates,omitempty"`
	Selections          []gate.CandidateSelection `json:"selections,omitempty"`
	SourceSHA256        string                    `json:"source_sha256,omitempty"`
	SourceBundleSHA256  string                    `json:"source_bundle_sha256,omitempty"`
	ValidationSHA256    string                    `json:"validation_sha256,omitempty"`
	Config              gate.CalibrationConfig    `json:"config"`
	Report              gate.CalibrationReport    `json:"report"`
	Checks              gate.CalibrationReport    `json:"checks"`
	QualificationSource string                    `json:"qualification_source"`
}

// Calibrate computes the production report once, then persists it and its text rendering.
func Calibrate(ctx context.Context, dir, config string, cfg gate.CalibrationConfig, pilot *PilotEvidence, phase func(string)) error {
	phase("verify inputs")
	source, err := ReadSource(ctx, dir, config)
	if err != nil {
		return err
	}
	validation, err := ReadValidationFor(dir, config)
	if err != nil {
		return err
	}
	expected := CalibrationConfig(config, validation, cfg.Workers)
	if pilot != nil {
		expected.Pilot, err = VerifyPilot(dir, config, *pilot, validation.Profile, source.Identity.SHA256)
		if err != nil {
			return err
		}
	}
	if validation.SourceSHA256 != source.Identity.SHA256 || !reflect.DeepEqual(expected, cfg) || (config != "offline" && cfg.Pilot == nil) {
		return fmt.Errorf("calibration requires source-bound validation and complete rate inputs")
	}
	validationBytes, err := os.ReadFile(ValidationPath(dir, config))
	if err != nil {
		return err
	}
	if err := VerifyEvidenceExecutable(ctx, dir); err != nil {
		return err
	}
	if _, err := os.Stat(CalibrationPath(dir, config)); err == nil {
		return fmt.Errorf("calibration already exists; verify retained evidence before reuse")
	} else if !os.IsNotExist(err) {
		return err
	}
	phase("check cells")
	checks, err := gate.CalibrateChecks(ctx, cfg)
	if err != nil {
		return err
	}
	artifact := CalibrationArtifact{Version: 2, Configuration: config, Config: cfg, Checks: checks, Pilot: pilot, QualificationSource: "offline structural validation; assumed baseline rates", SourceSHA256: source.Identity.SHA256, SourceBundleSHA256: source.BundleSHA256, ValidationSHA256: gate.Digest(validationBytes)}
	if cfg.Pilot != nil {
		artifact.QualificationSource = "offline structural validation; complete live baseline pilot"
	}
	phase("candidate grid and sensitivity")
	artifact.Candidates, err = gate.CalibrateCandidates(ctx, cfg)
	if err != nil {
		return err
	}
	phase("select candidates")
	artifact.Selections, err = gate.SelectCandidates(artifact.Candidates, cfg.Gate.Variants)
	if err != nil {
		return err
	}
	phase("verify source and evidence")
	after, err := ReadSource(ctx, dir, config)
	if err != nil {
		return err
	}
	currentValidation, err := os.ReadFile(ValidationPath(dir, config))
	if err != nil {
		return err
	}
	if after.SHA256 != source.SHA256 || gate.Digest(currentValidation) != artifact.ValidationSHA256 {
		return fmt.Errorf("source or validation changed during calibration")
	}
	if err := VerifyCalibration(artifact, cfg, source, artifact.ValidationSHA256); err != nil {
		return err
	}
	phase("write artifacts")
	return WriteCalibration(dir, config, artifact)
}

// WriteCalibration writes report artifacts once from simulation results.
// Reporting-only migrations can use this in a new directory without sampling.
func WriteCalibration(dir, config string, artifact CalibrationArtifact) error {
	path := CalibrationPath(dir, config)
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := writeOnce(path, append(data, '\n')); err != nil {
		return err
	}
	return writeOnce(strings.TrimSuffix(path, ".json")+".txt", []byte(CalibrationText(config, artifact)))
}

// CalibrationText renders saved simulation outcomes without running models or sampling.
func CalibrationText(config string, artifact CalibrationArtifact) string {
	if artifact.Version == 2 {
		return calibrationV2Text(config, artifact)
	}
	var text strings.Builder
	report := artifact.Report
	fmt.Fprintf(&text, "Configuration: %s\nQualification: %s\nRate source: %s\nSimulator: %s\nCells: %d (+ %d sensitivity)\n", config, artifact.QualificationSource, report.RateSource, gate.SimulatorVersion, len(report.Cells), len(report.Sensitivity))
	for _, target := range report.Targets {
		fmt.Fprintln(&text, target)
	}
	fmt.Fprintln(&text, "cell,dY,dU,dE,discordance,rho,recall,kappa,n,pass,mc_se,expected_looks,expected_items,not_passed")
	for i, c := range report.Cells {
		fmt.Fprintf(&text, "%d,%g,%g,%g,%g,%g,%g,%g,%d,%g,%g,%g,%g,%g\n", i, c.Cell.DY, c.Cell.DU, c.Cell.DE, c.Cell.Delta, c.Cell.Rho, c.Cell.Recall, c.Cell.Kappa, c.Simulations, c.Pass, c.SE, c.ExpectedLooks, c.ExpectedItems, c.NotPassed)
	}
	return text.String()
}

func calibrationV2Text(config string, artifact CalibrationArtifact) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Configuration: %s\nEvidence: %s\nSimulator: %s\nSource: %s\nValidation: %s\n", config, artifact.QualificationSource, gate.SimulatorVersion, artifact.SourceSHA256, artifact.ValidationSHA256)
	for _, selection := range artifact.Selections {
		fmt.Fprintf(&text, "%s: %s; selected margin=%g; worst null PASS=%g; boundary PASS=%v\n", selection.Variant, selection.Status, selection.Margin, selection.WorstNull, selection.WorstBoundary)
	}
	for _, candidate := range artifact.Candidates {
		fmt.Fprintf(&text, "\nCandidate margin: %g; cells: %d; sensitivity: %d\n", candidate.Margin, len(candidate.Report.Cells), len(candidate.Report.Sensitivity))
		for _, target := range candidate.Report.Targets {
			fmt.Fprintln(&text, target)
		}
		fmt.Fprintln(&text, "cell,variant,dY,dU,dE,discordance,rho,recall,kappa,n,pass,mc_se,expected_looks,expected_items,expected_attempts,not_passed,guard_stop,criterion_stop,infeasible")
		for i, cell := range append(candidate.Report.Cells[:len(candidate.Report.Cells):len(candidate.Report.Cells)], candidate.Report.Sensitivity...) {
			for _, variant := range artifact.Config.Gate.Variants {
				v := cell.Variants[variant]
				fmt.Fprintf(&text, "%d,%s,%g,%g,%g,%g,%g,%g,%g,%d,%g,%g,%g,%g,%g,%g,%g,%g,%q\n", i, variant, cell.Cell.DY, cell.Cell.DU, cell.Cell.DE, cell.Cell.Delta, cell.Cell.Rho, cell.Cell.Recall, cell.Cell.Kappa, cell.Simulations, v.Pass, v.SE, cell.ExpectedLooks, cell.ExpectedItems, cell.ExpectedAttempts, v.NotPassed, v.GuardStop, v.CriterionStop, cell.Infeasible)
			}
		}
	}
	return text.String()
}

// CalibrationPath names the committed assumed-rate report or an additional configuration report.
func CalibrationPath(dir, config string) string {
	if config == "offline" {
		config = "default"
	}
	return filepath.Join(dir, "gate", "calibration_"+config+".json")
}
