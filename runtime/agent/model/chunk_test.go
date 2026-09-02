package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestChunkVariantsExposeStableKinds(t *testing.T) {
	tests := []struct {
		chunk Chunk
		kind  string
	}{
		{chunk: TextChunk{}, kind: ChunkTypeText},
		{chunk: ThinkingChunk{}, kind: ChunkTypeThinking},
		{chunk: ToolCallChunk{}, kind: ChunkTypeToolCall},
		{chunk: ToolCallDeltaChunk{}, kind: ChunkTypeToolCallDelta},
		{chunk: CompletionChunk{}, kind: ChunkTypeCompletion},
		{chunk: CompletionDeltaChunk{}, kind: ChunkTypeCompletionDelta},
		{chunk: UsageChunk{}, kind: ChunkTypeUsage},
		{chunk: StopChunk{}, kind: ChunkTypeStop},
	}
	for _, test := range tests {
		assert.Equal(t, test.kind, test.chunk.Kind())
	}
}

func TestCloneModelChunkOwnsMutablePayloads(t *testing.T) {
	message := Message{
		Role:  ConversationRoleAssistant,
		Parts: []Part{TextPart{Text: "original"}},
		Meta:  map[string]any{"values": []string{"original"}},
	}
	toolPayload := rawjson.Message(`{"query":"original"}`)
	completionPayload := rawjson.Message(`{"value":"original"}`)
	budget := cloneBudget{active: make(map[cloneContainer]struct{})}

	textCopy, err := cloneModelChunk(TextChunk{Message: message}, &budget)
	require.NoError(t, err)
	toolCopy, err := cloneModelChunk(ToolCallChunk{ToolCall: ToolCall{
		Name: tools.Ident("lookup"), Payload: toolPayload,
	}}, &budget)
	require.NoError(t, err)
	completionCopy, err := cloneModelChunk(CompletionChunk{Completion: Completion{
		Name: "result", Payload: completionPayload,
	}}, &budget)
	require.NoError(t, err)

	message.Meta["values"].([]string)[0] = "mutated"
	toolPayload[0] = 'x'
	completionPayload[0] = 'x'

	assert.Equal(t, "original", textCopy.(TextChunk).Message.Meta["values"].([]string)[0])
	assert.JSONEq(t, `{"query":"original"}`, string(toolCopy.(ToolCallChunk).ToolCall.Payload))
	assert.JSONEq(t, `{"value":"original"}`, string(completionCopy.(CompletionChunk).Completion.Payload))
}

func TestValidatedStreamRejectsPointerVariantWithoutPanic(t *testing.T) {
	providerStream := &contractStream{chunks: []Chunk{&TextChunk{Message: Message{
		Role:  ConversationRoleAssistant,
		Parts: []Part{TextPart{Text: "unsafe"}},
	}}}}
	client, err := NewClient(&contractProvider{stream: providerStream})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.Error(t, err)
	assert.Nil(t, chunk)
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, OutputValidationResponseShape, validationErr.Kind())
}

func TestStreamEvidenceIncludesChunkKind(t *testing.T) {
	message := Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "same"}}}
	textErr := terminalStreamError(t, TextChunk{Message: message})
	thinkingErr := terminalStreamError(t, ThinkingChunk{Message: message})

	assert.NotEqual(t, textErr.Evidence().Fingerprint, thinkingErr.Evidence().Fingerprint)
}

func terminalStreamError(t *testing.T, chunk Chunk) *OutputValidationError {
	t.Helper()
	client, err := NewClient(&contractProvider{stream: &contractStream{chunks: []Chunk{chunk}}})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &Request{})
	require.NoError(t, err)
	for err == nil {
		_, err = stream.Recv()
	}
	var validationErr *OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	return validationErr
}
