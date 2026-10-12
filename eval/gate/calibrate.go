package gate

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
)

type evaluator func(Table, Config, int) (Evaluation, error)

// Calibrate runs the full grid and two central-null copula sensitivity rows.
// It uses Evaluate and Decide unchanged, including 10,000 frozen-seed resamples.
// Worker count changes scheduling only; each cell/simulation owns its RNG.
func Calibrate(ctx context.Context, cfg CalibrationConfig) (CalibrationReport, error) {
	report, err := calibrate(ctx, cfg, CalibrationGrid(), 0, Evaluate)
	if err != nil {
		return report, err
	}
	sensitivity := []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: 0}, {Delta: .2, Rho: .5, Recall: .9, Kappa: .8}}
	extra, err := calibrate(ctx, cfg, sensitivity, 0, Evaluate)
	report.Sensitivity = extra.Cells
	return report, err
}

// CalibrateChecks computes the two production-bootstrap, 200-simulation check
// cells. K19b persists this output and compares its deterministic JSON bytes.
func CalibrateChecks(ctx context.Context, cfg CalibrationConfig) (CalibrationReport, error) {
	cells := slices.DeleteFunc(CalibrationGrid(), func(c Cell) bool {
		return c.Delta != .2 || c.Rho != .5 || (c.axis() != measureNull && c.axis() != measureSuccess)
	})
	return calibrate(ctx, cfg, cells, 200, Evaluate)
}

func calibrate(ctx context.Context, cfg CalibrationConfig, cells []Cell, count int, eval evaluator) (CalibrationReport, error) {
	out := CalibrationReport{Version: SimulatorVersion, Seed: cfg.Seed, RateSource: "assumed defaults", Baselines: map[string]Rates{}, Qualifying: cfg.Qualifying, Cells: make([]CellReport, len(cells))}
	if err := validateCalibration(cfg); err != nil {
		return out, err
	}
	if cfg.Pilot != nil {
		out.RateSource = "pilot (clamped to [.02,.98])"
	}
	for _, s := range cfg.Gate.Scenarios {
		out.Baselines[s] = baselineRates(cfg, s)
	}
	jobs := make(chan int)
	errs := make([]error, len(cells))
	var wg sync.WaitGroup
	for range max(1, cfg.Workers) {
		wg.Go(func() {
			for i := range jobs {
				n := count
				if n == 0 {
					n = 400
					if cells[i].DecisionCell() {
						n = 2000
					}
				}
				out.Cells[i], errs[i] = simulateCell(ctx, cfg, cells[i], n, eval)
			}
		})
	}
	for i := range cells {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return out, err
		}
	}
	out.Targets = calibrationTargets(out.Cells)
	return out, nil
}
func simulateCell(ctx context.Context, cfg CalibrationConfig, c Cell, n int, eval evaluator) (CellReport, error) {
	out := CellReport{Cell: c, Simulations: n, VariantPass: map[string]float64{}, Final: map[string]map[Status]float64{}}
	if reason := infeasible(cfg, c); reason != "" {
		out.Infeasible = reason
		out.Simulations = 0
		return out, nil
	}
	var simSeed uint64
	for range n {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		table, err := synthetic(cfg, c, splitmix(cfg.Seed^cellSeed(c)^splitmix(simSeed+0x100000000)))
		if err != nil {
			return out, err
		}
		simSeed++
		if err := simulateLooks(table, cfg.Gate, &out, eval); err != nil {
			return out, err
		}
	}
	normalizeCell(&out, float64(n))
	return out, nil
}
func normalizeCell(r *CellReport, n float64) {
	r.Pass /= n
	r.NotPassed /= n
	r.ExpectedLooks /= n
	r.ExpectedItems /= n
	r.SE = math.Sqrt(r.Pass * (1 - r.Pass) / n)
	for i := range 3 {
		r.LookPass[i] /= n
		r.LookFail[i] /= n
	}
	for i := range 2 {
		r.TruncatedPass[i] /= n
	}
	for v := range r.VariantPass {
		r.VariantPass[v] /= n
	}
	for _, m := range r.Final {
		for s := range m {
			m[s] /= n
		}
	}
}
func calibrationTargets(cells []CellReport) []string {
	worst := map[string]float64{measureNull: 1}
	limiting := "unknown"
	lowest := 2.0
	for _, c := range cells {
		axis := c.Cell.axis()
		if c.Infeasible != "" || axis == "" {
			continue
		}
		if axis == measureNull {
			worst[axis] = min(worst[axis], c.Pass)
			for name, dist := range c.Final {
				if dist[Pass] < lowest || (dist[Pass] == lowest && name < limiting) {
					lowest = dist[Pass]
					limiting = name
				}
			}
		} else {
			worst[axis] = max(worst[axis], c.Pass)
		}
	}
	var out []string
	if worst[measureNull] < .8 {
		out = append(out, fmt.Sprintf("the gate cannot certify margin %s at 48 items per scenario (worst null pass %.4f)", limiting, worst[measureNull]))
	}
	for _, axis := range []string{measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
		if worst[axis] > .05 {
			out = append(out, fmt.Sprintf("the gate cannot certify margin %s at 48 items per scenario (margin pass %.4f)", axis, worst[axis]))
		}
	}
	if len(out) == 0 {
		out = append(out, "calibration targets met for supplied cells")
	}
	return out
}

func validateCalibration(cfg CalibrationConfig) error {
	if err := validateConfig(cfg.Gate, 1); err != nil {
		return err
	}
	if cfg.Gate.Items != nil {
		return fmt.Errorf("gate: calibration generates its own manifest")
	}
	for _, s := range cfg.Gate.Scenarios {
		if cfg.Pilot != nil {
			r, ok := cfg.Pilot[s]
			if !ok || math.IsNaN(r.Success) || math.IsNaN(r.Unsupported) || math.IsNaN(r.Evidence) {
				return fmt.Errorf("gate: missing or invalid pilot scenario %s", s)
			}
		}
		for _, v := range cfg.Gate.Variants {
			q, ok := cfg.Qualifying[v][s]
			if !ok || q < 0 || q > 1 || math.IsNaN(q) {
				return fmt.Errorf("gate: missing or invalid qualifying fraction %s/%s", v, s)
			}
		}
	}
	return nil
}
func infeasible(cfg CalibrationConfig, c Cell) string {
	if c.Rho <= 0 || c.Rho >= 1 || c.Kappa < 0 || c.Kappa > 1 || c.Recall < 0 || c.Recall > 1 {
		return "invalid synthetic population"
	}
	for _, s := range cfg.Gate.Scenarios {
		r := baselineRates(cfg, s)
		for _, pd := range [][2]float64{{r.Success, c.DY}, {1 - r.Unsupported, -c.DU}, {r.Evidence, c.DE}} {
			if _, _, err := conditional(pd[0], pd[1], c.Delta); err != nil {
				return err.Error()
			}
		}
	}
	return ""
}
func cellSeed(c Cell) uint64 {
	var seed uint64
	for _, v := range []float64{c.DY, c.DU, c.DE, c.Delta, c.Rho, c.Recall, c.Kappa} {
		seed = splitmix(seed ^ math.Float64bits(v))
	}
	return seed
}
