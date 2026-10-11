package callrecord

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestRecordExhaustive(t *testing.T) {
	ctx := context.Background()
	store := inmem.New()
	view := []*model.Message{recordMessage("m1"), recordMessage("m2"), recordMessage("m3")}
	meta := recordMessage("m2")
	meta.Meta = map[string]any{"changed": true}
	alphabet := append(append([]*model.Message{}, view...), recordMessage("o1"), recordMessage("o2"), meta)
	ref := index.ViewRef{IndexHead: "call-head"}
	later := []*model.Message{view[1], view[2], recordMessage("m4")}
	seen := 0
	var visit func([]*model.Message)
	visit = func(messages []*model.Message) {
		req := &model.Request{Messages: messages}
		parts, _, err := BuildRequest(ctx, store, "root", req, ref, view)
		require.NoError(t, err)
		started := Started{Request: parts}
		got, err := Rebuild(ctx, store, "root", started, func(_ context.Context, recorded index.ViewRef) ([]*model.Message, error) {
			if assert.Equal(t, ref, recorded) {
				return view, nil
			}
			return later, nil
		})
		require.NoError(t, err)
		wantBytes, err := Encode(req)
		require.NoError(t, err)
		gotBytes, err := Encode(got)
		require.NoError(t, err)
		assert.Equal(t, string(wantBytes), string(gotBytes))
		if len(messages) >= 3 && messages[0] == view[0] && messages[1] == view[1] && messages[2] == view[2] {
			additions := true
			for _, message := range messages[3:] {
				if message == view[0] || message == view[1] || message == view[2] {
					additions = false
				}
			}
			if additions {
				assert.Len(t, parts.Messages, 1+len(messages)-3)
				assert.Equal(t, MessageSegment{ViewFrom: 0, ViewTo: 3}, parts.Messages[0])
			}
		}
		seen++
		if len(messages) == 5 {
			return
		}
		for _, message := range alphabet {
			next := append(append([]*model.Message{}, messages...), message)
			visit(next)
		}
	}
	visit(nil)
	assert.Equal(t, 9331, seen)
}

func TestRequestRecordFaults(t *testing.T) {
	ctx := context.Background()
	view := []*model.Message{recordMessage("m1"), recordMessage("m2"), recordMessage("m3")}
	meta := recordMessage("m2")
	meta.Meta = map[string]any{"changed": true}
	for _, fault := range []string{"viewref_plus_added", "span_ignores_order", "hash_first_own", "decode_current_view", "match_canonical_hash"} {
		t.Run(fault, func(t *testing.T) {
			store := inmem.New()
			messages := []*model.Message{view[1]}
			switch fault {
			case "span_ignores_order":
				messages = []*model.Message{view[2], view[0]}
			case "hash_first_own":
				messages = []*model.Message{recordMessage("o1"), recordMessage("o2")}
			case "match_canonical_hash":
				messages = []*model.Message{meta}
			}
			parts, _, err := BuildRequest(ctx, store, "root", &model.Request{Messages: messages}, index.ViewRef{IndexHead: "call-head"}, view)
			require.NoError(t, err)
			decodeView := view
			switch fault {
			case "viewref_plus_added":
				parts.Messages = []MessageSegment{{ViewFrom: 0, ViewTo: 3}}
			case "span_ignores_order":
				parts.Messages = []MessageSegment{{ViewFrom: 0, ViewTo: 1}, {ViewFrom: 2, ViewTo: 3}}
			case "hash_first_own":
				parts.Messages[1].Hash = parts.Messages[0].Hash
			case "decode_current_view":
				decodeView = []*model.Message{view[1], view[2], recordMessage("m4")}
			case "match_canonical_hash":
				parts.View = &index.ViewRef{IndexHead: "call-head"}
				parts.Messages = []MessageSegment{{ViewFrom: 1, ViewTo: 2}}
			}
			_, err = Rebuild(ctx, store, "root", Started{Request: parts}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
				return decodeView, nil
			})
			assert.ErrorIs(t, err, ErrRequestMismatch)
		})
	}
}

