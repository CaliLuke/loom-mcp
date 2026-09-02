package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestValidatedStreamRequiresConsumerEOFBeforeResponseOrSuccessfulFinalize(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeToolCall, ToolCall: &ToolCall{Name: tools.Ident("lookup"), ID: "call-1", Payload: rawjson.Message(`{"query":"one"}`)}},
		{Type: ChunkTypeToolCall, ToolCall: &ToolCall{Name: tools.Ident("lookup"), ID: "call-2", Payload: rawjson.Message(`{"query":"two"}`)}},
		{Type: ChunkTypeUsage, UsageDelta: &TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{Type: ChunkTypeStop, StopReason: "tool_use"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "call-1", chunk.ToolCall.ID)
	assert.Nil(t, stream.Response())

	err = stream.Finalize(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not completely consumed")
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedStreamPublishesCanonicalResponseOnlyAfterConsumerEOF(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}},
		{Type: ChunkTypeUsage, UsageDelta: &TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{Type: ChunkTypeStop, StopReason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	for index := 0; index < 3; index++ {
		_, err = stream.Recv()
		require.NoError(t, err)
		assert.Nil(t, stream.Response())
	}
	_, err = stream.Recv()
	require.Equal(t, io.EOF, err)
	response := stream.Response()
	require.NotNil(t, response)
	assert.Equal(t, TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, response.Usage)
	require.NoError(t, stream.Finalize(nil))
}

func TestValidatedStreamRejectsCancellationBeforeTerminalAcceptanceAndJoinsCleanup(t *testing.T) {
	closeErr := errors.New("provider close failed")
	providerStream := &contractStream{
		chunks: []Chunk{
			{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}}},
			{Type: ChunkTypeStop, StopReason: "end_turn"},
		},
		closeErr: closeErr,
	}
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(ctx, &Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)
	cancel()
	_, err = stream.Recv()
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, stream.Response())

	finalizeErr := stream.Finalize(err)
	require.ErrorIs(t, finalizeErr, context.Canceled)
	require.ErrorIs(t, finalizeErr, closeErr)
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedStreamFirstFinalizationIsAuthoritative(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}},
		{Type: ChunkTypeStop, StopReason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)
	drainValidatedStream(t, stream)

	first := stream.Finalize(nil)
	second := stream.Finalize(errors.New("different primary"))
	require.NoError(t, first)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedStructuredStreamClassifiesOutputLimitBeforeMissingCompletion(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{{
		Type:          ChunkTypeStop,
		StopReason:    "max_tokens",
		OutputLimited: true,
	}}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), structuredContractRequest(t))
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationOutputBounds, validationErr.Kind())
}

func TestValidatedStreamClassifiesOutputLimitBeforeUnfinishedToolDelta(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeToolCallDelta, ToolCallDelta: &ToolCallDelta{Name: tools.Ident("lookup"), ID: "call-1", Delta: `{"query":"truncated`}},
		{Type: ChunkTypeStop, StopReason: "max_tokens", OutputLimited: true},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationOutputBounds, validationErr.Kind())
}

func TestValidatedStreamReconcilesToolDeltasAndUsage(t *testing.T) {
	request := contractRequest()
	request.ToolChoice = &ToolChoice{Mode: ToolChoiceModeAny}
	usage := TokenUsage{Model: "provider-model", InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeToolCallDelta, ToolCallDelta: &ToolCallDelta{Name: tools.Ident("lookup"), ID: "call-1", Delta: `{"query":`}},
		{Type: ChunkTypeToolCallDelta, ToolCallDelta: &ToolCallDelta{Name: tools.Ident("lookup"), ID: "call-1", Delta: `"one"}`}},
		{Type: ChunkTypeToolCall, ToolCall: &ToolCall{Name: tools.Ident("lookup"), ID: "call-1", Payload: rawjson.Message(`{"query":"one"}`)}},
		{Type: ChunkTypeUsage, UsageDelta: &usage},
		{Type: ChunkTypeStop, StopReason: "tool_use"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), request)
	require.NoError(t, err)

	drainValidatedStream(t, stream)
	response := stream.Response()
	require.NotNil(t, response)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, "call-1", response.ToolCalls[0].ID)
	assert.Equal(t, usage, response.Usage)
	require.NoError(t, stream.Finalize(nil))
}

