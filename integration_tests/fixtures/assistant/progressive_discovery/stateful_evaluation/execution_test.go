package statefulevaluation

import (
	"context"
	"testing"

	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeDiscoveryExecutionBoundary(t *testing.T) {
	f := newEvaluationFixture(t)
	key := discoveryKey{Agent: "a", Session: "one"}
	args := map[string]any{"text": "Alpha beta"}
	baseline := f.invoke(t, "compact", key, "summarize_text", args)
	require.Empty(t, baseline.Failure)
	native := f.invoke(t, "native", key, "summarize_text", args)
	assert.Contains(t, native.Failure, "unknown tool")
	require.Equal(t, 1, f.service.summaries)
	found := f.discover(t, key, "summarize_text")
	require.Contains(t, found.Names, "summarize_text")
	require.Empty(t, f.invoke(t, "native", key, "summarize_text", args).Failure)
	assert.Equal(t, 2, f.service.summaries)
	for _, other := range []discoveryKey{{Agent: "a", Session: "two"}, {Agent: "b", Session: "one"}} {
		assert.Contains(t, f.invoke(t, "native", other, "summarize_text", args).Failure, "unknown tool")
	}
	f.denied[key] = true
	for _, arm := range []string{"compact", "native"} {
		assert.Contains(t, f.invoke(t, arm, key, "summarize_text", args).Failure, "authority denied")
	}
	assert.Equal(t, 2, f.service.summaries)
	// Discovery discloses metadata but never grants execution authority.
	deniedKey := discoveryKey{Agent: "denied", Session: "one"}
	f.denied[deniedKey] = true
	require.Contains(t, f.discover(t, deniedKey, "summarize_text").Names, "summarize_text")
	assert.Contains(t, f.invoke(t, "native", deniedKey, "summarize_text", args).Failure, "authority denied")
	assert.Equal(t, 2, f.service.summaries)
	f.denied[key] = false
	f.generation = "catalog-v2"
	assert.Contains(t, f.invoke(t, "native", key, "summarize_text", args).Failure, "unknown tool")
	f.discover(t, key, "summarize_text")
	assert.NotEmpty(t, f.invoke(t, "native", key, "summarize_text", map[string]any{}).Failure)
	assert.Equal(t, 2, f.service.summaries, "current generated schema rejects before host effect")
	require.Empty(t, f.invoke(t, "native", key, "summarize_text", args).Failure)
	assert.Equal(t, 3, f.service.summaries)
}

// This records a pre-existing generator/result-codec mismatch without hiding it.
func TestGeneratedLocalRuntimeMaterializationLimitation(t *testing.T) {
	f := newEvaluationFixture(t)
	rt := agentsruntime.New()
	require.NoError(t, rt.RegisterToolset(f.compact))
	ctx := context.WithValue(t.Context(), authorityKey{}, discoveryKey{Agent: "a", Session: "one"})
	output, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{RunID: "limitation", RootRunID: "limitation", SessionID: "one", ToolName: "call_tool", ToolCallID: "one-effect", Payload: []byte(`{"name":"summarize_text","arguments":{"text":"Alpha beta"}}`)})
	require.EqualError(t, err, "encode call_tool tool result: tool call_tool result must be a typed Go value, got jsontext.Value")
	assert.Nil(t, output)
	assert.Equal(t, 1, f.service.summaries, "the effect happened before result materialization failed; never retry it implicitly")
}
