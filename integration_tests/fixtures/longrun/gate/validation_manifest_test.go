package suite

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/stretchr/testify/require"
)

func TestK19cContractValidationManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	complete := validationFixture(t, dir)
	require.NoError(t, VerifyValidation(dir, "offline", complete))
	for _, tc := range []struct {
		name   string
		change func(*Validation)
	}{
		{"missing row", func(v *Validation) { v.Rows = v.Rows[1:]; v.Attempts-- }},
		{"duplicate row", func(v *Validation) { v.Rows[0] = v.Rows[1] }},
		{"wrong partition", func(v *Validation) { v.Rows[0].Partition = "look99" }},
		{"unknown variant", func(v *Validation) { v.Rows[0].Key.Variant = "other" }},
		{"wrong repeat", func(v *Validation) { v.Rows[0].Key.Repeat = 2 }},
		{"failed structure", func(v *Validation) { v.Rows[0].Failure = "missing evidence" }},
		{"partial profiles", func(v *Validation) { delete(v.Profile.Roles, "planner_small") }},
		{"missing q", func(v *Validation) { delete(v.Qualifying, "hybrid") }},
		{"forged q", func(v *Validation) { v.Qualifying["hybrid"]["fact_early"] = .5 }},
		{"missing effective policy", func(v *Validation) { v.EffectivePolicies = nil }},
		{"missing source", func(v *Validation) { v.SourceSHA256 = "" }},
		{"unknown version", func(v *Validation) { v.Version = 99 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(complete)
			require.NoError(t, err)
			var modified Validation
			require.NoError(t, json.Unmarshal(data, &modified))
			tc.change(&modified)
			require.Error(t, VerifyValidation(dir, "offline", modified))
		})
	}
}

func validationFixture(t *testing.T, dir string) Validation {
	t.Helper()
	return validationFixtureFor(t, dir, "offline")
}

func validationFixtureFor(t *testing.T, dir, configuration string) Validation {
	t.Helper()
	profile, err := longrun.ResolveProfile(configuration)
	require.NoError(t, err)
	_, pools, err := Inputs(dir)
	require.NoError(t, err)
	data, err := json.Marshal(pools)
	require.NoError(t, err)
	v := Validation{Version: 2, Configuration: configuration, Profile: profile, Repeats: 1, MaterializerVersion: env.MaterializerVersion, PoolsSHA256: gate.Digest(data), SourceSHA256: strings.Repeat("a", 64), Qualifying: map[string]map[string]float64{"receipt_only": {}, "hybrid": {}}, EffectivePolicies: map[string]json.RawMessage{}}
	for _, pool := range pools {
		if !slices.Contains(env.ScenarioIDs(configuration), pool.Scenario) {
			continue
		}
		for _, item := range pool.Items {
			for _, variant := range scenarios.Variants() {
				v.Rows = append(v.Rows, ValidationRow{Key: eval.AttemptKey{Scenario: pool.Scenario, Variant: variant.ID, Item: item.ID, Repeat: 1}, Partition: pool.Partition, Qualifying: true, EvidencePresent: true})
			}
		}
		for _, variant := range scenarios.Variants() {
			for _, domain := range env.Domains() {
				agent := "longrun." + domain.Name
				if pool.Scenario == "no_summariser" {
					agent += "_nosum"
				}
				if pool.Scenario == "summariser_rejected" {
					agent += "_reject"
				}
				v.EffectivePolicies[pool.Scenario+"/"+variant.ID+"/"+agent] = expectedValidationPolicy(pool.Scenario, variant.ID, agent)
			}
		}
	}
	v.Attempts = len(v.Rows)
	for _, scenario := range env.GatedScenarios(configuration) {
		v.Qualifying["receipt_only"][scenario] = 1
		v.Qualifying["hybrid"][scenario] = 1
	}
	return v
}

func TestK19cContractValidateConfigurationPreflight(t *testing.T) {
	for _, tc := range []struct{ name, configuration, source string }{{"unknown profile", "unknown", strings.Repeat("a", 64)}, {"missing source", "offline", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateConfiguration(t.Context(), t.TempDir(), tc.configuration, tc.source, 1, io.Discard)
			require.Error(t, err)
		})
	}
}

func TestK19cContractValidationAllowsObservedZeroQ(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	v := validationFixture(t, dir)
	for i := range v.Rows {
		if v.Rows[i].Key.Scenario == "fact_early" && v.Rows[i].Key.Variant == "receipt_only" {
			v.Rows[i].Qualifying = false
		}
	}
	v.Qualifying["receipt_only"]["fact_early"] = 0
	require.NoError(t, VerifyValidation(dir, "offline", v), "zero q is a valid observed stratum with positive pooled qualifying evidence")
}

func TestK19cContractValidationRejectsWrongEffectiveSettings(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	complete := validationFixture(t, dir)
	for _, tc := range []struct {
		name   string
		change func(*compaction.Settings)
	}{
		{"threshold", func(s *compaction.Settings) { s.Threshold = false }},
		{"tier", func(s *compaction.Settings) { s.MaxTier = compaction.Clear }},
		{"window", func(s *compaction.Settings) { s.ContextWindow = 9999 }},
		{"budget", func(s *compaction.Settings) { s.KeepRecentTokens = 1000 }},
		{"pressure", func(s *compaction.Settings) { s.RecallReadCalls = 99 }},
		{"summary model", func(s *compaction.Settings) { s.Summary.Model.ID = "wrong" }},
		{"prompt version", func(s *compaction.Settings) { s.Prompt.Ref.Version = "wrong" }},
		{"prompt ref", func(s *compaction.Settings) { s.Prompt.Ref.ID = "wrong" }},
		{"instructions", func(s *compaction.Settings) { s.Summary.Settings.Instructions = "wrong" }},
		{"prompt", func(s *compaction.Settings) {
			s.Prompt.Template = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "recall_pressure/hybrid/longrun.ops_deployer"
			original := complete.EffectivePolicies[key]
			var settings compaction.Settings
			require.NoError(t, compaction.DecodeCanonical(original, &settings))
			tc.change(&settings)
			complete.EffectivePolicies[key] = compaction.Canonical(settings)
			require.Error(t, VerifyValidation(dir, "offline", complete))
			complete.EffectivePolicies[key] = original
		})
	}
}

func TestK19cContractValidationSummaryTemplateIdentity(t *testing.T) {
	data, err := os.ReadFile("../../../../runtime/agent/runtime/summary_prompt.tmpl")
	require.NoError(t, err)
	require.Equal(t, validationSummaryTemplate, fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
}

func TestK19cContractValidationResidentManifest(t *testing.T) {
	for _, role := range []string{"PLANNER", "PLANNER_SMALL", "SUMMARISER"} {
		t.Setenv("LONGRUN_OPENAI_"+role, "gpt-4.1-2025-04-14")
	}
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	v := validationFixtureFor(t, dir, "openai")
	for i := range v.Rows {
		if validationResident(v.Profile, v.Rows[i].Key) {
			v.Rows[i].Qualifying = false
		}
	}
	v.Qualifying = validationFractions(v.Rows, "openai")
	require.NoError(t, VerifyValidation(dir, "openai", v), "resident zero-q observations remain valid")
	for i := range v.Rows {
		if validationResident(v.Profile, v.Rows[i].Key) {
			v.Rows[i].EvidencePresent = false
			break
		}
	}
	require.ErrorContains(t, VerifyValidation(dir, "openai", v), "evidence")
}
