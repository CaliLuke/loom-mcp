package ollama_test

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
)

func TestClientCompleteEncodesToolsAndDecodesToolCalls(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if err := json.UnmarshalRead(r.Body, &captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := json.MarshalWrite(w, map[string]any{
			"model": "llama3.1",
			"done":  true,
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id": "call-1",
					"function": map[string]any{
						"name":      "lookup",
						"arguments": map[string]any{"query": "docs"},
					},
				}},
			},
			"prompt_eval_count": 8,
			"eval_count":        3,
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{
		ServerURL:    server.URL,
		DefaultModel: "llama3.1",
		MaxTokens:    128,
		Temperature:  0.2,
	})
	require.NoError(t, err)

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
			{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.ToolUsePart{ID: "prior-call", Name: "lookup", Input: map[string]any{"query": "old"}},
			}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{
				model.ToolResultPart{ToolUseID: "prior-call", Content: map[string]any{"hits": 2}},
			}},
		},
		Tools: []*model.ToolDefinition{{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, tools.Ident("lookup"), resp.ToolCalls[0].Name)
	require.Equal(t, "call-1", resp.ToolCalls[0].ID)
	require.JSONEq(t, `{"query":"docs"}`, string(resp.ToolCalls[0].Payload))
	require.Equal(t, 11, resp.Usage.TotalTokens)
	require.Equal(t, "stop", resp.StopReason)

	require.Equal(t, "llama3.1", captured["model"])
	require.Equal(t, false, captured["stream"])
	require.NotContains(t, captured, "think")
	require.Len(t, captured["tools"], 1)
	options := captured["options"].(map[string]any)
	require.EqualValues(t, 128, options["num_predict"])
	assert.InEpsilon(t, 0.2, options["temperature"], 0.0001)
	messages := captured["messages"].([]any)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.NotEmpty(t, messages[2].(map[string]any)["tool_calls"])
}

func TestClientCompleteReturnsEmbeddedProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, json.MarshalWrite(w, map[string]any{
			"error": "model runner crashed",
		}))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "llama3.1"})
	require.NoError(t, err)

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
	})
	require.Nil(t, resp)
	require.ErrorContains(t, err, "ollama chat: provider error: model runner crashed")
}

func TestClientCompleteEncodesThinkingOption(t *testing.T) {
	tests := []struct {
		name     string
		thinking *model.ThinkingOptions
		want     any
	}{
		{
			name:     "enabled",
			thinking: &model.ThinkingOptions{Enable: true},
			want:     true,
		},
		{
			name:     "explicit disabled",
			thinking: &model.ThinkingOptions{Enable: false},
			want:     false,
		},
		{
			name:     "omitted",
			thinking: nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.UnmarshalRead(r.Body, &captured); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				if err := json.MarshalWrite(w, map[string]any{
					"model": "gemma4",
					"done":  true,
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
				}); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()

			client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "gemma4"})
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), &model.Request{
				Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
				Thinking: tt.thinking,
			})
			require.NoError(t, err)

			if tt.want == nil {
				require.NotContains(t, captured, "think")
				return
			}
			require.Equal(t, tt.want, captured["think"])
		})
	}
}

func TestClientCompleteDecodesThinkingAndTextParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.MarshalWrite(w, map[string]any{
			"model": "gemma4",
			"done":  true,
			"message": map[string]any{
				"role":     "assistant",
				"thinking": "I should reason privately.",
				"content":  "Final answer.",
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "gemma4"})
	require.NoError(t, err)
	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
	})
	require.NoError(t, err)

	require.Len(t, resp.Content, 1)
	require.Len(t, resp.Content[0].Parts, 2)
	thinking, ok := resp.Content[0].Parts[0].(model.ThinkingPart)
	require.True(t, ok)
	require.Equal(t, "I should reason privately.", thinking.Text)
	require.True(t, thinking.Final)
	text, ok := resp.Content[0].Parts[1].(model.TextPart)
	require.True(t, ok)
	require.Equal(t, "Final answer.", text.Text)
}

