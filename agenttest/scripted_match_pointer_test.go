package agenttest_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestLastUserContainsPointerParts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []model.Part
	}{
		{"values", []model.Part{model.TextPart{Text: "hello "}, model.TextPart{Text: "world"}}},
		{"pointers", []model.Part{&model.TextPart{Text: "hello "}, &model.TextPart{Text: "world"}}},
		{"mixed", []model.Part{model.TextPart{Text: "hello "}, &model.TextPart{Text: "world"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "older"}}},
				{Role: model.ConversationRoleUser, Parts: tc.parts},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "assistant"}}},
			}}
			_, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NoError(t, agenttest.LastUserContains("hello world")(request))
			require.Error(t, agenttest.LastUserContains("older")(request))
			require.Error(t, agenttest.LastUserContains("assistant")(request))
		})
	}
}

func TestLastToolResultPointerParts(t *testing.T) {
	use := model.ToolUsePart{ID: "latest", Name: "lookup", Input: map[string]any{"id": "1"}}
	image := model.ImagePart{Format: "png", Bytes: []byte{1}}
	document := model.DocumentPart{Name: "report", Format: "pdf", Bytes: []byte{2}}
	parts := []model.ToolResultContentPart{image, document}
	result := model.ToolResultPart{ToolUseID: "latest", Content: map[string]any{"answer": "found"}, Parts: parts}
	stringResult := model.ToolResultPart{ToolUseID: "latest", Content: "found", Parts: parts}
	for _, tc := range []struct {
		name   string
		use    model.Part
		result model.Part
	}{
		{"values", use, result},
		{"pointer use", &use, result},
		{"pointer result", use, &result},
		{"pointers", &use, &result},
		{"string values", use, stringResult},
		{"string pointer use", &use, stringResult},
		{"string pointer result", use, &stringResult},
		{"string pointers", &use, &stringResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "future", Content: "unmatched"}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "future", Name: "future", Input: map[string]any{}}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "older", Name: "lookup", Input: map[string]any{}}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "older", Content: "older"}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{tc.use, model.ToolUsePart{ID: "other", Name: "other", Input: map[string]any{}}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{tc.result, model.ToolResultPart{ToolUseID: "other", Content: "other"}}},
			}}
			_, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NoError(t, agenttest.LastToolResult(tools.Ident("lookup"), "found")(request))
			assert.NoError(t, agenttest.LastToolResult(tools.Ident("other"), "other")(request))
			assert.NoError(t, agenttest.LastToolResultParts(tools.Ident("lookup"), parts...)(request))
			require.Error(t, agenttest.LastToolResult(tools.Ident("lookup"), "older")(request))
			require.Error(t, agenttest.LastToolResult(tools.Ident("future"), "unmatched")(request))
			require.Error(t, agenttest.LastToolResult(tools.Ident("missing"), "found")(request))
			require.Error(t, agenttest.LastToolResultParts(tools.Ident("lookup"), document, image)(request))
			require.Error(t, agenttest.LastToolResultParts(tools.Ident("missing"), parts...)(request))
		})
	}
}

func TestScriptDiagnosticsPointerText(t *testing.T) {
	for _, tc := range []struct {
		name string
		part model.Part
	}{
		{"value", model.TextPart{Text: "diagnostic text"}},
		{"pointer", &model.TextPart{Text: "diagnostic text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("ok")).When(agenttest.LastUserContains("different")))
			require.NoError(t, err)
			request := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{tc.part}}}}
			_, err = script.Complete(t.Context(), request)
			var diagnostic *agenttest.ScriptError
			require.ErrorAs(t, err, &diagnostic)
			assert.Contains(t, diagnostic.Summary, `last_text="diagnostic text"`)
			assert.Equal(t, 1, script.Remaining())
		})
	}
}

func TestMatchersIgnoreNilAndUnrelatedParts(t *testing.T) {
	for _, tc := range []struct {
		name string
		part model.Part
	}{
		{"nil", nil},
		{"nil text", (*model.TextPart)(nil)},
		{"nil tool use", (*model.ToolUsePart)(nil)},
		{"nil tool result", (*model.ToolResultPart)(nil)},
		{"invalid unrelated image", model.ImagePart{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &model.Request{Messages: []*model.Message{
				nil,
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{tc.part, model.ToolUsePart{ID: "call", Name: "lookup"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{tc.part, model.TextPart{Text: "hello"}, model.ToolResultPart{ToolUseID: "call", Content: "found"}}},
				nil,
			}}
			assert.NoError(t, agenttest.LastUserContains("hello")(request))
			assert.NoError(t, agenttest.LastToolResult(tools.Ident("lookup"), "found")(request))
			assert.NoError(t, agenttest.LastToolResultParts(tools.Ident("lookup"))(request))
			require.Error(t, agenttest.LastUserContains("missing")(request))
		})
	}
}
