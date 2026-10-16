package agenttest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestAttemptHarnessSelectsItemScriptsFixture(t *testing.T) {
	scenario := runnerScenario()
	scenario.Items = eval.Items(
		eval.Item{ID: "first", Input: "first", ExpectedTools: &expect.ExpectedTools{Exact: true}},
		eval.Item{ID: "second", Input: "second", ExpectedTools: &expect.ExpectedTools{Exact: true}},
	)
	var built []*agenttest.Harness
	factory := eval.NewAttemptHarnessFunc(func(_ context.Context, info eval.AttemptInfo) (eval.AttemptHarnessConfig, error) {
		turn := agenttest.Respond(agenttest.Text(info.Item.ID)).When(agenttest.LastUserContains(info.Item.Input))
		return eval.AttemptHarnessConfig{
			Register: registerScenarioFixture,
			Options:  []agenttest.Option{agenttest.ScriptedModel("fixture", turn)},
			Setup: func(h *agenttest.Harness) error {
				built = append(built, h)
				return nil
			},
		}, nil
	})
	report, err := eval.Run(t.Context(), eval.Config{AttemptHarness: factory, Scenarios: []eval.Scenario{scenario}, Repeats: 2})
	require.NoError(t, err)
	require.Len(t, report.Attempts, 4)
	for index, attempt := range report.Attempts {
		require.NoError(t, attempt.HarnessErr)
		require.NoError(t, attempt.Outcome.Err)
		assert.Equal(t, eval.VerdictPassed, attempt.Verdict())
		assert.Equal(t, attempt.Item, attempt.Outcome.Session.Runs[0].Output.Final.Parts[0].(model.TextPart).Text)
		for _, previous := range built[:index] {
			assert.NotSame(t, previous, built[index])
		}
	}
}
