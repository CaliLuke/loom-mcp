package gate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalibrationProgressPreservesResults(t *testing.T) {
	for _, workers := range []int{1, 2} {
		cfg := CalibrationConfig{Gate: testConfig(), Seed: 190019, Workers: workers, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
		cells := []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, {DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}
		reference, err := calibrateCandidates(t.Context(), cfg, cells, 3, Evaluate)
		require.NoError(t, err)
		var updates []CalibrationProgress
		ctx := WithCalibrationProgress(t.Context(), func(p CalibrationProgress) { updates = append(updates, p) })
		observed, err := calibrateCandidates(ctx, cfg, cells, 3, Evaluate)
		require.NoError(t, err)
		want, err := json.Marshal(reference)
		require.NoError(t, err)
		got, err := json.Marshal(observed)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got))
		require.NotEmpty(t, updates)
		assert.Equal(t, CalibrationProgress{CellsTotal: 2, SimulationsTotal: 6}, updates[0])
		assert.Equal(t, CalibrationProgress{CellsCompleted: 2, CellsTotal: 2, SimulationsCompleted: 6, SimulationsTotal: 6}, updates[len(updates)-1])
		assert.Equal(t, 0, updates[1].CellsCompleted, "progress must be visible before a long cell finishes")
		assert.Equal(t, 1, updates[1].SimulationsCompleted)
		for i := 1; i < len(updates); i++ {
			assert.GreaterOrEqual(t, updates[i].CellsCompleted, updates[i-1].CellsCompleted)
			assert.GreaterOrEqual(t, updates[i].SimulationsCompleted, updates[i-1].SimulationsCompleted)
		}
	}
}

func TestCalibrationProgressCancellation(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 7, Workers: 1, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	var latest CalibrationProgress
	ctx := WithCalibrationProgress(parent, func(p CalibrationProgress) {
		latest = p
		if p.SimulationsCompleted == 1 {
			cancel()
		}
	})
	_, err := calibrate(ctx, cfg, []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}, 3, Evaluate)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, latest.SimulationsCompleted)
	assert.Zero(t, latest.CellsCompleted)
}

func TestCalibrationProgressInfeasibleCells(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 7, Workers: 2, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	var latest CalibrationProgress
	ctx := WithCalibrationProgress(t.Context(), func(p CalibrationProgress) { latest = p })
	result, err := calibrateCandidates(ctx, cfg, []Cell{{DY: -1, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}, 3, Evaluate)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.NotEmpty(t, result[0].Report.Cells[0].Infeasible)
	assert.Equal(t, CalibrationProgress{CellsCompleted: 1, CellsTotal: 1}, latest)
}

func TestCalibrationProgressCountsSharedProductionWork(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 7, Workers: 2, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	var initial CalibrationProgress
	ctx := WithCalibrationProgress(parent, func(p CalibrationProgress) { initial = p; cancel() })
	_, err := calibrateCandidates(ctx, cfg, []Cell{
		{Delta: .2, Rho: .5, Recall: .9, Kappa: .5},
		{DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5},
		{DY: -.08, DU: .05, DE: -.05, Delta: .3, Rho: .7, Recall: .75, Kappa: .5},
	}, 0, Evaluate)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, CalibrationProgress{CellsTotal: 3, SimulationsTotal: 4400}, initial)
}
