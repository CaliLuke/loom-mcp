package scenarios_test

import (
	"testing"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHybridQualifyingEvidenceRecalled(t *testing.T) {
	for _, id := range env.GatedScenarios("offline") {
		t.Run(id, func(t *testing.T) {
			items := smokeItems(t, id)
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New(id, items)}, Variants: scenarios.Variants()[3:], Repeats: 1, Concurrency: 3})
			require.NoError(t, err)
			for _, attempt := range report.Attempts {
				require.NoError(t, attempt.HarnessErr)
				require.NotNil(t, attempt.Outcome)
				require.NotNil(t, attempt.Outcome.Session)
				records := make([]*runrecord.RunRecord, 0, len(attempt.Outcome.Session.Runs))
				for _, run := range attempt.Outcome.Session.Runs {
					require.NoError(t, run.RunErr)
					records = append(records, run.Record)
				}
				for _, item := range items.Items() {
					if item.ID != attempt.Item {
						continue
					}
					m, err := env.Materialize(item)
					require.NoError(t, err)
					result, err := evidence.Classify(records, m.Spec, true)
					require.NoError(t, err)
					if result.RecallQualifying {
						assert.True(t, result.RecallSuccess, "%s: needs=%+v", attempt.AttemptKey.String(), result.Needs)
					}
				}
			}
		})
	}
}
