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

func TestMultimodalEvidenceRecovered(t *testing.T) {
	for _, variant := range scenarios.Variants()[2:] {
		t.Run(variant.ID, func(t *testing.T) {
			items := smokeItems(t, "multimodal_offline")
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("multimodal_offline", items)}, Variants: []eval.Variant{variant}, Repeats: 1, Concurrency: 2})
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
				var spec evidence.Spec
				for _, item := range items.Items() {
					if item.ID == attempt.Item {
						m, err := env.Materialize(item)
						require.NoError(t, err)
						spec = m.Spec
					}
				}
				result, err := evidence.Classify(records, spec, true)
				require.NoError(t, err)
				require.Len(t, result.Needs, 1)
				assert.ElementsMatch(t, []evidence.EvidenceID{"image", "pdf"}, result.Needs[0].Absent)
				assert.True(t, result.RecallSuccess, attempt.AttemptKey.String())
				assert.Equal(t, eval.VerdictPassed, attempt.Verdict(), attempt.AttemptKey.String())
			}
		})
	}
}
