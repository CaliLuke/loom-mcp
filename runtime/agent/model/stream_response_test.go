package model

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamResponseBuilderReplacesProvisionalThinking(t *testing.T) {
	var builder StreamResponseBuilder
	require.NoError(t, builder.Add(ThinkingChunk{Message: Message{
		Role:  ConversationRoleAssistant,
		Parts: []Part{ThinkingPart{Text: "par", Index: 2}},
	}}))
	require.NoError(t, builder.Add(ThinkingChunk{Message: Message{
		Role:  ConversationRoleAssistant,
		Parts: []Part{ThinkingPart{Text: "partial", Signature: "sig", Index: 2, Final: true}},
	}}))
	require.NoError(t, builder.Add(StopChunk{Reason: "end_turn"}))

	response := builder.Response()
	require.NotNil(t, response)
	require.Len(t, response.Content, 1)
	assert.Equal(t, ThinkingPart{Text: "partial", Signature: "sig", Index: 2, Final: true}, response.Content[0].Parts[0])
}

func TestValidatedStreamRequiresProviderTerminalResponse(t *testing.T) {
	providerStream := &contractStream{
		chunks: []Chunk{
			TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "hello"}}}},
			StopChunk{Reason: "end_turn"},
		},
		omitResponse: true,
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStreamProtocol, validationErr.Kind())
	assert.Nil(t, stream.Response())
}

func TestValidatedStreamRejectsProviderResponseMismatch(t *testing.T) {
	providerStream := &contractStream{
		chunks: []Chunk{
			TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "observed"}}}},
			StopChunk{Reason: "end_turn"},
		},
		response: &Response{
			Content:    []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "different"}}}},
			StopReason: "end_turn",
		},
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationStreamProtocol, validationErr.Kind())
	assert.Nil(t, stream.Response())
}

func TestValidatedStreamReturnsProviderCanonicalResponse(t *testing.T) {
	citation := Citation{Title: "source"}
	providerResponse := &Response{
		Content: []Message{{
			Role: ConversationRoleAssistant,
			Parts: []Part{
				ThinkingPart{Text: "consider", Index: 0, Final: true},
				CitationsPart{Text: "answer", Citations: []Citation{citation}},
			},
			Meta: map[string]any{"provider_id": "response-1"},
		}},
		StopReason: "end_turn",
	}
	providerStream := &contractStream{
		chunks: []Chunk{
			ThinkingChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{ThinkingPart{Text: "consider", Index: 0}}}},
			TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{CitationsPart{Text: "answer", Citations: []Citation{citation}}}}},
			StopChunk{Reason: "end_turn"},
		},
		response: providerResponse,
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	require.Equal(t, io.EOF, err)
	response := stream.Response()
	require.NotNil(t, response)
	assert.Equal(t, "response-1", response.Content[0].Meta["provider_id"])
	assert.Equal(t, providerResponse.Content, response.Content)
	require.NoError(t, stream.Finalize(nil))
}

func TestValidatedStreamClassifiesOversizedTerminalResponse(t *testing.T) {
	providerStream := &contractStream{
		chunks: []Chunk{StopChunk{Reason: "end_turn"}},
		response: &Response{
			Content: []Message{{
				Role:  ConversationRoleAssistant,
				Parts: []Part{TextPart{Text: strings.Repeat("x", maxModelOutputBytes+1)}},
			}},
			StopReason: "end_turn",
		},
	}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationOutputBounds, validationErr.Kind())
}
