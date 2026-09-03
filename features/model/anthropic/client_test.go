package anthropic

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

type stubMessagesClient struct {
	lastParams sdk.MessageNewParams
	resp       *sdk.Message
	err        error

	stream  *ssestream.Stream[sdk.MessageStreamEventUnion]
	newFunc func(context.Context) (*sdk.Message, error)
}

func (s *stubMessagesClient) New(ctx context.Context, body sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
	s.lastParams = body
	if s.newFunc != nil {
		return s.newFunc(ctx)
	}
	return s.resp, s.err
}

func (s *stubMessagesClient) NewStreaming(_ context.Context, body sdk.MessageNewParams, _ ...option.RequestOption) *ssestream.Stream[sdk.MessageStreamEventUnion] {
	s.lastParams = body
	if s.stream == nil {
		dec := &noopDecoder{}
		s.stream = ssestream.NewStream[sdk.MessageStreamEventUnion](dec, nil)
	}
	return s.stream
}

type noopDecoder struct{}

func (n *noopDecoder) Event() ssestream.Event { return ssestream.Event{} }
func (n *noopDecoder) Next() bool             { return false }
func (n *noopDecoder) Close() error           { return nil }
func (n *noopDecoder) Err() error             { return nil }

func TestComplete_TextOnly(t *testing.T) {
	stub := &stubMessagesClient{}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    128,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
	}

	stub.resp = &sdk.Message{
		Content: []sdk.ContentBlockUnion{
			{
				Type: "text",
				Text: "world",
			},
		},
		StopReason: sdk.StopReasonEndTurn,
		Usage: sdk.Usage{
			InputTokens:              10,
			OutputTokens:             5,
			CacheReadInputTokens:     3,
			CacheCreationInputTokens: 4,
		},
	}

	resp, err := cl.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content message, got %d", len(resp.Content))
	}
	if got := resp.Content[0].Parts[0].(model.TextPart).Text; got != "world" {
		t.Fatalf("unexpected text %q", got)
	}
	if resp.StopReason != string(sdk.StopReasonEndTurn) {
		t.Fatalf("unexpected stop reason %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 || resp.Usage.TotalTokens != 22 ||
		resp.Usage.CacheReadTokens != 3 || resp.Usage.CacheWriteTokens != 4 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestPrepareRequestAppliesCachePolicies(t *testing.T) {
	client, err := New(&stubMessagesClient{}, Options{DefaultModel: "claude-sonnet", MaxTokens: 128})
	require.NoError(t, err)

	params, _, _, err := client.prepareRequest(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "first"}, model.TextPart{Text: "second"}}},
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
		},
		Tools: []*model.ToolDefinition{
			{Name: "test.first", Description: "first tool", InputSchema: jsontext.Value(`{"type":"object"}`)},
			{Name: "test.second", Description: "second tool", InputSchema: jsontext.Value(`{"type":"object"}`)},
		},
		Cache: &model.CacheOptions{AfterSystem: true, AfterTools: true},
	})
	require.NoError(t, err)
	require.Len(t, params.System, 2)
	require.Empty(t, params.System[0].CacheControl.Type)
	require.Equal(t, "ephemeral", string(params.System[1].CacheControl.Type))
	require.Len(t, params.Tools, 2)
	require.Empty(t, params.Tools[0].GetCacheControl().Type)
	require.Equal(t, "ephemeral", string(params.Tools[1].GetCacheControl().Type))
}

func TestPrepareRequestCachePoliciesIgnoreAbsentSections(t *testing.T) {
	client, err := New(&stubMessagesClient{}, Options{DefaultModel: "claude-sonnet", MaxTokens: 128})
	require.NoError(t, err)

	params, _, _, err := client.prepareRequest(context.Background(), &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}},
		Cache:    &model.CacheOptions{AfterSystem: true, AfterTools: true},
	})

	require.NoError(t, err)
	require.Empty(t, params.System)
	require.Empty(t, params.Tools)
}

func TestComplete_ThinkingBlocks(t *testing.T) {
	stub := &stubMessagesClient{}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    128,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stub.resp = &sdk.Message{
		Content: []sdk.ContentBlockUnion{
			mustContentBlock(t, `{"type":"thinking","thinking":"private reasoning","signature":"sig"}`),
			mustContentBlock(t, `{"type":"redacted_thinking","data":"opaque-redacted"}`),
			mustContentBlock(t, `{"type":"text","text":"done"}`),
		},
	}

	resp, err := cl.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content message count = %d, want 1", len(resp.Content))
	}
	if len(resp.Content[0].Parts) != 3 {
		t.Fatalf("content part count = %d, want 3", len(resp.Content[0].Parts))
	}
	thinking, ok := resp.Content[0].Parts[0].(model.ThinkingPart)
	if !ok {
		t.Fatalf("first content part = %T, want ThinkingPart", resp.Content[0].Parts[0])
	}
	if thinking.Text != "private reasoning" || thinking.Signature != "sig" || !thinking.Final {
		t.Fatalf("signed thinking = %+v, want text/signature/final", thinking)
	}
	redacted, ok := resp.Content[0].Parts[1].(model.ThinkingPart)
	if !ok {
		t.Fatalf("second content part = %T, want ThinkingPart", resp.Content[0].Parts[1])
	}
	if string(redacted.Redacted) != "opaque-redacted" || !redacted.Final {
		t.Fatalf("redacted thinking = %+v, want redacted/final", redacted)
	}
	text, ok := resp.Content[0].Parts[2].(model.TextPart)
	if !ok {
		t.Fatalf("third content part = %T, want TextPart", resp.Content[0].Parts[2])
	}
	if text.Text != "done" {
		t.Fatalf("text = %q, want done", text.Text)
	}
}

