package bedrock

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// bedrockStreamer adapts a Bedrock ConverseStream event stream to the
// model.Streamer interface. It stamps model attribution (modelID, modelClass)
// onto usage chunks so downstream consumers can attribute token costs.
type bedrockStreamer struct {
	ctx    context.Context
	cancel context.CancelFunc
	stream *bedrockruntime.ConverseStreamEventStream

	chunks chan model.Chunk

	errMu    sync.Mutex
	errSet   bool
	finalErr error

	responseMu  sync.RWMutex
	response    *model.Response
	consumerEOF bool
	builder     model.StreamResponseBuilder

	toolNameMap map[string]string
	modelID     string
	modelClass  model.ModelClass
	output      *model.StructuredOutput

	streamCloseOnce sync.Once
	streamCloseErr  error
}

func newBedrockStreamer(
	ctx context.Context,
	stream *bedrockruntime.ConverseStreamEventStream,
	nameMap map[string]string,
	modelID string,
	modelClass model.ModelClass,
	output *model.StructuredOutput,
) model.Streamer {
	cctx, cancel := context.WithCancel(ctx)
	bs := &bedrockStreamer{
		ctx:         cctx,
		cancel:      cancel,
		stream:      stream,
		chunks:      make(chan model.Chunk, 32),
		toolNameMap: nameMap,
		modelID:     modelID,
		modelClass:  modelClass,
		output:      output,
	}
	go bs.run()
	return bs
}

func (s *bedrockStreamer) Recv() (model.Chunk, error) {
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

func (s *bedrockStreamer) Close() error {
	s.cancel()
	return s.closeStream()
}

func (s *bedrockStreamer) Response() *model.Response {
	s.responseMu.RLock()
	defer s.responseMu.RUnlock()
	if !s.consumerEOF {
		return nil
	}
	return s.response
}

func (s *bedrockStreamer) run() {
	defer close(s.chunks)
	defer func() {
		if err := s.closeStream(); err != nil {
			s.setErr(err)
		}
	}()

	processor := newChunkProcessor(s.emitChunk, s.toolNameMap, s.modelID, s.modelClass, s.output)
	events := s.stream.Events()

	for {
		select {
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		case event, ok := <-events:
			if !ok {
				streamErr := s.stream.Err()
				ctxErr := s.ctx.Err()
				switch {
				case streamErr != nil:
					s.setErr(wrapBedrockError("converse_stream.recv", streamErr))
				case ctxErr != nil:
					s.setErr(ctxErr)
				case !processor.completed:
					s.setErr(errors.New("bedrock: stream ended before message_stop"))
				case !processor.metadataSeen:
					s.setErr(errors.New("bedrock: stream ended before metadata"))
				default:
					s.responseMu.Lock()
					s.response = s.builder.Response()
					s.responseMu.Unlock()
					s.setErr(nil)
				}
				return
			}
			if err := processor.Handle(event); err != nil {
				s.setErr(err)
				return
			}
		}
	}
}

func (s *bedrockStreamer) closeStream() error {
	s.streamCloseOnce.Do(func() {
		s.streamCloseErr = s.stream.Close()
	})
	return s.streamCloseErr
}

func (s *bedrockStreamer) emitChunk(chunk model.Chunk) error {
	if err := s.builder.Add(chunk); err != nil {
		return fmt.Errorf("bedrock: assemble stream response: %w", err)
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.chunks <- chunk:
		return nil
	}
}

func (s *bedrockStreamer) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.errSet {
		return
	}
	s.errSet = true
	s.finalErr = err
}

func (s *bedrockStreamer) err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.finalErr
}

// chunkProcessor converts Bedrock streaming events into model.Chunks. It
// stamps model attribution onto usage chunks using the resolved model ID and
// class provided at construction.
type chunkProcessor struct {
	emit func(model.Chunk) error

	toolBlocks map[int]*toolBuffer
	completion *completionBuffer
	// reasoningBlocks accumulates reasoning content per content index until stop.
	reasoningBlocks map[int]*reasoningBuffer

	toolNameMap  map[string]string
	modelID      string
	modelClass   model.ModelClass
	output       *model.StructuredOutput
	completed    bool
	metadataSeen bool
	pendingStop  model.Chunk
}

