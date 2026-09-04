package transcript

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

func TestLedger_BuildAndValidate(t *testing.T) {
	l := NewLedger()
	// Structured thinking first
	l.AppendThinking(ThinkingPart{Text: "let me think", Signature: "sig", Index: 0, Final: true})
	// Assistant text
	l.AppendText("calling tool")
	// Declare tool use
	l.DeclareToolUse("tu1", "search_assets", map[string]any{"q": "pump"})
	// Flush assistant turn
	l.FlushAssistant()
	// Append user tool result as a single user message
	l.AppendUserToolResults([]ToolResultSpec{{
		ToolUseID: "tu1",
		Content:   map[string]any{"ok": true},
		IsError:   false,
	}})

	msgs := l.BuildMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != model.ConversationRoleAssistant {
		t.Fatalf("first role = %s, want assistant", msgs[0].Role)
	}
	if len(msgs[0].Parts) < 2 {
		t.Fatalf("assistant parts too short")
	}
	if _, ok := msgs[0].Parts[0].(model.ThinkingPart); !ok {
		t.Fatalf("assistant does not start with thinking")
	}
	if _, ok := msgs[0].Parts[1].(model.TextPart); !ok {
		t.Fatalf("assistant second part should be text")
	}
	if msgs[1].Role != model.ConversationRoleUser {
		t.Fatalf("second role = %s, want user", msgs[1].Role)
	}
	if err := ValidateBedrock(msgs, true); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
}

func TestLedger_MultipleToolUseSingleUserMessage(t *testing.T) {
	l := NewLedger()
	l.AppendThinking(ThinkingPart{Text: "thinking", Signature: "sig", Index: 0, Final: true})
	l.AppendText("calling tools")
	l.DeclareToolUse("tu1", "tool_one", map[string]any{"x": 1})
	l.DeclareToolUse("tu2", "tool_two", map[string]any{"y": 2})
	l.FlushAssistant()
	l.AppendUserToolResults([]ToolResultSpec{
		{ToolUseID: "tu1", Content: map[string]any{"ok": true}, IsError: false},
		{ToolUseID: "tu2", Content: map[string]any{"ok": true}, IsError: false},
	})

	msgs := l.BuildMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if err := ValidateBedrock(msgs, true); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if msgs[0].Role != model.ConversationRoleAssistant {
		t.Fatalf("first role = %s, want assistant", msgs[0].Role)
	}
	if msgs[1].Role != model.ConversationRoleUser {
		t.Fatalf("second role = %s, want user", msgs[1].Role)
	}
}

func TestBuildMessages_DoesNotFlushCurrentAssistant(t *testing.T) {
	l := NewLedger()
	l.AppendText("working")

	first := l.BuildMessages()
	if len(first) != 1 {
		t.Fatalf("expected current assistant message, got %d messages", len(first))
	}

	l.DeclareToolUse("tu1", "search", map[string]any{"q": "test"})
	second := l.BuildMessages()
	if len(second) != 1 {
		t.Fatalf("query split the assistant turn: got %d messages", len(second))
	}
	if len(second[0].Parts) != 2 {
		t.Fatalf("expected text and tool use in one assistant message, got %d parts", len(second[0].Parts))
	}
}

