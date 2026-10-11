package callrecord_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeResponseRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *model.Response
	}{
		{name: "nil"},
		{name: "empty", response: &model.Response{}},
		{name: "precise content", response: &model.Response{
			Content: []model.Message{{Role: model.ConversationRoleAssistant,
				Meta: map[string]any{"large": rawjson.Message(`9007199254740993`)},
				Parts: []model.Part{
					model.TextPart{Text: "answer"},
					model.ToolUsePart{ID: "call", Name: "tool.read", Input: rawjson.Message(`{"large":9007199254740995}`)},
					model.ToolResultPart{ToolUseID: "call", Content: rawjson.Message(`{"large":9007199254740997}`)},
				},
			}},
			ToolCalls: []model.ToolCall{{Name: "tool.read", ID: "call", Payload: rawjson.Message(`{"large":9007199254740999}`)}},
			Usage:     model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, StopReason: "done", OutputLimited: true,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := callrecord.Encode(test.response)
			require.NoError(t, err)
			got, err := callrecord.DecodeResponse(data)
			require.NoError(t, err)
			again, err := callrecord.Encode(got)
			require.NoError(t, err)
			assert.Equal(t, data, again)
		})
	}
}

func TestDecodeResponsePreservesPayloadBytes(t *testing.T) {
	for _, payload := range []string{`{ "z": 9007199254740993, "a": [1, 2] }`, `null`, `9007199254740993`} {
		t.Run(payload, func(t *testing.T) {
			data := []byte(`{"Content":[],"ToolCalls":[{"Name":"tool.read","ID":"call","Payload":` + payload + `}]}`)
			got, err := callrecord.DecodeResponse(data)
			require.NoError(t, err)
			require.Len(t, got.ToolCalls, 1)
			assert.Equal(t, payload, string(got.ToolCalls[0].Payload))
		})
	}
}

func TestDecodeResponseRejectsMalformedContent(t *testing.T) {
	for _, data := range []string{`{`, `{"Content":[null]}`, `{"Content":[{"Parts":[{"type":"invalid"}]}]}`} {
		t.Run(data, func(t *testing.T) {
			_, err := callrecord.DecodeResponse([]byte(data))
			assert.Error(t, err)
		})
	}
}
