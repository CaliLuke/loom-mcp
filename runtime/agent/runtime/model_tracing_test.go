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

func TestTracedClientCanonicalizesInternalToolMessages(t *testing.T) {
	t.Parallel()

	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingCompleteClient{response: &model.Response{
			ToolCalls: []model.ToolCall{{
				ID:      "internal-call",
				Name:    tools.ToolUnavailable,
				Payload: rawjson.Message(`{"available_tools":["private.output"]}`),
			}},
			StopReason: "tool_use",
		}},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{ModelID: "default", CaptureGenAIMessages: true},
	)
	request := &model.Request{
		Messages: []*model.Message{{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{model.ToolUsePart{
				ID:    "prior-internal",
				Name:  tools.ToolUnavailable.String(),
				Input: map[string]any{availableToolsKey: []string{"private.input"}},
			}},
		}},
		Tools: []*model.ToolDefinition{
			{Name: "svc.read"},
			{Name: tools.ToolUnavailable.String()},
		},
	}

	_, err := client.Complete(context.Background(), request)
	require.NoError(t, err)
	attrs := tracer.singleSpan(t).attrsByKey()
	require.NotContains(t, attrs["gen_ai.input.messages"].AsString(), "private.input")
	require.NotContains(t, attrs["gen_ai.output.messages"].AsString(), "private.output")
	require.Contains(t, attrs["gen_ai.input.messages"].AsString(), "svc.read")
	require.Contains(t, attrs["gen_ai.output.messages"].AsString(), "svc.read")
}