func TestValidatedStructuredStreamAcceptsDeltasAndCanonicalCompletion(t *testing.T) {
	request := &Request{StructuredOutput: &StructuredOutput{
		Name:   "result",
		Schema: rawjson.Message(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}`),
	}}
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeCompletionDelta, CompletionDelta: &CompletionDelta{Name: "result", Delta: `{"value":`}},
		{Type: ChunkTypeCompletionDelta, CompletionDelta: &CompletionDelta{Name: "result", Delta: `"done"}`}},
		{Type: ChunkTypeCompletion, Completion: &Completion{Name: "result", Payload: rawjson.Message(`{"value":"done"}`)}},
		{Type: ChunkTypeStop, StopReason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), request)
	require.NoError(t, err)
	drainValidatedStream(t, stream)

	response := stream.Response()
	require.NotNil(t, response)
	require.Len(t, response.Content, 1)
	require.JSONEq(t, `{"value":"done"}`, response.Content[0].Parts[0].(TextPart).Text)
	require.NoError(t, stream.Finalize(nil))
}

func TestValidatedStructuredResponseRejectsContentAfterEmptyPart(t *testing.T) {
	tests := []struct {
		name  string
		first Part
	}{
		{name: "text", first: TextPart{}},
		{name: "citations", first: CitationsPart{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &contractProvider{response: &Response{
				Content: []Message{{
					Role:  ConversationRoleAssistant,
					Parts: []Part{test.first, TextPart{Text: `{"value":"done"}`}},
				}},
				StopReason: "end_turn",
			}}
			client, err := NewClient(provider)
			require.NoError(t, err)

			_, err = client.Complete(context.Background(), &Request{StructuredOutput: &StructuredOutput{
				Name:   "result",
				Schema: rawjson.Message(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}`),
			}})
			require.Error(t, err)
			var validationErr *OutputValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Equal(t, OutputValidationStructuredOutput, validationErr.Kind())
			require.Error(t, validationErr.Unwrap())
			assert.Contains(t, validationErr.Unwrap().Error(), "multiple content parts")
		})
	}
}

func TestRequestContractEnforcesEveryToolChoiceMode(t *testing.T) {
	tools := []*ToolDefinition{
		{Name: "first", InputSchema: map[string]any{"type": "object"}},
		{Name: "second", InputSchema: map[string]any{"type": "object"}},
	}
	tests := []struct {
		name     string
		choice   *ToolChoice
		calls    []ToolCall
		content  []Message
		wantKind OutputValidationKind
	}{
		{name: "auto permits text", choice: &ToolChoice{Mode: ToolChoiceModeAuto}, content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}}},
		{name: "none rejects call", choice: &ToolChoice{Mode: ToolChoiceModeNone}, calls: []ToolCall{{Name: "first", Payload: rawjson.Message(`{}`)}}, wantKind: OutputValidationToolChoice},
		{name: "any requires call", choice: &ToolChoice{Mode: ToolChoiceModeAny}, content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}}, wantKind: OutputValidationToolChoice},
		{name: "forced rejects other", choice: &ToolChoice{Mode: ToolChoiceModeTool, Name: "first"}, calls: []ToolCall{{Name: "second", Payload: rawjson.Message(`{}`)}}, wantKind: OutputValidationToolChoice},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(&contractProvider{response: &Response{
				Content: test.content, ToolCalls: test.calls, StopReason: "end_turn",
			}})
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), &Request{Tools: tools, ToolChoice: test.choice})
			if test.wantKind == "" {
				require.NoError(t, err)
				return
			}
			var validationErr *OutputValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Equal(t, test.wantKind, validationErr.Kind())
		})
	}
}

