package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
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

	metaMu   sync.RWMutex
	metadata map[string]any

	toolNameMap map[string]string
}

func newAnthropicStreamer(ctx context.Context, stream *ssestream.Stream[sdk.MessageStreamEventUnion], nameMap map[string]string) model.Streamer {
	cctx, cancel := context.WithCancel(ctx)
	as := &anthropicStreamer{
		ctx:         cctx,
		cancel:      cancel,
		stream:      stream,
		chunks:      make(chan model.Chunk, 32),
		toolNameMap: nameMap,
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
				return model.Chunk{}, err
			}
			s.setErr(err)
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

func (s *anthropicStreamer) Close() error {
	s.cancel()
	if s.stream == nil {
		return nil
	}
	return s.stream.Close()
}

func (s *anthropicStreamer) Metadata() map[string]any {
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	if len(s.metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.metadata))
	for k, v := range s.metadata {
		out[k] = v
	}
	return out
}

func (s *anthropicStreamer) run() {
	defer close(s.chunks)
	defer func() {
		if s.stream != nil {
			_ = s.stream.Close()
		}
	}()

	processor := newAnthropicChunkProcessor(s.emitChunk, s.recordUsage, s.toolNameMap)

	for {
		select {
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		default:
		}
		if !s.stream.Next() {
			if err := s.stream.Err(); err != nil {
				s.setErr(err)
			} else if err := s.ctx.Err(); err != nil {
				s.setErr(err)
			} else {
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

func (s *anthropicStreamer) emitChunk(chunk model.Chunk) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.chunks <- chunk:
		return nil
	}
}

func (s *anthropicStreamer) recordUsage(usage model.TokenUsage) {
	s.metaMu.Lock()
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.metadata["usage"] = usage
	s.metaMu.Unlock()
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
	emit        func(model.Chunk) error
	recordUsage func(model.TokenUsage)

	toolBlocks     map[int]*toolBuffer
	thinkingBlocks map[int]*thinkingBuffer

	toolNameMap map[string]string

	stopReason string
}

func newAnthropicChunkProcessor(emit func(model.Chunk) error, recordUsage func(model.TokenUsage), nameMap map[string]string) *anthropicChunkProcessor {
	return &anthropicChunkProcessor{
		emit:           emit,
		recordUsage:    recordUsage,
		toolBlocks:     make(map[int]*toolBuffer),
		thinkingBlocks: make(map[int]*thinkingBuffer),
		toolNameMap:    nameMap,
	}
}

func (p *anthropicChunkProcessor) Handle(event sdk.MessageStreamEventUnion) error {
	switch ev := event.AsAny().(type) {
	case sdk.MessageStartEvent:
		p.toolBlocks = make(map[int]*toolBuffer)
		p.thinkingBlocks = make(map[int]*thinkingBuffer)
		p.stopReason = ""
		return nil
	case sdk.ContentBlockStartEvent:
		return p.handleContentBlockStart(ev)
	case sdk.ContentBlockDeltaEvent:
		return p.handleContentBlockDelta(ev)
	case sdk.ContentBlockStopEvent:
		return p.handleContentBlockStop(ev)
	case sdk.MessageDeltaEvent:
		p.stopReason = string(ev.Delta.StopReason)
		usage := model.TokenUsage{
			InputTokens:      int(ev.Usage.InputTokens),
			OutputTokens:     int(ev.Usage.OutputTokens),
			TotalTokens:      int(ev.Usage.InputTokens + ev.Usage.OutputTokens),
			CacheReadTokens:  int(ev.Usage.CacheReadInputTokens),
			CacheWriteTokens: int(ev.Usage.CacheCreationInputTokens),
		}
		if p.recordUsage != nil {
			p.recordUsage(usage)
		}
		return p.emit(model.Chunk{Type: model.ChunkTypeUsage, UsageDelta: &usage})
	case sdk.MessageStopEvent:
		chunk := model.Chunk{Type: model.ChunkTypeStop}
		if p.stopReason != "" {
			chunk.StopReason = p.stopReason
		}
		p.toolBlocks = make(map[int]*toolBuffer)
		p.thinkingBlocks = make(map[int]*thinkingBuffer)
		return p.emit(chunk)
	}
	return nil
}

func (p *anthropicChunkProcessor) handleContentBlockStart(ev sdk.ContentBlockStartEvent) error {
	toolUse, ok := ev.ContentBlock.AsAny().(sdk.ToolUseBlock)
	if !ok {
		return nil
	}
	tb, err := p.newToolBuffer(toolUse)
	if err != nil {
		return err
	}
	p.toolBlocks[int(ev.Index)] = tb
	return nil
}

func (p *anthropicChunkProcessor) newToolBuffer(toolUse sdk.ToolUseBlock) (*toolBuffer, error) {
	if toolUse.ID == "" {
		return nil, fmt.Errorf("anthropic stream: tool use block missing id")
	}
	if toolUse.Name == "" {
		return nil, fmt.Errorf("anthropic stream: tool use block %q missing name", toolUse.ID)
	}
	tb := &toolBuffer{id: toolUse.ID}
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
	return p.emit(model.Chunk{
		Type: model.ChunkTypeText,
		Message: &model.Message{
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
	return p.emit(model.Chunk{
		Type: model.ChunkTypeToolCallDelta,
		ToolCallDelta: &model.ToolCallDelta{
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
	return p.emit(model.Chunk{
		Type:     model.ChunkTypeThinking,
		Thinking: text,
		Message: &model.Message{
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
	chunk := model.Chunk{
		Type: model.ChunkTypeThinking,
		Message: &model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{*part},
		},
	}
	if part.Text != "" {
		chunk.Thinking = part.Text
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
	payload := decodeToolPayload(tb.finalInput())
	delete(p.toolBlocks, idx)
	return p.emit(model.Chunk{
		Type: model.ChunkTypeToolCall,
		ToolCall: &model.ToolCall{
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

func decodeToolPayload(raw string) rawjson.Message {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	data := []byte(trimmed)
	if len(data) == 0 {
		return nil
	}
	return rawjson.Message(data)
}
