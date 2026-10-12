package suite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// Validation reports structural evidence only; its quality outcomes never enter the gate.
type Validation struct {
	MaterializerVersion string                        `json:"materializer_version"`
	PoolsSHA256         string                        `json:"pools_sha256"`
	Failures            []string                      `json:"failures,omitempty"`
	Attempts            int                           `json:"attempts"`
	Qualifying          map[string]map[string]float64 `json:"qualifying"`
}

// Validate runs every pilot/look/smoke item under the offline Reader before freezing.
func Validate(ctx context.Context, dir string, workers int, progress io.Writer) (Validation, error) {
	_, pools, err := Inputs(dir)
	if err != nil {
		return Validation{}, err
	}
	data, err := json.Marshal(pools)
	if err != nil {
		return Validation{}, err
	}
	out := Validation{MaterializerVersion: env.MaterializerVersion, PoolsSHA256: gate.Digest(data), Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}}
	failures := map[string]bool{}
	counts := map[string]map[string]int{"receipt_only": {}, "hybrid": {}}
	for _, pool := range pools {
		d, err := LoadPool(dir, pool.Scenario, pool.Partition)
		if err != nil {
			return out, err
		}
		cfg := eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New(pool.Scenario, d)}, Variants: scenarios.Variants(), Repeats: 1, Concurrency: workers}
		report, err := eval.Run(ctx, cfg)
		if err != nil {
			return out, err
		}
		for _, a := range report.Attempts {
			if err := attemptStructure(a); err != nil {
				failure := fmt.Sprintf("%s/%s: %v", a.Scenario, a.Variant, err)
				if !failures[failure] {
					out.Failures = append(out.Failures, failure)
					failures[failure] = true
				}
			}
			out.Attempts++
			if _, gated := out.Qualifying[a.Variant]; gated {
				counts[a.Variant][a.Scenario]++
				if a.Outcome == nil {
					continue
				}
				for _, score := range a.Outcome.Scores {
					if score.Name == "recall_qualifying" {
						out.Qualifying[a.Variant][a.Scenario] += score.Value
					}
				}
			}
		}
		if _, err := fmt.Fprintf(progress, "validated %s/%s: %d attempts\n", pool.Scenario, pool.Partition, len(report.Attempts)); err != nil {
			return out, err
		}
	}
	for variant, byScenario := range out.Qualifying {
		for scenario, n := range counts[variant] {
			byScenario[scenario] /= float64(n)
		}
	}
	if len(out.Failures) > 0 {
		return out, errors.New("offline structural validation failed; see gate/validation.json")
	}
	return out, nil
}

// CalibrationConfig binds the simulator to the offline-validated q and fixed seeds.
func CalibrationConfig(config string, v Validation, workers int) gate.CalibrationConfig {
	return gate.CalibrationConfig{Gate: GateConfig(env.GatedScenarios(config)), Seed: 190019, Workers: workers, Qualifying: v.Qualifying}
}
func attemptStructure(a *eval.AttemptRecord) error {
	if a.HarnessErr != nil {
		return a.HarnessErr
	}
	if a.Outcome == nil {
		return fmt.Errorf("missing outcome: %s", a.AttemptKey.String())
	}
	if a.Outcome.Err != nil {
		return a.Outcome.Err
	}
	if a.Outcome.Session == nil {
		return fmt.Errorf("missing session: %s", a.AttemptKey.String())
	}
	for _, r := range a.Outcome.Session.Runs {
		if r.RunErr != nil {
			return r.RunErr
		}
		if err := scenarios.CheckStructure(a.Scenario, a.Variant, r.Record); err != nil {
			return err
		}
	}
	return nil
}

// PilotRates computes baseline-only rates, leaving clamping to the simulator.
func PilotRates(exp *eval.Experiment, config string) (map[string]gate.Rates, error) {
	rows := make([]eval.StoredAttempt, 0, len(exp.Attempts))
	for _, a := range exp.Attempts {
		if a.Variant != "baseline" {
			return nil, fmt.Errorf("pilot contains a variant")
		}
		rows = append(rows, *a)
	}
	table, err := gate.FromStored(rows)
	if err != nil {
		return nil, err
	}
	out := map[string]gate.Rates{}
	counts := map[string]int{}
	for _, r := range table {
		if r.Infrastructure {
			return nil, fmt.Errorf("pilot has incomplete observations")
		}
		if !slices.Contains(env.GatedScenarios(config), r.Scenario) {
			continue
		}
		v := out[r.Scenario]
		if r.Success {
			v.Success++
		}
		if r.Unsupported {
			v.Unsupported++
		}
		if r.Evidence {
			v.Evidence++
		}
		out[r.Scenario] = v
		counts[r.Scenario]++
	}
	for _, s := range env.GatedScenarios(config) {
		n := float64(counts[s])
		if n == 0 {
			return nil, fmt.Errorf("pilot missing %s", s)
		}
		v := out[s]
		v.Success /= n
		v.Unsupported /= n
		v.Evidence /= n
		out[s] = v
	}
	return out, nil
}