func TestRequestContractRejectsInvalidToolChoiceRequests(t *testing.T) {
	tests := []struct {
		name    string
		request *Request
	}{
		{name: "none with name", request: &Request{ToolChoice: &ToolChoice{Mode: ToolChoiceModeNone, Name: "tool"}}},
		{name: "any without tools", request: &Request{ToolChoice: &ToolChoice{Mode: ToolChoiceModeAny}}},
		{name: "forced without name", request: &Request{Tools: []*ToolDefinition{{Name: "tool", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: &ToolChoice{Mode: ToolChoiceModeTool}}},
		{name: "forced unadvertised", request: &Request{Tools: []*ToolDefinition{{Name: "tool", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: &ToolChoice{Mode: ToolChoiceModeTool, Name: "other"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRequestContract(test.request)
			require.Error(t, err)
		})
	}
}

func TestValidatedStreamRejectsPostStopOutput(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeStop, StopReason: "end_turn"},
		{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "late"}}}},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStreamProtocol, validationErr.Kind())
}

func TestValidatedClientRejectsInvalidUsageAndIdentity(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
	}{
		{name: "inconsistent total", usage: TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 6}},
		{name: "wrong model", usage: TokenUsage{Model: "other"}},
		{name: "wrong model class", usage: TokenUsage{ModelClass: ModelClassSmall}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &contractProvider{response: &Response{
				Content:    []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}},
				Usage:      test.usage,
				StopReason: "end_turn",
			}}
			client, err := NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), &Request{Model: "expected", ModelClass: ModelClassDefault})
			require.Error(t, err)
			var validationErr *OutputValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Equal(t, OutputValidationUsage, validationErr.Kind())
		})
	}
}

func TestValidatedClientRejectsInvalidResponseShapes(t *testing.T) {
	tests := []struct {
		name     string
		response *Response
		wantKind OutputValidationKind
	}{
		{name: "nil response", wantKind: OutputValidationResponseShape},
		{
			name: "missing stop reason",
			response: &Response{Content: []Message{{
				Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}},
			}}},
			wantKind: OutputValidationResponseShape,
		},
		{
			name:     "missing output",
			response: &Response{StopReason: "end_turn"},
			wantKind: OutputValidationResponseShape,
		},
		{
			name: "invalid response role",
			response: &Response{Content: []Message{{
				Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "done"}},
			}}, StopReason: "end_turn"},
			wantKind: OutputValidationResponseShape,
		},
		{
			name: "unsupported response part",
			response: &Response{Content: []Message{{
				Role: ConversationRoleAssistant, Parts: []Part{CacheCheckpointPart{}},
			}}, StopReason: "end_turn"},
			wantKind: OutputValidationResponseShape,
		},
		{
			name: "duplicate tool call identifiers",
			response: &Response{ToolCalls: []ToolCall{
				{Name: tools.Ident("lookup"), ID: "duplicate", Payload: rawjson.Message(`{"query":"one"}`)},
				{Name: tools.Ident("lookup"), ID: "duplicate", Payload: rawjson.Message(`{"query":"two"}`)},
			}, StopReason: "tool_use"},
			wantKind: OutputValidationToolIdentity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(&contractProvider{response: test.response})
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), contractRequest())
			require.Error(t, err)
			var validationErr *OutputValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Equal(t, test.wantKind, validationErr.Kind())
		})
	}
}

func TestResponseEvidenceFingerprintIsDeterministic(t *testing.T) {
	left := &Response{
		Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "same"}}, Meta: map[string]any{"a": 1, "b": 2}}},
	}
	right := &Response{
		Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "same"}}, Meta: map[string]any{"b": 2, "a": 1}}},
	}
	assert.Equal(t, responseEvidence(left), responseEvidence(right))
}

func TestRejectedStreamCarriesBoundedEvidence(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{{
		Type: ChunkTypeToolCall,
		ToolCall: &ToolCall{
			Name:    tools.Ident("unadvertised"),
			Payload: rawjson.Message(`{}`),
		},
	}}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	evidence := validationErr.Evidence()
	assert.True(t, evidence.Present)
	assert.Positive(t, evidence.ByteCount)
	assert.NotEqual(t, [32]byte{}, evidence.Fingerprint)
}

