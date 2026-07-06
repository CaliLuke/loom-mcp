package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
)

type stubMessagesClient struct {
	lastParams sdk.MessageNewParams
	resp       *sdk.Message
	err        error

	stream *ssestream.Stream[sdk.MessageStreamEventUnion]
}

func (s *stubMessagesClient) New(_ context.Context, body sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
	s.lastParams = body
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
			InputTokens:  10,
			OutputTokens: 5,
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
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
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
				InputSchema: json.RawMessage(`{"type":"object"}`),
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
				Input: json.RawMessage(`{"x":1}`),
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
