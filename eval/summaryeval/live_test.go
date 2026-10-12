package summaryeval

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/features/model/anthropic"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveSummarySmoke(t *testing.T) {
	const id = "claude-sonnet-4-5-20250929"
	agenttest.SkipUnlessLive(t, id)
	if testing.Short() {
		t.Skip("live summaries are outside the short-test gate")
	}
	provider, err := anthropic.NewFromAPIKey(os.Getenv("ANTHROPIC_API_KEY"), id)
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	cfg := Config{Prompts: map[string]string{"smoke": "Summarize this quoted evidence in one sentence: {{.Evicted}}"}, NewModels: func() (map[string]Model, error) {
		return map[string]Model{id: {Client: client}}, nil
	}}
	v, err := ParseVariant(cfg, "prompt=smoke,model="+id)
	require.NoError(t, err)
	e, err := Run(t.Context(), cfg, Options{Dir: t.TempDir(), Cuts: Cuts(recordedFixture(t)), Variants: []Variant{v}})
	require.NoError(t, err)
	assert.NotEmpty(t, e.Lines[0].New.Calls)
}
