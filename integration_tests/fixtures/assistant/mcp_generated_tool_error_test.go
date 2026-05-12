package assistantapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolErrorAssistant struct {
	assistant.Service
	err error
}

func (s toolErrorAssistant) AnalyzeSentiment(ctx context.Context, p *assistant.AnalyzeSentimentPayload) (res *assistant.AnalyzeSentimentResult, err error) {
	return nil, s.err
}

type capturedToolsCallStream struct {
	events          []*mcpassistant.ToolsCallResult
	sendErrorCalled bool
}

func (s *capturedToolsCallStream) Send(ctx context.Context, ev mcpassistant.ToolsCallEvent) error {
	result, ok := ev.(*mcpassistant.ToolsCallResult)
	if !ok {
		return fmt.Errorf("unexpected tools/call event %T", ev)
	}
	s.events = append(s.events, result)
	return nil
}

func (s *capturedToolsCallStream) SendAndClose(ctx context.Context, ev mcpassistant.ToolsCallEvent) error {
	return s.Send(ctx, ev)
}

func (s *capturedToolsCallStream) SendError(ctx context.Context, method string, err error) error {
	s.sendErrorCalled = true
	return nil
}

func TestGeneratedAdapterToolsCallReturnsToolErrorResultForServiceErrors(t *testing.T) {
	t.Parallel()

	serviceErr := loom.WithErrorRemedy(
		loom.PermanentError("analyze_sentiment_failed", "backend detail that should not leak"),
		&loom.ErrorRemedy{
			Code:        "assistant.sentiment.invalid",
			SafeMessage: "Sentiment input is invalid.",
			RetryHint:   "Retry with a non-empty text payload.",
		},
	)
	adapter := mcpassistant.NewMCPAdapter(toolErrorAssistant{
		Service: NewAssistant(),
		err:     serviceErr,
	}, promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-error-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"boom"}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled, "tool failures must not escape as transport errors")
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Equal(t,
		"[assistant.sentiment.invalid] Sentiment input is invalid.\nRecovery: Retry with a non-empty text payload.",
		*stream.events[0].Content[0].Text,
	)
}

func TestGeneratedAdapterToolsCallReturnsToolErrorResultForInvalidPayload(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-invalid-payload-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled, "invalid payload must not escape as transport error")
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Equal(t,
		"[invalid_params] Missing required field: text\nRecovery: Include required field \"text\". Example: {\"text\":\"example\"}",
		*stream.events[0].Content[0].Text,
	)
}

func TestGeneratedAdapterToolsCallReturnsSpecificRecoveryForNestedActionPayloads(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-action-payload-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "dispatch_action",
		Arguments: json.RawMessage(`{"request":{"action":"list"}}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled, "invalid payload must not escape as transport error")
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Equal(t,
		"[invalid_params] Missing required field: value\nRecovery: Include required field \"value\". Example: {\"request\":{\"action\":\"list\",\"value\":{}}}",
		*stream.events[0].Content[0].Text,
	)
}

func TestGeneratedAdapterToolsCallSuggestsValidTagOnInvalidDiscriminator(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-invalid-action-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "dispatch_action",
		Arguments: json.RawMessage(`{"request":{"action":"GetActive","value":{}}}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled, "invalid payload must not escape as transport error")
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Equal(t,
		"[invalid_params] invalid value for \"action\": got \"GetActive\", expected one of \"list\", \"create\"\nRecovery: Field \"request\" must use one of \"list\", \"create\" for \"action\". Example: {\"request\":{\"action\":\"list\",\"value\":{}}}",
		*stream.events[0].Content[0].Text,
	)
}

func TestGeneratedAdapterToolsCallBranchKeyAwareRecovery(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-branch-key-recovery-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "dispatch_command",
		Arguments: json.RawMessage(`{"command":{"action":"NotARealTag","args":{}}}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled, "invalid payload must not escape as transport error")
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.NotNil(t, stream.events[0].Content[0].Text)
	text := *stream.events[0].Content[0].Text
	_, recovery, ok := strings.Cut(text, "\nRecovery: ")
	require.True(t, ok, "response must include Recovery prefix, got %q", text)
	assert.Contains(t, recovery, `"args"`, "recovery must reference configured branch key")
	assert.True(t,
		strings.Contains(recovery, `"foo"`) || strings.Contains(recovery, `"bar"`),
		"recovery must suggest a declared tag, got %q", recovery,
	)
	assert.NotContains(t, recovery, `"NotARealTag"`, "recovery must not echo the invalid caller value")
	assert.NotContains(t, recovery, `"value":{}`, "recovery must not contain the hardcoded value branch key")
}

func TestGeneratedAdapterToolsCallReusesCallerTagWhenInnerBranchIsMalformed(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, nil)

	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-caller-tag-reuse-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream := &capturedToolsCallStream{}
	// Caller's tag ("create") is valid but the inner branch payload "value"
	// is missing from the envelope. The recovery hint must preserve "create"
	// rather than rewriting it to the first-declared tag, and must include
	// the branch's required inner fields in the example.
	err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "dispatch_action",
		Arguments: json.RawMessage(`{"request":{"action":"create"}}`),
	}, stream)
	require.NoError(t, err)

	require.False(t, stream.sendErrorCalled)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	text := *stream.events[0].Content[0].Text
	_, recovery, ok := strings.Cut(text, "\nRecovery: ")
	require.True(t, ok, "response must include Recovery prefix, got %q", text)
	assert.Contains(t, recovery, `"action":"create"`,
		"recovery must keep caller's valid tag, got %q", recovery)
	assert.NotContains(t, recovery, `"action":"list"`,
		"recovery must not rewrite caller's valid tag to a different declared tag")
	assert.Contains(t, recovery, `"name":"example"`,
		"recovery example for the create branch must populate its required inner fields, got %q", recovery)
}
