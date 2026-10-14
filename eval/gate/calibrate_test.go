package gate

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCalibrationGrid(t *testing.T) {
	cells := CalibrationGrid()
	require.Len(t, cells, 1620)
	count := 0
	for _, c := range cells {
		if c.DecisionCell() {
			count++
		}
	}
	assert.Equal(t, 45, count)
	_, _, err := conditional(.98, .1, .2)
	require.Error(t, err)
	a, b, err := conditional(.9, -.03, .3)
	require.NoError(t, err)
	assert.InDelta(t, .97, a, .2)
	assert.InDelta(t, .23, .9*(1-a)+.1*b, 1e-12)
}
func TestCalibrationSameEvaluatorDeterministic(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 7, Workers: 1, Qualifying: map[string]map[string]float64{"receipt_only": {"a": 1, "b": 1}, "hybrid": {"a": 1, "b": 1}}}
	cells := []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, {DY: -.05, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}
	var calls atomic.Int64
	evaluator := func(table Table, c Config, look int) (Evaluation, error) {
		calls.Add(1)
		return evaluate(table, c, look, 2000)
	}
	a, err := calibrate(context.Background(), cfg, cells, 50, evaluator)
	require.NoError(t, err)
	assert.Positive(t, calls.Load())
	cfg.Workers = 2
	b, err := calibrate(context.Background(), cfg, cells, 50, evaluator)
	require.NoError(t, err)
	aj, err := json.Marshal(a)
	require.NoError(t, err)
	bj, err := json.Marshal(b)
	require.NoError(t, err)
	assert.Equal(t, string(aj), string(bj))
	assert.Contains(t, strings.Join(a.Targets, "\n"), "cannot certify")
}
