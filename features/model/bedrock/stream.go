package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
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

	metaMu      sync.RWMutex
	metadata    map[string]any
	toolNameMap map[string]string
	modelID     string
	modelClass  model.ModelClass
}

func newBedrockStreamer(ctx context.Context, stream *bedrockruntime.ConverseStreamEventStream, nameMap map[string]string, modelID string, modelClass model.ModelClass) model.Streamer {
	cctx, cancel := context.WithCancel(ctx)
	bs := &bedrockStreamer{
		ctx:         cctx,
		cancel:      cancel,
		stream:      stream,
		chunks:      make(chan model.Chunk, 32),
		toolNameMap: nameMap,
		modelID:     modelID,
		modelClass:  modelClass,
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

func (s *bedrockStreamer) Close() error {
	s.cancel()
	return s.stream.Close()
}

func (s *bedrockStreamer) Metadata() map[string]any {
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

func (s *bedrockStreamer) run() {
	defer close(s.chunks)
	defer func() {
		if err := s.stream.Close(); err != nil {
			s.setErr(err)
		}
	}()

	processor := newChunkProcessor(s.emitChunk, s.recordUsage, s.recordCitations, s.toolNameMap, s.modelID, s.modelClass)
	events := s.stream.Events()

	for {
		select {
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		case event, ok := <-events:
			if !ok {
				if err := s.stream.Err(); err != nil {
					s.setErr(wrapBedrockError("converse_stream.recv", err))
				} else if err := s.ctx.Err(); err != nil {
					s.setErr(err)
				} else {
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

func (s *bedrockStreamer) emitChunk(chunk model.Chunk) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.chunks <- chunk:
		return nil
	}
}

func (s *bedrockStreamer) recordUsage(usage model.TokenUsage) {
	s.metaMu.Lock()
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.metadata["usage"] = usage
	s.metaMu.Unlock()
}

func (s *bedrockStreamer) recordCitations(citations []model.Citation) {
	if len(citations) == 0 {
		return
	}
	s.metaMu.Lock()
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	if prev, ok := s.metadata["citations"].([]model.Citation); ok && len(prev) > 0 {
		citations = append(prev, citations...)
	}
	s.metadata["citations"] = citations
	s.metaMu.Unlock()
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
	emit        func(model.Chunk) error
	recordUsage func(model.TokenUsage)
	recordCites func([]model.Citation)

	toolBlocks map[int]*toolBuffer
	// reasoningBlocks accumulates reasoning content per content index until stop.
	reasoningBlocks map[int]*reasoningBuffer

	toolNameMap map[string]string
	modelID     string
	modelClass  model.ModelClass
}

func newChunkProcessor(
	emit func(model.Chunk) error,
	recordUsage func(model.TokenUsage),
	recordCites func([]model.Citation),
	nameMap map[string]string,
	modelID string,
	modelClass model.ModelClass,
) *chunkProcessor {
	return &chunkProcessor{
		emit:            emit,
		recordUsage:     recordUsage,
		recordCites:     recordCites,
		toolBlocks:      make(map[int]*toolBuffer),
		reasoningBlocks: make(map[int]*reasoningBuffer),
		toolNameMap:     nameMap,
		modelID:         modelID,
		modelClass:      modelClass,
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
}

func (p *chunkProcessor) handleMessageStop(ev *brtypes.ConverseStreamOutputMemberMessageStop) error {
	chunk := model.Chunk{Type: model.ChunkTypeStop}
	if ev.Value.StopReason != "" {
		chunk.StopReason = string(ev.Value.StopReason)
	}
	p.toolBlocks = make(map[int]*toolBuffer)
	p.reasoningBlocks = make(map[int]*reasoningBuffer)
	return p.emit(chunk)
}

func (p *chunkProcessor) handleMetadata(ev *brtypes.ConverseStreamOutputMemberMetadata) error {
	usage := bedrockStreamUsage(ev.Value.Usage, p.modelID, p.modelClass)
	if usage == nil {
		return nil
	}
	if p.recordUsage != nil {
		p.recordUsage(*usage)
	}
	return p.emit(model.Chunk{Type: model.ChunkTypeUsage, UsageDelta: usage})
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
		return p.recordCitationDelta(delta)
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
	return p.emit(model.Chunk{
		Type: model.ChunkTypeText,
		Message: &model.Message{
			Role:  "assistant",
			Parts: []model.Part{model.TextPart{Text: text}},
			Meta:  map[string]any{"content_index": idx},
		},
	})
}

func (p *chunkProcessor) recordCitationDelta(delta *brtypes.ContentBlockDeltaMemberCitation) error {
	if p.recordCites == nil {
		return nil
	}
	citation := translateCitationDelta(delta.Value)
	if citation.Title == "" && citation.Source == "" && citation.Location == (model.CitationLocation{}) && len(citation.SourceContent) == 0 {
		return nil
	}
	p.recordCites([]model.Citation{citation})
	return nil
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
	return p.emit(model.Chunk{
		Type:     model.ChunkTypeThinking,
		Thinking: text,
		Message: &model.Message{
			Role: "assistant",
			Parts: []model.Part{model.ThinkingPart{
				Text:  text,
				Index: idx,
				Final: false,
			}},
		},
	})
}

func (p *chunkProcessor) emitToolUseDelta(idx int, delta *brtypes.ContentBlockDeltaMemberToolUse) error {
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
	return p.emit(model.Chunk{
		Type: model.ChunkTypeToolCallDelta,
		ToolCallDelta: &model.ToolCallDelta{
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
	chunk := model.Chunk{
		Type: model.ChunkTypeThinking,
		Message: &model.Message{
			Role:  "assistant",
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

func (p *chunkProcessor) emitFinalToolCall(idx int) error {
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
	if joined == "" {
		return "{}"
	}
	return joined
}

func contentIndex(idx *int32) (int, error) {
	if idx == nil {
		return 0, fmt.Errorf("bedrock: content block index missing")
	}
	return int(*idx), nil
}

func decodeToolPayload(raw string) rawjson.Message {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return rawjson.Message([]byte("{}"))
	}
	data := []byte(trimmed)
	if !json.Valid(data) {
		// Tool payload fragments come from a model stream boundary and can be
		// truncated when the provider stops on max_tokens. Return an empty object
		// so tool schema validation can produce a structured tool error.
		return rawjson.Message([]byte("{}"))
	}
	return rawjson.Message(data)
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