func TestRequestFieldsAndNumericFallback(t *testing.T) {
	ctx := context.Background()
	for _, surface := range []string{"meta", "tool_input", "tool_result", "cache_move", "all_fields"} {
		t.Run(surface, func(t *testing.T) {
			store := inmem.New()
			message := recordMessage("m")
			const large = int64(9007199254740993)
			switch surface {
			case "meta":
				message.Meta = map[string]any{"z": large, "a": map[string]any{"n": large}}
			case "tool_input":
				message.Parts = []model.Part{model.ToolUsePart{Name: "tool", Input: map[string]any{"n": large}}}
			case "tool_result":
				message.Parts = []model.Part{model.ToolResultPart{ToolUseID: "use", Content: map[string]any{"n": large}}}
			case "cache_move":
				message.Parts = append(message.Parts, model.CacheCheckpointPart{})
			}
			view := []*model.Message{message}
			req := &model.Request{Messages: []*model.Message{message}}
			if surface == "cache_move" {
				req.Messages = []*model.Message{{Role: message.Role, Parts: []model.Part{model.CacheCheckpointPart{}, model.TextPart{Text: "m"}}}}
			}
			if surface == "all_fields" {
				req = &model.Request{RunID: "run", Model: "model", ModelClass: "class", PromptRefs: []prompt.PromptRef{{}}, Messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}}}, message}, Temperature: .25, Tools: []*model.ToolDefinition{{Name: "tool", Description: "desc", InputSchema: map[string]any{"maximum": large}}}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "tool"}, MaxTokens: 1024, StructuredOutput: &model.StructuredOutput{Name: "out", Schema: rawjson.Message(`{"maximum":9007199254740993}`)}, Stream: true, Thinking: &model.ThinkingOptions{Enable: true, Interleaved: true, BudgetTokens: 12}, Cache: &model.CacheOptions{AfterSystem: true, AfterTools: true}}
			}
			parts, _, err := BuildRequest(ctx, store, "root", req, index.ViewRef{IndexHead: "head"}, view)
			require.NoError(t, err)
			if surface != "all_fields" {
				require.Len(t, parts.Messages, 1)
				assert.NotEmpty(t, parts.Messages[0].Hash)
				assert.Nil(t, parts.View)
			}
			got, err := Rebuild(ctx, store, "root", Started{Request: parts, Model: req.Model, Class: req.ModelClass, Prompts: req.PromptRefs}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
				return view, nil
			})
			require.NoError(t, err)
			wantBytes, err := Encode(req)
			require.NoError(t, err)
			gotBytes, err := Encode(got)
			require.NoError(t, err)
			assert.Equal(t, string(wantBytes), string(gotBytes))
		})
	}
}

func TestDeterministicEncoding(t *testing.T) {
	message := recordMessage("m")
	message.Meta = map[string]any{"z": 9007199254740993, "a": 1, "nested": map[string]any{"z": 3, "a": 2}}
	first, err := Encode(message)
	require.NoError(t, err)
	assert.Contains(t, string(first), "9007199254740993")
	for range 100 {
		data, err := Encode(message)
		require.NoError(t, err)
		assert.Equal(t, string(first), string(data))
	}
	var raw map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(first, &raw))
}

func TestRequestGrowthBytes(t *testing.T) {
	ctx := context.Background()
	view := []*model.Message{recordMessage("view")}
	for _, test := range []struct {
		name     string
		messages []*model.Message
		grows    bool
	}{
		{"view", view, false},
		{"equal", []*model.Message{view[0], recordMessage("own!")}, false},
		{"outside", []*model.Message{view[0], recordMessage("own history growing")}, true},
		{"no_view", []*model.Message{recordMessage("own")}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, sizes, err := BuildRequest(ctx, inmem.New(), "root", &model.Request{Messages: test.messages}, index.ViewRef{IndexHead: "head"}, view)
			require.NoError(t, err)
			assert.Equal(t, test.grows, sizes.Outside > sizes.Inside)
		})
	}
}

func TestRequestRecordRepeatedViewMessages(t *testing.T) {
	view := []*model.Message{recordMessage("same"), recordMessage("same"), recordMessage("end")}
	parts, _, err := BuildRequest(t.Context(), inmem.New(), "root", &model.Request{Messages: view}, index.ViewRef{IndexHead: "head"}, view)
	require.NoError(t, err)
	assert.Equal(t, []MessageSegment{{ViewFrom: 0, ViewTo: 3}}, parts.Messages)
}

func TestRequestVerificationDigestIsNotStored(t *testing.T) {
	store := inmem.New()
	request := &model.Request{Model: "provider", Tools: []*model.ToolDefinition{}, Messages: []*model.Message{recordMessage("view message")}}
	view := index.ViewRef{IndexHead: "recorded-head"}
	parts, _, err := BuildRequest(t.Context(), store, "root", request, view, request.Messages)
	require.NoError(t, err)
	_, err = store.Get(t.Context(), "root", parts.Request)
	require.ErrorIs(t, err, runstore.ErrNotFound, "the full request is only a verification digest")
	rebuilt, err := Rebuild(t.Context(), store, "root", Started{Model: request.Model, Request: parts}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
		return request.Messages, nil
	})
	require.NoError(t, err)
	assert.Equal(t, request, rebuilt)
	parts.Request = "sha256:wrong-digest"
	_, err = Rebuild(t.Context(), store, "root", Started{Model: request.Model, Request: parts}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
		return request.Messages, nil
	})
	assert.ErrorIs(t, err, ErrRequestMismatch)
}

func recordMessage(text string) *model.Message {
	return &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}
}
