package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextSummaryPromptPinnedAtStart(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	store := prompt.NewInMemoryStore()
	counted := &summaryCountingPromptStore{Store: store}
	rt.PromptRegistry.SetStore(counted)
	scope := prompt.Scope{SessionID: "selected"}
	require.NoError(t, store.Set(t.Context(), in.PromptID, scope, "PINNED {{.Budget}} {{.Evicted}}", nil))
	start := &RunInput{AgentID: in.AgentID, RootRunID: in.Scope, RunID: in.RunID, SessionID: scope.SessionID}
	rt.stampContextCompaction(start)
	require.NoError(t, rt.prepareContextStart(t.Context(), start))
	require.Equal(t, 1, counted.resolves)
	require.NoError(t, store.Set(t.Context(), in.PromptID, scope, "CHANGED {{.Budget}} {{.Evicted}}", nil))
	in.SessionID = scope.SessionID
	in.ContextSettings = start.ContextSettings
	in.ContextFingerprint = start.ContextFingerprint
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	assert.Equal(t, 1, counted.resolves)
	require.NotEmpty(t, client.requests)
	for _, req := range client.requests {
		assert.Contains(t, req.Messages[0].Parts[0].(model.TextPart).Text, "PINNED")
		assert.NotContains(t, req.Messages[0].Parts[0].(model.TextPart).Text, "CHANGED")
		assert.Equal(t, []prompt.PromptRef{start.ContextSettings.Prompt.Ref}, req.PromptRefs)
	}
}
