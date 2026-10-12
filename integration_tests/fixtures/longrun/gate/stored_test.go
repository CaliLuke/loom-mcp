package suite_test

import (
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	suite "example.com/longrun/gate"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoredOnlyMatchesInMemory(t *testing.T) {
	pool, err := suite.LoadPool("..", "fact_early", "smoke")
	require.NoError(t, err)
	item := pool.Items()[0]
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	cfg := eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("fact_early", eval.Items(item))}, Variants: scenarios.Variants(), Repeats: 2, Concurrency: 2}
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: "stored-only"})
	require.NoError(t, err)
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(report, nil))
	exp, err := store.Load(writer.ID())
	require.NoError(t, err)
	stored := make([]eval.StoredAttempt, 0, len(exp.Attempts))
	memory := make([]eval.StoredAttempt, 0, len(report.Attempts))
	for _, a := range exp.Attempts {
		require.NoError(t, suite.VerifyMeasures(*a, item))
		stored = append(stored, *a)
	}
	for _, a := range report.Attempts {
		require.NoError(t, a.HarnessErr)
		require.NoError(t, a.Outcome.Err)
		memory = append(memory, eval.StoredAttempt{AttemptKey: a.AttemptKey, Checks: a.Outcome.Checks, Scores: a.Outcome.Scores})
	}
	c := gate.Config{Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Scenarios: []string{"fact_early"}, Repeats: 2, Seed: 19, Items: map[string][]string{"fact_early": {item.ID}}}
	actual, decision, err := suite.Analyze(stored, c, 1, nil)
	require.NoError(t, err)
	want, expected, err := suite.Analyze(memory, c, 1, nil)
	require.NoError(t, err)
	assert.Equal(t, want, actual)
	assert.Equal(t, expected, decision)
	// Tampering with any reserved score is rejected without constructing a harness.
	stored[0].Scores = append([]expect.Score(nil), stored[0].Scores...)
	for i := range stored[0].Scores {
		if stored[0].Scores[i].Name == "compactions" {
			stored[0].Scores[i].Value++
		}
	}
	assert.ErrorContains(t, suite.VerifyMeasures(stored[0], item), "compactions")
}
