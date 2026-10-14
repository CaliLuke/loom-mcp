package suite

import (
	"encoding/json"
	"slices"
	"testing"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/require"
)

func TestK19cContractValidationCapturesRuntimeSettings(t *testing.T) {
	profile, err := longrun.ResolveProfile("offline")
	require.NoError(t, err)
	_, pools, err := Inputs("..")
	require.NoError(t, err)
	expected, _, err := validationManifest("..", "offline", pools)
	require.NoError(t, err)
	for _, scenario := range env.ScenarioIDs("offline") {
		t.Run(scenario, func(t *testing.T) {
			dataset, err := LoadPool("..", scenario, "smoke")
			require.NoError(t, err)
			items := dataset.Items()
			require.NotEmpty(t, items)
			keys := make([]eval.AttemptKey, 0, 12)
			for _, item := range items {
				for _, variant := range scenarios.Variants() {
					keys = append(keys, eval.AttemptKey{Scenario: scenario, Variant: variant.ID, Item: item.ID, Repeat: 1})
				}
			}
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.ProfileHarness(profile, fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New(scenario, dataset)}, Variants: scenarios.Variants(), Repeats: 1, Concurrency: 4, Select: eval.Selection{Keys: keys}})
			require.NoError(t, err)
			require.Len(t, report.Attempts, 12)
			v := Validation{Profile: profile, EffectivePolicies: map[string]json.RawMessage{}}
			for _, attempt := range report.Attempts {
				row := ValidationRow{Key: attempt.AttemptKey, Partition: "smoke"}
				err := captureValidationAttempt(&v, &row, attempt, expected[row.Key].agent)
				require.NoError(t, err, row.Key.String())
			}
			require.Len(t, v.EffectivePolicies, 12)
		})
	}
}

func TestK19cContractValidationRejectsResidentEvidenceLoss(t *testing.T) {
	for _, role := range []string{"PLANNER", "PLANNER_SMALL", "SUMMARISER"} {
		t.Setenv("LONGRUN_OPENAI_"+role, "gpt-4.1-2025-04-14")
	}
	profile, err := longrun.ResolveProfile("openai")
	require.NoError(t, err)
	dataset, err := LoadPool("..", "multimodal_openai", "smoke")
	require.NoError(t, err)
	report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.ProfileHarness(profile, fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("multimodal_openai", eval.Items(dataset.Items()[0]))}, Variants: scenarios.Variants()[2:], Repeats: 1, Concurrency: 2})
	require.NoError(t, err)
	require.Len(t, report.Attempts, 2)
	for _, attempt := range report.Attempts {
		t.Run(attempt.AttemptKey.Variant, func(t *testing.T) {
			agent := string(attempt.Outcome.Session.Runs[0].Trajectory.AgentID)
			v := Validation{Profile: profile, EffectivePolicies: map[string]json.RawMessage{}}
			row := ValidationRow{Key: attempt.AttemptKey, Partition: "smoke"}
			require.NoError(t, captureValidationAttempt(&v, &row, attempt, agent))
			scores := slices.Clone(attempt.Outcome.Scores)
			for _, mode := range []string{"lost", "missing", "duplicate", "residency", "capability"} {
				t.Run(mode, func(t *testing.T) {
					attempt.Outcome.Scores = slices.Clone(scores)
					record := &attempt.Outcome.Session.Runs[0].Record.Runs[0]
					resident, support := record.NonTextResident, record.ToolResultParts
					defer func() { record.NonTextResident, record.ToolResultParts = resident, support }()
					found := false
					for i := range attempt.Outcome.Scores {
						if attempt.Outcome.Scores[i].Name != "evidence_used" {
							continue
						}
						found = true
						switch mode {
						case "lost":
							attempt.Outcome.Scores[i].Value = 0
						case "missing":
							attempt.Outcome.Scores = append(attempt.Outcome.Scores[:i], attempt.Outcome.Scores[i+1:]...)
						case "duplicate":
							attempt.Outcome.Scores = append(attempt.Outcome.Scores, attempt.Outcome.Scores[i])
						case "residency":
							record.NonTextResident = false
						case "capability":
							record.ToolResultParts.MaxImageBytes = 1
						}
						break
					}
					require.True(t, found)
					require.Error(t, captureValidationAttempt(&v, &row, attempt, agent))
				})
			}
		})
	}
}
