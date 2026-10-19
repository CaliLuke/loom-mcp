package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryRollingSeedAndTailRetention(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	transcript := compactionFixture(t, rt.RunStore, 4)
	for i := range transcript.Entries {
		if transcript.Entries[i].Kind == index.ToolResult {
			transcript.Entries[i].Hash = compactionPut(t, rt.RunStore, strings.Repeat("eligible result ", 500))
			transcript.Entries[i].Bytes = 8000
			transcript.Entries[i].Result = nil
		}
	}
	replaceSummaryEntries(t, rt, in, transcript.Entries)
	entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, in.Scope, hash)
	}, in.ViewRef.IndexHead)
	require.NoError(t, err)
	base := summaryWorkflowBase(in, entries)
	full, err := rt.loadTestView(t.Context(), in.Scope, in.RunID, base.view())
	require.NoError(t, err)
	require.Len(t, full.Messages, 9)
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}}
	summaries := []string{strings.Replace(summaryTestText, "Fact.", "First complete multiline summary.", 1), strings.Replace(summaryTestText, "Fact.", "Second superseding summary.", 1)}
	var previous *runstore.Hash
	for pass, cut := range []index.MessageID{4, 6} {
		client.response = summaryResponse(summaries[pass])
		view := base.view()
		view.TailFrom, view.Budget = cut, in.Budget
		base.compactionDecision.View = view
		input := PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: view, Step: in.Step + pass, ContextSettings: base.contextSettings}
		require.NoError(t, rt.prepareWorkflowSummary(wf, base, &input, time.Time{}))
		require.NotNil(t, input.View.Checkpoint, "summary pass %d must be accepted", pass)
		assert.Equal(t, cut, input.View.TailFrom)
		data, err := rt.RunStore.Get(t.Context(), in.Scope, *input.View.Checkpoint)
		require.NoError(t, err)
		var record index.CheckpointRecord
		require.NoError(t, json.Unmarshal(data, &record))
		assert.Equal(t, previous, record.Prev)
		assert.Equal(t, cut-1, record.Through)
		assert.Equal(t, base.Entries[cut-2].Chain, record.Chain)
		previous = input.View.Checkpoint
		loaded, err := rt.loadTestView(t.Context(), in.Scope, in.RunID, input.View)
		require.NoError(t, err)
		require.NotEmpty(t, loaded.Messages)
		text := loaded.Messages[0].Parts[0].(model.TextPart).Text
		assert.Contains(t, text, summaries[pass])
		assert.LessOrEqual(t, len(text), checkpoint.MaxBytes(in.Budget))
		assert.Equal(t, full.Messages[cut-1:], loaded.Messages[1:], "accepted summary preserves the exact recent tool-call/result tail")
		assertCompactedGrammar(t, loaded)
		if pass == 1 {
			assert.NotContains(t, text, summaries[0], "only the current summary is rendered")
		}
	}
	require.Len(t, client.requests, 2, "two accepted summaries each dispatch one context-model request")
	require.Len(t, client.requests[1].Messages, 2)
	quoted := "> " + strings.ReplaceAll(summaries[0], "\n", "\n> ")
	assert.Contains(t, client.requests[1].Messages[1].Parts[0].(model.TextPart).Text, "Previous summary:\n"+quoted+"\nTranscript (quoted data):", "the next actual request must contain the complete prior accepted summary")
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 2)
	for _, call := range record.Runs[0].Calls {
		assert.Equal(t, runrecord.Adopted, call.Class)
	}
}