func newChunkProcessor(
	emit func(model.Chunk) error,
	nameMap map[string]string,
	modelID string,
	modelClass model.ModelClass,
	output *model.StructuredOutput,
) *chunkProcessor {
	return &chunkProcessor{
		emit:            emit,
		toolBlocks:      make(map[int]*toolBuffer),
		reasoningBlocks: make(map[int]*reasoningBuffer),
		toolNameMap:     nameMap,
		modelID:         modelID,
		modelClass:      modelClass,
		output:          output,
	}
}

func (p *chunkProcessor) Handle(event any) error {
	switch ev := event.(type) {
	case *brtypes.ConverseStreamOutputMemberMessageStart:
		p.resetMessageState()
		return nil
	case *brtypes.ConverseStreamOutputMemberContentBlockStart:
		return p.handleContentBlockStart(ev)
	case *brtypes.ConverseStreamOutputMemberContentBlockDelta:
		return p.handleContentBlockDelta(ev)
	case *brtypes.ConverseStreamOutputMemberContentBlockStop:
		return p.handleContentBlockStop(ev)
	case *brtypes.ConverseStreamOutputMemberMessageStop:
		return p.handleMessageStop(ev)
	case *brtypes.ConverseStreamOutputMemberMetadata:
		return p.handleMetadata(ev)
	}
	return nil
}

func (p *chunkProcessor) resetMessageState() {
	p.toolBlocks = make(map[int]*toolBuffer)
	p.reasoningBlocks = make(map[int]*reasoningBuffer)
	p.completion = nil
	p.completed = false
	p.metadataSeen = false
	p.pendingStop = nil
}

func (p *chunkProcessor) handleMessageStop(ev *brtypes.ConverseStreamOutputMemberMessageStop) error {
	p.completed = true
	outputLimited := bedrockOutputLimited(ev.Value.StopReason)
	if p.completion != nil {
		if outputLimited {
			p.completion = nil
		} else if err := p.finalizeCompletion(p.completion.index); err != nil {
			return err
		}
	}
	chunk := model.StopChunk{Reason: string(ev.Value.StopReason), OutputLimited: outputLimited}
	p.toolBlocks = make(map[int]*toolBuffer)
	p.reasoningBlocks = make(map[int]*reasoningBuffer)
	p.pendingStop = chunk
	if p.metadataSeen {
		return p.emitPendingStop()
	}
	return nil
}

func (p *chunkProcessor) handleMetadata(ev *brtypes.ConverseStreamOutputMemberMetadata) error {
	p.metadataSeen = true
	usage := bedrockStreamUsage(ev.Value.Usage, p.modelID, p.modelClass)
	if usage != nil {
		if err := p.emit(model.UsageChunk{Usage: *usage}); err != nil {
			return err
		}
	}
	return p.emitPendingStop()
}

func (p *chunkProcessor) emitPendingStop() error {
	if p.pendingStop == nil {
		return nil
	}
	chunk := p.pendingStop
	p.pendingStop = nil
	return p.emit(chunk)
}

func bedrockStreamUsage(usage *brtypes.TokenUsage, modelID string, modelClass model.ModelClass) *model.TokenUsage {
	if usage == nil {
		return nil
	}
	out := model.TokenUsage{
		Model:            modelID,
		ModelClass:       modelClass,
		InputTokens:      int(ptrValue(usage.InputTokens)),
		OutputTokens:     int(ptrValue(usage.OutputTokens)),
		TotalTokens:      int(ptrValue(usage.TotalTokens)),
		CacheReadTokens:  int(ptrValue(usage.CacheReadInputTokens)),
		CacheWriteTokens: int(ptrValue(usage.CacheWriteInputTokens)),
	}
	return &out
}

func (p *chunkProcessor) handleContentBlockStart(ev *brtypes.ConverseStreamOutputMemberContentBlockStart) error {
	idx, err := contentIndex(ev.Value.ContentBlockIndex)
	if err != nil {
		return err
	}
	start := ev.Value.Start
	if start == nil {
		return nil
	}
	toolUse, ok := start.(*brtypes.ContentBlockStartMemberToolUse)
	if !ok {
		return nil
	}
	if p.output != nil {
		return fmt.Errorf("bedrock stream: structured output %q emitted tool_use start", p.output.Name)
	}
	tb, err := p.newToolBuffer(toolUse)
	if err != nil {
		return err
	}
	p.toolBlocks[idx] = tb
	return nil
}

