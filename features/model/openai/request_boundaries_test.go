package openai_test

import (
	"encoding/json/jsontext"
	"testing"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIRejectsUnencodableRequestsBeforeProviderCall(t *testing.T) {
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}
	for _, tc := range []struct {
		name    string
		request model.Request
		want    string
	}{
		{"no messages", model.Request{}, "messages are required"},
		{"nil part", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{nil}}}}, "unsupported message part"},
		{"system image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("data")}}}, user}}, "only supported in user"},
		{"empty image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG}}}}}, "image bytes"},
		{"unknown image", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "unknown", Bytes: []byte("data")}}}}}, "image format"},
		{"missing tool name", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call"}}}}}, "requires name"},
		{"missing tool id", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{Name: "read"}}}}}, "requires id"},
		{"unmarshalable tool arguments", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: make(chan int)}}}}}, "encode tool use"},
		{"missing result id", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{Content: "value"}}}}}, "requires tool use id"},
		{"unmarshalable result", model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: make(chan int)}}}}}, "encode tool result"},
		{"tool schema value", model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: make(chan int)}}}, "schema"},
		{"tool schema malformed", model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: rawjson.Message("{")}}}, "schema"},
		{"tool choice missing name", model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool}}, "requires a tool name"},
		{"tool choice unknown name", model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{nil}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "missing"}}, "does not match"},
		{"unknown choice mode", model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: "invalid"}}, "unsupported tool choice"},
		{"missing output schema", model.Request{Messages: []*model.Message{user}, StructuredOutput: &model.StructuredOutput{Name: "output"}}, "schema is required"},
		{"malformed output schema", model.Request{Messages: []*model.Message{user}, StructuredOutput: &model.StructuredOutput{Name: "output", Schema: rawjson.Message("{")}}, "schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockResponsesClient{}
			client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			result, err := client.Complete(t.Context(), &tc.request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
			stream, err := client.Stream(t.Context(), &tc.request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, stream)
			assert.Empty(t, mock.captured.Model, "invalid requests must not reach the provider")
		})
	}
}

func TestOpenAIPreservesRawToolHistory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"raw JSON", rawjson.Message(`{"id":"one"}`), `{"id":"one"}`},
		{"empty raw JSON", rawjson.Message{}, "null"},
		{"jsontext", jsontext.Value(`{"id":"one"}`), `{"id":"one"}`},
		{"empty jsontext", jsontext.Value{}, "null"},
		{"bytes", []byte(`{"id":"one"}`), `{"id":"one"}`},
		{"empty bytes", []byte{}, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{}}
			client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: tc.value}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: tc.value}}},
			}})
			require.NoError(t, err)
			require.Len(t, mock.captured.Input.OfInputItemList, 2)
			assert.JSONEq(t, tc.want, mock.captured.Input.OfInputItemList[0].OfFunctionCall.Arguments)
			assert.JSONEq(t, tc.want, mock.captured.Input.OfInputItemList[1].OfFunctionCallOutput.Output.OfString.Value)
		})
	}
}

func TestOpenAIRejectsIncompleteStructuredResponses(t *testing.T) {
	for _, text := range []string{"", "not JSON"} {
		t.Run(text, func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{Type: "message", Content: []responses.ResponseOutputMessageContentUnion{{Type: "output_text", Text: text}}}}}}
			client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			result, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}}}, StructuredOutput: &model.StructuredOutput{Name: "output", Schema: rawjson.Message(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)}})
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}
