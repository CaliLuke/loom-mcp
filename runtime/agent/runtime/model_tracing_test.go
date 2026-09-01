package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func TestTracedClientCompleteCapturesGenAIMessagesWhenEnabled(t *testing.T) {
	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingCompleteClient{
			response: &model.Response{
				Content: []model.Message{
					{
						Role: model.ConversationRoleAssistant,
						Parts: []model.Part{
							model.TextPart{Text: "done"},
							model.ThinkingPart{Text: "do not capture"},
						},
					},
				},
				ToolCalls: []model.ToolCall{
					{
						ID:      "tool-1",
						Name:    tools.Ident("svc.search"),
						Payload: rawjson.Message(`{"query":"status"}`),
					},
				},
				Usage: model.TokenUsage{
					Model:            "provider-model",
					InputTokens:      10,
					OutputTokens:     5,
					CacheReadTokens:  3,
					CacheWriteTokens: 2,
				},
				StopReason: "tool_use",
			},
		},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{
			ModelID:              "default",
			AgentID:              "service.agent",
			AgentName:            "service.agent",
			ConversationID:       "session-1",
			CaptureGenAIMessages: true,
		},
	)

	_, err := client.Complete(context.Background(), &model.Request{
		RunID:       "run-1",
		Model:       "requested-model",
		MaxTokens:   256,
		Temperature: 0.2,
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
					model.ThinkingPart{Text: "do not capture"},
				},
			},
		},
	})
	require.NoError(t, err)

	span := tracer.singleSpan(t)
	attrs := span.attrsByKey()
	require.Equal(t, "chat", attrs["gen_ai.operation.name"].AsString())
	require.Equal(t, "session-1", attrs["gen_ai.conversation.id"].AsString())
	require.Equal(t, "service.agent", attrs["gen_ai.agent.id"].AsString())
	require.Equal(t, "requested-model", attrs["gen_ai.request.model"].AsString())
	require.Equal(t, "provider-model", attrs["gen_ai.response.model"].AsString())
	require.Equal(t, int64(10), attrs["gen_ai.usage.input_tokens"].AsInt64())
	require.Equal(t, int64(5), attrs["gen_ai.usage.output_tokens"].AsInt64())
	require.Equal(t, int64(3), attrs["gen_ai.usage.cache_read.input_tokens"].AsInt64())
	require.Equal(t, int64(2), attrs["gen_ai.usage.cache_creation.input_tokens"].AsInt64())
	require.Equal(t, []string{"tool_use"}, attrs["gen_ai.response.finish_reasons"].AsStringSlice())
	require.Equal(t, "run-1", attrs["loom_mcp.run_id"].AsString())

	input := decodeGenAIMessages(t, attrs["gen_ai.input.messages"].AsString())
	require.Equal(t, "user", input[0]["role"])
	inputParts := input[0]["parts"].([]any)
	require.Equal(t, map[string]any{"type": "text", "content": "hello"}, inputParts[0])
	require.NotContains(t, attrs["gen_ai.input.messages"].AsString(), "do not capture")

	output := decodeGenAIMessages(t, attrs["gen_ai.output.messages"].AsString())
	require.Len(t, output, 2)
	require.Equal(t, "assistant", output[0]["role"])
	require.Equal(t, "tool_use", output[0]["finish_reason"])
	outputParts := output[1]["parts"].([]any)
	require.Equal(t, "tool_call", outputParts[0].(map[string]any)["type"])
	require.Equal(t, "svc.search", outputParts[0].(map[string]any)["name"])
}

func TestTracedClientDoesNotCaptureGenAIMessagesByDefault(t *testing.T) {
	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingCompleteClient{
			response: &model.Response{
				Content: []model.Message{{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "done"}},
				}},
			},
		},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{ModelID: "default"},
	)

	_, err := client.Complete(context.Background(), &model.Request{
		RunID: "run-1",
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "secret"}},
		}},
	})
	require.NoError(t, err)

	attrs := tracer.singleSpan(t).attrsByKey()
	require.NotContains(t, attrs, "gen_ai.input.messages")
	require.NotContains(t, attrs, "gen_ai.output.messages")
}