func (p *chunkProcessor) newToolBuffer(toolUse *brtypes.ContentBlockStartMemberToolUse) (*toolBuffer, error) {
	if toolUse.Value.ToolUseId == nil || *toolUse.Value.ToolUseId == "" {
		return nil, fmt.Errorf("bedrock stream: tool use block missing tool_use_id")
	}
	if toolUse.Value.Name == nil || *toolUse.Value.Name == "" {
		return nil, fmt.Errorf("bedrock stream: tool use block %q missing name", *toolUse.Value.ToolUseId)
	}
	tb := &toolBuffer{id: *toolUse.Value.ToolUseId}
	name := normalizeToolName(*toolUse.Value.Name)
	if canonical, ok := p.toolNameMap[name]; ok {
		tb.name = canonical
	} else {
		tb.name = name
	}
	return tb, nil
}

func (p *chunkProcessor) handleContentBlockDelta(ev *brtypes.ConverseStreamOutputMemberContentBlockDelta) error {
	idx, err := contentIndex(ev.Value.ContentBlockIndex)
	if err != nil {
		return err
	}
	switch delta := ev.Value.Delta.(type) {
	case *brtypes.ContentBlockDeltaMemberText:
		return p.emitTextDelta(idx, delta.Value)
	case *brtypes.ContentBlockDeltaMemberCitation:
		return p.emitCitationDelta(idx, delta)
	case *brtypes.ContentBlockDeltaMemberReasoningContent:
		return p.handleReasoningDelta(idx, delta)
	case *brtypes.ContentBlockDeltaMemberToolUse:
		return p.emitToolUseDelta(idx, delta)
	default:
		return nil
	}
}

func (p *chunkProcessor) emitTextDelta(idx int, text string) error {
	if text == "" {
		return nil
	}
	if p.output != nil {
		return p.handleCompletionDelta(idx, text)
	}
	return p.emit(model.TextChunk{
		Message: model.Message{
			Role:  bedrockRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: text}},
			Meta:  map[string]any{"content_index": idx},
		},
	})
}

func (p *chunkProcessor) emitCitationDelta(idx int, delta *brtypes.ContentBlockDeltaMemberCitation) error {
	citation := translateCitationDelta(delta.Value)
	if citation.Title == "" && citation.Source == "" && citation.Location == (model.CitationLocation{}) && len(citation.SourceContent) == 0 {
		return nil
	}
	return p.emit(model.TextChunk{Message: model.Message{
		Role: model.ConversationRoleAssistant,
		Parts: []model.Part{model.CitationsPart{
			Citations: []model.Citation{citation},
		}},
		Meta: map[string]any{"content_index": idx},
	}})
}

func (p *chunkProcessor) handleReasoningDelta(idx int, delta *brtypes.ContentBlockDeltaMemberReasoningContent) error {
	rb := p.ensureReasoningBuffer(idx)
	switch value := delta.Value.(type) {
	case *brtypes.ReasoningContentBlockDeltaMemberText:
		return p.emitReasoningText(idx, rb, value.Value)
	case *brtypes.ReasoningContentBlockDeltaMemberRedactedContent:
		if len(value.Value) > 0 {
			rb.redacted = append(rb.redacted, value.Value...)
		}
		return nil
	case *brtypes.ReasoningContentBlockDeltaMemberSignature:
		if value.Value != "" {
			rb.signature = value.Value
		}
		return nil
	default:
		return nil
	}
}

func (p *chunkProcessor) ensureReasoningBuffer(idx int) *reasoningBuffer {
	rb := p.reasoningBlocks[idx]
	if rb == nil {
		rb = &reasoningBuffer{}
		p.reasoningBlocks[idx] = rb
	}
	return rb
}

