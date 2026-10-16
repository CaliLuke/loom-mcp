package codex

import (
	"net/http"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexRejectsInvalidRequestBeforeNetwork(t *testing.T) {
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}
	for _, tc := range []struct {
		name    string
		request *model.Request
		want    string
	}{
		{"nil", nil, "request is required"},
		{"empty", &model.Request{}, "messages are required"},
		{"nil message", &model.Request{Messages: []*model.Message{nil}}, "nil message"},
		{"interleaved thinking", &model.Request{Messages: []*model.Message{user}, Thinking: &model.ThinkingOptions{Interleaved: true}}, "interleaved thinking"},
		{"blank tool name", &model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: " "}}}, "tool name is required"},
		{"missing schema", &model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read"}}}, "object input schema"},
		{"unencodable schema", &model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: make(chan int)}}}, "encode tool"},
		{"array schema", &model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: []int{1}}}}, "object input schema"},
		{"scalar schema", &model.Request{Messages: []*model.Message{user}, Tools: []*model.ToolDefinition{{Name: "read", InputSchema: map[string]any{"type": "string"}}}}, "object input schema"},
		{"system image", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}}}, user}}, "system message part"},
		{"system empty", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleSystem}, user}}, "system message is empty"},
		{"unknown role", &model.Request{Messages: []*model.Message{{Role: "unknown", Parts: user.Parts}}}, "message role"},
		{"empty message", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser}}}, "message is empty"},
		{"empty text", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{}}}}}, "empty text"},
		{"nil part", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{nil}}}}, "message part"},
		{"assistant image", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}}}}}, "only in user"},
		{"empty image", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG}}}}}, "image bytes"},
		{"invalid image format", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "unknown", Bytes: []byte("image")}}}}}, "image format"},
		{"unsigned thinking", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "thought"}}}}}, "encrypted assistant thinking"},
		{"document", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.DocumentPart{Name: "doc", Text: "text"}}}}}, "documents are not supported"},
		{"citations", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.CitationsPart{Text: "text"}}}}}, "citations are not supported"},
		{"cache checkpoint", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.CacheCheckpointPart{}}}}}, "cache checkpoints are not supported"},
		{"missing call id", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{Name: "read", Input: map[string]any{}}}}}}, "requires id and name"},
		{"missing call name", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Input: map[string]any{}}}}}}, "requires id and name"},
		{"invalid call input", &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: make(chan int)}}}}}, "valid object arguments"},
		{"missing choice name", &model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool}}, "requires a name"},
		{"unknown choice name", &model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "absent"}}, "does not match"},
		{"unknown choice mode", &model.Request{Messages: []*model.Message{user}, ToolChoice: &model.ToolChoice{Mode: "unknown"}}, "unsupported tool choice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "test", Transport: TransportSSE, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, assert.AnError
			})}})
			require.NoError(t, err)
			response, err := client.Complete(t.Context(), tc.request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, response)
			stream, err := client.Stream(t.Context(), tc.request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, stream)
			assert.Zero(t, calls)
		})
	}
}
