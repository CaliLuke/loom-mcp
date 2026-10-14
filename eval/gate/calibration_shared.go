package gate

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

type calibrationEvaluationKey struct {
	look          int
	first, second string
	variants      int
}

// simulateCandidateCell retains independent stopping and simulation counts while
// sharing only identical population/bootstrap work within one simulation.
func simulateCandidateCell(ctx context.Context, configs []CalibrationConfig, cell Cell, counts []int, evaluate evaluator) ([]CellReport, error) {
	reports, maximum, err := prepareCandidateCell(configs, cell, counts)
	if err != nil {
		return nil, err
	}
	if reason := infeasible(configs[0], cell); reason != "" {
		for i := range reports {
			reports[i].Infeasible = reason
			reports[i].Simulations = 0
		}
		return reports, nil
	}
	for simulation := range maximum {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		table, err := synthetic(configs[0], cell, splitmix(configs[0].Seed^cellSeed(cell)^splitmix(uint64(simulation)+0x100000000)))
		if err != nil {
			return nil, err
		}
		shared := cachedCandidateEvaluator(evaluate)
		for i, cfg := range configs {
			if simulation < counts[i] {
				if err := simulateLooks(table, cfg.Gate, &reports[i], shared); err != nil {
					return nil, err
				}
			}
		}
		advanceCalibrationProgress(ctx, 0, 1)
	}
	for i := range reports {
		normalizeCell(&reports[i], float64(counts[i]))
	}
	return reports, nil
}

func unsupportedCriterion(interval Interval, policy *Policy, variant string) CriterionResult {
	return Criterion(interval, policy.Unsupported[variant], false)
}

// CalibrateCandidates computes all three fixed margins with production counts
// and shared identical bootstrap work. Each candidate retains its own stopping
// path, decision-cell count and complete independently selectable report.
func CalibrateCandidates(ctx context.Context, cfg CalibrationConfig) ([]CandidateReport, error) {
	cells := CalibrationGrid()
	cells = append(cells, Cell{Delta: .2, Rho: .5, Recall: .9, Kappa: 0}, Cell{Delta: .2, Rho: .5, Recall: .9, Kappa: .8})
	return calibrateCandidates(ctx, cfg, cells, 0, Evaluate)
}

