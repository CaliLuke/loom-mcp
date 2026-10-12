package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalRecallCopiesEarlierRun(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	image := model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember"}, image}}
	start := continuationTestStart(t, rt, "r1", []*model.Message{user})
	// Synthetic compatibility case built by the current runtime, with a v17 seal.
	// Captured v17 byte compatibility is covered separately by callrecord's
	// TestToolResultPartsLegacyCallBytes and TestToolResultPartsLegacyStartedBytes;
	// this test covers continuation and original-image recall across that seal.
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}
	data, err := json.Marshal(final)
	require.NoError(t, err)
	hash, err := rt.RunStore.Put(t.Context(), "r1", data)
	require.NoError(t, err)
	_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{RootRunID: "r1", RunID: "r1", ProtocolVersion: 17, IndexHead: start.IndexHead, EventKey: "run_sealed:r1", Timestamp: time.Unix(2, 0), SessionID: "session", Pos: 1, Publish: true, FinalHash: hash, ContextCompaction: &ContextCompaction{}})
	require.NoError(t, err)
	legacy, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "r1", "r1")
	require.NoError(t, err)
	assert.Equal(t, 17, legacy.Runs[0].WorkflowProtocolVersion)
	exported, err := legacy.Export().Marshal()
	require.NoError(t, err)
	_, err = runrecord.FromExport(exported)
	require.NoError(t, err)
	next := continuationTestStart(t, rt, "r2", []*model.Message{user, final, contentMessage("recall")})
	entered := false
	rt.recallAuthorizer = func(_ context.Context, a recall.Access) error {
		if a.TargetRunID == "r1" {
			entered = true
		}
		return nil
	}
	out, err := rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "r2", Scope: "r2", Head: next.IndexHead, ViewRef: next.Continuation.View, Parts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, MaxImageBytes: 100}, PartTokens: 1500, Call: planner.ToolRequest{Payload: rawjson.Message(`{"read":{"id":"1","run":"r1","part":"attachment","index":2,"offset":0}}`)}})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	assert.True(t, entered)
	require.NotEmpty(t, out.Parts)
	assert.Equal(t, out.Parts, out.Result.Refs.Parts)
	assert.Equal(t, 1500, out.NonText)
	data, err = rt.RunStore.Get(t.Context(), "r2", out.Parts)
	require.NoError(t, err)
	var message model.Message
	require.NoError(t, json.Unmarshal(data, &message))
	assert.Equal(t, []model.Part{image}, message.Parts)
	entries := []index.MessageEntry{{ID: 1, Kind: index.ToolResult, Hash: out.Result.Refs.Result, Batch: 1, ToolUseIDs: []string{"read"}, Result: &index.ResultProjection{Parts: out.Parts}, NonText: out.NonText}}
	rendered, err := index.Render(t.Context(), entries, func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "r2", h)
	})
	require.NoError(t, err)
	assert.Len(t, rendered[0].Parts[0].(model.ToolResultPart).Parts, 1)
}

func TestMultimodalOrdinaryToolCannotSetParts(t *testing.T) {
	call := planner.ToolRequest{Name: "ordinary.tool", ToolCallID: "call"}
	err := validateToolResultContract(tools.ToolSpec{Name: call.Name}, call, &planner.ToolResult{Refs: tools.ResultRefs{Parts: "blob"}})
	assert.Error(t, err)
}

type multimodalCarryProbe struct {
	runstore.Store
	t *testing.T
}

func (p multimodalCarryProbe) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if scope == "r2" {
		assert.NotContains(p.t, string(data), "U0VDUkVUX1JFU1VMVA==")
	}
	return p.Store.Put(ctx, scope, data)
}
func TestMultimodalCarryWritesNoAttachment(t *testing.T) {
	rt := New()
	rt.RunStore = multimodalCarryProbe{Store: rt.RunStore, t: t}
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	history := make([]*model.Message, 0, 5)
	history = append(history, []*model.Message{
		contentMessage("read"),
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("SECRET_RESULT")}}}}},
	}...)
	start := continuationTestStart(t, rt, "r1", history)
	continuationTestSeal(t, rt, "r1", start, true)
	history = append(history, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}, contentMessage("next"))
	next := continuationTestStart(t, rt, "r2", history)
	require.Equal(t, "continued", next.Continuation.Outcome)
	for _, entry := range next.Entries {
		assert.Zero(t, entry.NonText)
		if entry.Result != nil {
			assert.Empty(t, entry.Result.Parts)
		}
	}
	loaded, err := rt.loadView(t.Context(), "r2", "r2", next.Continuation.View)
	require.NoError(t, err)
	for _, message := range loaded.Messages {
		for _, part := range message.Parts {
			if result, ok := part.(model.ToolResultPart); ok {
				assert.Empty(t, result.Parts)
			}
		}
	}
}
