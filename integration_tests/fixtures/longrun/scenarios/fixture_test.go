package scenarios_test

import (
	"os"
	"testing"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmokeStructure(t *testing.T) {
	for _, id := range env.ScenarioIDs("offline") {
		t.Run(id, func(t *testing.T) {
			for _, v := range scenarios.Variants() {
				t.Run(v.ID, func(t *testing.T) {
					s := scenarios.New(id, smokeItems(t, id))
					r, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{s}, Variants: []eval.Variant{v}, Repeats: 1, Concurrency: 2})
					require.NoError(t, err)
					for _, a := range r.Attempts {
						require.NoError(t, a.HarnessErr)
						require.NotNil(t, a.Outcome)
						require.NoError(t, a.Outcome.Err)
						require.NotNil(t, a.Outcome.Session)
						for _, run := range a.Outcome.Session.Runs {
							require.NoError(t, run.RunErr)
							require.NoError(t, scenarios.CheckStructure(id, v.ID, run.Record), a.AttemptKey.String())
							if v.ID == "baseline" {
								for _, recorded := range run.Record.Runs {
									for _, call := range recorded.Calls {
										if call.Completion != nil {
											assert.Less(t, call.Completion.Usage.InputTokens, 128000, a.AttemptKey.String())
										}
									}
								}
							}
						}
						if v.ID != "clear_only" && id != "model_change" {
							assert.Equal(t, eval.VerdictPassed, a.Verdict(), a.AttemptKey.String())
						}
					}
				})
			}
		})
	}
}

func smokeItems(t *testing.T, id string) eval.Dataset {
	t.Helper()
	data := eval.JSONL(os.DirFS("../testdata/pools"), id+"/smoke.jsonl")
	require.NoError(t, data.Err())
	return data
}
