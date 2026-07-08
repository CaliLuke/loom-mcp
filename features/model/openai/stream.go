package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/openai/openai-go/responses"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type responseStream interface {
	Next() bool
	Current() responses.ResponseStreamEventUnion
	Err() error
	Close() error
}

type openAIStreamer struct {
	ctx    context.Context
	cancel context.CancelFunc
	stream responseStream

	chunks chan model.Chunk

	errMu    sync.Mutex
	errSet   bool
	finalErr error

	metaMu   sync.RWMutex
	metadata map[string]any
}

type openAIChunkProcessor struct {
	emit        func(model.Chunk) error
	recordUsage func(model.TokenUsage)

	toolCalls map[string]*streamToolBuffer

	codec   *openAIToolCodec
	modelID string
	output  *model.StructuredOutput

	completed bool
	sawText   bool
}

type streamToolBuffer struct {
	itemID  string
	callID  string
	name    string
	pending []string
}

func newOpenAIStreamer(ctx context.Context, stream responseStream, codec *openAIToolCodec, modelID string, output *model.StructuredOutput) model.Streamer {
	cctx, cancel := context.WithCancel(ctx)
	streamer := &openAIStreamer{
		ctx:    cctx,
		cancel: cancel,
		stream: stream,
		chunks: make(chan model.Chunk, 32),
	}
	processor := &openAIChunkProcessor{
		emit:        streamer.emitChunk,
		recordUsage: streamer.recordUsage,
		toolCalls:   make(map[string]*streamToolBuffer),
		codec:       codec,
		modelID:     modelID,
		output:      output,
	}
	go streamer.run(processor)
	return streamer
}

func (s *openAIStreamer) Recv() (model.Chunk, error) {
	select {
	case chunk, ok := <-s.chunks:
		if ok {
			return chunk, nil
		}
		if err := s.err(); err != nil {
			return model.Chunk{}, err
		}
		return model.Chunk{}, io.EOF
	case <-s.ctx.Done():
		err := s.ctx.Err()
		if err == nil {
			err = context.Canceled
		}
		s.setErr(err)
		return model.Chunk{}, err
	}
}

func (s *openAIStreamer) Close() error {
	s.cancel()
	if s.stream == nil {
		return nil
	}
	return s.stream.Close()
}

func (s *openAIStreamer) Metadata() map[string]any {
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	if len(s.metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.metadata))
	for key, value := range s.metadata {
		out[key] = value
	}
	return out
}

func (s *openAIStreamer) run(processor *openAIChunkProcessor) {
	defer close(s.chunks)
	defer func() {
		if s.stream != nil {
			_ = s.stream.Close()
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		default:
		}
		if !s.stream.Next() {
			if err := s.stream.Err(); err != nil {
				s.setErr(wrapResponsesStreamError(err))
				return
			}
			if !processor.completed {
				s.setErr(errors.New("openai: stream ended before response.completed"))
				return
			}
			s.setErr(nil)
			return
		}
		if err := processor.Handle(s.stream.Current()); err != nil {
			s.setErr(err)
			return
		}
	}
}

func (s *openAIStreamer) emitChunk(chunk model.Chunk) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.chunks <- chunk:
		return nil
	}
}

func (s *openAIStreamer) recordUsage(usage model.TokenUsage) {
	s.metaMu.Lock()
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.metadata["usage"] = usage
	s.metaMu.Unlock()
}

func (s *openAIStreamer) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.errSet {
		return
	}
	s.errSet = true
	s.finalErr = err
}

func (s *openAIStreamer) err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.finalErr
}

func (p *openAIChunkProcessor) Handle(event responses.ResponseStreamEventUnion) error {
	switch actual := event.AsAny().(type) {
	case responses.ResponseOutputItemAddedEvent:
		return p.registerOutputItem(actual.Item)
	case responses.ResponseOutputItemDoneEvent:
		return p.registerOutputItem(actual.Item)
	case responses.ResponseFunctionCallArgumentsDeltaEvent:
		return p.handleToolCallArgumentsDelta(actual)
	case responses.ResponseTextDeltaEvent:
		return p.handleTextDelta(actual.Delta, actual.ItemID, actual.OutputIndex)
	case responses.ResponseRefusalDeltaEvent:
		return p.handleTextDelta(actual.Delta, actual.ItemID, actual.OutputIndex)
	case responses.ResponseReasoningSummaryTextDeltaEvent:
		return p.handleThinkingDelta(actual)
	case responses.ResponseCompletedEvent:
		return p.handleCompleted(actual.Response)
	case responses.ResponseIncompleteEvent:
		return p.handleCompleted(actual.Response)
	case responses.ResponseFailedEvent:
		return fmt.Errorf("openai responses stream failed: %s", actual.Response.Error.Message)
	case responses.ResponseErrorEvent:
		return fmt.Errorf("openai responses stream error: %s", actual.Message)
	default:
		return nil
	}
}

func (p *openAIChunkProcessor) registerOutputItem(item responses.ResponseOutputItemUnion) error {
	switch actual := item.AsAny().(type) {
	case responses.ResponseFunctionToolCall:
		buffer := p.toolCalls[actual.ID]
		if buffer == nil {
			buffer = &streamToolBuffer{itemID: actual.ID}
			p.toolCalls[actual.ID] = buffer
		}
		if actual.CallID != "" {
			buffer.callID = actual.CallID
		}
		if actual.Name != "" {
			buffer.name = actual.Name
		}
		return p.flushPendingToolDeltas(buffer)
	default:
		return nil
	}
}

