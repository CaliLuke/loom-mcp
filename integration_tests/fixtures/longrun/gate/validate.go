package suite

import (
	"encoding/json"
	"fmt"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// Validation reports structural evidence only; its quality outcomes never enter the gate.
type Validation struct {
	Version             int                           `json:"version"`
	Configuration       string                        `json:"configuration"`
	Profile             longrun.ConfigurationProfile  `json:"profile"`
	Repeats             int                           `json:"repeats"`
	Rows                []ValidationRow               `json:"rows"`
	EffectivePolicies   map[string]json.RawMessage    `json:"effective_policies"`
	SourceSHA256        string                        `json:"source_sha256"`
	MaterializerVersion string                        `json:"materializer_version"`
	PoolsSHA256         string                        `json:"pools_sha256"`
	Failures            []string                      `json:"failures,omitempty"`
	Attempts            int                           `json:"attempts"`
	Qualifying          map[string]map[string]float64 `json:"qualifying"`
}

// CalibrationConfig binds the simulator to the offline-validated q and fixed seeds.
func CalibrationConfig(config string, v Validation, workers int) gate.CalibrationConfig {
	return gate.CalibrationConfig{Gate: GateConfig(env.GatedScenarios(config), ProposedOfflinePolicy()), Seed: 190019, Workers: workers, Qualifying: v.Qualifying}
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
	if a.Outcome.Session == nil || len(a.Outcome.Session.Runs) == 0 {
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
