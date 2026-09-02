package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type contractProvider struct {
	response *Response
	err      error
	stream   Streamer
	request  *Request
}

func (p *contractProvider) Complete(_ context.Context, request *Request) (*Response, error) {
	p.request = request
	return p.response, p.err
}

func (p *contractProvider) Stream(_ context.Context, request *Request) (Streamer, error) {
	p.request = request
	return p.stream, p.err
}

type contractCountingProvider struct {
	*contractProvider
	count    TokenCount
	countErr error
}

func (p *contractCountingProvider) CountTokens(context.Context, *Request) (TokenCount, error) {
	return p.count, p.countErr
}

type contractStream struct {
	chunks   []Chunk
	index    int
	err      error
	closed   int
	closeErr error
}

func (s *contractStream) Recv() (Chunk, error) {
	if s.index < len(s.chunks) {
		chunk := s.chunks[s.index]
		s.index++
		return chunk, nil
	}
	if s.err != nil {
		err := s.err
		s.err = nil
		return nil, err
	}
	return nil, io.EOF
}

func (s *contractStream) Close() error {
	s.closed++
	return s.closeErr
}

func (s *contractStream) Metadata() map[string]any {
	return nil
}

func TestValidatedClientRejectsUnadvertisedToolWithoutRenderingIt(t *testing.T) {
	provider := &contractProvider{response: &Response{
		ToolCalls: []ToolCall{{
			Name:    tools.Ident("private_tool_name"),
			Payload: rawjson.Message(`{"secret":"value"}`),
			ID:      "call-secret",
		}},
		StopReason: "tool_use",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), contractRequest())
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationToolIdentity, validationErr.Kind())
	assert.Equal(t, "model output does not meet its request contract", err.Error())
	assert.NotContains(t, err.Error(), "private_tool_name")
	assert.NotContains(t, err.Error(), "secret")
	assert.True(t, validationErr.Evidence().Present)
}

func TestValidatedClientRejectsToolCatalogBeyondRecoveryBound(t *testing.T) {
	t.Parallel()

	provider := &contractProvider{response: &Response{}}
	client, err := NewClient(provider)
	require.NoError(t, err)
	request := &Request{Tools: make([]*ToolDefinition, MaxToolDefinitionsPerRequest+1)}
	for i := range request.Tools {
		request.Tools[i] = &ToolDefinition{
			Name:        fmt.Sprintf("tool_%d", i),
			InputSchema: map[string]any{"type": "object"},
		}
	}

	_, err = client.Complete(context.Background(), request)
	require.ErrorContains(t, err, "model request has 257 tools; limit is 256")
	require.Nil(t, provider.request)
}

func TestValidatedClientRejectsToolArgumentsOutsideSchema(t *testing.T) {
	provider := &contractProvider{response: &Response{
		ToolCalls: []ToolCall{{
			Name:    tools.Ident("lookup"),
			Payload: rawjson.Message(`{"query":42}`),
			ID:      "call-1",
		}},
		StopReason: "tool_use",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), contractRequest())
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationToolArguments, validationErr.Kind())
}

func TestValidatedClientRejectsOutputLimitedResponse(t *testing.T) {
	provider := &contractProvider{response: &Response{
		Content:       []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}}},
		StopReason:    "max_tokens",
		OutputLimited: true,
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), contractRequest())
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationOutputBounds, validationErr.Kind())
}

func TestValidatedClientRejectsStreamWithoutTerminalStop(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{TextChunk{
		Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "partial"}}},
	}}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStreamProtocol, validationErr.Kind())
}

func TestValidatedClientWithholdsToolCallsUntilTerminalAcceptance(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		ToolCallChunk{
			ToolCall: ToolCall{
				Name:    tools.Ident("lookup"),
				Payload: rawjson.Message(`{"query":"safe"}`),
				ID:      "call-1",
			},
		},
		StopChunk{Reason: "tool_use"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	assert.IsType(t, ToolCallChunk{}, chunk)
	chunk, err = stream.Recv()
	require.NoError(t, err)
	assert.IsType(t, StopChunk{}, chunk)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	response := stream.Response()
	require.NotNil(t, response)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, tools.Ident("lookup"), response.ToolCalls[0].Name)
	require.NoError(t, stream.Finalize(nil))
	require.NoError(t, stream.Finalize(nil))
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedClientDiscardsHeldToolCallOnPrematureEOF(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{ToolCallChunk{
		ToolCall: ToolCall{
			Name:    tools.Ident("lookup"),
			Payload: rawjson.Message(`{"query":"unsafe"}`),
			ID:      "call-1",
		},
	}}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), contractRequest())
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.Error(t, err)
	assert.Nil(t, chunk)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStreamProtocol, validationErr.Kind())
	assert.Nil(t, stream.Response())
}

func TestValidatedClientRejectsWrappedEOF(t *testing.T) {
	wrappedEOF := fmt.Errorf("provider wrapper: %w", io.EOF)
	providerStream := &contractStream{
		chunks: []Chunk{StopChunk{Reason: "end_turn"}},
		err:    wrappedEOF,
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, wrappedEOF, err)
	assert.Nil(t, stream.Response())
}

