package anthropic

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// anthropicStreamer adapts an Anthropic Messages streaming stream to the
// model.Streamer interface.
type anthropicStreamer struct {
	ctx    context.Context
	cancel context.CancelFunc
	stream *ssestream.Stream[sdk.MessageStreamEventUnion]

	chunks chan model.Chunk

	errMu    sync.Mutex
	errSet   bool
	finalErr error

	responseMu  sync.RWMutex
	response    *model.Response
	consumerEOF bool
	builder     model.StreamResponseBuilder

	toolNameMap map[string]string
	toolUseIDs  *toolUseIDCodec
	modelID     string
	modelClass  model.ModelClass

	streamCloseOnce sync.Once
	streamCloseErr  error
}

func newAnthropicStreamer(ctx context.Context, stream *ssestream.Stream[sdk.MessageStreamEventUnion], modelID string, modelClass model.ModelClass, nameMap map[string]string, toolUseIDs *toolUseIDCodec) model.Streamer {
	cctx, cancel := context.WithCancel(ctx)
	as := &anthropicStreamer{
		ctx:         cctx,
		cancel:      cancel,
		stream:      stream,
		chunks:      make(chan model.Chunk, 32),
		toolNameMap: nameMap,
		toolUseIDs:  toolUseIDs,
		modelID:     modelID,
		modelClass:  modelClass,
	}
	go as.run()
	return as
}

func (s *anthropicStreamer) Recv() (model.Chunk, error) {
	select {
	case chunk, ok := <-s.chunks:
		if ok {
			return chunk, nil
		}
		if err := s.err(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			s.setErr(err)
			return nil, err
		}
		s.responseMu.Lock()
		s.consumerEOF = true
		s.responseMu.Unlock()
		return nil, io.EOF
	case <-s.ctx.Done():
		err := s.ctx.Err()
		if err == nil {
			err = context.Canceled
		}
		s.setErr(err)
		return nil, err
	}
}

func (s *anthropicStreamer) Close() error {
	s.cancel()
	return s.closeStream()
}

func (s *anthropicStreamer) Response() *model.Response {
	s.responseMu.RLock()
	defer s.responseMu.RUnlock()
	if !s.consumerEOF {
		return nil
	}
	return s.response
}

func (s *anthropicStreamer) run() {
	defer close(s.chunks)
	defer func() {
		_ = s.closeStream()
	}()

	processor := newAnthropicChunkProcessor(s.emitChunk, s.modelID, s.modelClass, s.toolNameMap, s.toolUseIDs)

	for {
		select {
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		default:
		}
		if !s.stream.Next() {
			if err := s.stream.Err(); err != nil {
				s.setErr(wrapAnthropicStreamError(err))
			} else if err := s.ctx.Err(); err != nil {
				s.setErr(err)
			} else if !processor.completed {
				s.setErr(errors.New("anthropic: stream ended before message_stop"))
			} else {
				s.responseMu.Lock()
				s.response = s.builder.Response()
				s.responseMu.Unlock()
				s.setErr(nil)
			}
			return
		}
		event := s.stream.Current()
		if err := processor.Handle(event); err != nil {
			s.setErr(err)
			return
		}
	}
}

func (s *anthropicStreamer) closeStream() error {
	s.streamCloseOnce.Do(func() {
		if s.stream != nil {
			s.streamCloseErr = s.stream.Close()
		}
	})
	return s.streamCloseErr
}

func (s *anthropicStreamer) emitChunk(chunk model.Chunk) error {
	if err := s.builder.Add(chunk); err != nil {
		return fmt.Errorf("anthropic: assemble stream response: %w", err)
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.chunks <- chunk:
		return nil
	}
}

func (s *anthropicStreamer) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.errSet {
		return
	}
	s.errSet = true
	s.finalErr = err
}

func (s *anthropicStreamer) err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.finalErr
}

// anthropicChunkProcessor converts Anthropic streaming events into model.Chunks.
type anthropicChunkProcessor struct {
	emit func(model.Chunk) error

	toolBlocks     map[int]*toolBuffer
	thinkingBlocks map[int]*thinkingBuffer

	toolNameMap map[string]string
	toolUseIDs  *toolUseIDCodec
	modelID     string
	modelClass  model.ModelClass
	usage       model.TokenUsage

	stopReason string
	completed  bool
}

func newAnthropicChunkProcessor(emit func(model.Chunk) error, modelID string, modelClass model.ModelClass, nameMap map[string]string, toolUseIDs *toolUseIDCodec) *anthropicChunkProcessor {
	return &anthropicChunkProcessor{
		emit:           emit,
		toolBlocks:     make(map[int]*toolBuffer),
		thinkingBlocks: make(map[int]*thinkingBuffer),
		toolNameMap:    nameMap,
		toolUseIDs:     toolUseIDs,
		modelID:        modelID,
		modelClass:     modelClass,
	}
}

