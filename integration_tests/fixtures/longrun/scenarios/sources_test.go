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

func TestObsoleteEvidenceShapes(t *testing.T) {
	for _, id := range []string{"obsolete_index_line", "obsolete_collapsed", "obsolete_buried", "obsolete_cross_run"} {
		t.Run(id, func(t *testing.T) {
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New(id, smokeItems(t, id))}, Variants: scenarios.Variants()[2:3], Repeats: 1, Concurrency: 2})
			require.NoError(t, err)
			for _, a := range report.Attempts {
				require.NoError(t, a.HarnessErr)
				require.NotNil(t, a.Outcome)
				scores := map[string]float64{}
				for _, score := range a.Outcome.Scores {
					scores[score.Name] += score.Value
				}
				if id == "obsolete_index_line" || id == "obsolete_cross_run" {
					assert.Positive(t, scores["source_user_index"], "%s: %v", a.AttemptKey.String(), scores)
				} else {
					assert.Positive(t, scores["recall_qualifying"], "%s: %v", a.AttemptKey.String(), scores)
				}
			}
		})
	}
}

func TestDecisionDependenciesAbsentBeforeRecall(t *testing.T) {
	for _, item := range smokeItems(t, "decisions_dependencies").Items() {
		materialized, err := env.Materialize(item)
		require.NoError(t, err)
		report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("decisions_dependencies", eval.Items(item))}, Variants: scenarios.Variants()[2:3], Repeats: 1})
		require.NoError(t, err)
		attempt := report.Attempts[0]
		require.NoError(t, attempt.HarnessErr)
		require.NotNil(t, attempt.Outcome)
		require.NotNil(t, attempt.Outcome.Session)
		records := make([]*runrecord.RunRecord, 0, len(attempt.Outcome.Session.Runs))
		for _, r := range attempt.Outcome.Session.Runs {
			records = append(records, r.Record)
		}
		result, err := evidence.Classify(records, materialized.Spec, true)
		require.NoError(t, err)
		require.Len(t, result.Needs, 1)
		assert.ElementsMatch(t, []evidence.EvidenceID{"decision", "invoice_a", "invoice_b", "invoice_c"}, result.Needs[0].Absent, "%s: present=%v", item.ID, result.Needs[0].Present)
		assert.True(t, result.RecallSuccess, item.ID)
		assert.Equal(t, eval.VerdictPassed, attempt.Verdict(), item.ID)
	}
}

func TestHybridKeptFactIsNotRecallQualifying(t *testing.T) {
	for _, id := range []string{"obsolete_buried", "obsolete_collapsed"} {
		t.Run(id, func(t *testing.T) {
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New(id, smokeItems(t, id))}, Variants: scenarios.Variants()[3:], Repeats: 1, Concurrency: 3})
			require.NoError(t, err)
			require.Len(t, report.Attempts, 3)
			for _, a := range report.Attempts {
				require.NoError(t, a.HarnessErr)
				require.NotNil(t, a.Outcome)
				scores := map[string]float64{}
				for _, s := range a.Outcome.Scores {
					scores[s.Name] = s.Value
				}
				assert.Zero(t, scores["recall_qualifying"], a.AttemptKey.String())
				assert.Positive(t, scores["source_summary"]+scores["source_user_index"], a.AttemptKey.String())
				assert.InDelta(t, 1.0, scores["evidence_used"], 0, a.AttemptKey.String())
			}
		})
	}
}