func (p *chunkProcessor) emitReasoningText(idx int, rb *reasoningBuffer, text string) error {
	if text == "" {
		return nil
	}
	rb.text.WriteString(text)
	return p.emit(model.ThinkingChunk{
		Message: model.Message{
			Role: bedrockRoleAssistant,
			Parts: []model.Part{model.ThinkingPart{
				Text:  text,
				Index: idx,
				Final: false,
			}},
		},
	})
}

func (p *chunkProcessor) emitToolUseDelta(idx int, delta *brtypes.ContentBlockDeltaMemberToolUse) error {
	if p.output != nil {
		return fmt.Errorf("bedrock stream: structured output %q emitted tool_use delta", p.output.Name)
	}
	tb := p.toolBlocks[idx]
	if tb == nil || delta.Value.Input == nil {
		return nil
	}
	fragment := *delta.Value.Input
	tb.fragments = append(tb.fragments, fragment)
	if tb.id == "" {
		return fmt.Errorf("bedrock stream: tool JSON delta missing tool call id")
	}
	if tb.name == "" {
		return fmt.Errorf("bedrock stream: tool JSON delta missing tool name for id %q", tb.id)
	}
	return p.emit(model.ToolCallDeltaChunk{
		Delta: model.ToolCallDelta{
			Name:  tools.Ident(tb.name),
			ID:    tb.id,
			Delta: fragment,
		},
	})
}

func (p *chunkProcessor) handleContentBlockStop(ev *brtypes.ConverseStreamOutputMemberContentBlockStop) error {
	idx, err := contentIndex(ev.Value.ContentBlockIndex)
	if err != nil {
		return err
	}
	if err := p.emitFinalReasoning(idx); err != nil {
		return err
	}
	return p.emitFinalToolCall(idx)
}