func TestBuildMessagesFromEvents_ParentToolOnly(t *testing.T) {
	events := []memory.Event{
		memory.NewEvent(time.Now(), memory.ThinkingData{
			Text:         "thinking",
			Signature:    "sig",
			ContentIndex: 0,
			Final:        true,
		}, nil),
		memory.NewEvent(time.Now(), memory.AssistantMessageData{
			Message: "calling tool",
		}, nil),
		memory.NewEvent(time.Now(), memory.ToolCallData{
			ToolCallID:  "tc-1",
			ToolName:    "svc.tool",
			PayloadJSON: rawjson.Message(`{"q":1}`),
		}, nil),
		memory.NewEvent(time.Now(), memory.ToolResultData{
			ToolCallID: "tc-1",
			ToolName:   "svc.tool",
			ResultJSON: rawjson.Message(`{"ok":true}`),
			Duration:   time.Second,
		}, nil),
	}

	msgs, err := BuildMessagesFromEvents(events)
	if err != nil {
		t.Fatalf("BuildMessagesFromEvents error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if err := ValidateBedrock(msgs, true); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if msgs[0].Role != model.ConversationRoleAssistant {
		t.Fatalf("first role = %s, want assistant", msgs[0].Role)
	}
	if msgs[1].Role != model.ConversationRoleUser {
		t.Fatalf("second role = %s, want user", msgs[1].Role)
	}
	tr, ok := msgs[1].Parts[0].(model.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", msgs[1].Parts[0])
	}
	if tr.IsError {
		t.Fatalf("expected IsError=false")
	}
	wantSuccess := map[string]any{"ok": true}
	if !reflect.DeepEqual(tr.Content, wantSuccess) {
		t.Fatalf("content mismatch:\n got: %#v\nwant: %#v", tr.Content, wantSuccess)
	}
}

func TestBuildMessagesFromEventsPreservesAssistantTurnBoundaries(t *testing.T) {
	at := time.Unix(0, 0)
	events := []memory.Event{
		memory.NewEvent(at, memory.ThinkingData{Text: "think one", Signature: "sig-1", Final: true}, nil),
		memory.NewEvent(at, memory.AssistantMessageData{Message: "turn one"}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "call-1", ToolName: "svc.one", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "call-1", ResultJSON: rawjson.Message(`"result one"`)}, nil),
		memory.NewEvent(at, memory.AssistantMessageData{Message: "turn two"}, nil),
		memory.NewEvent(at, memory.ThinkingData{Text: "think two", Signature: "sig-2", Final: true}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "call-2", ToolName: "svc.two", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "call-2", ResultJSON: rawjson.Message(`"result two"`)}, nil),
		memory.NewEvent(at, memory.ThinkingData{Text: "think three", Signature: "sig-3", Final: true}, nil),
		memory.NewEvent(at, memory.AssistantMessageData{Message: "turn three"}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "call-3", ToolName: "svc.three", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "call-3", ResultJSON: rawjson.Message(`"result three"`)}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "call-4", ToolName: "svc.four", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ThinkingData{Text: "think four", Signature: "sig-4", Final: true}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "call-4", ResultJSON: rawjson.Message(`"result four"`)}, nil),
	}

	messages, err := BuildMessagesFromEvents(events)
	require.NoError(t, err)
	require.NoError(t, ValidateBedrock(messages, true))
	require.Len(t, messages, 8)
	for i, role := range []model.ConversationRole{
		model.ConversationRoleAssistant, model.ConversationRoleUser,
		model.ConversationRoleAssistant, model.ConversationRoleUser,
		model.ConversationRoleAssistant, model.ConversationRoleUser,
		model.ConversationRoleAssistant, model.ConversationRoleUser,
	} {
		assert.Equal(t, role, messages[i].Role)
	}
	assert.Equal(t, model.TextPart{Text: "turn one"}, messages[0].Parts[1])
	assert.Equal(t, model.TextPart{Text: "turn two"}, messages[2].Parts[1])
	assert.Equal(t, model.ThinkingPart{Text: "think three", Signature: "sig-3", Final: true}, messages[4].Parts[0])
	assert.Equal(t, model.ToolUsePart{ID: "call-4", Name: "svc.four", Input: map[string]any{}}, messages[6].Parts[1])
}

