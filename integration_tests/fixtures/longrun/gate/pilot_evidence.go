package suite

import (
	"fmt"
	"reflect"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/google/uuid"
)

// PilotObservation preserves one validated baseline outcome and observed models.
type PilotObservation struct {
	Key         eval.AttemptKey   `json:"key"`
	Success     bool              `json:"success"`
	Unsupported bool              `json:"unsupported"`
	Evidence    bool              `json:"evidence"`
	Models      map[string]string `json:"models"`
	Error       string            `json:"error,omitempty"`
}

// PilotEvidence binds a complete baseline-only pilot to source and role choices.
type PilotEvidence struct {
	Version          int                          `json:"version"`
	Configuration    string                       `json:"configuration"`
	ExperimentID     string                       `json:"experiment_id"`
	ExperimentSHA256 string                       `json:"experiment_sha256"`
	SourceSHA256     string                       `json:"source_sha256"`
	Profile          longrun.ConfigurationProfile `json:"profile"`
	Rows             []PilotObservation           `json:"rows"`
}

// VerifyPilot requires the exact two-repeat pilot manifest and recorded roles.
func VerifyPilot(dir, configuration string, pilot PilotEvidence, profile longrun.ConfigurationProfile, sourceSHA string) (map[string]gate.Rates, error) {
	if pilot.Version != 1 || pilot.Configuration != configuration || profile.Configuration != configuration || !reflect.DeepEqual(pilot.Profile, profile) || !sourceHex(sourceSHA, 32) || pilot.SourceSHA256 != sourceSHA || !sourceHex(pilot.ExperimentSHA256, 32) {
		return nil, fmt.Errorf("pilot source or model profile mismatch")
	}
	if _, err := uuid.Parse(pilot.ExperimentID); err != nil {
		return nil, fmt.Errorf("invalid pilot experiment identity: %w", err)
	}
	if _, err := longrun.ProfileConfiguration(profile, fake.Faults{}); err != nil {
		return nil, err
	}
	expected, err := pilotItems(dir, configuration)
	if err != nil {
		return nil, err
	}
	if len(pilot.Rows) != len(expected)*2 {
		return nil, fmt.Errorf("pilot requires the complete two-repeat baseline manifest")
	}
	seen := map[eval.AttemptKey]bool{}
	rates := map[string]gate.Rates{}
	for _, row := range pilot.Rows {
		if _, ok := expected[row.Key.Scenario+"/"+row.Key.Item]; !ok || row.Key.Variant != "baseline" || row.Key.Repeat < 1 || row.Key.Repeat > 2 || seen[row.Key] || row.Error != "" {
			return nil, fmt.Errorf("invalid, duplicate or infrastructure-failed pilot row %s", row.Key.String())
		}
		seen[row.Key] = true
		if row.Models["planner"] != profile.Roles["planner"].Normalized {
			return nil, fmt.Errorf("pilot lacks the configured planner identity")
		}
		for role, observed := range row.Models {
			want, ok := profile.Roles[role]
			if !ok || observed != want.Normalized {
				return nil, fmt.Errorf("pilot observed model identity differs for %s", role)
			}
		}
		rate := rates[row.Key.Scenario]
		if row.Success {
			rate.Success++
		}
		if row.Unsupported {
			rate.Unsupported++
		}
		if row.Evidence {
			rate.Evidence++
		}
		rates[row.Key.Scenario] = rate
	}
	for _, scenario := range env.GatedScenarios(configuration) {
		rate := rates[scenario]
		rate.Success /= 12
		rate.Unsupported /= 12
		rate.Evidence /= 12
		rates[scenario] = rate
	}
	return rates, nil
}

func pilotItems(dir, configuration string) (map[string]eval.Item, error) {
	items := map[string]eval.Item{}
	for _, scenario := range env.GatedScenarios(configuration) {
		pool, err := LoadPool(dir, scenario, "pilot")
		if err != nil {
			return nil, err
		}
		if len(pool.Items()) != 6 {
			return nil, fmt.Errorf("pilot requires six items for %s", scenario)
		}
		for _, item := range pool.Items() {
			key := scenario + "/" + item.ID
			if _, ok := items[key]; ok {
				return nil, fmt.Errorf("duplicate pilot pool item %s", key)
			}
			items[key] = item
		}
	}
	return items, nil
}