func (p *anthropicChunkProcessor) Handle(event sdk.MessageStreamEventUnion) error {
	switch ev := event.AsAny().(type) {
	case sdk.MessageStartEvent:
		p.toolBlocks = make(map[int]*toolBuffer)
		p.thinkingBlocks = make(map[int]*thinkingBuffer)
		p.stopReason = ""
		if ev.Message.Model != "" {
			p.modelID = ev.Message.Model
		}
		p.usage = anthropicUsage(ev.Message.Usage, p.modelID, p.modelClass)
		return nil
	case sdk.ContentBlockStartEvent:
		return p.handleContentBlockStart(ev)
	case sdk.ContentBlockDeltaEvent:
		return p.handleContentBlockDelta(ev)
	case sdk.ContentBlockStopEvent:
		return p.handleContentBlockStop(ev)
	case sdk.MessageDeltaEvent:
		p.stopReason = string(ev.Delta.StopReason)
		p.mergeUsage(
			ev.Usage.InputTokens,
			ev.Usage.OutputTokens,
			ev.Usage.CacheReadInputTokens,
			ev.Usage.CacheCreationInputTokens,
		)
		usage := p.usage
		return p.emit(model.UsageChunk{Usage: usage})
	case sdk.MessageStopEvent:
		p.completed = true
		chunk := model.StopChunk{Reason: p.stopReason, OutputLimited: anthropicOutputLimited(p.stopReason)}
		p.toolBlocks = make(map[int]*toolBuffer)
		p.thinkingBlocks = make(map[int]*thinkingBuffer)
		return p.emit(chunk)
	}
	return nil
}

func (p *anthropicChunkProcessor) mergeUsage(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) {
	if inputTokens != 0 {
		p.usage.InputTokens = int(inputTokens)
	}
	if outputTokens != 0 {
		p.usage.OutputTokens = int(outputTokens)
	}
	if cacheReadTokens != 0 {
		p.usage.CacheReadTokens = int(cacheReadTokens)
	}
	if cacheWriteTokens != 0 {
		p.usage.CacheWriteTokens = int(cacheWriteTokens)
	}
	p.usage.Model = p.modelID
	p.usage.ModelClass = p.modelClass
	p.usage.TotalTokens = p.usage.InputTokens + p.usage.OutputTokens + p.usage.CacheReadTokens + p.usage.CacheWriteTokens
}

func (p *anthropicChunkProcessor) handleContentBlockStart(ev sdk.ContentBlockStartEvent) error {
	switch block := ev.ContentBlock.AsAny().(type) {
	case sdk.ToolUseBlock:
		tb, err := p.newToolBuffer(block)
		if err != nil {
			return err
		}
		p.toolBlocks[int(ev.Index)] = tb
	case sdk.ThinkingBlock:
		p.recordThinkingBlockStart(int(ev.Index), block)
	case sdk.RedactedThinkingBlock:
		p.recordRedactedThinkingBlockStart(int(ev.Index), block)
	}
	return nil
}

func (p *anthropicChunkProcessor) recordThinkingBlockStart(idx int, block sdk.ThinkingBlock) {
	if block.Thinking == "" && block.Signature == "" {
		return
	}
	tb := p.ensureThinkingBuffer(idx)
	if block.Thinking != "" {
		tb.text.WriteString(block.Thinking)
	}
	if block.Signature != "" {
		tb.signature = block.Signature
	}
}

func (p *anthropicChunkProcessor) recordRedactedThinkingBlockStart(idx int, block sdk.RedactedThinkingBlock) {
	if block.Data == "" {
		return
	}
	tb := p.ensureThinkingBuffer(idx)
	tb.redacted = []byte(block.Data)
}

func (p *anthropicChunkProcessor) newToolBuffer(toolUse sdk.ToolUseBlock) (*toolBuffer, error) {
	if toolUse.ID == "" {
		return nil, fmt.Errorf("anthropic stream: tool use block missing id")
	}
	if toolUse.Name == "" {
		return nil, fmt.Errorf("anthropic stream: tool use block %q missing name", toolUse.ID)
	}
	tb := &toolBuffer{id: p.toolUseIDs.decode(toolUse.ID)}
	if canonical, ok := p.toolNameMap[toolUse.Name]; ok {
		tb.name = canonical
	} else {
		tb.name = toolUse.Name
	}
	return tb, nil
}

func (p *anthropicChunkProcessor) handleContentBlockDelta(ev sdk.ContentBlockDeltaEvent) error {
	idx := int(ev.Index)
	switch delta := ev.Delta.AsAny().(type) {
	case sdk.TextDelta:
		return p.emitTextDelta(idx, delta.Text)
	case sdk.InputJSONDelta:
		return p.emitToolJSONDelta(idx, delta.PartialJSON)
	case sdk.ThinkingDelta:
		return p.emitThinkingDelta(idx, delta.Thinking)
	case sdk.SignatureDelta:
		return p.recordThinkingSignature(idx, delta.Signature)
	default:
		return nil
	}
}