func (p *chunkProcessor) emitFinalReasoning(idx int) error {
	rb := p.reasoningBlocks[idx]
	if rb == nil {
		return nil
	}
	delete(p.reasoningBlocks, idx)
	part := rb.finalize()
	if part == nil {
		return nil
	}
	part.Index = idx
	part.Final = true
	chunk := model.ThinkingChunk{
		Message: model.Message{
			Role:  bedrockRoleAssistant,
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

func (p *chunkProcessor) emitFinalToolCall(idx int) error {
	tb := p.toolBlocks[idx]
	if tb == nil {
		return nil
	}
	delete(p.toolBlocks, idx)
	payload, err := decodeToolPayload(tb.finalInput())
	if err != nil {
		return fmt.Errorf("bedrock stream: tool call %q payload: %w", tb.id, err)
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

// completionBuffer accumulates one structured-output content block until the
// provider closes it and the adapter can emit the canonical completion chunk.
type completionBuffer struct {
	name      string
	index     int
	fragments []string
}

func (tb *toolBuffer) finalInput() string {
	if len(tb.fragments) == 0 {
		return "{}"
	}
	joined := strings.Join(tb.fragments, "")
	if joined == "" {
		return "{}"
	}
	return joined
}

// finalPayload returns the canonical JSON payload for the structured completion
// block. Unlike tool payloads, typed completions do not use fallbacks: invalid
// or empty JSON is a hard provider contract violation.
func (cb *completionBuffer) finalPayload() (rawjson.Message, error) {
	if len(cb.fragments) == 0 {
		return nil, errors.New("structured completion payload is empty")
	}
	joined := strings.Join(cb.fragments, "")
	trimmed := strings.TrimSpace(joined)
	if trimmed == "" {
		return nil, errors.New("structured completion payload is empty")
	}
	data := []byte(trimmed)
	if !jsontext.Value(data).IsValid() {
		return nil, fmt.Errorf("structured completion payload is not valid JSON: %q", trimmed)
	}
	return rawjson.Message(data), nil
}

// handleCompletionDelta records and emits one structured-output preview
// fragment for the currently open Bedrock content block.
func (p *chunkProcessor) handleCompletionDelta(idx int, delta string) error {
	if p.output == nil {
		return errors.New("bedrock stream: completion delta requested without structured output")
	}
	if p.completion == nil {
		p.completion = &completionBuffer{
			name:  p.output.Name,
			index: idx,
		}
	}
	if p.completion.index != idx {
		return fmt.Errorf(
			"bedrock stream: structured output %q spanned multiple content blocks (%d, %d)",
			p.output.Name,
			p.completion.index,
			idx,
		)
	}
	p.completion.fragments = append(p.completion.fragments, delta)
	return p.emit(model.CompletionDeltaChunk{
		Delta: model.CompletionDelta{
			Name:  p.completion.name,
			Delta: delta,
		},
	})
}

// finalizeCompletion emits the canonical structured completion payload for the
// given content block index when one is buffered there.
func (p *chunkProcessor) finalizeCompletion(idx int) error {
	if p.completion == nil || p.completion.index != idx {
		return nil
	}
	payload, err := p.completion.finalPayload()
	if err != nil {
		return fmt.Errorf("bedrock stream: structured output %q: %w", p.output.Name, err)
	}
	completion := p.completion
	p.completion = nil
	return p.emit(model.CompletionChunk{
		Completion: model.Completion{
			Name:    completion.name,
			Payload: payload,
		},
	})
}

func contentIndex(idx *int32) (int, error) {
	if idx == nil {
		return 0, fmt.Errorf("bedrock: content block index missing")
	}
	return int(*idx), nil
}

func decodeToolPayload(raw string) (rawjson.Message, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return rawjson.Message([]byte("{}")), nil
	}
	data := []byte(trimmed)
	if !jsontext.Value(data).IsValid() {
		return nil, errors.New("invalid JSON")
	}
	return rawjson.Message(data), nil
}

func translateCitationDelta(delta brtypes.CitationsDelta) model.Citation {
	out := model.Citation{
		Location:      translateCitationLocationDelta(delta.Location),
		SourceContent: translateCitationSourceContentDelta(delta.SourceContent),
	}
	if delta.Title != nil {
		out.Title = *delta.Title
	}
	if delta.Source != nil {
		out.Source = *delta.Source
	}
	return out
}

func translateCitationLocationDelta(loc brtypes.CitationLocation) model.CitationLocation {
	switch v := loc.(type) {
	case *brtypes.CitationLocationMemberDocumentChar:
		return model.CitationLocation{
			DocumentChar: &model.DocumentCharLocation{
				DocumentIndex: int32Value(v.Value.DocumentIndex),
				Start:         int32Value(v.Value.Start),
				End:           int32Value(v.Value.End),
			},
		}
	case *brtypes.CitationLocationMemberDocumentChunk:
		return model.CitationLocation{
			DocumentChunk: &model.DocumentChunkLocation{
				DocumentIndex: int32Value(v.Value.DocumentIndex),
				Start:         int32Value(v.Value.Start),
				End:           int32Value(v.Value.End),
			},
		}
	case *brtypes.CitationLocationMemberDocumentPage:
		return model.CitationLocation{
			DocumentPage: &model.DocumentPageLocation{
				DocumentIndex: int32Value(v.Value.DocumentIndex),
				Start:         int32Value(v.Value.Start),
				End:           int32Value(v.Value.End),
			},
		}
	default:
		return model.CitationLocation{}
	}
}

func translateCitationSourceContentDelta(contents []brtypes.CitationSourceContentDelta) []string {
	if len(contents) == 0 {
		return nil
	}
	out := make([]string, 0, len(contents))
	for _, content := range contents {
		if content.Text != nil && *content.Text != "" {
			out = append(out, *content.Text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func int32Value(ptr *int32) int {
	if ptr == nil {
		return 0
	}
	return int(*ptr)
}

func normalizeToolName(name string) string {
	if strings.HasPrefix(name, "$FUNCTIONS.") {
		return strings.TrimPrefix(name, "$FUNCTIONS.")
	}
	return name
}

type reasoningBuffer struct {
	text      strings.Builder
	redacted  []byte
	signature string
}

func (rb *reasoningBuffer) finalize() *model.ThinkingPart {
	// Prefer redacted variant when present.
	if len(rb.redacted) > 0 {
		return &model.ThinkingPart{Redacted: append([]byte(nil), rb.redacted...)}
	}
	if s := rb.text.String(); s != "" && rb.signature != "" {
		return &model.ThinkingPart{
			Text:      s,
			Signature: rb.signature,
		}
	}
	return nil
}