func TestTracedClientCapturesEffectivePolicyAndInterceptorRequest(t *testing.T) {
	t.Parallel()

	tracer := &modelTracingRecorder{}
	rt := New(
		WithTracer(tracer),
		WithCaptureGenAIMessages(true),
		WithInterceptors(RuntimeInterceptorFuncs{
			BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
				return &BeforeModelDecision{Request: &model.Request{
					Model: "effective-model",
					Messages: []*model.Message{{
						Role: model.ConversationRoleAssistant,
						Parts: []model.Part{model.ToolUsePart{
							ID:    "prior-internal",
							Name:  tools.ToolUnavailable.String(),
							Input: map[string]any{availableToolsKey: []string{"private.input"}},
						}},
					}},
					Tools: []*model.ToolDefinition{
						{Name: "allowed", InputSchema: map[string]any{"type": "object"}},
						{Name: "blocked", InputSchema: map[string]any{"type": "object"}},
					},
				}}, nil
			},
			AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
				input.Request.Messages[0].Parts[0] = model.ToolUsePart{
					ID:    "mutated-after-model",
					Name:  tools.ToolUnavailable.String(),
					Input: map[string]any{availableToolsKey: []string{"private.after_model"}},
				}
				return nil, nil
			},
		}),
	)
	rt.models["default"] = &modelTracingCompleteClient{response: &model.Response{
		ToolCalls: []model.ToolCall{{
			ID:      "internal-call",
			Name:    tools.ToolUnavailable,
			Payload: rawjson.Message(`{"available_tools":["private.output"]}`),
		}},
		StopReason: "tool_use",
	}}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-effective-trace",
		toolPolicy: toolPolicyEnvelope{
			Active:  true,
			Allowed: []tools.Ident{"allowed"},
		},
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	_, err := client.Complete(context.Background(), &model.Request{
		Model: "original-model",
		Tools: []*model.ToolDefinition{{
			Name:        "private.original",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)

	attrs := tracer.singleSpan(t).attrsByKey()
	require.Equal(t, "effective-model", attrs["gen_ai.request.model"].AsString())
	input := attrs["gen_ai.input.messages"].AsString()
	output := attrs["gen_ai.output.messages"].AsString()
	for _, captured := range []string{input, output} {
		require.Contains(t, captured, "allowed")
		require.NotContains(t, captured, "blocked")
		require.NotContains(t, captured, "private")
	}
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
	require.NoError(t, streamer.Finalize(nil))

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

func TestTracedStreamCanonicalizesInternalToolOutput(t *testing.T) {
	t.Parallel()

	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingStreamClient{streamer: &modelTracingScriptedStreamer{chunks: []model.Chunk{
			{
				Type: model.ChunkTypeToolCall,
				ToolCall: &model.ToolCall{
					ID:      "internal-call",
					Name:    tools.ToolUnavailable,
					Payload: rawjson.Message(`{"available_tools":["private.stream"]}`),
				},
			},
			{Type: model.ChunkTypeStop, StopReason: "tool_use"},
		}}},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{ModelID: "default", CaptureGenAIMessages: true},
	)
	streamer, err := client.Stream(context.Background(), &model.Request{
		Tools: []*model.ToolDefinition{
			{Name: "svc.read"},
			{Name: tools.ToolUnavailable.String()},
		},
	})
	require.NoError(t, err)
	for {
		_, err = streamer.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	require.NoError(t, streamer.Finalize(nil))

	output := tracer.singleSpan(t).attrsByKey()["gen_ai.output.messages"].AsString()
	require.NotContains(t, output, "private.stream")
	require.Contains(t, output, "svc.read")
}

func TestTracedStreamCommitsLifecycleOnlyAtFinalize(t *testing.T) {
	closeErr := errors.New("provider close failed")
	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingStreamClient{streamer: &modelTracingScriptedStreamer{closeErr: closeErr}},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{ModelID: "default"},
	)

	streamer, err := client.Stream(context.Background(), &model.Request{Stream: true})
	require.NoError(t, err)
	_, err = streamer.Recv()
	require.Equal(t, io.EOF, err)
	span := tracer.singleSpan(t)
	require.False(t, span.ended)

	err = streamer.Close()
	require.ErrorIs(t, err, closeErr)
	require.False(t, span.ended, "cleanup-only Close must not commit lifecycle state")

	err = streamer.Finalize(nil)
	require.ErrorIs(t, err, closeErr)
	require.True(t, span.ended)
	require.Equal(t, codes.Error, span.code)
}

func TestTracedStreamDoesNotCaptureRejectedOutput(t *testing.T) {
	t.Parallel()

	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolArguments,
		errors.New("private validation cause"),
		model.ResponseEvidence{Present: true, ByteCount: 14},
		nil,
	)
	require.NoError(t, err)
	tracer := &modelTracingRecorder{}
	client := newTracedClient(
		&modelTracingStreamClient{streamer: &modelTracingScriptedStreamer{
			chunks: []model.Chunk{
				{Type: model.ChunkTypeText, Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "private rejected output"}},
				}},
				{Type: model.ChunkTypeStop, StopReason: "stop"},
			},
			finalizeErr: rejected,
		}},
		tracer,
		telemetry.NoopLogger{},
		modelTraceConfig{ModelID: "default", CaptureGenAIMessages: true},
	)

	streamer, err := client.Stream(context.Background(), &model.Request{Stream: true})
	require.NoError(t, err)
	for {
		_, err = streamer.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	err = streamer.Finalize(nil)
	require.ErrorIs(t, err, rejected)

	attrs := tracer.singleSpan(t).attrsByKey()
	require.NotContains(t, attrs, "gen_ai.output.messages")
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

func (c *modelTracingCompleteClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

type modelTracingStreamClient struct {
	streamer model.ValidatedStreamer
}

func (c *modelTracingStreamClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, errors.New("unexpected complete")
}

func (c *modelTracingStreamClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return c.streamer, nil
}

type modelTracingScriptedStreamer struct {
	chunks      []model.Chunk
	index       int
	closeErr    error
	finalizeErr error
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
	return s.closeErr
}

func (s *modelTracingScriptedStreamer) Metadata() map[string]any {
	return nil
}

func (s *modelTracingScriptedStreamer) Response() *model.Response {
	return nil
}

func (s *modelTracingScriptedStreamer) Finalize(primaryErr error) error {
	return errors.Join(primaryErr, s.finalizeErr, s.Close())
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
