package scenarios_test

import (
	"encoding/json"
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionUsesContinuationOnlyWhenCompacted(t *testing.T) {
	scenario := scenarios.New("session_three_runs", eval.Items(smokeItems(t, "session_three_runs").Items()[0]))
	report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: []eval.Scenario{scenario}, Variants: scenarios.Variants(), Repeats: 1, Concurrency: 2})
	require.NoError(t, err)
	for _, a := range report.Attempts {
		require.NoError(t, a.HarnessErr)
		require.NotNil(t, a.Outcome)
		require.NotNil(t, a.Outcome.Session)
		require.Len(t, a.Outcome.Session.Runs, 3)
		for i, result := range a.Outcome.Session.Runs {
			started, continued := 0, 0
			for _, run := range result.Record.Runs {
				for _, event := range run.Events {
					if event.Type != hooks.ContinuationStarted {
						continue
					}
					started++
					var payload struct {
						Outcome string `json:"outcome"`
					}
					require.NoError(t, json.Unmarshal(event.Payload, &payload))
					if payload.Outcome == "continued" {
						continued++
					}
				}
			}
			if a.Variant == "baseline" {
				assert.Zero(t, started)
			} else if i > 0 {
				assert.Equal(t, 1, continued, a.AttemptKey.String())
			}
		}
	}
}