func TestClientStreamEmitsThinkingTextToolCallUsageAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer does not implement http.Flusher")
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","thinking":"Considering tools."}}` + "\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","content":"Hel"}}` + "\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","content":"lo"}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","tool_calls":[{"id":"call-1","function":{"name":"lookup","arguments":{"query":"docs"}}}]}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.1","done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}` + "\n"))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "llama3.1"})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
		Tools:    []*model.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 6)
	thinking := chunks[0].(model.ThinkingChunk).Message.Parts[0].(model.ThinkingPart)
	require.Equal(t, "Considering tools.", thinking.Text)
	require.Equal(t, "Hel", chunks[1].(model.TextChunk).Message.Parts[0].(model.TextPart).Text)
	require.Equal(t, "lo", chunks[2].(model.TextChunk).Message.Parts[0].(model.TextPart).Text)
	call := chunks[3].(model.ToolCallChunk).ToolCall
	require.Equal(t, tools.Ident("lookup"), call.Name)
	require.JSONEq(t, `{"query":"docs"}`, string(call.Payload))
	require.Equal(t, 15, chunks[4].(model.UsageChunk).Usage.TotalTokens)
	require.Equal(t, "stop", chunks[5].(model.StopChunk).Reason)
}

func TestClientStreamTimeoutDoesNotLimitBodyLifetime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer does not implement http.Flusher")
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","content":"first"}}` + "\n"))
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","content":"second"}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.1","done":true,"done_reason":"stop"}` + "\n"))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{
		ServerURL:    server.URL,
		DefaultModel: "llama3.1",
		Timeout:      10 * time.Millisecond,
	})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunk, err := streamer.Recv()
	require.NoError(t, err)
	require.IsType(t, model.TextChunk{}, chunk)
	require.Equal(t, "first", chunk.(model.TextChunk).Message.Parts[0].(model.TextPart).Text)

	chunk, err = streamer.Recv()
	require.NoError(t, err)
	require.IsType(t, model.TextChunk{}, chunk)
	require.Equal(t, "second", chunk.(model.TextChunk).Message.Parts[0].(model.TextPart).Text)

	chunk, err = streamer.Recv()
	require.NoError(t, err)
	require.IsType(t, model.StopChunk{}, chunk)
}

func TestClientStreamStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"llama3.1","message":{"role":"assistant","content":"{\"answer\":\"ok\"}"}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.1","done":true,"done_reason":"stop"}` + "\n"))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "llama3.1"})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`),
		},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 3)
	delta := chunks[0].(model.CompletionDeltaChunk).Delta
	require.Equal(t, "draft", delta.Name)
	require.JSONEq(t, `{"answer":"ok"}`, delta.Delta)
	completion := chunks[1].(model.CompletionChunk).Completion
	require.Equal(t, "draft", completion.Name)
	require.JSONEq(t, `{"answer":"ok"}`, string(completion.Payload))
	require.IsType(t, model.StopChunk{}, chunks[2])
}

func TestClientStreamReturnsEmbeddedProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"out of memory"}` + "\n"))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "llama3.1"})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	_, err = streamer.Recv()
	require.ErrorContains(t, err, "ollama chat stream: provider error: out of memory")
}

func TestClientStreamRejectsMalformedNDJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{invalid\n"))
	}))
	defer server.Close()

	streamer := newTestStreamer(t, server.URL)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	_, err := streamer.Recv()
	require.ErrorContains(t, err, "ollama: decode stream chunk")
}

func TestClientStreamRejectsTruncatedResponseBeforeDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"partial\"}}\n"))
	}))
	defer server.Close()

	streamer := newTestStreamer(t, server.URL)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunk, err := streamer.Recv()
	require.NoError(t, err)
	require.IsType(t, model.TextChunk{}, chunk)
	_, err = streamer.Recv()
	require.EqualError(t, err, "ollama: stream ended before done")
}

func TestClientStreamReportsScannerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4*1024*1024+1)))
	}))
	defer server.Close()

	streamer := newTestStreamer(t, server.URL)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	_, err := streamer.Recv()
	require.ErrorContains(t, err, "ollama chat stream")
	require.ErrorContains(t, err, "token too long")
}

func TestClientStreamStructuredOutputExcludesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"gemma4","message":{"role":"assistant","thinking":"Draft JSON privately."}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"gemma4","message":{"role":"assistant","content":"{\"answer\":\"ok\"}"}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"gemma4","done":true,"done_reason":"stop"}` + "\n"))
	}))
	defer server.Close()

	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "gemma4"})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`),
		},
		Thinking: &model.ThinkingOptions{Enable: true},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 4)
	thinking := chunks[0].(model.ThinkingChunk).Message.Parts[0].(model.ThinkingPart)
	require.Equal(t, "Draft JSON privately.", thinking.Text)
	require.JSONEq(t, `{"answer":"ok"}`, chunks[1].(model.CompletionDeltaChunk).Delta.Delta)
	require.JSONEq(t, `{"answer":"ok"}`, string(chunks[2].(model.CompletionChunk).Completion.Payload))
	require.IsType(t, model.StopChunk{}, chunks[3])
}

func TestClientValidation(t *testing.T) {
	client, err := ollamamodel.New(ollamamodel.Options{DefaultModel: "llama3.1"})
	require.ErrorContains(t, err, "server URL is required")
	require.Nil(t, client)

	client, err = ollamamodel.New(ollamamodel.Options{ServerURL: "http://localhost:11434"})
	require.ErrorContains(t, err, "default model is required")
	require.Nil(t, client)

	client, err = ollamamodel.New(ollamamodel.Options{ServerURL: "http://localhost:11434", DefaultModel: "llama3.1", Timeout: time.Second})
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestClientRejectsUnsupportedToolChoice(t *testing.T) {
	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: "http://localhost:11434", DefaultModel: "llama3.1"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages:   []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeAny},
	})
	require.ErrorContains(t, err, "tool choice mode")
}

func newTestStreamer(t *testing.T, serverURL string) model.Streamer {
	t.Helper()
	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: serverURL, DefaultModel: "llama3.1"})
	require.NoError(t, err)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "ping"}},
		}},
	})
	require.NoError(t, err)
	return streamer
}
