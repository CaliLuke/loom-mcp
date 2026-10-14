package gate

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// VerifyCalibrationReport checks population identity and complete measured
// result consistency without sampling or constructing a provider. Callers must
// additionally require their planned cells and simulation counts.
func VerifyCalibrationReport(report CalibrationReport, cfg CalibrationConfig) error {
	if err := validateCalibration(cfg); err != nil {
		return err
	}
	rates := make(map[string]Rates, len(cfg.Gate.Scenarios))
	for _, scenario := range cfg.Gate.Scenarios {
		rates[scenario] = baselineRates(cfg, scenario)
	}
	source := assumedRateSource
	if cfg.Pilot != nil {
		source = pilotRateSource
	}
	if report.Version != SimulatorVersion || report.Seed != cfg.Seed || report.RateSource != source || !reflect.DeepEqual(report.Baselines, rates) || !reflect.DeepEqual(report.Qualifying, cfg.Qualifying) {
		return fmt.Errorf("gate: calibration population identity differs")
	}
	for _, rows := range [][]CellReport{report.Cells, report.Sensitivity} {
		for i, row := range rows {
			if err := verifyCalibrationCell(row, cfg); err != nil {
				return fmt.Errorf("gate: calibration cell %d: %w", i, err)
			}
		}
	}
	targets, err := CalibrationTargets(report.Cells, cfg.Gate.Policy)
	if err != nil {
		return err
	}
	if !slices.Equal(targets, report.Targets) {
		return fmt.Errorf("gate: calibration targets differ from measured outcomes")
	}
	return nil
}

func verifyCalibrationCell(row CellReport, cfg CalibrationConfig) error {
	if reason := infeasible(cfg, row.Cell); reason != "" {
		want := CellReport{Cell: row.Cell, Infeasible: reason, VariantPass: map[string]float64{}, Final: map[string]map[Status]float64{}}
		if !reflect.DeepEqual(row, want) {
			return fmt.Errorf("invalid infeasible population result")
		}
		return nil
	}
	if row.Infeasible != "" || row.Simulations < 1 || len(row.Variants) != len(cfg.Gate.Variants) || len(row.Final) != 4*len(cfg.Gate.Variants) {
		return fmt.Errorf("missing feasible simulation outcomes")
	}
	if !probability(row.Pass) || !probability(row.NotPassed) || !closeSimulation(row.SE, math.Sqrt(row.Pass*(1-row.Pass)/float64(row.Simulations))) {
		return fmt.Errorf("invalid combined probability or uncertainty")
	}
	pass, fail := 0.0, 0.0
	for i := range 3 {
		if !probability(row.LookPass[i]) || !probability(row.LookFail[i]) {
			return fmt.Errorf("invalid combined look outcomes")
		}
		pass += row.LookPass[i]
		fail += row.LookFail[i]
		if i < 2 && !closeSimulation(pass, row.TruncatedPass[i]) {
			return fmt.Errorf("inconsistent truncated PASS")
		}
	}
	if !closeSimulation(pass, row.Pass) || pass+fail > 1+1e-10 {
		return fmt.Errorf("inconsistent combined outcomes")
	}
	if err := verifyCalibrationVariants(row, cfg.Gate.Variants); err != nil {
		return err
	}
	return verifySimulationCost(row, cfg)
}

func verifyVariantSimulation(v VariantSimulation, n int) error {
	if !probability(v.Pass) || !probability(v.NotPassed) || !probability(v.GuardStop) || !probability(v.CriterionStop) || !closeSimulation(v.SE, math.Sqrt(v.Pass*(1-v.Pass)/float64(n))) || v.FailureMasks == nil {
		return fmt.Errorf("invalid variant probability, uncertainty or causes")
	}
	pass, fail := 0.0, 0.0
	for i := range 3 {
		if !probability(v.LookPass[i]) || !probability(v.LookFail[i]) {
			return fmt.Errorf("invalid variant look outcomes")
		}
		pass += v.LookPass[i]
		fail += v.LookFail[i]
	}
	if !closeSimulation(pass, v.Pass) || !closeSimulation(pass+fail+v.NotPassed, 1) {
		return fmt.Errorf("incomplete variant outcomes")
	}
	return verifyFailureMasks(v, fail)
}

