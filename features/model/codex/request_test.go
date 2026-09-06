package codex

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

func TestBuildRequestEncodesTranscriptToolsAndThinking(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "default", HighModel: "high"})
	require.NoError(t, err)
	request := &model.Request{
		ModelClass: model.ModelClassHighReasoning,
		Messages: []*model.Message{
			{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "base"}}},
			{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "later"}}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "look"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")}}},
			{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call-1", Name: "catalog.lookup", Input: map[string]any{"q": "x"}}}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"ok": true}}}},
			{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Redacted: []byte("encrypted")}}},
		},
		Tools:      []*model.ToolDefinition{{Name: "catalog.lookup", Description: "Lookup", InputSchema: map[string]any{"type": "object"}}},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "catalog.lookup"},
		Thinking:   &model.ThinkingOptions{Enable: true},
	}
	built, err := client.buildRequest(request)
	require.NoError(t, err)
	assert.Equal(t, "high", built.modelID)
	assert.Equal(t, "base", built.body.Instructions)
	assert.Equal(t, []string{"reasoning.encrypted_content"}, built.body.Include)
	assert.Equal(t, map[string]any{"summary": "auto"}, built.body.Reasoning)
	require.Len(t, built.body.Tools, 1)
	assert.Equal(t, "catalog_lookup", built.body.Tools[0].Name)
	assert.Equal(t, map[string]any{"type": "function", "name": "catalog_lookup"}, built.body.ToolChoice)

	encoded, err := built.marshal(false)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body))
	input := body["input"].([]any)
	assert.Equal(t, "developer", input[0].(map[string]any)["role"])
	userContent := input[1].(map[string]any)["content"].([]any)
	image := userContent[1].(map[string]any)
	assert.Equal(t, "data:image/png;base64,cG5n", image["image_url"])
	call := input[2].(map[string]any)
	assert.Equal(t, "catalog_lookup", call["name"])
	result := input[3].(map[string]any)
	assert.Equal(t, `{"ok":true}`, result["output"])
	reasoning := input[4].(map[string]any)
	assert.Equal(t, "encrypted", reasoning["encrypted_content"])
}

func TestBuildRequestAddsLatestVisibleInputForSystemOnlyTranscript(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	built, err := client.buildRequest(&model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "first instruction"}}},
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "latest instruction"}}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "first instruction", built.body.Instructions)
	require.Len(t, built.body.Input, 2)
	developer := built.body.Input[0].(map[string]any)
	assert.Equal(t, "developer", developer["role"])
	developerContent := developer["content"].([]any)
	require.Len(t, developerContent, 1)
	assert.Equal(t, "latest instruction", developerContent[0].(map[string]any)["text"])
	user := built.body.Input[1].(map[string]any)
	assert.Equal(t, "user", user["role"])
	content := user["content"].([]any)
	require.Len(t, content, 1)
	assert.Equal(t, "latest instruction", content[0].(map[string]any)["text"])
}

func TestBuildRequestRepairsInterruptedAndOrphanToolHistory(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	built, err := client.buildRequest(&model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "missing", Name: "tool", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "orphan", Content: "result"}}},
	}})
	require.NoError(t, err)
	require.Len(t, built.body.Input, 3)
	placeholder := built.body.Input[1].(map[string]any)
	assert.Equal(t, interruptedToolOutput, placeholder["output"])
	orphan := built.body.Input[2].(map[string]any)
	assert.Contains(t, orphan["content"].([]any)[0].(map[string]any)["text"], "call_id=orphan")
}

func TestBuildRequestAppliesCompleteLiteShape(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", ResponsesLite: true})
	require.NoError(t, err)
	request := &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatJPEG, Bytes: []byte("jpg")}}},
		},
		Tools:      []*model.ToolDefinition{{Name: "tool.one", InputSchema: map[string]any{"type": "object"}}, {Name: "tool.two", InputSchema: map[string]any{"type": "object"}}},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "tool.two"},
	}
	built, err := client.buildRequest(request)
	require.NoError(t, err)
	assert.Empty(t, built.body.Instructions)
	assert.Empty(t, built.body.Tools)
	assert.Equal(t, "required", built.body.ToolChoice)
	assert.False(t, *built.body.ParallelToolCalls)
	assert.Equal(t, "all_turns", built.body.Reasoning["context"])
	additional := built.body.Input[0].(map[string]any)
	selected := additional["tools"].([]wireTool)
	require.Len(t, selected, 1)
	assert.Equal(t, "tool_two", selected[0].Name)
	image := built.body.Input[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	_, hasDetail := image["detail"]
	assert.False(t, hasDetail)
	wsBody, err := built.marshal(true)
	require.NoError(t, err)
	assert.Contains(t, string(wsBody), responsesLiteMetadata)
}

