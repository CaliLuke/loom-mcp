package planner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumeStreamAggregatesChunksAndEvents(t *testing.T) {
	usage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 23, CacheReadTokens: 7, CacheWriteTokens: 11}
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

func TestConsumeStreamAggregatesCacheUsageAcrossChunks(t *testing.T) {
	recvErr := errors.New("receive failed")
	stream := &streamStub{
		chunks: []model.Chunk{
			model.UsageChunk{Usage: model.TokenUsage{
				InputTokens: 1, OutputTokens: 2, TotalTokens: 10, CacheReadTokens: 3, CacheWriteTokens: 4,
			}},
			model.UsageChunk{Usage: model.TokenUsage{
				InputTokens: 5, OutputTokens: 6, TotalTokens: 26, CacheReadTokens: 7, CacheWriteTokens: 8,
			}},
		},
		recvErr: recvErr,
	}

	summary, err := ConsumeStream(context.Background(), stream, &plannerEventsStub{})

	require.ErrorIs(t, err, recvErr)
	assert.Equal(t, model.TokenUsage{
		InputTokens: 6, OutputTokens: 8, TotalTokens: 36, CacheReadTokens: 10, CacheWriteTokens: 12,
	}, summary.Usage)
}

func TestAddUsagePreservesUnknownTotalWithCachedTokens(t *testing.T) {
	current := model.TokenUsage{CacheReadTokens: 1}
	delta := model.TokenUsage{InputTokens: 1, TotalTokens: 1}

	usage := addUsage(current, delta)

	assert.Equal(t, model.TokenUsage{InputTokens: 1, CacheReadTokens: 1}, usage)
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
		response: &model.Response{Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.CitationsPart{Text: "cited answer", Citations: []model.Citation{{Title: "source"}}}},
		}}, StopReason: "end_turn"},
	}
	events := &plannerEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	assert.Equal(t, "cited answer", summary.Text)
	assert.Equal(t, []string{"cited answer"}, events.text)
}

func TestConsumeStreamUsesProvisionalPresentationWhenAvailable(t *testing.T) {
	thinking := model.ThinkingPart{Text: "reason", Final: true}
	stream := &streamStub{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "answer"}}}},
			model.ThinkingChunk{Message: model.Message{Parts: []model.Part{thinking}}},
			model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "tools.search", Delta: `{"q":`}},
		},
		response: &model.Response{Content: []model.Message{{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.TextPart{Text: "answer"},
				thinking,
			},
		}}, StopReason: "end_turn"},
	}
	events := &presentationEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	assert.Equal(t, "answer", summary.Text)
	assert.Equal(t, []string{"started:presentation-1", "text:answer", "thinking:reason", "commit:2", "accepted:presentation-1"}, events.presentation)
	assert.Empty(t, events.text)
	assert.Empty(t, events.thinking)
	assert.Empty(t, events.toolDeltas)
}

func TestConsumeStreamDiscardsRejectedPresentation(t *testing.T) {
	streamErr := errors.New("receive failed")
	stream := &streamStub{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "partial"}}}},
		},
		recvErr: streamErr,
	}
	events := &presentationEventsStub{}

	_, err := ConsumeStream(context.Background(), stream, events)

	require.ErrorIs(t, err, streamErr)
	assert.Equal(t, []string{"started:presentation-1", "text:partial", "discarded:presentation-1"}, events.presentation)
	assert.Empty(t, events.text)
}

func TestConsumeStreamDiscardsWhenPresentationCommitFails(t *testing.T) {
	commitErr := errors.New("canonical commit failed")
	stream := &streamStub{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "complete"}}}},
		},
		response: &model.Response{Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "complete"}},
		}}, StopReason: "end_turn"},
	}
	events := &presentationEventsStub{commitErr: commitErr}

	_, err := ConsumeStream(context.Background(), stream, events)

	require.ErrorIs(t, err, commitErr)
	assert.Equal(t, []string{"started:presentation-1", "text:complete", "commit:1", "discarded:presentation-1"}, events.presentation)
	assert.Empty(t, events.text)
}

