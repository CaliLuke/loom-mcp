package longrun_test

import (
	"os"
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveOptInPrecedesConstruction(t *testing.T) {
	t.Setenv("LOOM_LIVE_MODEL", "0")
	for _, id := range []string{"anthropic", "bedrock-anthropic", "openai", "gemini"} {
		h, err := longrun.Harness(id, fake.Faults{})(t.Context(), eval.Variant{})
		require.ErrorIs(t, err, agenttest.ErrLiveModelDisabled)
		assert.Nil(t, h)
	}
}
func TestLiveSmoke(t *testing.T) {
	agenttest.SkipUnlessLive(t, "planner")
	pool := eval.JSONL(os.DirFS("testdata/pools"), "fact_early/smoke.jsonl")
	require.NoError(t, pool.Err())
	for _, id := range []string{"anthropic", "bedrock-anthropic", "openai", "gemini"} {
		t.Run(id, func(t *testing.T) {
			item := pool.Items()[0]
			report, err := eval.Run(t.Context(), eval.Config{Harness: longrun.Harness(id, fake.Faults{}), Scenarios: []eval.Scenario{scenarios.New("fact_early", eval.Items(item))}, Variants: scenarios.Variants()[:1]})
			require.NoError(t, err)
			require.NoError(t, report.Attempts[0].HarnessErr)
			require.NoError(t, report.Attempts[0].Outcome.Err)
		})
	}
}
