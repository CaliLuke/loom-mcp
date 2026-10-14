package scenarios_test

import (
	"os"
	"strings"
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cProfileVariantDefaults(t *testing.T) {
	for _, variant := range scenarios.Variants() {
		t.Run(variant.ID, func(t *testing.T) {
			policy := variant.Policy.Context
			require.NotNil(t, policy.Threshold)
			assert.Equal(t, variant.ID != "baseline", *policy.Threshold)
			assert.Nil(t, policy.ContextWindow)
			assert.Nil(t, policy.KeepRecentTokens)
			assert.Nil(t, policy.KeepUserTokens)
			assert.Nil(t, policy.ProtectRecentToolTokens)
			assert.Nil(t, policy.MinSavingTokens)
			assert.Nil(t, policy.MaxSummaryTokens)
			if variant.ID != "baseline" {
				require.NotNil(t, policy.KeepNonTextResident)
				assert.True(t, *policy.KeepNonTextResident)
			}
		})
	}
}

func TestK19cProfileMultimodalStructure(t *testing.T) {
	for _, tc := range []struct {
		configuration, model string
		resident             bool
	}{
		{"offline", "reader", false},
		{"anthropic", "claude-sonnet-4-20250514", false},
		{"bedrock-anthropic", "us.anthropic.claude-sonnet-4-20250514-v1:0", false},
		{"openai", "gpt-4.1-2025-04-14", true},
		{"gemini", "gemini-2.5-pro", true},
	} {
		t.Run(tc.configuration, func(t *testing.T) {
			for _, role := range []string{"planner", "planner_small", "summariser"} {
				t.Setenv("LONGRUN_"+strings.ToUpper(strings.ReplaceAll(tc.configuration, "-", "_"))+"_"+strings.ToUpper(role), tc.model)
			}
			profile, err := longrun.ResolveProfile(tc.configuration)
			require.NoError(t, err)
			dataset := eval.JSONL(os.DirFS("../testdata/pools"), "multimodal_offline/smoke.jsonl")
			require.NoError(t, dataset.Err())
			items := dataset.Items()
			require.NotEmpty(t, items)
			for _, variant := range scenarios.Variants()[2:] {
				t.Run(variant.ID, func(t *testing.T) {
					report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.ProfileHarness(profile, fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("multimodal_offline", eval.Items(items[0]))}, Variants: []eval.Variant{variant}, Repeats: 1, Concurrency: 1})
					require.NoError(t, err)
					require.Len(t, report.Attempts, 1)
					attempt := report.Attempts[0]
					require.NoError(t, attempt.HarnessErr)
					require.NotNil(t, attempt.Outcome)
					require.NoError(t, attempt.Outcome.Err)
					require.NotNil(t, attempt.Outcome.Session)
					require.NotEmpty(t, attempt.Outcome.Session.Runs)
					for _, run := range attempt.Outcome.Session.Runs {
						require.NoError(t, run.RunErr)
						require.NoError(t, scenarios.CheckStructure("multimodal_offline", variant.ID, run.Record))
						for _, recorded := range run.Record.Runs {
							assert.Equal(t, tc.resident, recorded.NonTextResident)
							if tc.resident {
								assert.Zero(t, recorded.ToolResultParts.MaxImageBytes)
								assert.Zero(t, recorded.ToolResultParts.MaxDocumentBytes)
							}
							count := 0
							for _, event := range recorded.Events {
								if event.Type == hooks.ContextNonTextResident {
									count++
								}
							}
							if tc.resident {
								assert.Equal(t, 1, count)
							} else {
								assert.Zero(t, count)
							}
							require.NotNil(t, recorded.ContextSettings)
							assert.True(t, recorded.ContextSettings.KeepNonTextResident)
						}
					}
					assert.Equal(t, eval.VerdictPassed, attempt.Verdict(), "offline profile must retain scenario evidence")
					evidence := false
					for _, score := range attempt.Outcome.Scores {
						if score.Name == "evidence_used" {
							evidence = score.Value == 1
						}
					}
					assert.True(t, evidence, "original evidence remains available even when recall is disabled")
				})
			}
		})
	}
}