func TestComplete_ToolUse(t *testing.T) {
	stub := &stubMessagesClient{}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    128,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "call tool"},
				},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "test.tool",
				Description: "test tool",
				InputSchema: jsontext.Value(`{"type":"object"}`),
			},
		},
	}

	tools, canon, prov, err := encodeTools(context.Background(), req.Tools)
	if err != nil {
		t.Fatalf("encodeTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 encoded tool, got %d", len(tools))
	}
	if len(canon) != 1 || len(prov) != 1 {
		t.Fatalf("expected name maps, got canon=%v prov=%v", canon, prov)
	}

	sanitized := canon["test.tool"]
	if sanitized == "" {
		t.Fatalf("sanitizeToolName returned empty")
	}

	stub.resp = &sdk.Message{
		Content: []sdk.ContentBlockUnion{
			{
				Type:  "tool_use",
				Name:  sanitized,
				ID:    "tool-1",
				Input: jsontext.Value(`{"x":1}`),
			},
		},
		StopReason: sdk.StopReasonToolUse,
	}

	resp, err := cl.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if string(call.Name) != "test.tool" {
		t.Fatalf("unexpected tool name %q", call.Name)
	}
	if call.ID != "tool-1" {
		t.Fatalf("unexpected tool ID %q", call.ID)
	}
	if string(call.Payload) != `{"x":1}` {
		t.Fatalf("unexpected payload %s", string(call.Payload))
	}
}

func mustContentBlock(t *testing.T, raw string) sdk.ContentBlockUnion {
	t.Helper()
	var block sdk.ContentBlockUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &block))
	return block
}

func TestComplete_RateLimited(t *testing.T) {
	stub := &stubMessagesClient{
		err: model.ErrRateLimited,
	}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hi"},
				},
			},
		},
	}

	_, err = cl.Complete(context.Background(), req)
	if !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestComplete_RateLimitedAPIError(t *testing.T) {
	stub := &stubMessagesClient{
		err: newAnthropicRateLimitError(t),
	}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = cl.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hi"},
				},
			},
		},
	})
	if !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestStream_RateLimitedAPIError(t *testing.T) {
	stub := &stubMessagesClient{
		stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&noopDecoder{}, newAnthropicRateLimitError(t)),
	}
	cl, err := New(stub, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = cl.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hi"},
				},
			},
		},
	})
	if !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestComplete_SendsTemperatureWhenClaudeSupportsIt(t *testing.T) {
	stub := &stubMessagesClient{resp: &sdk.Message{}}
	cl, err := New(stub, Options{
		DefaultModel: "claude-sonnet-4-5-20250929",
		MaxTokens:    64,
		Temperature:  0.7,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = cl.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !stub.lastParams.Temperature.Valid() {
		t.Fatal("expected temperature to be sent")
	}
	if stub.lastParams.Temperature.Value != 0.7 {
		t.Fatalf("unexpected temperature %v", stub.lastParams.Temperature.Value)
	}
}

func TestComplete_OmitsTemperatureWhenClaudeRejectsIt(t *testing.T) {
	stub := &stubMessagesClient{resp: &sdk.Message{}}
	cl, err := New(stub, Options{
		DefaultModel: "claude-sonnet-5",
		MaxTokens:    64,
		Temperature:  0.7,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "test")

	_, err = cl.Complete(ctx, &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hi"}}},
		},
	})
	span.End()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if stub.lastParams.Temperature.Valid() {
		t.Fatalf("expected temperature to be omitted, got %v", stub.lastParams.Temperature.Value)
	}
	attrs := spanAttrMap(spanRecorder.Ended()[0].Attributes())
	require.InEpsilon(t, 0.7, attrs[telemetry.AttrGenAIRequestTemperature].AsFloat64(), 0.0001)
	require.True(t, attrs[telemetry.AttrGenAIRequestTemperatureOmitted].AsBool())
	require.Equal(t, "claude-sonnet-5", attrs[telemetry.AttrGenAIRequestModel].AsString())
}

func spanAttrMap(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	values := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	return values
}

func TestComplete_RejectsStructuredOutput(t *testing.T) {
	cl, err := New(&stubMessagesClient{}, Options{
		DefaultModel: "claude-3.5-sonnet",
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = cl.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hi"}}},
		},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object"}`),
		},
	})
	if !errors.Is(err, model.ErrStructuredOutputUnsupported) {
		t.Fatalf("expected ErrStructuredOutputUnsupported, got %v", err)
	}
}

func newAnthropicRateLimitError(t *testing.T) *sdk.Error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	require.NoError(t, err)
	return &sdk.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    req,
		Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Request:    req,
		},
	}
}
