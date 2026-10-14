package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cSharedCalibrationExactReference(t *testing.T) {
	for _, cell := range []Cell{
		{Delta: .2, Rho: .5, Recall: .9, Kappa: .5},
		{DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5},
		{DY: -.08, DU: .05, DE: -.05, Delta: .3, Rho: .7, Recall: .75, Kappa: .5},
	} {
		for _, q := range []float64{1, .3} {
			configs := make([]CalibrationConfig, 0, 3)
			for _, margin := range []float64{.02, .03, .05} {
				cfg := CalibrationConfig{Gate: testConfig(), Seed: 190019, Qualifying: map[string]map[string]float64{"receipt_only": {"a": q, "b": q}, "hybrid": {"a": 1, "b": 1}}}
				cfg.Gate.Policy.Unsupported = map[string]float64{"receipt_only": margin, "hybrid": margin}
				configs = append(configs, cfg)
			}
			counts := []int{3, 4, 5}
			reference := make([]CellReport, len(configs))
			referenceCalls := 0
			for i, cfg := range configs {
				report, err := simulateCell(t.Context(), cfg, cell, counts[i], func(table Table, cfg Config, look int) (Evaluation, error) {
					referenceCalls++
					return Evaluate(table, cfg, look)
				})
				require.NoError(t, err)
				reference[i] = report
			}
			sharedCalls := 0
			got, err := simulateCandidateCell(t.Context(), configs, cell, counts, func(table Table, cfg Config, look int) (Evaluation, error) {
				sharedCalls++
				return Evaluate(table, cfg, look)
			})
			require.NoError(t, err)
			assert.Equal(t, reference, got, "all probabilities, variant terminal masks, look costs and counts must match")
			assert.Less(t, sharedCalls, referenceCalls, "identical bootstrap populations are evaluated once across margins")
		}
	}
}

// This retained pre-optimization implementation is the exact draw/order oracle.
// It intentionally retains dynamic per-draw allocations.
func allocationBootstrapReference(scenarios [][]itemDelta, variants int, seed uint64, look, b int) ([][3][]float64, [][][]float64) {
	scenarioSamples := make([][][]float64, variants)
	for v := range variants {
		scenarioSamples[v] = make([][]float64, len(scenarios))
		for s := range scenarios {
			scenarioSamples[v][s] = make([]float64, b)
		}
	}
	out := make([][3][]float64, variants)
	for v := range variants {
		for m := range 3 {
			out[v][m] = make([]float64, b)
		}
	}
	rng := newSimulationRandom(seed, []uint64{0, 1, 2, 3}[look])
	for i := range b {
		for si, items := range scenarios {
			sums := make([][3]float64, variants)
			counts := make([]int, variants)
			for range items {
				d := items[rng.IntN(len(items))]
				for v := range variants {
					if d.counts[v] > 0 {
						counts[v]++
						for m := range 3 {
							sums[v][m] += d.values[v][m]
						}
					}
				}
			}
			for v := range variants {
				if counts[v] > 0 {
					scenarioSamples[v][si][i] = sums[v][0] / float64(counts[v])
					for m := range 3 {
						out[v][m][i] += sums[v][m] / float64(counts[v]*len(scenarios))
					}
				}
			}
		}
	}
	return out, scenarioSamples
}

func TestK19cBootstrapAllocationReference(t *testing.T) {
	for _, variants := range [][]string{{"receipt_only"}, {"receipt_only", "hybrid"}} {
		cfg := testConfig()
		cfg.Variants = variants
		for _, look := range []int{1, 2, 3} {
			table, err := syntheticPrefix(testTable(48), cfg.Policy.Looks[look-1])
			require.NoError(t, err)
			pop, err := pair(table, cfg)
			require.NoError(t, err)
			// Missing pairs exercise sample counts without changing draw ownership.
			pop.scenarios[0][0].counts[0] = 0
			want, wantScenarios := allocationBootstrapReference(pop.scenarios, len(variants), 190017, look, 10000)
			got, gotScenarios := bootstrapIntervals(pop.scenarios, len(variants), 190017, look, 10000)
			assert.Equal(t, want, got)
			assert.Equal(t, wantScenarios, gotScenarios)
		}
	}
}

func TestK19cSharedCalibrationWorkerReference(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Seed: 190019, Workers: 2, Qualifying: map[string]map[string]float64{"receipt_only": {"a": .3, "b": .3}, "hybrid": {"a": 1, "b": 1}}}
	cells := []Cell{{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}, {DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}}
	shared, err := calibrateCandidates(t.Context(), cfg, cells, 3, Evaluate)
	require.NoError(t, err)
	require.Len(t, shared, 3)
	for _, candidate := range shared {
		cfg.Gate.Policy = clonePolicy(cfg.Gate.Policy)
		for variant := range cfg.Gate.Policy.Unsupported {
			cfg.Gate.Policy.Unsupported[variant] = candidate.Margin
		}
		reference, err := calibrate(t.Context(), cfg, cells, 3, Evaluate)
		require.NoError(t, err)
		assert.Equal(t, reference, candidate.Report)
	}
}