func TestConsumeStreamCommitsToolOnlyPresentationBoundary(t *testing.T) {
	stream := &streamStub{
		chunks: []model.Chunk{
			model.ToolCallChunk{ToolCall: model.ToolCall{ID: "call-1", Name: "tools.search", Payload: []byte(`{"q":"loom"}`)}},
		},
		response: &model.Response{StopReason: "tool_use"},
	}
	events := &presentationEventsStub{}

	summary, err := ConsumeStream(context.Background(), stream, events)

	require.NoError(t, err)
	require.Len(t, summary.ToolCalls, 1)
	assert.Equal(t, []string{"started:presentation-1", "commit:0", "accepted:presentation-1"}, events.presentation)
}

func TestConsumeStreamRejectsNilInputs(t *testing.T) {
	_, err := ConsumeStream(context.Background(), nil, &plannerEventsStub{})
	require.EqualError(t, err, "nil streamer")

	_, err = ConsumeStream(context.Background(), &streamStub{}, nil)
	require.EqualError(t, err, "nil PlannerEvents")
}

func TestConsumeStreamWithObserverObservesEveryAcceptedChunk(t *testing.T) {
	citation := model.CitationsPart{Text: "cited", Citations: []model.Citation{{Title: "source"}}}
	chunks := []model.Chunk{
		model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "text"}, citation}}},
		model.ThinkingChunk{Message: model.Message{Parts: []model.Part{model.ThinkingPart{Text: "thinking"}}}},
		model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "tools.search", Delta: `{"q":`}},
		model.ToolCallChunk{ToolCall: model.ToolCall{ID: "call-1", Name: "tools.search", Payload: []byte(`{"q":"loom"}`)}},
		model.CompletionDeltaChunk{Delta: model.CompletionDelta{Name: "answer", Delta: `{"answer":`}},
		model.CompletionChunk{Completion: model.Completion{Name: "answer", Payload: []byte(`{"answer":"done"}`)}},
		model.UsageChunk{Usage: model.TokenUsage{TotalTokens: 3}},
		model.StopChunk{Reason: "end_turn"},
	}
	stream := &streamStub{chunks: chunks, response: &model.Response{StopReason: "end_turn"}}
	var observed []model.Chunk
	observer := StreamObserverFunc(func(_ context.Context, chunk model.Chunk) error {
		observed = append(observed, chunk)
		return nil
	})

	summary, response, err := ConsumeStreamWithObserver(context.Background(), stream, &plannerEventsStub{}, observer)

	require.NoError(t, err)
	assert.Same(t, stream.response, response)
	assert.Equal(t, "textcited", summary.Text)
	assert.Equal(t, chunks, observed)
}

func TestConsumeStreamWithObserverOwnsTerminalLifecycle(t *testing.T) {
	observerErr := errors.New("projection failed")
	receiveErr := errors.New("receive failed")
	cleanupErr := errors.New("cleanup failed")

	tests := []struct {
		name          string
		stream        *streamStub
		observer      StreamObserver
		wantPrimary   error
		wantResponse  bool
		wantFinalize  error
		wantErrString string
	}{
		{
			name:         "clean EOF",
			stream:       &streamStub{response: &model.Response{StopReason: "end_turn"}},
			observer:     &streamObserverStub{},
			wantResponse: true,
		},
		{
			name:          "wrapped EOF",
			stream:        &streamStub{recvErr: fmt.Errorf("provider wrapped EOF: %w", io.EOF)},
			observer:      &streamObserverStub{},
			wantErrString: "provider wrapped EOF: EOF",
		},
		{
			name:          "missing response",
			stream:        &streamStub{},
			observer:      &streamObserverStub{},
			wantErrString: "validated model stream ended without a canonical response",
		},
		{
			name:         "receive failure",
			stream:       &streamStub{recvErr: receiveErr},
			observer:     &streamObserverStub{},
			wantPrimary:  receiveErr,
			wantFinalize: receiveErr,
		},
		{
			name: "observer failure",
			stream: &streamStub{
				chunks:   []model.Chunk{model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "partial"}}}}},
				response: &model.Response{StopReason: "end_turn"},
			},
			observer:     &streamObserverStub{err: observerErr},
			wantPrimary:  observerErr,
			wantFinalize: observerErr,
		},
		{
			name:         "cleanup failure",
			stream:       &streamStub{response: &model.Response{}, closeErr: cleanupErr},
			observer:     &streamObserverStub{},
			wantResponse: true,
			wantFinalize: cleanupErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, response, err := ConsumeStreamWithObserver(context.Background(), tt.stream, &plannerEventsStub{}, tt.observer)

			assert.Equal(t, tt.wantResponse, response != nil)
			assert.Equal(t, 1, tt.stream.finalizeCalls)
			if tt.wantErrString == "" || tt.wantPrimary != nil {
				assert.Equal(t, tt.wantPrimary, tt.stream.finalizePrimary)
			}
			if tt.wantFinalize == nil && tt.wantErrString == "" {
				require.NoError(t, err)
				return
			}
			if tt.wantFinalize != nil {
				require.ErrorIs(t, err, tt.wantFinalize)
			}
			if tt.wantErrString != "" {
				assert.Contains(t, err.Error(), tt.wantErrString)
			}
		})
	}
}

