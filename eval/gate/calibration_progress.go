package gate

import (
	"context"
	"sync"
)

// CalibrationProgress reports actual work completed in the current calibration phase.
// Shared candidate simulations are counted once, not once per candidate.
type CalibrationProgress struct {
	CellsCompleted       int
	CellsTotal           int
	SimulationsCompleted int
	SimulationsTotal     int
}

type calibrationObserverKey struct{}
type calibrationTrackerKey struct{}
type calibrationTracker struct {
	mu      sync.Mutex
	value   CalibrationProgress
	observe func(CalibrationProgress)
}

// WithCalibrationProgress observes calibration work without changing its inputs or results.
// Calls within each calibration are serialized. The observer must return promptly
// and must not panic. Counts restart for each calibration call; no callback is
// retained in configuration or serialized into evidence. A nil observer disables it.
func WithCalibrationProgress(ctx context.Context, observe func(CalibrationProgress)) context.Context {
	return context.WithValue(ctx, calibrationObserverKey{}, observe)
}

// startCalibrationProgress counts actual synthetic populations, accounting for
// infeasible cells and candidate sharing before the workers start.
func startCalibrationProgress(ctx context.Context, configs []CalibrationConfig, cells []Cell, count int) context.Context {
	observe, ok := ctx.Value(calibrationObserverKey{}).(func(CalibrationProgress))
	if !ok || observe == nil {
		return ctx
	}
	p := &calibrationTracker{observe: observe, value: CalibrationProgress{CellsTotal: len(cells)}}
	for _, cell := range cells {
		if infeasible(configs[0], cell) != "" {
			continue
		}
		maximum := 0
		for _, cfg := range configs {
			maximum = max(maximum, calibrationSimulationCount(cfg, cell, count))
		}
		p.value.SimulationsTotal += maximum
	}
	observe(p.value)
	return context.WithValue(ctx, calibrationTrackerKey{}, p)
}

func calibrationSimulationCount(cfg CalibrationConfig, cell Cell, count int) int {
	if count != 0 {
		return count
	}
	if calibrationDecisionCell(cell, cfg.Gate.Policy) {
		return 2000
	}
	return 400
}

func advanceCalibrationProgress(ctx context.Context, cells, simulations int) {
	p, ok := ctx.Value(calibrationTrackerKey{}).(*calibrationTracker)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.value.CellsCompleted += cells
	p.value.SimulationsCompleted += simulations
	p.observe(p.value)
}
