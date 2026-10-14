package gate

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkK19cProductionCellTiming measures full production bootstrap work only.
// Eight actual tuple-seeded simulations estimate cost; they are never written
// as a calibration artifact or substituted for the fixed 2000/400 cell counts.
func BenchmarkK19cProductionCellTiming(b *testing.B) {
	for _, margin := range []float64{.02, .03, .05} {
		for _, sample := range []struct {
			name string
			cell Cell
		}{
			{"null", Cell{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}},
			{"unsupported_boundary", Cell{DU: margin, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}},
			{"combined_adverse", Cell{DY: -.08, DU: .05, DE: -.05, Delta: .3, Rho: .7, Recall: .75, Kappa: .5}},
		} {
			b.Run(fmt.Sprintf("margin_%.2f/%s", margin, sample.name), func(b *testing.B) {
				policy := ProposedPolicy(map[string]float64{"receipt_only": margin, "hybrid": margin}, 190017)
				cfg := CalibrationConfig{Gate: Config{Repeats: 2, Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Policy: &policy}, Seed: 190019, Workers: 4, Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}}
				for i := range 12 {
					scenario := fmt.Sprintf("scenario_%02d", i)
					cfg.Gate.Scenarios = append(cfg.Gate.Scenarios, scenario)
					cfg.Qualifying["receipt_only"][scenario] = 1
					cfg.Qualifying["hybrid"][scenario] = 1
				}
				cfg.Gate.Policy.Unsupported["receipt_only"] = margin
				cfg.Gate.Policy.Unsupported["hybrid"] = margin
				b.ResetTimer()
				started := time.Now()
				var looks float64
				for range b.N {
					result, err := simulateCell(b.Context(), cfg, sample.cell, 8, Evaluate)
					if err != nil {
						b.Fatal(err)
					}
					looks += result.ExpectedLooks
				}
				b.ReportMetric(float64(time.Since(started).Nanoseconds())/float64(b.N*8), "ns/simulation")
				b.ReportMetric(looks/float64(b.N), "looks/simulation")
			})
		}
	}
}

// BenchmarkK19cSharedCellTiming times all candidates with the exact shared-work
// implementation. These eight-simulation samples never become gate evidence.
func BenchmarkK19cSharedCellTiming(b *testing.B) {
	configs := make([]CalibrationConfig, 0, 3)
	for _, margin := range []float64{.02, .03, .05} {
		policy := ProposedPolicy(map[string]float64{"receipt_only": margin, "hybrid": margin}, 190017)
		cfg := CalibrationConfig{Gate: Config{Repeats: 2, Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Policy: &policy}, Seed: 190019, Workers: 4, Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}}
		for i := range 12 {
			scenario := fmt.Sprintf("scenario_%02d", i)
			cfg.Gate.Scenarios = append(cfg.Gate.Scenarios, scenario)
			cfg.Qualifying["receipt_only"][scenario] = 1
			cfg.Qualifying["hybrid"][scenario] = 1
		}
		configs = append(configs, cfg)
	}
	for _, sample := range []struct {
		name string
		cell Cell
	}{
		{"null", Cell{Delta: .2, Rho: .5, Recall: .9, Kappa: .5}},
		{"unsupported_boundary", Cell{DU: .03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}},
		{"combined_adverse", Cell{DY: -.08, DU: .05, DE: -.05, Delta: .3, Rho: .7, Recall: .75, Kappa: .5}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			started := time.Now()
			for range b.N {
				if _, err := simulateCandidateCell(b.Context(), configs, sample.cell, []int{8, 8, 8}, Evaluate); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(time.Since(started).Nanoseconds())/float64(b.N*8), "ns/shared-simulation")
		})
	}
}