// verifyFailureMasks reconciles joint failure causes with terminal FAIL counts.
func verifyFailureMasks(v VariantSimulation, fail float64) error {
	masks, guard, criterion := 0.0, 0.0, 0.0
	for mask, p := range v.FailureMasks {
		parts := strings.Split(mask, "+")
		if !probability(p) || !slices.IsSorted(parts) {
			return fmt.Errorf("invalid joint failure mask")
		}
		hasGuard, hasCriterion := false, false
		for i, part := range parts {
			if i > 0 && part == parts[i-1] {
				return fmt.Errorf("duplicate failure cause")
			}
			switch part {
			case "guard":
				hasGuard = true
			case measureSuccess, measureUnsupported, measureEvidence, measureRecall:
				hasCriterion = true
			default:
				return fmt.Errorf("unknown failure cause")
			}
		}
		masks += p
		if hasGuard {
			guard += p
		}
		if hasCriterion {
			criterion += p
		}
	}
	if !closeSimulation(masks, fail) || !closeSimulation(guard, v.GuardStop) || !closeSimulation(criterion, v.CriterionStop) {
		return fmt.Errorf("joint failure masks disagree with stopping outcomes")
	}
	return nil
}

// The cumulative 12/24/48 design determines schedule cost from look survival.
func verifySimulationCost(row CellReport, cfg CalibrationConfig) error {
	scenarios := float64(len(cfg.Gate.Scenarios))
	second := row.ExpectedItems/(12*scenarios) - row.ExpectedLooks
	first := row.ExpectedLooks - 1 - second
	if !probability(first) || !probability(second) || second > first+1e-10 {
		return fmt.Errorf("invalid look or item cost")
	}
	attempts := 12*(1+float64(len(cfg.Gate.Variants))) + 12*first + 24*second
	for _, name := range cfg.Gate.Variants {
		v := row.Variants[name]
		active := 1 - v.LookPass[0] - v.LookFail[0]
		attempts += 12 * active
		active -= v.LookPass[1] + v.LookFail[1]
		attempts += 24 * active
	}
	if !closeSimulation(row.ExpectedAttempts, attempts*scenarios*float64(cfg.Gate.Policy.Repeats)) {
		return fmt.Errorf("scheduled attempt cost differs from stopping outcomes")
	}
	return nil
}

func probability(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= -1e-10 && p <= 1+1e-10
}

func closeSimulation(a, b float64) bool {
	return !math.IsNaN(a) && !math.IsInf(a, 0) && math.Abs(a-b) <= 1e-8
}

// verifyCalibrationVariants requires complete terminal outcomes for every variant.
func verifyCalibrationVariants(row CellReport, variants []string) error {
	for name := range row.VariantPass {
		if !slices.Contains(variants, name) {
			return fmt.Errorf("unknown variant probability")
		}
	}
	for _, name := range variants {
		v, ok := row.Variants[name]
		if !ok {
			return fmt.Errorf("missing variant %s", name)
		}
		if err := verifyVariantSimulation(v, row.Simulations); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if !closeSimulation(row.VariantPass[name], v.Pass) {
			return fmt.Errorf("variant PASS differs")
		}
		for _, measure := range []string{measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
			dist := row.Final[name+"/"+measure]
			total := 0.0
			for status, p := range dist {
				if (status != Pass && status != Fail && status != Inconclusive) || !probability(p) {
					return fmt.Errorf("invalid terminal criterion outcome")
				}
				total += p
			}
			if !closeSimulation(total, 1) {
				return fmt.Errorf("missing terminal criterion distribution")
			}
		}
	}
	return nil
}
