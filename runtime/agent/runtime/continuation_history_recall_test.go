package runtime

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationHistoryPlaceholderRecall(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	messages := make([]*model.Message, 0, 4)
	messages = append(messages, []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "question"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]string{"q": "evidence"}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "original evidence"}}},
	}...)
	start := continuationTestStart(t, rt, "original", messages)
	in := continuationTestSeal(t, rt, "original", start, false)
	head, err := rt.continuationHead(t.Context(), "original", "original")
	require.NoError(t, err)
	next, err := rt.buildContinuation(t.Context(), in, head)
	require.NoError(t, err)
	require.Nil(t, next.Checkpoint)
	// A previously bounded history can carry a placeholder without any eviction.
	entry := next.Tail[2].Entry
	entry.Kind, entry.Hash, entry.Keep, entry.Batch = index.Structural, "", true, 3
	entry.ToolUseIDs = []string{"call"}
	entry.Origin.Form = "placeholder"
	entry.Structural = &index.StructuralEntry{Kind: index.CarriedResult, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Text: "[result cleared: 17 B, r1 call]"}}}
	next.Tail[2] = session.Carried{Entry: entry}
	_, err = rt.SessionStore.(session.ContinuationStore).AdvanceContinuation(t.Context(), "session", nil, next)
	require.NoError(t, err)
	messages = append(messages, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}})
	second := continuationTestStart(t, rt, "next", messages)
	require.True(t, second.Continuation.View.CarriedPartial)
	require.Nil(t, second.Continuation.View.Checkpoint)
	payload := rawjson.Message(`{"read":{"run":"r1","id":"call","part":"result","offset":0}}`)
	out, err := rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "next", Scope: "next", ViewRef: second.Continuation.View, Head: second.IndexHead, Call: planner.ToolRequest{Payload: payload}})
	require.NoError(t, err)
	data, err := rt.RunStore.Get(t.Context(), "next", out.Result.Refs.Result)
	require.NoError(t, err)
	assert.Contains(t, string(data), "original evidence")
	loaded, err := rt.loadView(t.Context(), "next", "next", second.Continuation.View)
	require.NoError(t, err)
	encoded, err := json.Marshal(loaded.Messages)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "original evidence")
	assert.Contains(t, string(encoded), "result cleared")
}
