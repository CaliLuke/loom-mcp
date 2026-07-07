package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestClientCompleteEncodesToolsAndDecodesToolCalls(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
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
	require.Len(t, captured["tools"], 1)
	options := captured["options"].(map[string]any)
	require.EqualValues(t, 128, options["num_predict"])
	assert.InEpsilon(t, 0.2, options["temperature"], 0.0001)
	messages := captured["messages"].([]any)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.NotEmpty(t, messages[2].(map[string]any)["tool_calls"])
}

func TestClientStreamEmitsTextToolCallUsageAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer does not implement http.Flusher")
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
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

	chunks := collectStreamChunks(t, streamer)
	require.Len(t, chunks, 5)
	require.Equal(t, model.ChunkTypeText, chunks[0].Type)
	require.Equal(t, "Hel", chunks[0].Message.Parts[0].(model.TextPart).Text)
	require.Equal(t, model.ChunkTypeText, chunks[1].Type)
	require.Equal(t, "lo", chunks[1].Message.Parts[0].(model.TextPart).Text)
	require.Equal(t, model.ChunkTypeToolCall, chunks[2].Type)
	require.Equal(t, tools.Ident("lookup"), chunks[2].ToolCall.Name)
	require.JSONEq(t, `{"query":"docs"}`, string(chunks[2].ToolCall.Payload))
	require.Equal(t, model.ChunkTypeUsage, chunks[3].Type)
	require.Equal(t, 15, chunks[3].UsageDelta.TotalTokens)
	require.Equal(t, model.ChunkTypeStop, chunks[4].Type)
	require.Equal(t, "stop", chunks[4].StopReason)
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

	chunks := collectStreamChunks(t, streamer)
	require.Len(t, chunks, 3)
	require.Equal(t, model.ChunkTypeCompletionDelta, chunks[0].Type)
	require.Equal(t, "draft", chunks[0].CompletionDelta.Name)
	require.JSONEq(t, `{"answer":"ok"}`, chunks[0].CompletionDelta.Delta)
	require.Equal(t, model.ChunkTypeCompletion, chunks[1].Type)
	require.Equal(t, "draft", chunks[1].Completion.Name)
	require.JSONEq(t, `{"answer":"ok"}`, string(chunks[1].Completion.Payload))
	require.Equal(t, model.ChunkTypeStop, chunks[2].Type)
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

func collectStreamChunks(t *testing.T, streamer model.Streamer) []model.Chunk {
	t.Helper()
	var chunks []model.Chunk
	for {
		chunk, err := streamer.Recv()
		if errors.Is(err, io.EOF) {
			return chunks
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
}
