package planner

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumeStreamAggregatesChunksAndEvents(t *testing.T) {
	usage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, CacheReadTokens: 7, CacheWriteTokens: 11}
	thinking := model.ThinkingPart{Text: "reason", Final: true}
	stream := &streamStub{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "hello "}, model.TextPart{Text: "world"}}}},
			model.ThinkingChunk{Message: model.Message{Parts: []model.Part{thinking}}},
			model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "tools.search", Delta: "{\"q\":"}},
			model.ToolCallChunk{ToolCall: model.ToolCall{ID: "call-1", Name: "tools.search", Payload: []byte("{\"q\":\"loom\"}")}},
			model.UsageChunk{Usage: usage},
			model.StopChunk{Reason: "tool_use"},
		},
		response: &model.Response{Usage: usage, StopReason: "tool_use"},
	}
	events := &plannerEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	assert.True(t, stream.closed)
	assert.Equal(t, "hello world", summary.Text)
	require.Len(t, summary.ToolCalls, 1)
	assert.Equal(t, ToolRequest{Name: "tools.search", Payload: []byte("{\"q\":\"loom\"}"), ToolCallID: "call-1"}, summary.ToolCalls[0])
	assert.Equal(t, "tool_use", summary.StopReason)
	assert.Equal(t, usage, summary.Usage)
	assert.Equal(t, []string{"hello world"}, events.text)
	assert.Equal(t, []model.ThinkingPart{thinking}, events.thinking)
	assert.Equal(t, []toolDelta{{id: "call-1", name: "tools.search", delta: "{\"q\":"}}, events.toolDeltas)
	assert.Equal(t, []model.TokenUsage{usage}, events.usage)
}

func TestConsumeStreamErrorsAndAlwaysCloses(t *testing.T) {
	recvErr := errors.New("receive failed")
	closeErr := errors.New("close failed")
	stream := &streamStub{
		chunks:   []model.Chunk{model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "partial"}}}}},
		recvErr:  recvErr,
		closeErr: closeErr,
	}

	summary, err := ConsumeStream(context.Background(), stream, &plannerEventsStub{})

	assert.Equal(t, "partial", summary.Text)
	assert.True(t, stream.closed)
	require.ErrorIs(t, err, recvErr)
	require.ErrorIs(t, err, closeErr)
}

func TestConsumeStreamReportsCloseFailureAfterEOF(t *testing.T) {
	closeErr := errors.New("close failed")
	stream := &streamStub{closeErr: closeErr, response: &model.Response{}}

	_, err := ConsumeStream(context.Background(), stream, &plannerEventsStub{})

	require.ErrorIs(t, err, closeErr)
	assert.True(t, stream.closed)
}

func TestConsumeValidatedStreamUsesCanonicalUsageOnce(t *testing.T) {
	chunkUsage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
	canonicalUsage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, Model: "provider-model"}
	stream := &validatedStreamStub{
		streamStub: streamStub{
			chunks: []model.Chunk{model.UsageChunk{Usage: chunkUsage}},
		},
		response: &model.Response{Usage: canonicalUsage, StopReason: "end_turn"},
	}
	events := &plannerEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	assert.True(t, stream.finalized)
	assert.Equal(t, canonicalUsage, summary.Usage)
	assert.Equal(t, "end_turn", summary.StopReason)
	assert.Equal(t, []model.TokenUsage{chunkUsage}, events.usage)
}

func TestConsumeStreamIncludesCitationBackedText(t *testing.T) {
	stream := &streamStub{
		chunks: []model.Chunk{model.TextChunk{Message: model.Message{Parts: []model.Part{
			model.CitationsPart{Text: "cited answer", Citations: []model.Citation{{Title: "source"}}},
		}}}},
		response: &model.Response{StopReason: "end_turn"},
	}
	events := &plannerEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	assert.Equal(t, "cited answer", summary.Text)
	assert.Equal(t, []string{"cited answer"}, events.text)
}

func TestConsumeStreamRejectsNilInputs(t *testing.T) {
	_, err := ConsumeStream(context.Background(), nil, &plannerEventsStub{})
	require.EqualError(t, err, "nil streamer")

	_, err = ConsumeStream(context.Background(), &streamStub{}, nil)
	require.EqualError(t, err, "nil PlannerEvents")
}

type streamStub struct {
	chunks    []model.Chunk
	response  *model.Response
	recvErr   error
	closeErr  error
	index     int
	closed    bool
	finalized bool
}

type validatedStreamStub struct {
	streamStub
	response  *model.Response
	finalized bool
}

func (s *streamStub) Recv() (model.Chunk, error) {
	if s.index < len(s.chunks) {
		chunk := s.chunks[s.index]
		s.index++
		return chunk, nil
	}
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	return nil, io.EOF
}

func (s *streamStub) Close() error {
	s.closed = true
	return s.closeErr
}

func (s *streamStub) Response() *model.Response {
	return s.response
}

func (s *streamStub) Finalize(primaryErr error) error {
	s.finalized = true
	return errors.Join(primaryErr, s.Close())
}

func (s *validatedStreamStub) Response() *model.Response {
	return s.response
}

func (s *validatedStreamStub) Finalize(primaryErr error) error {
	s.finalized = true
	return errors.Join(primaryErr, s.Close())
}

type toolDelta struct {
	id    string
	name  tools.Ident
	delta string
}

type plannerEventsStub struct {
	text       []string
	thinking   []model.ThinkingPart
	toolDeltas []toolDelta
	usage      []model.TokenUsage
}

func (e *plannerEventsStub) AssistantChunk(_ context.Context, text string) {
	e.text = append(e.text, text)
}

func (e *plannerEventsStub) ToolCallArgsDelta(_ context.Context, id string, name tools.Ident, delta string) {
	e.toolDeltas = append(e.toolDeltas, toolDelta{id: id, name: name, delta: delta})
}

func (e *plannerEventsStub) PlannerThinkingBlock(_ context.Context, block model.ThinkingPart) {
	e.thinking = append(e.thinking, block)
}

func (*plannerEventsStub) PlannerThought(context.Context, string, map[string]string) {}

func (e *plannerEventsStub) UsageDelta(_ context.Context, usage model.TokenUsage) {
	e.usage = append(e.usage, usage)
}