func (p *anthropicChunkProcessor) emitTextDelta(idx int, text string) error {
	if text == "" {
		return nil
	}
	return p.emit(model.TextChunk{
		Message: model.Message{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.TextPart{Text: text},
			},
			Meta: map[string]any{"content_index": idx},
		},
	})
}

func (p *anthropicChunkProcessor) emitToolJSONDelta(idx int, fragment string) error {
	if fragment == "" {
		return nil
	}
	tb := p.toolBlocks[idx]
	if tb == nil {
		return nil
	}
	tb.fragments = append(tb.fragments, fragment)
	if tb.id == "" {
		return fmt.Errorf("anthropic stream: tool JSON delta missing tool call id")
	}
	if tb.name == "" {
		return fmt.Errorf("anthropic stream: tool JSON delta missing tool name for id %q", tb.id)
	}
	return p.emit(model.ToolCallDeltaChunk{
		Delta: model.ToolCallDelta{
			Name:  tools.Ident(tb.name),
			ID:    tb.id,
			Delta: fragment,
		},
	})
}

func (p *anthropicChunkProcessor) emitThinkingDelta(idx int, text string) error {
	if text == "" {
		return nil
	}
	tb := p.ensureThinkingBuffer(idx)
	tb.text.WriteString(text)
	return p.emit(model.ThinkingChunk{
		Message: model.Message{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.ThinkingPart{
					Text:  text,
					Index: idx,
					Final: false,
				},
			},
		},
	})
}

func (p *anthropicChunkProcessor) recordThinkingSignature(idx int, signature string) error {
	if signature == "" {
		return nil
	}
	p.ensureThinkingBuffer(idx).signature = signature
	return nil
}

func (p *anthropicChunkProcessor) ensureThinkingBuffer(idx int) *thinkingBuffer {
	tb := p.thinkingBlocks[idx]
	if tb == nil {
		tb = &thinkingBuffer{}
		p.thinkingBlocks[idx] = tb
	}
	return tb
}

func (p *anthropicChunkProcessor) handleContentBlockStop(ev sdk.ContentBlockStopEvent) error {
	idx := int(ev.Index)
	if err := p.emitFinalThinking(idx); err != nil {
		return err
	}
	return p.emitFinalToolCall(idx)
}

func (p *anthropicChunkProcessor) emitFinalThinking(idx int) error {
	tb := p.thinkingBlocks[idx]
	if tb == nil {
		return nil
	}
	delete(p.thinkingBlocks, idx)
	part := tb.finalize(idx)
	if part == nil {
		return nil
	}
	chunk := model.ThinkingChunk{
		Message: model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{*part},
		},
	}
	if part.Text != "" {
		return p.emit(chunk)
	}
	if len(part.Redacted) > 0 {
		return p.emit(chunk)
	}
	return nil
}

func (p *anthropicChunkProcessor) emitFinalToolCall(idx int) error {
	tb := p.toolBlocks[idx]
	if tb == nil {
		return nil
	}
	delete(p.toolBlocks, idx)
	payload, err := decodeToolPayload(tb.finalInput())
	if err != nil {
		return fmt.Errorf("anthropic stream: tool call %q payload: %w", tb.id, err)
	}
	return p.emit(model.ToolCallChunk{
		ToolCall: model.ToolCall{
			Name:    tools.Ident(tb.name),
			Payload: payload,
			ID:      tb.id,
		},
	})
}

type toolBuffer struct {
	name      string
	id        string
	fragments []string
}

func (tb *toolBuffer) finalInput() string {
	if len(tb.fragments) == 0 {
		return "{}"
	}
	joined := strings.Join(tb.fragments, "")
	if strings.TrimSpace(joined) == "" {
		return "{}"
	}
	return joined
}

type thinkingBuffer struct {
	text      strings.Builder
	signature string
	redacted  []byte
}

func (tb *thinkingBuffer) finalize(index int) *model.ThinkingPart {
	if len(tb.redacted) > 0 {
		return &model.ThinkingPart{
			Redacted: append([]byte(nil), tb.redacted...),
			Index:    index,
			Final:    true,
		}
	}
	if s := tb.text.String(); s != "" && tb.signature != "" {
		return &model.ThinkingPart{
			Text:      s,
			Signature: tb.signature,
			Index:     index,
			Final:     true,
		}
	}
	return nil
}

func decodeToolPayload(raw string) (rawjson.Message, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	data := []byte(trimmed)
	if !jsontext.Value(data).IsValid() {
		return nil, errors.New("invalid JSON")
	}
	return rawjson.Message(data), nil
}
