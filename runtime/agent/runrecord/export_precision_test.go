package runrecord_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

func TestExportTranscriptPreservesExactDynamicNumbers(t *testing.T) {
	for _, value := range []string{
		`{"n":9007199254740993}`,
		`{"nested":[9007199254740993,0.1234567890123456789,1.234567890123456789e30]}`,
	} {
		t.Run(value, func(t *testing.T) {
			record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{
				{RunID: "parent", Transcript: []*model.Message{{Role: model.ConversationRoleAssistant, Meta: map[string]any{"numbers": jsontext.Value(value)}, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "lookup", Input: jsontext.Value(value)}}}}},
				{RunID: "child", Transcript: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: jsontext.Value(value)}}}}},
			}}
			original, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(original)
			require.NoError(t, err)
			encoded, err := restored.Export().Marshal()
			require.NoError(t, err)
			assert.Equal(t, string(original), string(encoded))
			assert.Contains(t, string(encoded), value)
		})
	}
}

func TestExportTranscriptNilAndEmptyMessages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []*model.Message
	}{
		{name: "nil transcript"},
		{name: "empty transcript", messages: []*model.Message{}},
		{name: "nil message", messages: []*model.Message{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "run", Transcript: tc.messages}}}
			original, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(original)
			require.NoError(t, err)
			assert.Equal(t, tc.messages, restored.Runs[0].Transcript)
			encoded, err := restored.Export().Marshal()
			require.NoError(t, err)
			assert.Equal(t, string(original), string(encoded))
		})
	}
}

func TestExportTranscriptPreservesAcceptedMessageShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		part string
		want string
	}{
		{name: "legacy arguments", part: `{"Kind":"tool_use","Name":"lookup","Args":{"n":9007199254740993}}`, want: `"Input":{"n":9007199254740993}`},
		{name: "omitted input", part: `{"Kind":"tool_use","Name":"lookup"}`, want: `"Input":null`},
		{name: "null input precedes arguments", part: `{"Kind":"tool_use","Name":"lookup","Input":null,"Args":{"n":1}}`, want: `"Input":null`},
		{name: "input precedes arguments", part: `{"Kind":"tool_use","Name":"lookup","Input":{"n":9007199254740993},"Args":{"n":1}}`, want: `"Input":{"n":9007199254740993}`},
		{name: "null arguments", part: `{"Kind":"tool_use","Name":"lookup","Args":null}`, want: `"Input":null`},
		{name: "omitted result content", part: `{"Kind":"tool_result","ToolUseID":"call"}`, want: `"Content":null`},
		{name: "null result content", part: `{"Kind":"tool_result","ToolUseID":"call","Content":null}`, want: `"Content":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := `{"Role":"assistant","Parts":[` + tc.part + `]}`
			var accepted model.Message
			require.NoError(t, json.Unmarshal([]byte(message), &accepted))
			wire := `{"version":1,"root_run_id":"root","runs":[{"run_id":"run","transcript":[` + message + `]}]}`
			restored, err := runrecord.FromExport([]byte(wire))
			require.NoError(t, err)
			encoded, err := restored.Export().Marshal()
			require.NoError(t, err)
			assert.Contains(t, string(encoded), tc.want)
		})
	}
}
