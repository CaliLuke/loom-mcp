package transcript

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromModelMessagesPreservesAssistantMessageBoundaries(t *testing.T) {
	t.Parallel()

	ledger := FromModelMessages([]*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "first"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ignored"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "second"}}},
	})
	messages := ledger.BuildMessages()
	require.Len(t, messages, 2)
	assert.Equal(t, "first", messages[0].Parts[0].(model.TextPart).Text)
	assert.Equal(t, "second", messages[1].Parts[0].(model.TextPart).Text)
}

func TestFromModelMessagesAcceptsPointerPartsAndDetachesReasoning(t *testing.T) {
	t.Parallel()

	redacted := []byte("opaque")
	thinking := &model.ThinkingPart{Redacted: redacted, Index: 1, Final: true}
	text := &model.TextPart{Text: "calling"}
	toolUse := &model.ToolUsePart{ID: "call-1", Name: "svc.tool", Input: map[string]any{"q": "loom"}}
	ledger := FromModelMessages([]*model.Message{{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{thinking, text, toolUse},
	}})
	redacted[0] = 'X'
	messages := ledger.BuildMessages()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Parts, 3)
	assert.Equal(t, []byte("opaque"), messages[0].Parts[0].(model.ThinkingPart).Redacted)
	assert.Equal(t, "calling", messages[0].Parts[1].(model.TextPart).Text)
	assert.Equal(t, "call-1", messages[0].Parts[2].(model.ToolUsePart).ID)
}

func TestMessageJSONRoundTripsEveryLedgerPart(t *testing.T) {
	t.Parallel()

	want := Message{
		Role: "assistant",
		Parts: []Part{
			ThinkingPart{Text: "thinking", Signature: "sig", Index: 1, Final: true},
			TextPart{Text: "calling"},
			ToolUsePart{ID: "call-1", Name: "svc.tool", Args: map[string]any{"q": "loom"}},
			ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"ok": true}, IsError: false},
		},
		Meta: map[string]any{"trace": "trace-1"},
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got Message
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got.Parts, 4)
	assert.IsType(t, ThinkingPart{}, got.Parts[0])
	assert.IsType(t, TextPart{}, got.Parts[1])
	assert.IsType(t, ToolUsePart{}, got.Parts[2])
	assert.IsType(t, ToolResultPart{}, got.Parts[3])
	assert.Equal(t, "thinking", got.Parts[0].(ThinkingPart).Text)
	assert.Equal(t, "loom", got.Parts[2].(ToolUsePart).Args.(map[string]any)["q"])
	assert.Equal(t, true, got.Parts[3].(ToolResultPart).Content.(map[string]any)["ok"])
	assert.Equal(t, "trace-1", got.Meta["trace"])
}

func TestMessageJSONSupportsLegacyStringParts(t *testing.T) {
	t.Parallel()

	var message Message
	require.NoError(t, json.Unmarshal([]byte(`{"Role":"assistant","Parts":["legacy text"]}`), &message))
	require.Len(t, message.Parts, 1)
	assert.Equal(t, TextPart{Text: "legacy text"}, message.Parts[0])
}

func TestMessageJSONRejectsMalformedParts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "invalid JSON", payload: `{`, want: "unexpected EOF"},
		{name: "empty part", payload: `{"Role":"assistant","Parts":[{}]}`, want: "empty part payload"},
		{name: "unknown part", payload: `{"Role":"assistant","Parts":[{"Other":true}]}`, want: "unknown part shape"},
		{name: "tool result without ID", payload: `{"Role":"user","Parts":[{"ToolUseID":"","Content":"result"}]}`, want: "requires ToolUseID"},
		{name: "tool use without name", payload: `{"Role":"assistant","Parts":[{"ID":"call-1","Name":""}]}`, want: "requires Name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var message Message
			err := json.Unmarshal([]byte(test.payload), &message)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestLedgerJSONRoundTripsCommittedAndPendingMessages(t *testing.T) {
	t.Parallel()

	ledger := NewLedger()
	ledger.AppendThinking(ThinkingPart{Text: "thinking", Signature: "sig", Index: 1, Final: true})
	ledger.AppendText("calling")
	ledger.DeclareToolUse("call-1", "svc.tool", map[string]any{"q": "loom"})
	ledger.FlushAssistant()
	ledger.AppendUserToolResults([]ToolResultSpec{{
		ToolUseID: "call-1",
		Content:   map[string]any{"ok": true},
	}})
	ledger.AppendText("pending")

	raw, err := json.Marshal(ledger)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"messages": [
			{"Role":"assistant","Parts":[
				{"Text":"thinking","Signature":"sig","Redacted":null,"Index":1,"Final":true},
				{"Text":"calling"},
				{"ID":"call-1","Name":"svc.tool","Args":{"q":"loom"}}
			],"Meta":null},
			{"Role":"user","Parts":[
				{"ToolUseID":"call-1","Content":{"ok":true},"IsError":false}
			],"Meta":null}
		],
		"current":{"Role":"assistant","Parts":[{"Text":"pending"}],"Meta":null}
	}`, string(raw))

	var restored Ledger
	require.NoError(t, json.Unmarshal(raw, &restored))
	restored.AppendText(" response")
	messages := restored.BuildMessages()
	require.Len(t, messages, 3)
	require.Len(t, messages[0].Parts, 3)
	assert.IsType(t, model.ThinkingPart{}, messages[0].Parts[0])
	assert.Equal(t, "loom", messages[0].Parts[2].(model.ToolUsePart).Input.(map[string]any)["q"])
	assert.Equal(t, true, messages[1].Parts[0].(model.ToolResultPart).Content.(map[string]any)["ok"])
	assert.Equal(t, "pending response", messages[2].Parts[0].(model.TextPart).Text)
}

func TestLedgerIsEmptyTracksPendingAndCommittedParts(t *testing.T) {
	t.Parallel()

	var nilLedger *Ledger
	assert.True(t, nilLedger.IsEmpty())
	ledger := NewLedger()
	assert.True(t, ledger.IsEmpty())
	ledger.AppendText("text")
	assert.False(t, ledger.IsEmpty())
	ledger.FlushAssistant()
	assert.False(t, ledger.IsEmpty())
}