func TestBuildMessagesFromEventsPreservesEveryToolResultInOrder(t *testing.T) {
	at := time.Unix(0, 0)
	events := []memory.Event{
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "a", ToolName: "svc.a", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "b", ToolName: "svc.b", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolCallData{ToolCallID: "a", ToolName: "svc.a", PayloadJSON: rawjson.Message(`{}`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "a", ResultJSON: rawjson.Message(`"first"`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "b", ResultJSON: rawjson.Message(`"second"`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "a", ResultJSON: rawjson.Message(`"third"`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "unmatched-z", ResultJSON: rawjson.Message(`"fourth"`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ToolCallID: "unmatched-a", ResultJSON: rawjson.Message(`"fifth"`)}, nil),
		memory.NewEvent(at, memory.ToolResultData{ResultJSON: rawjson.Message(`"ignored"`)}, nil),
	}

	messages, err := BuildMessagesFromEvents(events)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Len(t, messages[1].Parts, 5)
	wantIDs := []string{"a", "b", "a", "unmatched-z", "unmatched-a"}
	wantContent := []string{"first", "second", "third", "fourth", "fifth"}
	for i, part := range messages[1].Parts {
		result, ok := part.(model.ToolResultPart)
		require.True(t, ok, "part %d has type %T", i, part)
		assert.Equal(t, wantIDs[i], result.ToolUseID)
		assert.Equal(t, wantContent[i], result.Content)
	}
}

func TestBuildMessages_ThinkingSignatureLost(t *testing.T) {
	// When the Bedrock stream emits intermediate thinking (text only, no
	// signature) and the final signature delta is lost, BuildMessages must
	// still include a redacted ThinkingPart placeholder so the assistant
	// message satisfies the thinking→content ordering contract.
	l := NewLedger()
	// Append intermediate thinking: text but no signature (simulates lost delta).
	l.AppendThinking(ThinkingPart{Text: "let me think about this", Index: 0, Final: false})
	l.AppendText("calling tool")
	l.DeclareToolUse("tu1", "search", map[string]any{"q": "test"})
	l.FlushAssistant()
	l.AppendUserToolResults([]ToolResultSpec{{
		ToolUseID: "tu1",
		Content:   map[string]any{"ok": true},
	}})

	msgs := l.BuildMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	// The assistant message must start with a redacted thinking placeholder.
	asstMsg := msgs[0]
	if asstMsg.Role != model.ConversationRoleAssistant {
		t.Fatalf("first role = %s, want assistant", asstMsg.Role)
	}
	tp, ok := asstMsg.Parts[0].(model.ThinkingPart)
	if !ok {
		t.Fatalf("assistant first part should be ThinkingPart, got %T", asstMsg.Parts[0])
	}
	if len(tp.Redacted) == 0 {
		t.Fatalf("expected redacted placeholder, got text=%q sig=%q", tp.Text, tp.Signature)
	}
	if !tp.Final {
		t.Fatalf("expected Final=true on placeholder")
	}
	// Validate Bedrock should pass with the placeholder.
	if err := ValidateBedrock(msgs, true); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
}

func TestBuildMessages_NoThinkingNoop(t *testing.T) {
	// When no thinking parts were present at all, BuildMessages should NOT
	// inject a placeholder — thinking absence is normal for non-thinking models.
	l := NewLedger()
	l.AppendText("hello")
	l.DeclareToolUse("tu1", "search", map[string]any{"q": "test"})
	l.FlushAssistant()
	l.AppendUserToolResults([]ToolResultSpec{{
		ToolUseID: "tu1",
		Content:   map[string]any{"ok": true},
	}})

	msgs := l.BuildMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	// First part should be text, not thinking.
	if _, ok := msgs[0].Parts[0].(model.TextPart); !ok {
		t.Fatalf("expected TextPart first, got %T", msgs[0].Parts[0])
	}
}

func TestValidateBedrock_ErrorIncludesIndex(t *testing.T) {
	// Verify that validation errors include the offending message index.
	msgs := []*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.TextPart{Text: "calling tool"},
				model.ToolUsePart{ID: "tu1", Name: "search"},
			},
		},
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ToolResultPart{ToolUseID: "tu1", Content: "ok"},
			},
		},
	}
	err := ValidateBedrock(msgs, true)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !contains(err.Error(), "message[0]") {
		t.Fatalf("error should include message index, got: %s", err.Error())
	}
	if !contains(err.Error(), "tool_use") {
		t.Fatalf("error should include parts summary, got: %s", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBuildMessagesFromEvents_ToolErrorIncludesErrorContent(t *testing.T) {
	events := []memory.Event{
		memory.NewEvent(time.Now(), memory.AssistantMessageData{
			Message: "calling tool",
		}, nil),
		memory.NewEvent(time.Now(), memory.ToolCallData{
			ToolCallID:  "tc-1",
			ToolName:    "svc.tool",
			PayloadJSON: rawjson.Message(`{"q":1}`),
		}, nil),
		memory.NewEvent(time.Now(), memory.ToolResultData{
			ToolCallID:   "tc-1",
			ToolName:     "svc.tool",
			ErrorMessage: "access denied: missing controlleddevices.write privilege",
			Duration:     time.Second,
		}, nil),
	}

	msgs, err := BuildMessagesFromEvents(events)
	if err != nil {
		t.Fatalf("BuildMessagesFromEvents error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Role != model.ConversationRoleUser {
		t.Fatalf("second role = %s, want user", msgs[1].Role)
	}
	if len(msgs[1].Parts) != 1 {
		t.Fatalf("expected 1 user part, got %d", len(msgs[1].Parts))
	}
	tr, ok := msgs[1].Parts[0].(model.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", msgs[1].Parts[0])
	}
	if !tr.IsError {
		t.Fatalf("expected IsError=true")
	}
	want := "access denied: missing controlleddevices.write privilege"
	if tr.Content != want {
		t.Fatalf("content mismatch:\n got: %#v\nwant: %#v", tr.Content, want)
	}
}

func TestBuildMessagesFromEvents_AcceptsLegacyToolCallPayloadShape(t *testing.T) {
	events := []memory.Event{
		memory.NewEvent(time.Now(), memory.AssistantMessageData{
			Message: "calling tool",
		}, nil),
		{
			Type:      memory.EventToolCall,
			Timestamp: time.Now(),
			Data: map[string]any{
				"tool_call_id": "tc-1",
				"tool_name":    "svc.tool",
				"payload": map[string]any{
					"q": 1,
				},
			},
		},
		memory.NewEvent(time.Now(), memory.ToolResultData{
			ToolCallID: "tc-1",
			ToolName:   "svc.tool",
			ResultJSON: rawjson.Message(`{"ok":true}`),
		}, nil),
	}

	msgs, err := BuildMessagesFromEvents(events)
	if err != nil {
		t.Fatalf("BuildMessagesFromEvents error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if len(msgs[0].Parts) != 2 {
		t.Fatalf("expected 2 assistant parts, got %d", len(msgs[0].Parts))
	}
	tu, ok := msgs[0].Parts[1].(model.ToolUsePart)
	if !ok {
		t.Fatalf("expected ToolUsePart, got %T", msgs[0].Parts[1])
	}
	want := map[string]any{"q": float64(1)}
	if !reflect.DeepEqual(tu.Input, want) {
		t.Fatalf("input mismatch:\n got: %#v\nwant: %#v", tu.Input, want)
	}
}

func TestBuildMessagesFromEvents_AcceptsLegacyThinkingBytes(t *testing.T) {
	events := []memory.Event{
		{
			Type:      memory.EventThinking,
			Timestamp: time.Now(),
			Data: map[string]any{
				"text":          "reasoning",
				"signature":     "sig",
				"redacted":      []byte("opaque"),
				"content_index": 0,
				"final":         true,
			},
		},
		memory.NewEvent(time.Now(), memory.AssistantMessageData{
			Message: "done",
		}, nil),
	}

	msgs, err := BuildMessagesFromEvents(events)
	if err != nil {
		t.Fatalf("BuildMessagesFromEvents error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Parts) != 2 {
		t.Fatalf("expected 2 assistant parts, got %d", len(msgs[0].Parts))
	}
	part, ok := msgs[0].Parts[0].(model.ThinkingPart)
	if !ok {
		t.Fatalf("expected ThinkingPart, got %T", msgs[0].Parts[0])
	}
	if !reflect.DeepEqual(part.Redacted, []byte("opaque")) {
		t.Fatalf("redacted mismatch:\n got: %#v\nwant: %#v", part.Redacted, []byte("opaque"))
	}
}

func TestBuildMessagesFromEvents_ReturnsDecodeError(t *testing.T) {
	events := []memory.Event{
		{
			Type:      memory.EventToolCall,
			Timestamp: time.Now(),
			Data: map[string]any{
				"tool_call_id": "tc-1",
				"tool_name":    "svc.tool",
				"payload":      "{not-json",
			},
		},
	}

	msgs, err := BuildMessagesFromEvents(events)
	if err == nil {
		t.Fatal("expected error")
	}
	if msgs != nil {
		t.Fatalf("expected nil messages, got %#v", msgs)
	}
	if !contains(err.Error(), `decode tool_call "tc-1" payload`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
