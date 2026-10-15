package ollama_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	ollama "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type requestTransport func(*http.Request) (*http.Response, error)

func TestOllamaRejectsInvalidRequestsBeforeHTTP(t *testing.T) {
	tests := []struct {
		name    string
		request *model.Request
		want    string
	}{
		{"nil", nil, "request is required"},
		{"no messages", &model.Request{}, "messages are required"},
		{"nil message", &model.Request{Messages: []*model.Message{nil}}, "at least one message"},
		{"empty message", ollamaParts(), "at least one message"},
		{"unknown role", &model.Request{Messages: []*model.Message{{Role: "unknown"}}}, "unsupported message role"},
		{"unknown part", ollamaParts(model.DocumentPart{}), "unsupported message part"},
		{"gif", ollamaParts(model.ImagePart{Format: model.ImageFormatGIF, Bytes: []byte("gif")}), "GIF images"},
		{"unknown image", ollamaParts(model.ImagePart{Format: "bmp", Bytes: []byte("bmp")}), "unsupported image format"},
		{"no image bytes", ollamaParts(model.ImagePart{Format: model.ImageFormatPNG}), "image bytes are required"},
		{"tool use without name", ollamaParts(model.ToolUsePart{ID: "call"}), "requires name"},
		{"result without id", ollamaParts(model.ToolResultPart{}), "requires tool use id"},
		{"non JSON result", ollamaParts(model.ToolResultPart{ToolUseID: "call", Content: make(chan int)}), "tool result"},
		{"malformed raw result", ollamaParts(model.ToolResultPart{ToolUseID: "call", Content: rawjson.Message("{")}), "tool result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			client, err := ollama.New(ollama.Options{ServerURL: "https://ollama.invalid", DefaultModel: "test", HTTPClient: &http.Client{Transport: requestTransport(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, assert.AnError
			})}})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), tt.request)
			require.ErrorContains(t, err, tt.want)
			_, err = client.Stream(t.Context(), tt.request)
			require.ErrorContains(t, err, tt.want)
			assert.False(t, called)
		})
	}
}

func TestOllamaRejectsInvalidToolAndOutputConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.Request)
		want   string
	}{
		{"missing name", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{{Name: " "}}
		}, "tool name is required"},
		{"forced tool", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "tool"}
		}, "not supported"},
		{"unknown choice", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: "unknown"}
		}, "unsupported tool choice"},
		{"missing schema", func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Name: "answer"}
		}, "schema is required"},
		{"malformed schema", func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Name: "answer", Schema: []byte("{")}
		}, "must be valid JSON"},
		{"schema with tools", func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Name: "answer", Schema: []byte("{}")}
			r.Tools = []*model.ToolDefinition{{Name: "tool"}}
		}, "cannot be combined"},
		{"non JSON schema", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{{Name: "tool", InputSchema: make(chan int)}}
		}, "marshal request"},
		{"non JSON history", func(r *model.Request) {
			r.Messages = ollamaParts(model.ToolUsePart{Name: "tool", ID: "call", Input: make(chan int)}).Messages
		}, "marshal request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			client, err := ollama.New(ollama.Options{ServerURL: "https://ollama.invalid", DefaultModel: "test", HTTPClient: &http.Client{Transport: requestTransport(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, assert.AnError
			})}})
			require.NoError(t, err)
			request := ollamaParts(model.TextPart{Text: "hello"})
			tt.change(request)
			_, err = client.Complete(t.Context(), request)
			require.ErrorContains(t, err, tt.want)
			_, err = client.Stream(t.Context(), request)
			require.ErrorContains(t, err, tt.want)
			assert.False(t, called)
		})
	}
}

func TestOllamaToolResultEncodingsPreserveContent(t *testing.T) {
	tests := []struct {
		name    string
		content any
		failed  bool
		want    string
	}{
		{"nil error", nil, true, `{"error":"tool execution failed"}`},
		{"empty error", "", true, `{"error":"tool execution failed"}`},
		{"empty bytes", []byte{}, true, `{"error":"tool execution failed"}`},
		{"text error", "unavailable", true, `{"error":"unavailable"}`},
		{"bytes error", []byte("unavailable"), true, `{"error":"unavailable"}`},
		{"object error", map[string]any{"retry": true}, true, `{"error":"{\"retry\":true}"}`},
		{"nil result", nil, false, `null`},
		{"raw result", rawjson.Message(`{"ok":true}`), false, `{"ok":true}`},
		{"jsontext result", jsontext.Value(`{"ok":true}`), false, `{"ok":true}`},
		{"bytes result", []byte(`{"ok":true}`), false, `{"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			client, err := ollama.New(ollama.Options{ServerURL: "https://ollama.invalid", DefaultModel: "test", HTTPClient: &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				err := json.UnmarshalRead(request.Body, &captured)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"model":"test","done":true,"message":{"role":"assistant","content":"ok"}}`)), Header: make(http.Header)}, nil
			})}})
			require.NoError(t, err)
			response, err := client.Complete(t.Context(), ollamaParts(model.ToolResultPart{ToolUseID: "call", IsError: tt.failed, Content: tt.content}))
			require.NoError(t, err)
			require.NotNil(t, response)
			require.Len(t, captured.Messages, 1)
			assert.JSONEq(t, tt.want, captured.Messages[0].Content)
		})
	}
}

func (f requestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func ollamaParts(parts ...model.Part) *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: parts}}}
}
