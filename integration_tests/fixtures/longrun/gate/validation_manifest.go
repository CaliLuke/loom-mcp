package suite

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// ValidationRow records one required structural attempt without borrowing live evidence.
type ValidationRow struct {
	Key             eval.AttemptKey `json:"key"`
	Partition       string          `json:"partition"`
	EvidencePresent bool            `json:"evidence_present"`
	Qualifying      bool            `json:"qualifying"`
	Failure         string          `json:"failure,omitempty"`
}

type validationExpectation struct {
	partition, agent string
}

// VerifyValidation requires complete, unique structural evidence for the configuration.
func VerifyValidation(dir, configuration string, validation Validation) error {
	if validation.Version != 2 || validation.Configuration != configuration || validation.Profile.Configuration != configuration || validation.Repeats != 1 || validation.MaterializerVersion != env.MaterializerVersion {
		return fmt.Errorf("unsupported or mismatched validation configuration")
	}
	if _, err := longrun.ProfileConfiguration(validation.Profile, fake.Faults{}); err != nil {
		return err
	}
	source, err := hex.DecodeString(validation.SourceSHA256)
	if err != nil || len(source) != 32 {
		return fmt.Errorf("validation has no source identity")
	}
	_, pools, err := Inputs(dir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(pools)
	if err != nil {
		return err
	}
	if validation.PoolsSHA256 != gate.Digest(raw) {
		return fmt.Errorf("validation pool identity mismatch")
	}
	expected, policies, err := validationManifest(dir, configuration, pools)
	if err != nil {
		return err
	}
	if len(validation.Failures) > 0 || validation.Attempts != len(expected) || len(validation.Rows) != len(expected) {
		return fmt.Errorf("validation is incomplete or structurally failed")
	}
	seen := make(map[eval.AttemptKey]bool, len(expected))
	totals := map[string]map[string]int{"receipt_only": {}, "hybrid": {}}
	qualifying := map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}
	for _, row := range validation.Rows {
		want, ok := expected[row.Key]
		if !ok || seen[row.Key] || row.Partition != want.partition || row.Failure != "" {
			return fmt.Errorf("invalid validation observation %s", row.Key.String())
		}
		if validationResident(validation.Profile, row.Key) && !row.EvidencePresent {
			return fmt.Errorf("resident multimodal validation lacks original evidence: %s", row.Key.String())
		}
		seen[row.Key] = true
		if byScenario, ok := totals[row.Key.Variant]; ok && slices.Contains(env.GatedScenarios(configuration), row.Key.Scenario) {
			byScenario[row.Key.Scenario]++
			if row.Qualifying {
				qualifying[row.Key.Variant][row.Key.Scenario]++
			}
		}
	}
	for variant, counts := range totals {
		for _, scenario := range env.GatedScenarios(configuration) {
			if counts[scenario] == 0 {
				return fmt.Errorf("validation lacks %s/%s", variant, scenario)
			}
			qualifying[variant][scenario] /= float64(counts[scenario])
		}
	}
	if !reflect.DeepEqual(qualifying, validation.Qualifying) {
		return fmt.Errorf("validation qualifying fractions differ from complete observations")
	}
	if len(policies) != len(validation.EffectivePolicies) {
		return fmt.Errorf("validation effective policy manifest is incomplete")
	}
	for key := range policies {
		raw, ok := validation.EffectivePolicies[key]
		if !ok {
			return fmt.Errorf("validation lacks effective policy %s", key)
		}
		if err := verifyValidationPolicy(key, raw); err != nil {
			return err
		}
	}
	return nil
}

// validationManifest enumerates required partitions independently of filesystem discovery.
func validationManifest(dir, configuration string, pools []gate.Pool) (map[eval.AttemptKey]validationExpectation, map[string]bool, error) {
	expected := make(map[eval.AttemptKey]validationExpectation)
	policies := make(map[string]bool)
	byName := make(map[string]gate.Pool, len(pools))
	for _, pool := range pools {
		byName[pool.Scenario+"/"+pool.Partition] = pool
	}
	for _, scenario := range env.ScenarioIDs(configuration) {
		for _, part := range []struct {
			name  string
			count int
		}{{"pilot", 6}, {"look1", 12}, {"look2", 12}, {"look3", 24}, {"smoke", 3}} {
			if scenario == "model_change" && part.name == "pilot" {
				continue
			}
			pool, ok := byName[scenario+"/"+part.name]
			if !ok || len(pool.Items) != part.count {
				return nil, nil, fmt.Errorf("missing or incomplete validation pool %s/%s", scenario, part.name)
			}
			dataset, err := LoadPool(dir, scenario, part.name)
			if err != nil {
				return nil, nil, err
			}
			for _, item := range dataset.Items() {
				var parameters env.Parameters
				if err := json.Unmarshal(item.Data, &parameters); err != nil {
					return nil, nil, err
				}
				if parameters.Scenario != scenario {
					return nil, nil, fmt.Errorf("validation item scenario differs from pool")
				}
				agent := "longrun." + parameters.Template
				if scenario == "no_summariser" {
					agent += "_nosum"
				}
				if scenario == "summariser_rejected" {
					agent += "_reject"
				}
				for _, variant := range scenarios.Variants() {
					key := eval.AttemptKey{Variant: variant.ID, Scenario: scenario, Item: item.ID, Repeat: 1}
					if _, exists := expected[key]; exists {
						return nil, nil, fmt.Errorf("duplicate validation item %s", key.String())
					}
					expected[key] = validationExpectation{partition: part.name, agent: agent}
					policies[scenario+"/"+variant.ID+"/"+agent] = true
				}
			}
		}
	}
	return expected, policies, nil
}
