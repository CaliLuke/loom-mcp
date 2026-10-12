package runtime

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	"github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

type multimodalRoundTrip func(*http.Request) (*http.Response, error)
type multimodalGemini struct{}

func TestMultimodalRecallUnsupportedAdapters(t *testing.T) {
	for _, name := range []string{"openai", "gemini", "ollama", "codex"} {
		t.Run(name, func(t *testing.T) {
			client := unsupportedRecallClient(t, name)
			_, supports := client.(model.ToolResultPartsInfo)
			assert.False(t, supports)
			rt := New()
			_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
			require.NoError(t, err)
			start := continuationTestStart(t, rt, "run", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}, model.DocumentPart{Name: "pdf", Format: model.DocumentFormatPDF, Bytes: []byte("%PDF-test")}}}})
			for _, number := range []int{1, 2} {
				payload, err := json.Marshal(map[string]any{"read": map[string]any{"id": "1", "part": "attachment", "index": number, "offset": 0}})
				require.NoError(t, err)
				out, err := rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "run", Scope: "run", Head: start.IndexHead, ViewRef: start.Continuation.View, PartTokens: 1500, Call: planner.ToolRequest{Payload: rawjson.Message(payload)}})
				require.NoError(t, err)
				assert.Equal(t, recall.CodeUnsupportedPart, out.Code)
				assert.Empty(t, out.Parts)
				data, err := rt.RunStore.Get(t.Context(), "run", out.Result.Refs.Result)
				require.NoError(t, err)
				var result recall.Response
				require.NoError(t, json.Unmarshal(data, &result))
				assert.Equal(t, "provider", result.Reason)
				request := &model.Request{Messages: []*model.Message{contentMessage("read"), {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: rawjson.Message(data)}}}}, Tools: []*model.ToolDefinition{{Name: "read", Description: "Read context", InputSchema: map[string]any{"type": "object"}}}}
				_, err = client.Complete(t.Context(), request)
				require.NoError(t, err)
			}
		})
	}
}

func (f multimodalRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
func (multimodalGemini) GenerateContent(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "ok"}}}, FinishReason: genai.FinishReasonStop}}}, nil
}
func unsupportedRecallClient(t *testing.T, name string) model.Client {
	t.Helper()
	transport := multimodalRoundTrip(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		assert.Contains(t, string(data), "recall_unsupported_part")
		response := `{"id":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
		contentType := "application/json"
		if name == "ollama" {
			response = `{"message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`
		}
		if name == "codex" {
			contentType = "text/event-stream"
			response = "data: " + `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"msg-1","type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}` + "\n\n"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	var provider model.Provider
	var err error
	switch name {
	case "openai":
		sdk := openaisdk.NewClient(option.WithAPIKey("test"), option.WithHTTPClient(&http.Client{Transport: transport}))
		provider, err = openaimodel.New(openaimodel.Options{Client: &sdk.Responses, DefaultModel: "gpt-test"})
	case "gemini":
		provider, err = gemini.New(gemini.Options{Client: multimodalGemini{}, DefaultModel: "gemini-test"})
	case "ollama":
		provider, err = ollama.New(ollama.Options{HTTPClient: &http.Client{Transport: transport}, ServerURL: "http://test.local", DefaultModel: "test"})
	case "codex":
		provider, err = codex.New(codex.Options{HTTPClient: &http.Client{Transport: transport}, CredentialSource: codex.CredentialSourceFunc(func(context.Context) (codex.Credentials, error) {
			return codex.Credentials{AccessToken: "test", AccountID: "test"}, nil
		}), Transport: codex.TransportSSE, DefaultModel: "test"})
	}
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	return client
}