func TestValidatedStreamFinalizeJoinsCloseFailure(t *testing.T) {
	closeErr := errors.New("close failed")
	providerStream := &contractStream{closeErr: closeErr}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	finalizeErr := stream.Finalize(nil)
	require.Error(t, finalizeErr)
	require.ErrorIs(t, finalizeErr, closeErr)
	assert.Contains(t, finalizeErr.Error(), "not completely consumed")
	assert.Equal(t, finalizeErr, stream.Finalize(nil))
	assert.Equal(t, 1, providerStream.closed)
}

func TestValidatedClientAppliesApplicationCompletionValidator(t *testing.T) {
	request := structuredContractRequest(t)
	provider := &contractProvider{response: &Response{
		Content: []Message{{
			Role:  ConversationRoleAssistant,
			Parts: []Part{TextPart{Text: `{"value":"schema-valid"}`}},
		}},
		StopReason: "end_turn",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), request)
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStructuredOutput, validationErr.Kind())
}

func TestValidatedStreamAppliesApplicationCompletionValidatorBeforeExposure(t *testing.T) {
	request := structuredContractRequest(t)
	providerStream := &contractStream{chunks: []Chunk{
		CompletionChunk{
			Completion: Completion{
				Name:    "typed_result",
				Payload: rawjson.Message(`{"value":"schema-valid"}`),
			},
		},
		StopChunk{Reason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), request)
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.Error(t, err)
	assert.Nil(t, chunk)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStructuredOutput, validationErr.Kind())
}

func TestValidatedClientPreservesOptionalTokenCounting(t *testing.T) {
	client, err := NewClient(&contractCountingProvider{
		contractProvider: &contractProvider{},
		count:            TokenCount{InputTokens: 12, Exact: true},
	})
	require.NoError(t, err)
	counter, ok := client.(TokenCounter)
	require.True(t, ok)

	count, err := counter.CountTokens(context.Background(), contractRequest())
	require.NoError(t, err)
	assert.Equal(t, 12, count.InputTokens)

	plain, err := NewClient(&contractProvider{})
	require.NoError(t, err)
	_, ok = plain.(TokenCounter)
	assert.False(t, ok)
}

func TestRestoreOutputValidationErrorRejectsNestedClassification(t *testing.T) {
	original := newOutputValidationError(OutputValidationResponseShape, errors.New("invalid"), ResponseEvidence{Present: true}, nil)
	_, err := RestoreOutputValidationError(OutputValidationResponseShape, original, ResponseEvidence{Present: true}, nil)
	require.Error(t, err)
}

func TestValidatedClientOwnsRequestAndResponse(t *testing.T) {
	response := &Response{
		Content: []Message{{
			Role: ConversationRoleAssistant,
			Parts: []Part{
				CitationsPart{Text: "response", Citations: []Citation{{SourceContent: []string{"citation"}}}},
				ThinkingPart{Redacted: []byte("thinking")},
			},
			Meta: map[string]any{"nested": []string{"response"}},
		}},
		StopReason: "end_turn",
	}
	provider := &contractProvider{response: response}
	client, err := NewClient(provider)
	require.NoError(t, err)
	request := &Request{
		Messages: []*Message{{
			Role: ConversationRoleUser,
			Parts: []Part{
				ImagePart{Format: ImageFormatPNG, Bytes: []byte("request")},
			},
			Meta: map[string]any{"nested": []string{"request"}},
		}},
	}

	got, err := client.Complete(context.Background(), request)
	require.NoError(t, err)

	requestImage := request.Messages[0].Parts[0].(ImagePart)
	requestImage.Bytes[0] = 'X'
	request.Messages[0].Meta["nested"].([]string)[0] = "changed"
	providerImage := provider.request.Messages[0].Parts[0].(ImagePart)
	assert.Equal(t, "request", string(providerImage.Bytes))
	assert.Equal(t, "request", provider.request.Messages[0].Meta["nested"].([]string)[0])

	responseCitation := response.Content[0].Parts[0].(CitationsPart)
	responseCitation.Citations[0].SourceContent[0] = "changed"
	responseThinking := response.Content[0].Parts[1].(ThinkingPart)
	responseThinking.Redacted[0] = 'X'
	response.Content[0].Meta["nested"].([]string)[0] = "changed"
	gotCitation := got.Content[0].Parts[0].(CitationsPart)
	assert.Equal(t, "citation", gotCitation.Citations[0].SourceContent[0])
	gotThinking := got.Content[0].Parts[1].(ThinkingPart)
	assert.Equal(t, "thinking", string(gotThinking.Redacted))
	assert.Equal(t, "response", got.Content[0].Meta["nested"].([]string)[0])
}