func TestTracedStreamCapturesCoalescedOutputAtEnd(t *testing.T) {
	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingStreamClient{
			streamer: &modelTracingScriptedStreamer{
				chunks: []model.Chunk{
					{Type: model.ChunkTypeText, Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "hel"}}}},
					{Type: model.ChunkTypeText, Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "lo"}}}},
					{Type: model.ChunkTypeThinking, Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "do not capture"}}}},
					{Type: model.ChunkTypeToolCall, ToolCall: &model.ToolCall{ID: "tool-1", Name: tools.Ident("svc.lookup"), Payload: rawjson.Message(`{"id":1}`)}},
					{Type: model.ChunkTypeUsage, UsageDelta: &model.TokenUsage{InputTokens: 4, OutputTokens: 2}},
					{Type: model.ChunkTypeStop, StopReason: "stop"},
				},
			},
		},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{
			ModelID:              "default",
			CaptureGenAIMessages: true,
		},
	)

	streamer, err := client.Stream(context.Background(), &model.Request{RunID: "run-1", Stream: true})
	require.NoError(t, err)
	for {
		_, err = streamer.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}

	attrs := tracer.singleSpan(t).attrsByKey()
	output := decodeGenAIMessages(t, attrs["gen_ai.output.messages"].AsString())
	require.Len(t, output, 1)
	parts := output[0]["parts"].([]any)
	require.Equal(t, map[string]any{"type": "text", "content": "hello"}, parts[0])
	require.Equal(t, "tool_call", parts[1].(map[string]any)["type"])
	require.NotContains(t, attrs["gen_ai.output.messages"].AsString(), "do not capture")
	require.Equal(t, int64(4), attrs["gen_ai.usage.input_tokens"].AsInt64())
	require.Equal(t, int64(2), attrs["gen_ai.usage.output_tokens"].AsInt64())
	require.Equal(t, []string{"stop"}, attrs["gen_ai.response.finish_reasons"].AsStringSlice())
	_, ok := attrs["gen_ai.response.time_to_first_chunk"]
	require.True(t, ok)
}

func TestWithCaptureGenAIMessagesConfiguresRuntime(t *testing.T) {
	rt := New(WithCaptureGenAIMessages(true))
	require.True(t, rt.captureGenAIMessages)
}

type modelTracingCompleteClient struct {
	response *model.Response
}

func (c *modelTracingCompleteClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return c.response, nil
}

func (c *modelTracingCompleteClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

type modelTracingStreamClient struct {
	streamer model.Streamer
}

func (c *modelTracingStreamClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, errors.New("unexpected complete")
}

func (c *modelTracingStreamClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return c.streamer, nil
}

type modelTracingScriptedStreamer struct {
	chunks []model.Chunk
	index  int
}

func (s *modelTracingScriptedStreamer) Recv() (model.Chunk, error) {
	if s.index >= len(s.chunks) {
		return model.Chunk{}, io.EOF
	}
	ch := s.chunks[s.index]
	s.index++
	return ch, nil
}

func (s *modelTracingScriptedStreamer) Close() error {
	return nil
}

func (s *modelTracingScriptedStreamer) Metadata() map[string]any {
	return nil
}

type modelTracingRecorder struct {
	mu    sync.Mutex
	spans []*modelTracingSpan
}

func (r *modelTracingRecorder) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, telemetry.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	span := &modelTracingSpan{name: name, attrs: cfg.Attributes()}
	r.mu.Lock()
	r.spans = append(r.spans, span)
	r.mu.Unlock()
	return ctx, span
}

func (r *modelTracingRecorder) Span(context.Context) telemetry.Span {
	return &modelTracingSpan{}
}

func (r *modelTracingRecorder) singleSpan(t *testing.T) *modelTracingSpan {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.spans, 1)
	return r.spans[0]
}

type modelTracingSpan struct {
	name   string
	attrs  []attribute.KeyValue
	events []modelTracingEvent
	code   codes.Code
	desc   string
	ended  bool
}

type modelTracingEvent struct {
	name  string
	attrs []attribute.KeyValue
}

func (s *modelTracingSpan) End(...trace.SpanEndOption) {
	s.ended = true
}

func (s *modelTracingSpan) AddEvent(name string, attrs ...any) {
	s.events = append(s.events, modelTracingEvent{name: name, attrs: modelTracingAttrsFromKV(attrs)})
}

func (s *modelTracingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *modelTracingSpan) SetStatus(code codes.Code, description string) {
	s.code = code
	s.desc = description
}

func (s *modelTracingSpan) RecordError(error, ...trace.EventOption) {}

func (s *modelTracingSpan) attrsByKey() map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(s.attrs))
	for _, attr := range s.attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func modelTracingAttrsFromKV(keyvals []any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(keyvals)/2)
	for i := 0; i+1 < len(keyvals); i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			continue
		}
		switch value := keyvals[i+1].(type) {
		case string:
			attrs = append(attrs, attribute.String(key, value))
		case int:
			attrs = append(attrs, attribute.Int(key, value))
		case int64:
			attrs = append(attrs, attribute.Int64(key, value))
		case bool:
			attrs = append(attrs, attribute.Bool(key, value))
		}
	}
	return attrs
}

func decodeGenAIMessages(t *testing.T, payload string) []map[string]any {
	t.Helper()
	var out []map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &out))
	return out
}
