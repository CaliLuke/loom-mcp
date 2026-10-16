package gemini_test

import (
	"encoding/json/jsontext"
	"math"
	"testing"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestGeminiRejectsUnencodableRequestsBeforeProviderCall(t *testing.T) {
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}
	for _, tc := range []struct {
		name    string
		request model.Request
		want    string
	}{
		{"no messages", model.Request{}, "messages are required"},
		{"system only", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "rules"}}}}}, "at least one"},
		{"invalid role", model.Request{Messages: []*model.Message{{Role: "invalid", Parts: user.Parts}}}, "unsupported message role"},
		{"system image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("data")}}}, user}}, "unsupported system"},
		{"empty image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG}}}}}, "image bytes"},
		{"GIF image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatGIF, Bytes: []byte("data")}}}}}, "GIF"},
		{"unknown image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "unknown", Bytes: []byte("data")}}}}}, "image format"},
		{"missing tool name", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Input: map[string]any{}}}}}}, "requires name"},
		{"array tool arguments", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: []int{1}}}}}}, "JSON object"},
		{"unmarshalable tool arguments", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: make(chan int)}}}}}, "args"},
		{"unknown tool result", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "missing", Content: "value"}}}}}, "unknown tool use"},
		{"max output overflow", model.Request{Messages: []*model.Message{user}, MaxTokens: math.MaxInt32 + 1}, "int32 range"},
		{"thinking overflow", model.Request{Messages: []*model.Message{user}, Thinking: &model.ThinkingOptions{Enable: true, BudgetTokens: math.MaxInt32 + 1}}, "thinking budget"},
		{"tool schema value", model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: make(chan int)}}}, "schema"},
		{"tool schema array", model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: []int{1}}}}, "schema object"},
		{"tool choice missing name", model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool}}, "requires a tool name"},
		{"tool choice unknown name", model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "missing"}}, "does not match"},
		{"unknown choice mode", model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: "invalid"}}, "unsupported tool choice"},
		{"missing output schema", model.Request{Messages: []*model.Message{user}, StructuredOutput: &model.StructuredOutput{Name: "output"}}, "schema is required"},
		{"malformed output schema", model.Request{Messages: []*model.Message{user}, StructuredOutput: &model.StructuredOutput{Name: "output", Schema: rawjson.Message("{")}}, "valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockModelsClient{}
			client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			result, err := client.Complete(t.Context(), &tc.request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
			assert.Empty(t, mock.model)
		})
	}
}

func TestGeminiPreservesToolArgumentRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  map[string]any
	}{
		{"nil", nil, map[string]any{}},
		{"raw JSON", rawjson.Message(`{"id":"one"}`), map[string]any{"id": "one"}},
		{"empty raw JSON", rawjson.Message{}, map[string]any{}},
		{"jsontext", jsontext.Value(`{"id":"one"}`), map[string]any{"id": "one"}},
		{"empty jsontext", jsontext.Value{}, map[string]any{}},
		{"bytes", []byte(`{"id":"one"}`), map[string]any{"id": "one"}},
		{"empty bytes", []byte{}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
			client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: tc.value}}}}})
			require.NoError(t, err)
			require.Len(t, mock.contents, 1)
			require.Len(t, mock.contents[0].Parts, 1)
			assert.Equal(t, tc.want, mock.contents[0].Parts[0].FunctionCall.Args)
		})
	}
}

func TestGeminiEncodesToolFailureWithoutLosingFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "tool execution failed"},
		{"empty string", "", "tool execution failed"},
		{"message", "denied", "denied"},
		{"empty bytes", []byte{}, "tool execution failed"},
		{"bytes", []byte("denied"), "denied"},
		{"object", map[string]any{"error": "denied"}, `{"error":"denied"}`},
		{"unmarshalable", make(chan int), "tool execution failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
			client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", IsError: true, Content: tc.value}}},
			}})
			require.NoError(t, err)
			require.Len(t, mock.contents, 2)
			assert.Equal(t, map[string]any{"error": tc.want}, mock.contents[1].Parts[0].FunctionResponse.Response)
		})
	}
}