func calibrateCandidates(ctx context.Context, cfg CalibrationConfig, cells []Cell, count int, evaluate evaluator) ([]CandidateReport, error) {
	if err := validateCalibration(cfg); err != nil {
		return nil, err
	}
	configs, output := prepareCandidateReports(cfg, len(cells))
	ctx = startCalibrationProgress(ctx, configs, cells, count)
	jobs := make(chan int)
	errors := make([]error, len(cells))
	var workers sync.WaitGroup
	for range max(1, cfg.Workers) {
		workers.Go(func() {
			for index := range jobs {
				errors[index] = calibrateCandidateIndex(ctx, configs, output, cells[index], index, count, evaluate)
				if errors[index] == nil {
					advanceCalibrationProgress(ctx, 1, 0)
				}
			}
		})
	}
	for i := range cells {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	for i := range output {
		report := &output[i].Report
		// Sensitivity rows have distinct kappa and never alter full-grid membership.
		for j, cell := range cells {
			if cell.Kappa != .5 {
				report.Sensitivity = append(report.Sensitivity, report.Cells[j])
			}
		}
		report.Cells = slices.DeleteFunc(report.Cells, func(cell CellReport) bool { return cell.Cell.Kappa != .5 })
		var err error
		report.Targets, err = CalibrationTargets(report.Cells, configs[i].Gate.Policy)
		if err != nil {
			return nil, err
		}
	}
	return output, nil
}

// cachedCandidateEvaluator shares intervals only within one synthetic population.
func cachedCandidateEvaluator(evaluate evaluator) evaluator {
	cache := map[calibrationEvaluationKey]Evaluation{}
	return func(rows Table, cfg Config, look int) (Evaluation, error) {
		key := calibrationEvaluationKey{look: look, variants: len(cfg.Variants), first: cfg.Variants[0]}
		if len(cfg.Variants) == 2 {
			key.second = cfg.Variants[1]
		}
		value, ok := cache[key]
		if !ok {
			var err error
			value, err = evaluate(rows, cfg, look)
			if err != nil {
				return Evaluation{}, err
			}
			cache[key] = value
		}
		// All other policy and population inputs were compared above. The shared
		// evaluator owns every bound; only the candidate's explicit margin differs.
		value.Policy = clonePolicy(cfg.Policy)
		value.Variants = slices.Clone(value.Variants)
		for i, variant := range value.Variants {
			value.Variants[i] = cloneVariantResult(variant)
			value.Variants[i].Unsupported = unsupportedCriterion(variant.Unsupported.Interval, cfg.Policy, variant.Variant)
		}
		return value, nil
	}
}

// prepareCandidateCell requires identical inputs apart from unsupported margins.
func prepareCandidateCell(configs []CalibrationConfig, cell Cell, counts []int) ([]CellReport, int, error) {
	if len(configs) == 0 || len(configs) != len(counts) {
		return nil, 0, fmt.Errorf("gate: invalid shared calibration candidates")
	}
	if err := validateCalibration(configs[0]); err != nil {
		return nil, 0, err
	}
	reference := configs[0]
	reference.Gate.Policy = clonePolicy(reference.Gate.Policy)
	reference.Gate.Policy.Unsupported = nil
	reports := make([]CellReport, len(configs))
	maximum := 0
	for i, cfg := range configs {
		if err := validateCalibration(cfg); err != nil {
			return nil, 0, err
		}
		same := cfg
		same.Gate.Policy = clonePolicy(cfg.Gate.Policy)
		same.Gate.Policy.Unsupported = nil
		if counts[i] <= 0 || !reflect.DeepEqual(reference, same) {
			return nil, 0, fmt.Errorf("gate: shared candidates have different simulation inputs")
		}
		reports[i] = CellReport{Cell: cell, Simulations: counts[i], VariantPass: map[string]float64{}, Final: map[string]map[Status]float64{}}
		maximum = max(maximum, counts[i])
	}
	return reports, maximum, nil
}

// prepareCandidateReports initializes all three fixed-margin report populations.
func prepareCandidateReports(cfg CalibrationConfig, cells int) ([]CalibrationConfig, []CandidateReport) {
	configs := make([]CalibrationConfig, 3)
	output := make([]CandidateReport, 3)
	for i, margin := range []float64{.02, .03, .05} {
		configs[i] = cfg
		configs[i].Gate.Policy = clonePolicy(cfg.Gate.Policy)
		for variant := range configs[i].Gate.Policy.Unsupported {
			configs[i].Gate.Policy.Unsupported[variant] = margin
		}
		report := CalibrationReport{Version: SimulatorVersion, Seed: cfg.Seed, RateSource: assumedRateSource, Baselines: map[string]Rates{}, Qualifying: cfg.Qualifying, Cells: make([]CellReport, cells)}
		if cfg.Pilot != nil {
			report.RateSource = pilotRateSource
		}
		for _, scenario := range cfg.Gate.Scenarios {
			report.Baselines[scenario] = baselineRates(cfg, scenario)
		}
		output[i] = CandidateReport{Margin: margin, Report: report}
	}
	return configs, output
}

// calibrateCandidateIndex writes one complete cell for each independent candidate.
func calibrateCandidateIndex(ctx context.Context, configs []CalibrationConfig, output []CandidateReport, cell Cell, index, count int, evaluate evaluator) error {
	counts := make([]int, len(configs))
	for i, candidate := range configs {
		counts[i] = calibrationSimulationCount(candidate, cell, count)
	}
	reports, err := simulateCandidateCell(ctx, configs, cell, counts, evaluate)
	if err == nil {
		for i, report := range reports {
			output[i].Report.Cells[index] = report
		}
	}
	return err
}