func TestValidatedClientAcceptsAndOwnsPointerMessageParts(t *testing.T) {
	requestText := &TextPart{Text: "request"}
	responseText := &TextPart{Text: "response"}
	provider := &contractProvider{response: &Response{
		Content:    []Message{{Role: ConversationRoleAssistant, Parts: []Part{responseText}}},
		StopReason: "end_turn",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	response, err := client.Complete(context.Background(), &Request{Messages: []*Message{{
		Role:  ConversationRoleUser,
		Parts: []Part{requestText},
	}}})
	require.NoError(t, err)

	requestText.Text = "mutated request"
	responseText.Text = "mutated response"
	assert.Equal(t, TextPart{Text: "request"}, provider.request.Messages[0].Parts[0])
	assert.Equal(t, TextPart{Text: "response"}, response.Content[0].Parts[0])
}

func TestValidatedStreamLiveMessageCannotMutateCanonicalResponse(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{
		TextChunk{
			Message: Message{
				Role:  ConversationRoleAssistant,
				Parts: []Part{TextPart{Text: "original"}},
				Meta:  map[string]any{"source": "provider"},
			},
		},
		StopChunk{Reason: "end_turn"},
	}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	textChunk := chunk.(TextChunk)
	textChunk.Message.Parts[0] = TextPart{Text: "consumer mutation"}
	textChunk.Message.Meta["source"] = "consumer"
	drainValidatedStream(t, stream)

	response := stream.Response()
	require.NotNil(t, response)
	assert.Equal(t, TextPart{Text: "original"}, response.Content[0].Parts[0])
	assert.Equal(t, "provider", response.Content[0].Meta["source"])
	require.NoError(t, stream.Finalize(nil))
}

func TestValidatedClientRejectsOversizedOutput(t *testing.T) {
	provider := &contractProvider{response: &Response{
		Content: []Message{{
			Role:  ConversationRoleAssistant,
			Parts: []Part{TextPart{Text: string(make([]byte, maxModelOutputBytes+1))}},
		}},
		StopReason: "end_turn",
	}}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &Request{})
	require.Error(t, err)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationOutputBounds, validationErr.Kind())
}

func TestValidatedClientRejectsCyclicRequestMetadata(t *testing.T) {
	metadata := make(map[string]any)
	metadata["cycle"] = metadata
	client, err := NewClient(&contractProvider{})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &Request{
		Messages: []*Message{{Role: ConversationRoleUser, Meta: metadata}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cyclic value")
}

func TestValidatedClientRejectsMalformedProgrammaticPartsBeforeProviderWork(t *testing.T) {
	tests := []struct {
		name string
		part Part
	}{
		{name: "image without format", part: ImagePart{Bytes: []byte("image")}},
		{name: "document with conflicting sources", part: DocumentPart{Text: "text", URI: "https://example.com"}},
		{name: "tool use without identity", part: ToolUsePart{Input: map[string]any{}}},
		{name: "tool result without identity", part: ToolResultPart{Content: map[string]any{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &contractProvider{}
			client, err := NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), &Request{Messages: []*Message{{
				Role:  ConversationRoleUser,
				Parts: []Part{test.part},
			}}})
			require.Error(t, err)
			assert.Nil(t, provider.request)
		})
	}
}

func TestValidatedClientBoundsStructuredOutputNameBeforeProviderWork(t *testing.T) {
	provider := &contractProvider{}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &Request{StructuredOutput: &StructuredOutput{
		Name:   string(make([]byte, maxModelOutputBytes+1)),
		Schema: rawjson.Message(`{"type":"object"}`),
	}})
	require.Error(t, err)
	assert.Nil(t, provider.request)
}

func TestValidatedClientRejectsInvalidRequestConfigurationBeforeProviderWork(t *testing.T) {
	tests := []struct {
		name    string
		request *Request
	}{
		{name: "negative max tokens", request: &Request{MaxTokens: -1}},
		{name: "nan temperature", request: &Request{Temperature: float32(math.NaN())}},
		{name: "infinite temperature", request: &Request{Temperature: float32(math.Inf(1))}},
		{name: "negative thinking budget", request: &Request{Thinking: &ThinkingOptions{BudgetTokens: -1}}},
		{name: "invalid message role", request: &Request{Messages: []*Message{{Role: "tool"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &contractProvider{}
			client, err := NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), test.request)
			require.Error(t, err)
			assert.Nil(t, provider.request)
		})
	}
}

func TestValidatedClientRejectsExcessiveRequestCollectionBeforeProviderWork(t *testing.T) {
	provider := &contractProvider{}
	client, err := NewClient(provider)
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &Request{
		Messages: make([]*Message, maxModelValueVisits+1),
	})
	require.Error(t, err)
	assert.Nil(t, provider.request)
}

func contractRequest() *Request {
	return &Request{
		Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "Find it"}}}},
		Tools: []*ToolDefinition{{
			Name: "lookup",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"query"},
				"additionalProperties": false,
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		}},
	}
}

func structuredContractRequest(t *testing.T) *Request {
	t.Helper()
	request := &Request{
		StructuredOutput: &StructuredOutput{
			Name:   "typed_result",
			Schema: rawjson.Message(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}`),
		},
	}
	require.NoError(t, SetCompletionValidator(request, func(*Response, *Completion) error {
		return errors.New("generated decoder rejected the value")
	}))
	return request
}