func (p *openAIChunkProcessor) handleToolCallArgumentsDelta(event responses.ResponseFunctionCallArgumentsDeltaEvent) error {
	if p.output != nil {
		return errors.New("openai: structured output emitted tool calls")
	}
	buffer := p.toolCalls[event.ItemID]
	if buffer == nil {
		buffer = &streamToolBuffer{itemID: event.ItemID}
		p.toolCalls[event.ItemID] = buffer
	}
	if buffer.callID == "" || buffer.name == "" {
		buffer.pending = append(buffer.pending, event.Delta)
		return nil
	}
	return p.emitToolCallDelta(buffer, event.Delta)
}

func (p *openAIChunkProcessor) flushPendingToolDeltas(buffer *streamToolBuffer) error {
	if buffer == nil || buffer.callID == "" || buffer.name == "" || len(buffer.pending) == 0 {
		return nil
	}
	for _, delta := range buffer.pending {
		if err := p.emitToolCallDelta(buffer, delta); err != nil {
			return err
		}
	}
	buffer.pending = nil
	return nil
}

func (p *openAIChunkProcessor) emitToolCallDelta(buffer *streamToolBuffer, delta string) error {
	if delta == "" {
		return nil
	}
	return p.emit(model.Chunk{
		Type: model.ChunkTypeToolCallDelta,
		ToolCallDelta: &model.ToolCallDelta{
			Name:  tools.Ident(p.codec.canonicalName(buffer.name)),
			ID:    buffer.callID,
			Delta: delta,
		},
	})
}

func (p *openAIChunkProcessor) handleTextDelta(delta string, itemID string, outputIndex int64) error {
	if delta == "" {
		return nil
	}
	p.sawText = true
	if p.output != nil {
		return p.emit(model.Chunk{
			Type: model.ChunkTypeCompletionDelta,
			CompletionDelta: &model.CompletionDelta{
				Name:  structuredOutputName(p.output),
				Delta: delta,
			},
		})
	}
	return p.emit(model.Chunk{
		Type: model.ChunkTypeText,
		Message: &model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: delta}},
			Meta: map[string]any{
				"item_id":      itemID,
				"output_index": outputIndex,
			},
		},
	})
}

func (p *openAIChunkProcessor) handleThinkingDelta(event responses.ResponseReasoningSummaryTextDeltaEvent) error {
	if event.Delta == "" {
		return nil
	}
	return p.emit(model.Chunk{
		Type:     model.ChunkTypeThinking,
		Thinking: event.Delta,
		Message: &model.Message{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{model.ThinkingPart{
				Text:  event.Delta,
				Index: int(event.SummaryIndex),
				Final: false,
			}},
			Meta: map[string]any{
				"item_id":      event.ItemID,
				"output_index": event.OutputIndex,
			},
		},
	})
}

func (p *openAIChunkProcessor) handleCompleted(resp responses.Response) error {
	p.completed = true
	if resp.Model != "" {
		p.modelID = resp.Model
	}
	translated, err := translateResponse(&resp, p.codec, p.output)
	if err != nil {
		return err
	}
	translated.Usage.Model = p.modelID
	if p.output != nil {
		if err := p.emitCompletion(translated.Content); err != nil {
			return err
		}
	} else {
		if err := p.emitFinalToolCalls(translated.ToolCalls); err != nil {
			return err
		}
		if err := p.emitFinalTextIfNeeded(translated.Content); err != nil {
			return err
		}
	}
	return p.emitUsageAndStop(translated.Usage, translated.StopReason)
}

func (p *openAIChunkProcessor) emitCompletion(content []model.Message) error {
	payload, err := structuredOutputPayload(content, p.output)
	if err != nil {
		return err
	}
	return p.emit(model.Chunk{
		Type: model.ChunkTypeCompletion,
		Completion: &model.Completion{
			Name:    structuredOutputName(p.output),
			Payload: payload,
		},
	})
}

func (p *openAIChunkProcessor) emitFinalToolCalls(calls []model.ToolCall) error {
	for _, call := range calls {
		callCopy := call
		if err := p.emit(model.Chunk{
			Type:     model.ChunkTypeToolCall,
			ToolCall: &callCopy,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *openAIChunkProcessor) emitFinalTextIfNeeded(content []model.Message) error {
	if p.sawText {
		return nil
	}
	text := extractAssistantText(content)
	if text == "" {
		return nil
	}
	return p.emit(model.Chunk{
		Type: model.ChunkTypeText,
		Message: &model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: text}},
		},
	})
}

func (p *openAIChunkProcessor) emitUsageAndStop(usage model.TokenUsage, stopReason string) error {
	if hasOpenAITokenUsage(usage) {
		if p.recordUsage != nil {
			p.recordUsage(usage)
		}
		if err := p.emit(model.Chunk{
			Type:       model.ChunkTypeUsage,
			UsageDelta: &usage,
		}); err != nil {
			return err
		}
	}
	return p.emit(model.Chunk{
		Type:       model.ChunkTypeStop,
		StopReason: stopReason,
	})
}

func hasOpenAITokenUsage(usage model.TokenUsage) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.CacheReadTokens != 0 ||
		usage.CacheWriteTokens != 0
}

func structuredOutputName(output *model.StructuredOutput) string {
	if output == nil || output.Name == "" {
		return "structured_output"
	}
	return output.Name
}
