package suite

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
)

// ValidateConfiguration exercises the declared offline capability profile and
// records the complete structural manifest under a verified source identity.
// The evidence-writing boundary verifies source correspondence before and after it.
func ValidateConfiguration(ctx context.Context, dir, configuration, sourceSHA256 string, workers int, progress io.Writer) (Validation, error) {
	profile, err := longrun.ResolveProfile(configuration)
	if err != nil {
		return Validation{}, err
	}
	source, err := hex.DecodeString(sourceSHA256)
	if err != nil || len(source) != 32 || workers < 1 {
		return Validation{}, fmt.Errorf("validation requires verified source identity and positive workers")
	}
	if progress == nil {
		progress = io.Discard
	}
	_, pools, err := Inputs(dir)
	if err != nil {
		return Validation{}, err
	}
	expected, _, err := validationManifest(dir, configuration, pools)
	if err != nil {
		return Validation{}, err
	}
	data, err := json.Marshal(pools)
	if err != nil {
		return Validation{}, err
	}
	result := Validation{Version: 2, Configuration: configuration, Profile: profile, Repeats: 1, SourceSHA256: sourceSHA256, MaterializerVersion: env.MaterializerVersion, PoolsSHA256: gate.Digest(data), Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}, EffectivePolicies: map[string]json.RawMessage{}}
	harness := longrun.ProfileHarness(profile, fake.Faults{})
	for _, pool := range pools {
		if !slices.Contains(env.ScenarioIDs(configuration), pool.Scenario) {
			continue
		}
		dataset, err := LoadPool(dir, pool.Scenario, pool.Partition)
		if err != nil {
			return result, err
		}
		report, err := eval.Run(ctx, eval.Config{Harness: harness, Scenarios: []eval.Scenario{scenarios.New(pool.Scenario, dataset)}, Variants: scenarios.Variants(), Repeats: 1, Concurrency: workers})
		if err != nil {
			return result, err
		}
		for _, attempt := range report.Attempts {
			planned, ok := expected[attempt.AttemptKey]
			if !ok || planned.partition != pool.Partition {
				return result, fmt.Errorf("validation executed an unplanned key %s", attempt.AttemptKey.String())
			}
			row := ValidationRow{Key: attempt.AttemptKey, Partition: pool.Partition}
			if err := captureValidationAttempt(&result, &row, attempt, planned.agent); err != nil {
				row.Failure = err.Error()
				result.Failures = append(result.Failures, row.Key.String()+": "+row.Failure)
			}
			result.Rows = append(result.Rows, row)
		}
		if _, err := fmt.Fprintf(progress, "validated %s/%s [%s]: %d attempts\n", pool.Scenario, pool.Partition, configuration, len(report.Attempts)); err != nil {
			return result, err
		}
	}
	result.Attempts = len(result.Rows)
	result.Qualifying = validationFractions(result.Rows, configuration)
	return result, VerifyValidation(dir, configuration, result)
}

// captureValidationAttempt takes settings from the actual runtime's recorded
// stamp, then canonicalizes them with its public window-dependent budget rules.
func captureValidationAttempt(validation *Validation, row *ValidationRow, attempt *eval.AttemptRecord, agent string) (err error) {
	structureErr := attemptStructure(attempt)
	defer func() { err = errors.Join(structureErr, err) }()
	if attempt.Outcome == nil || attempt.Outcome.Session == nil {
		return fmt.Errorf("missing validation outcome or session")
	}
	row.EvidencePresent = false
	evidenceCount := 0
	for _, score := range attempt.Outcome.Scores {
		if score.Name == "evidence_used" {
			evidenceCount++
			row.EvidencePresent = score.Value == 1
		}
	}
	if validationResident(validation.Profile, row.Key) && (evidenceCount != 1 || !row.EvidencePresent) {
		return fmt.Errorf("resident multimodal validation requires original evidence")
	}
	seenScore := false
	for _, score := range attempt.Outcome.Scores {
		if score.Name != "recall_qualifying" {
			continue
		}
		if seenScore || (score.Value != 0 && score.Value != 1) {
			return fmt.Errorf("invalid recall qualifying score")
		}
		row.Qualifying = score.Value == 1
		seenScore = true
	}
	if !seenScore {
		return fmt.Errorf("missing recall qualifying observation")
	}
	key := row.Key.Scenario + "/" + row.Key.Variant + "/" + agent
	for _, result := range attempt.Outcome.Session.Runs {
		if string(result.Trajectory.AgentID) != agent {
			return fmt.Errorf("validation agent identity mismatch")
		}
		if result.Record == nil || len(result.Record.Runs) == 0 {
			return fmt.Errorf("missing canonical run record")
		}
		for _, record := range result.Record.Runs {
			if strings.HasPrefix(row.Key.Scenario, "multimodal_") && row.Key.Variant != "baseline" {
				support := validation.Profile.Supports["planner"]
				if record.NonTextResident != validationResident(validation.Profile, row.Key) || !reflect.DeepEqual(record.ToolResultParts.Intersect(record.ToolResultParts), support.Intersect(support)) {
					return fmt.Errorf("multimodal validation capability or residency differs from declared profile")
				}
			}
			if record.ContextSettings == nil {
				return fmt.Errorf("missing runtime-stamped context settings")
			}
			canonical := compaction.Canonical(*record.ContextSettings)
			if err := verifyValidationPolicy(key, canonical); err != nil {
				return err
			}
			if previous, ok := validation.EffectivePolicies[key]; ok && !bytes.Equal(previous, canonical) {
				return fmt.Errorf("effective context policy changed within validation: %s", key)
			}
			validation.EffectivePolicies[key] = canonical
		}
	}
	return nil
}

func validationFractions(rows []ValidationRow, configuration string) map[string]map[string]float64 {
	fractions := map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}
	counts := map[string]map[string]int{"receipt_only": {}, "hybrid": {}}
	for _, row := range rows {
		if byScenario, ok := counts[row.Key.Variant]; ok && slices.Contains(env.GatedScenarios(configuration), row.Key.Scenario) {
			byScenario[row.Key.Scenario]++
			if row.Qualifying {
				fractions[row.Key.Variant][row.Key.Scenario]++
			}
		}
	}
	for variant, byScenario := range counts {
		for _, scenario := range env.GatedScenarios(configuration) {
			if byScenario[scenario] > 0 {
				fractions[variant][scenario] /= float64(byScenario[scenario])
			}
		}
	}
	return fractions
}
