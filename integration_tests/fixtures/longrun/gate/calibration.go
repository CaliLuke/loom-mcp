package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// CalibrationArtifact binds production results and the independently reproducible check cells.
type CalibrationArtifact struct {
	Config              gate.CalibrationConfig `json:"config"`
	Report              gate.CalibrationReport `json:"report"`
	Checks              gate.CalibrationReport `json:"checks"`
	QualificationSource string                 `json:"qualification_source"`
}

// ReadValidation refuses q values derived from different pools or materialisation.
func ReadValidation(dir string) (Validation, error) {
	var v Validation
	b, err := readFile(dir, "gate/validation.json")
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	in, pools, err := Inputs(dir)
	if err != nil {
		return v, err
	}
	data, err := json.Marshal(pools)
	if err != nil {
		return v, err
	}
	if v.PoolsSHA256 != gate.Digest(data) || v.MaterializerVersion != in.MaterializerVersion {
		return v, fmt.Errorf("validation is stale; rerun validate")
	}
	if len(v.Failures) > 0 {
		return v, fmt.Errorf("structural validation failed; production calibration is blocked")
	}
	return v, nil
}

// Calibrate computes the production report once, then persists it and its text rendering.
func Calibrate(ctx context.Context, dir, config string, cfg gate.CalibrationConfig) error {
	checks, err := gate.CalibrateChecks(ctx, cfg)
	if err != nil {
		return err
	}
	artifact := CalibrationArtifact{Config: cfg, Checks: checks, QualificationSource: "offline validate"}
	artifact.Report, err = gate.Calibrate(ctx, cfg)
	if err != nil {
		return err
	}
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

// CalibrationPath names the committed assumed-rate report or an additional configuration report.
func CalibrationPath(dir, config string) string {
	if config == "offline" {
		config = "default"
	}
	return filepath.Join(dir, "gate", "calibration_"+config+".json")
}