func TestValidatedClientCanonicalizesInvocationMode(t *testing.T) {
	provider := &contractProvider{response: &Response{
		Content:    []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}},
		StopReason: "end_turn",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), &Request{Stream: true})
	require.NoError(t, err)
	assert.False(t, provider.request.Stream)

	provider.stream = &contractStream{}
	stream, err := client.Stream(context.Background(), &Request{Stream: false})
	require.NoError(t, err)
	assert.True(t, provider.request.Stream)
	require.Error(t, stream.Finalize(errors.New("abort")))
}

func TestValidatedClientRejectsExcessiveDynamicDepthBeforeProviderWork(t *testing.T) {
	value := any("leaf")
	for range maxModelValueDepth + 2 {
		value = map[string]any{"nested": value}
	}
	provider := &contractProvider{}
	client, err := NewClient(provider)
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), &Request{Messages: []*Message{{
		Role: ConversationRoleUser,
		Meta: map[string]any{"root": value},
	}}})
	require.Error(t, err)
	assert.Nil(t, provider.request)
}

func TestValidatedStreamConcurrentFinalizationReturnsOneAuthoritativeResult(t *testing.T) {
	closeErr := errors.New("close failed")
	providerStream := &contractStream{
		chunks: []Chunk{
			{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "done"}}}},
			{Type: ChunkTypeStop, StopReason: "end_turn"},
		},
		closeErr: closeErr,
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)
	drainValidatedStream(t, stream)

	const callers = 32
	results := make(chan error, callers)
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- stream.Finalize(fmt.Errorf("primary-%d", index))
		}()
	}
	group.Wait()
	close(results)
	var first error
	for result := range results {
		if first == nil {
			first = result
		}
		assert.Equal(t, first, result)
		require.ErrorIs(t, result, closeErr)
	}
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedStreamEarlyFinalizationIsTerminal(t *testing.T) {
	processErr := errors.New("consumer processing failed")
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}}},
		{Type: ChunkTypeStop, StopReason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)

	finalizeErr := stream.Finalize(processErr)
	require.ErrorIs(t, finalizeErr, processErr)
	_, recvErr := stream.Recv()
	assert.Equal(t, finalizeErr, recvErr)
	assert.Nil(t, stream.Response())
	assert.Equal(t, 1, providerStream.index)
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedStreamJoinsCancellationWithProviderAndCleanupFailures(t *testing.T) {
	providerErr := errors.New("provider receive failed")
	closeErr := errors.New("provider close failed")
	providerStream := &contractStream{
		chunks:   []Chunk{{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}}}},
		err:      providerErr,
		closeErr: closeErr,
	}
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(ctx, &Request{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	cancel()
	_, err = stream.Recv()
	require.ErrorIs(t, err, providerErr)
	require.ErrorIs(t, err, context.Canceled)

	finalizeErr := stream.Finalize(err)
	require.ErrorIs(t, finalizeErr, providerErr)
	require.ErrorIs(t, finalizeErr, context.Canceled)
	require.ErrorIs(t, finalizeErr, closeErr)
}

func TestValidatedStreamJoinsCancellationWithEarlyProcessingFailure(t *testing.T) {
	processErr := errors.New("consumer processing failed")
	providerStream := &contractStream{chunks: []Chunk{
		{Type: ChunkTypeText, Message: &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}}},
		{Type: ChunkTypeStop, StopReason: "end_turn"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(ctx, &Request{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	cancel()

	finalizeErr := stream.Finalize(processErr)
	require.ErrorIs(t, finalizeErr, processErr)
	require.ErrorIs(t, finalizeErr, context.Canceled)
}

func drainValidatedStream(t *testing.T, stream ValidatedStreamer) {
	t.Helper()
	for {
		_, err := stream.Recv()
		//nolint:errorlint // Only literal EOF proves validated completion.
		if err == io.EOF {
			return
		}
		require.NoError(t, err)
	}
}