func TestUnsupportedRequestFailsBeforeCredentialsAndNetwork(t *testing.T) {
	credentialCalls := 0
	networkCalls := 0
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(func(context.Context) (Credentials, error) {
			credentialCalls++
			return Credentials{AccessToken: "token", AccountID: "account"}, nil
		}),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
		})},
		Transport: TransportSSE, DefaultModel: "model",
	})
	require.NoError(t, err)
	invalidSerialization := testRequest()
	invalidSerialization.Messages[0].Parts[0] = model.TextPart{Text: string([]byte{0xff})}
	tests := []*model.Request{testRequest(), testRequest(), testRequest(), testRequest(), testRequest(), invalidSerialization}
	tests[0].Temperature = 0.1
	tests[1].MaxTokens = 1
	tests[2].StructuredOutput = &model.StructuredOutput{Name: "x", Schema: []byte(`{"type":"object"}`)}
	tests[3].Cache = &model.CacheOptions{}
	tests[4].Thinking = &model.ThinkingOptions{BudgetTokens: 1}
	for _, request := range tests {
		stream, err := client.Stream(context.Background(), request)
		require.Error(t, err)
		require.Nil(t, stream)
	}
	assert.Zero(t, credentialCalls)
	assert.Zero(t, networkCalls)
}

func TestBuildRequestPairsRepeatedToolIDsInTranscriptOrder(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	built, err := client.buildRequest(&model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{
			model.ToolUsePart{ID: "reused", Name: "history.first", Input: map[string]any{"n": 1}},
			model.ToolUsePart{ID: "reused", Name: "history.second", Input: map[string]any{"n": 2}},
		}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{
			model.ToolResultPart{ToolUseID: "reused", Content: "first"},
			model.ToolResultPart{ToolUseID: "reused", Content: "second"},
		}},
	}})
	require.NoError(t, err)
	require.Len(t, built.body.Input, 4)
	firstCall := built.body.Input[0].(map[string]any)
	secondCall := built.body.Input[1].(map[string]any)
	assert.Equal(t, "history.first", built.codec.CanonicalName(firstCall["name"].(string)))
	assert.Equal(t, "history.second", built.codec.CanonicalName(secondCall["name"].(string)))
	assert.Equal(t, `"first"`, built.body.Input[2].(map[string]any)["output"])
	assert.Equal(t, `"second"`, built.body.Input[3].(map[string]any)["output"])
}

func TestBuildRequestDoesNotPairToolResultsBeforeCalls(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	built, err := client.buildRequest(&model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "early"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "history.tool", Input: map[string]any{}}}},
	}})
	require.NoError(t, err)
	require.Len(t, built.body.Input, 3)
	orphan := built.body.Input[0].(map[string]any)
	assert.Contains(t, orphan["content"].([]any)[0].(map[string]any)["text"], "call_id=call")
	assert.Equal(t, interruptedToolOutput, built.body.Input[2].(map[string]any)["output"])
}

func TestBuildRequestRejectsInvalidToolCatalogAndRoles(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	tests := []struct {
		name    string
		request *model.Request
	}{
		{name: "nil definition", request: &model.Request{Messages: testRequest().Messages, Tools: []*model.ToolDefinition{nil}}},
		{name: "duplicate definition", request: &model.Request{Messages: testRequest().Messages, Tools: []*model.ToolDefinition{{Name: "same", InputSchema: map[string]any{"type": "object"}}, {Name: "same", InputSchema: map[string]any{"type": "object"}}}}},
		{name: "user tool use", request: &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]any{}}}}}}},
		{name: "assistant tool result", request: &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "result"}}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.buildRequest(tt.request)
			require.Error(t, err)
		})
	}
}

func TestBuildRequestRejectsMalformedRawToolResult(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	_, err = client.buildRequest(&model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: rawjson.Message(`{`)}}},
	}})
	require.ErrorContains(t, err, "invalid JSON")
}

func TestOrphanToolResultTruncationPreservesUTF8(t *testing.T) {
	output := strings.Repeat("a", maxOrphanOutputBytes-1) + "€"
	message := orphanToolResultMessage(model.ToolResultPart{ToolUseID: "call"}, output)
	text := message["content"].([]any)[0].(map[string]any)["text"].(string)
	assert.True(t, utf8.ValidString(text))
	assert.Contains(t, text, "...[truncated]")
	_, err := json.Marshal(message)
	require.NoError(t, err)
}
