package runtime

import (
	"context"
	"encoding/json/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationThreeRunReceiptRecallAndBoundedSummary(t *testing.T) {
	rt := New(WithContextLimits(map[string]model.ContextLimits{"small": {ContextWindow: 8000}}))
	require.NoError(t, rt.RegisterModel("small", contextLimitsClient{}))
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "choose the earlier option"}}}
	start := continuationTestStart(t, rt, "r1", []*model.Message{user})
	messages := []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "old-call", Name: "tool", Input: map[string]string{"q": "evidence"}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "old-call", Content: "original result from run one"}}},
	}
	pending := make([]index.MessageEntry, 0, len(messages))
	prev := start.Entries[0].Chain
	for i, message := range messages {
		data, err := json.Marshal(message)
		require.NoError(t, err)
		hash, err := rt.RunStore.Put(t.Context(), "r1", data)
		require.NoError(t, err)
		entry := index.MessageEntry{ID: index.MessageID(i + 2), Kind: index.Message, Hash: hash, Role: message.Role, Bytes: len(data)}
		entry.Chain, err = index.ComputeChain(prev, entry)
		require.NoError(t, err)
		prev = entry.Chain
		pending = append(pending, entry)
	}
	head, committed, err := storeIndexSegments(t.Context(), rt.RunStore, "r1", start.IndexHead, start.Entries[0].Chain, pending)
	require.NoError(t, err)
	start.IndexHead = head
	start.Entries = append(start.Entries, committed...)
	summary, err := rt.RunStore.Put(t.Context(), "r1", []byte(strings.Repeat("summary fact ", 4000)+"[message 1]"))
	require.NoError(t, err)
	record := index.CheckpointRecord{Summary: &summary, SummaryRun: "r1", SummaryPos: 1, Through: 3, Chain: start.Entries[2].Chain, Receipt: []index.ReceiptLine{{Kind: "call", From: 2, Through: 3, ToolUseID: "old-call", Tool: "tool", Text: "evidence"}}}
	data, err := json.Marshal(record)
	require.NoError(t, err)
	cp, err := rt.RunStore.Put(t.Context(), "r1", data)
	require.NoError(t, err)
	start.Continuation.View = index.ViewRef{Checkpoint: &cp, TailFrom: 4, Pinned: []index.MessageID{1}, Budget: compaction.RenderBudget(compaction.Settings{}, 60000, 0), ChainPos: 1}
	continuationTestSeal(t, rt, "r1", start, true)
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}
	correction := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "correction from run two"}}}
	nextMessages := make([]*model.Message, 0, 5)
	nextMessages = append(nextMessages, user, final, correction)
	second := continuationTestStart(t, rt, "r2", nextMessages)
	require.Equal(t, "continued", second.Continuation.Outcome)
	loaded, err := rt.loadView(t.Context(), "r2", "r2", second.Continuation.View)
	require.NoError(t, err)
	require.NotEmpty(t, loaded.Messages)
	text := messagePartsText(loaded.Messages[0])
	assert.Contains(t, text, "[r1 message 1]")
	assert.LessOrEqual(t, len(text), checkpoint.MaxBytes(second.Continuation.View.Budget))
	continuationTestSeal(t, rt, "r2", second, true)
	third := continuationTestStart(t, rt, "r3", append(nextMessages, final, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "recall the evidence"}}}))
	loaded, err = rt.loadView(t.Context(), "r3", "r3", third.Continuation.View)
	require.NoError(t, err)
	checkpointText := messagePartsText(loaded.Messages[0])
	assert.Contains(t, checkpointText, "earlier")
	handles := regexp.MustCompile(`\[receipt ([^\]]+)\]`).FindStringSubmatch(checkpointText)
	require.Len(t, handles, 2)
	handle := handles[1]
	payload, err := json.Marshal(map[string]any{"list": map[string]any{"handle": handle}})
	require.NoError(t, err)
	result, err := rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "r3", Scope: "r3", ViewRef: third.Continuation.View, Head: third.IndexHead, Call: planner.ToolRequest{Payload: rawjson.Message(payload)}})
	require.NoError(t, err)
	data, err = rt.RunStore.Get(t.Context(), "r3", result.Result.Refs.Result)
	require.NoError(t, err)
	var listed recall.Response
	require.NoError(t, json.Unmarshal(data, &listed))
	require.Empty(t, listed.Code)
	require.NotEmpty(t, listed.Entries)
	assert.Equal(t, "old-call", listed.Entries[0].ID)
	payload, err = json.Marshal(map[string]any{"read": map[string]any{"run": listed.Entries[0].Run, "id": listed.Entries[0].ID, "part": "result", "offset": 0}})
	require.NoError(t, err)
	result, err = rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "r3", Scope: "r3", ViewRef: third.Continuation.View, Head: third.IndexHead, Call: planner.ToolRequest{Payload: rawjson.Message(payload)}})
	require.NoError(t, err)
	data, err = rt.RunStore.Get(t.Context(), "r3", result.Result.Refs.Result)
	require.NoError(t, err)
	assert.Contains(t, string(data), "original result from run one")
	chain, err := rt.continuationChain(t.Context(), "r3", "r3")
	require.NoError(t, err)
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "r3", hash)
	}
	resolver := chain.Citations(t.Context(), source, third.Entries, index.MessageID(len(third.Entries)), nil)
	cited, err := summarize.Validate(summarize.ValidationInput{Text: "Old [r1 message 1], correction [r2 message 3], now [message 5].", CurrentPos: 3, To: 6, SummaryBytes: 1000, ReplacedTokens: 1000, Resolver: resolver, RenderedCheckpoint: func(string) (int, error) {
		return 1, nil
	}})
	require.NoError(t, err)
	require.Len(t, cited.CitedUsers, 3)
	assert.Equal(t, 1, cited.CitedUsers[0].Origin.Pos)
	assert.Equal(t, 2, cited.CitedUsers[1].Origin.Pos)
	preparation, err := rt.prepareSummary(t.Context(), &SummarizeContextInput{RunID: "r3", Scope: "r3", ViewRef: third.Continuation.View, From: third.Continuation.View.TailFrom, To: 4, Budget: third.Continuation.View.Budget})
	require.NoError(t, err)
	assert.Contains(t, preparation.evidence.Previous, "[r1 message 1]")
}

func messagePartsText(message *model.Message) string {
	var text strings.Builder
	for _, part := range message.Parts {
		if p, ok := part.(model.TextPart); ok {
			text.WriteString(p.Text)
		}
	}
	return text.String()
}