func TestConsumeStreamWithObserverSeparatesPrimaryAndFinalizationErrors(t *testing.T) {
	observerErr := errors.New("projection failed")
	cleanupErr := errors.New("provider cleanup leaked details")
	stream := &streamStub{
		chunks:   []model.Chunk{model.TextChunk{Message: model.Message{Parts: []model.Part{model.TextPart{Text: "partial"}}}}},
		response: &model.Response{StopReason: "end_turn"},
		closeErr: cleanupErr,
	}

	_, response, err := ConsumeStreamWithObserver(context.Background(), stream, &plannerEventsStub{}, &streamObserverStub{err: observerErr})

	assert.Nil(t, response)
	require.ErrorIs(t, err, observerErr)
	require.ErrorIs(t, err, cleanupErr)
	var consumptionErr *StreamConsumptionError
	require.ErrorAs(t, err, &consumptionErr)
	assert.Same(t, observerErr, consumptionErr.PrimaryError())
	assert.Equal(t, observerErr.Error(), err.Error())
	assert.NotContains(t, err.Error(), cleanupErr.Error())
	require.ErrorIs(t, consumptionErr.FinalizationError(), cleanupErr)
	assert.Equal(t, 0, stream.responseCalls, "Response must not be read before consumer-observed EOF")
}

type streamStub struct {
	chunks          []model.Chunk
	response        *model.Response
	recvErr         error
	closeErr        error
	index           int
	closed          bool
	finalized       bool
	responseCalls   int
	finalizeCalls   int
	finalizePrimary error
}

type streamObserverStub struct {
	chunks []model.Chunk
	err    error
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
	s.responseCalls++
	return s.response
}

func (s *streamStub) Finalize(primaryErr error) error {
	s.finalized = true
	s.finalizeCalls++
	s.finalizePrimary = primaryErr
	return errors.Join(primaryErr, s.Close())
}

func (o *streamObserverStub) OnChunk(_ context.Context, chunk model.Chunk) error {
	o.chunks = append(o.chunks, chunk)
	return o.err
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

type presentationEventsStub struct {
	plannerEventsStub
	presentation []string
	commitErr    error
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

func (e *presentationEventsStub) StartModelPresentation(context.Context) string {
	e.presentation = append(e.presentation, "started:presentation-1")
	return "presentation-1"
}

func (e *presentationEventsStub) PublishModelText(_ context.Context, _ string, text string) {
	e.presentation = append(e.presentation, "text:"+text)
}

func (e *presentationEventsStub) PublishModelThinking(_ context.Context, _ string, block model.ThinkingPart) {
	e.presentation = append(e.presentation, "thinking:"+block.Text)
}

func (e *presentationEventsStub) CommitModelPresentation(_ context.Context, _ string, response *model.Response) error {
	partCount := 0
	for _, message := range response.Content {
		partCount += len(message.Parts)
	}
	e.presentation = append(e.presentation, fmt.Sprintf("commit:%d", partCount))
	return e.commitErr
}

func (e *presentationEventsStub) FinishModelPresentation(_ context.Context, presentationID string, accepted bool) {
	state := "discarded:"
	if accepted {
		state = "accepted:"
	}
	e.presentation = append(e.presentation, state+presentationID)
}
