package codex

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type wireEvent struct {
	Type         string        `json:"type"`
	Code         string        `json:"code"`
	Message      string        `json:"message"`
	Status       int           `json:"status"`
	RequestID    string        `json:"request_id"`
	Delta        string        `json:"delta"`
	Text         *string       `json:"text"`
	Refusal      *string       `json:"refusal"`
	Arguments    *string       `json:"arguments"`
	ItemID       string        `json:"item_id"`
	OutputIndex  *int          `json:"output_index"`
	ContentIndex *int          `json:"content_index"`
	SummaryIndex *int          `json:"summary_index"`
	Item         wireItem      `json:"item"`
	Part         wireContent   `json:"part"`
	Response     *wireResponse `json:"response"`
	Error        wireError     `json:"error"`
}

type wireResponse struct {
	ID                string                `json:"id"`
	Status            string                `json:"status"`
	Output            []wireItem            `json:"output"`
	Usage             wireUsage             `json:"usage"`
	IncompleteDetails wireIncompleteDetails `json:"incomplete_details"`
	Error             wireError             `json:"error"`
}

type wireIncompleteDetails struct {
	Reason string `json:"reason"`
}

type wireUsage struct {
	InputTokens        int              `json:"input_tokens"`
	OutputTokens       int              `json:"output_tokens"`
	TotalTokens        int              `json:"total_tokens"`
	InputTokensDetails wireInputDetails `json:"input_tokens_details"`
}

type wireInputDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type wireError struct {
	Code      string `json:"code"`
	Type      string `json:"type"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id"`
}

type wireItem struct {
	ID               string        `json:"id"`
	Type             string        `json:"type"`
	Name             string        `json:"name"`
	CallID           string        `json:"call_id"`
	Arguments        *string       `json:"arguments"`
	EncryptedContent *string       `json:"encrypted_content"`
	Content          []wireContent `json:"content"`
	Summary          []wireContent `json:"summary"`
}

type wireContent struct {
	Type    string  `json:"type"`
	Text    *string `json:"text"`
	Refusal *string `json:"refusal"`
}

type reasoningPartKey struct {
	kind  string
	index int
}

type itemState struct {
	item            wireItem
	key             string
	outputIndex     int
	text            map[int]*strings.Builder
	textKinds       map[int]string
	textFinal       map[int]bool
	thinking        map[reasoningPartKey]*strings.Builder
	thinkingFinal   map[reasoningPartKey]bool
	thinkingIndexes map[reasoningPartKey]int
	arguments       strings.Builder
	toolFinal       bool
	redactedFinal   bool
	completed       bool
}

type codexStreamState struct {
	codec             *openaitoolname.Codec
	modelID           string
	modelClass        model.ModelClass
	credentials       Credentials
	items             map[string]*itemState
	responseID        string
	nextThinkingIndex int
	terminal          bool
}

type codexStreamer struct {
	ctx        context.Context
	source     eventSource
	fallback   func() (eventSource, error)
	state      codexStreamState
	builder    model.StreamResponseBuilder
	queue      []model.Chunk
	emitted    bool
	eof        bool
	fatalErr   error
	response   *model.Response
	cleanupErr error
	closeOnce  sync.Once
}

func (c *Client) startStream(ctx context.Context, built *builtRequest, credentials Credentials) (model.Streamer, error) {
	newStreamer := func(source eventSource, fallback func() (eventSource, error)) model.Streamer {
		return &codexStreamer{
			ctx:      ctx,
			source:   source,
			fallback: fallback,
			state: codexStreamState{
				codec:       built.codec,
				modelID:     built.modelID,
				modelClass:  built.modelClass,
				credentials: credentials,
				items:       make(map[string]*itemState),
			},
		}
	}
	switch c.transport {
	case TransportSSE:
		source, err := c.startSSE(ctx, built, credentials)
		if err != nil {
			return nil, normalizeStartError(ctx, "sse", err)
		}
		return newStreamer(source, nil), nil
	case TransportWebSocket:
		source, err := c.startWebSocket(ctx, built, credentials)
		if err != nil {
			return nil, normalizeStartError(ctx, "websocket", err)
		}
		return newStreamer(source, nil), nil
	case TransportAuto:
		source, err := c.startWebSocket(ctx, built, credentials)
		if err != nil {
			if !fallbackable(err) || ctx.Err() != nil {
				return nil, normalizeStartError(ctx, "websocket", err)
			}
			source, err = c.startSSE(ctx, built, credentials)
			if err != nil {
				return nil, normalizeStartError(ctx, "sse", err)
			}
			return newStreamer(source, nil), nil
		}
		fallback := func() (eventSource, error) {
			return c.startSSE(ctx, built, credentials)
		}
		return newStreamer(source, fallback), nil
	}
	return nil, fmt.Errorf("codex: invalid transport %d", c.transport)
}

func (s *codexStreamState) resetAttempt() {
	s.items = make(map[string]*itemState)
	s.responseID = ""
	s.nextThinkingIndex = 0
	s.terminal = false
}
func normalizeStartError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, ok := model.AsProviderError(err); ok || errors.Is(err, model.ErrRateLimited) {
		return err
	}
	return normalizeTransportError(operation, err)
}

func (s *codexStreamer) Recv() (model.Chunk, error) { //nolint:gocyclo,maintidx // Receive keeps fallback and EOF ownership in one state transition.
	if s.fatalErr != nil {
		return nil, s.fatalErr
	}
	if len(s.queue) > 0 {
		chunk := s.queue[0]
		s.queue = s.queue[1:]
		s.emitted = true
		return chunk, nil
	}
	if s.eof {
		return nil, io.EOF
	}
	if s.state.terminal {
		s.response = s.builder.Response()
		s.eof = true
		return nil, io.EOF
	}
	for {
		data, err := s.source.Next()
		if err != nil {
			if s.fallback != nil && !s.emitted && fallbackable(err) && s.ctx.Err() == nil {
				s.cleanupErr = errors.Join(s.cleanupErr, s.source.Close())
				source, fallbackErr := s.fallback()
				s.fallback = nil
				if fallbackErr == nil {
					s.source = source
					s.state.resetAttempt()
					continue
				}
				return s.fail(normalizeStartError(s.ctx, "sse fallback", fallbackErr))
			}
			if s.ctx.Err() != nil {
				return s.fail(s.ctx.Err())
			}
			if _, ok := model.AsProviderError(err); ok || errors.Is(err, model.ErrRateLimited) {
				return s.fail(err)
			}
			return s.fail(normalizeTransportError("stream receive", err))
		}
		chunks, err := s.state.process(data)
		if err != nil {
			if _, ok := model.AsProviderError(err); ok || errors.Is(err, model.ErrRateLimited) {
				return s.fail(err)
			}
			return s.fail(invalidStreamError())
		}
		for _, chunk := range chunks {
			if err := s.builder.Add(chunk); err != nil {
				return s.fail(fmt.Errorf("codex: build stream response: %w", err))
			}
			s.queue = append(s.queue, chunk)
		}
		if len(s.queue) > 0 {
			chunk := s.queue[0]
			s.queue = s.queue[1:]
			s.emitted = true
			return chunk, nil
		}
		if s.state.terminal {
			s.response = s.builder.Response()
			s.eof = true
			return nil, io.EOF
		}
	}
}

func (s *codexStreamer) fail(err error) (model.Chunk, error) {
	s.fallback = nil
	s.fatalErr = err
	return nil, err
}

func (s *codexStreamer) Close() error {
	s.closeOnce.Do(func() {
		s.cleanupErr = errors.Join(s.cleanupErr, s.source.Close())
	})
	return s.cleanupErr
}

func (s *codexStreamer) Response() *model.Response {
	if !s.eof {
		return nil
	}
	return s.response
}

func (s *codexStreamState) process(data []byte) ([]model.Chunk, error) { //nolint:funlen,gocyclo,maintidx // Protocol event dispatch is deliberately centralized.
	if string(data) == "[DONE]" {
		return nil, errors.New("codex: unexpected SSE [DONE] before terminal response")
	}
	var event wireEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("codex: malformed stream event: %w", err)
	}
	if event.Type == "" {
		return nil, errors.New("codex: stream event is missing type")
	}
	switch event.Type {
	case "response.created":
		return nil, s.acceptProgress(event.Response, "queued", "in_progress")
	case "response.in_progress":
		return nil, s.acceptProgress(event.Response, "in_progress")
	case "response.queued":
		return nil, s.acceptProgress(event.Response, "queued")
	case "codex.rate_limits", "codex.response.metadata", "responsesapi.websocket_timing":
		return nil, nil
	case "response.output_item.added":
		return nil, s.addItem(event)
	case "response.output_text.delta", "response.refusal.delta":
		item, err := s.requireItemType(event, wireMessageItem)
		if err != nil {
			return nil, err
		}
		if err := requireMutableItem(event.Type, item); err != nil {
			return nil, err
		}
		index, err := requiredStreamIndex("content", event.ContentIndex)
		if err != nil {
			return nil, err
		}
		if event.Delta == "" {
			return nil, errors.New("codex: text delta is empty")
		}
		kind := textEventKind(event.Type)
		if item.textFinal[index] {
			return nil, fmt.Errorf("codex: %s follows completed content for item %q", event.Type, item.key)
		}
		builder, err := item.textBuilder(index, kind)
		if err != nil {
			return nil, err
		}
		builder.WriteString(event.Delta)
		return []model.Chunk{textChunk(event.Delta)}, nil
	case "response.output_text.done", "response.refusal.done":
		item, err := s.requireItemType(event, wireMessageItem)
		if err != nil {
			return nil, err
		}
		index, err := requiredStreamIndex("content", event.ContentIndex)
		if err != nil {
			return nil, err
		}
		kind := textEventKind(event.Type)
		complete := event.Text
		if kind == wireRefusal {
			complete = event.Refusal
		}
		value, err := requiredWireString(kind, complete)
		if err != nil {
			return nil, err
		}
		return s.finishText(item, index, kind, value)
	case "response.reasoning_summary_text.delta":
		return s.appendReasoning(event, wireSummary, event.SummaryIndex)
	case "response.reasoning_text.delta":
		return s.appendReasoning(event, wireContentKey, event.ContentIndex)
	case "response.reasoning_summary_text.done":
		return s.finishReasoningEvent(event, wireSummary, event.SummaryIndex)
	case "response.reasoning_text.done":
		return s.finishReasoningEvent(event, wireContentKey, event.ContentIndex)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		return s.handleReasoningPart(event)
	case "response.content_part.added", "response.content_part.done":
		return s.handleMessagePart(event)
	case "response.function_call_arguments.delta":
		item, err := s.requireItemType(event, wireFunctionCallItem)
		if err != nil {
			return nil, err
		}
		if err := requireMutableItem(event.Type, item); err != nil {
			return nil, err
		}
		if item.toolFinal {
			return nil, fmt.Errorf("codex: %s follows completed arguments for item %q", event.Type, item.key)
		}
		if event.Delta == "" {
			return nil, errors.New("codex: function arguments delta is empty")
		}
		if item.item.Name == "" || item.item.CallID == "" {
			return nil, errors.New("codex: function delta is missing tool name or call id")
		}
		item.arguments.WriteString(event.Delta)
		return []model.Chunk{model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{
			Name: tools.Ident(s.codec.CanonicalName(item.item.Name)), ID: item.item.CallID, Delta: event.Delta,
		}}}, nil
	case "response.function_call_arguments.done":
		item, err := s.requireItemType(event, wireFunctionCallItem)
		if err != nil {
			return nil, err
		}
		arguments, err := requiredWireString("function arguments", event.Arguments)
		if err != nil {
			return nil, err
		}
		return s.finishTool(item, arguments)
	case "response.output_item.done":
		return s.finishItem(event)
	case "response.completed", "response.done", wireIncompleteEvent:
		if event.Response == nil {
			return nil, errors.New("codex: terminal event is missing response")
		}
		return s.finishResponse(event.Type, *event.Response)
	case "response.failed":
		if event.Response == nil {
			return nil, errors.New("codex: failed event is missing response")
		}
		if event.Response.ID == "" || event.Response.Status != "failed" || s.responseConflict(event.Response.ID) {
			return nil, errors.New("codex: failed response has missing or conflicting identity")
		}
		return nil, s.providerEventError(event.Response.Error)
	case "error":
		value := event.Error
		if value.Code == "" {
			value.Code = event.Code
		}
		if value.Status == 0 {
			value.Status = event.Status
		}
		if value.RequestID == "" {
			value.RequestID = event.RequestID
		}
		return nil, s.providerEventError(value)
	default:
		return nil, fmt.Errorf("codex: unsupported stream event type %q", event.Type)
	}
}

func (s *codexStreamState) addItem(event wireEvent) error {
	if event.OutputIndex == nil {
		return errors.New("codex: output item addition is missing output index")
	}
	if *event.OutputIndex < 0 {
		return errors.New("codex: output item addition has negative output index")
	}
	if err := validateItemType(event.Item.Type); err != nil {
		return err
	}
	index := *event.OutputIndex
	indexKey := eventKey("", index)
	if _, exists := s.items[indexKey]; exists {
		return fmt.Errorf("codex: duplicate output item %q", indexKey)
	}
	if event.Item.ID != "" {
		if _, exists := s.items[event.Item.ID]; exists {
			return fmt.Errorf("codex: duplicate output item %q", event.Item.ID)
		}
	}
	item := newItemState(event.Item, index)
	s.items[indexKey] = item
	if event.Item.ID != "" {
		s.items[event.Item.ID] = item
	}
	return nil
}

func newItemState(item wireItem, outputIndex int) *itemState {
	return &itemState{
		item: item, key: eventKey(item.ID, outputIndex), outputIndex: outputIndex,
		text: make(map[int]*strings.Builder), textKinds: make(map[int]string), textFinal: make(map[int]bool),
		thinking: make(map[reasoningPartKey]*strings.Builder), thinkingFinal: make(map[reasoningPartKey]bool),
		thinkingIndexes: make(map[reasoningPartKey]int),
	}
}

func validateItemType(itemType string) error {
	if itemType == "" {
		return errors.New("codex: output item is missing type")
	}
	switch itemType {
	case wireMessageItem, wireReasoningItem, wireFunctionCallItem:
		return nil
	default:
		return fmt.Errorf("codex: unsupported output item type %q", itemType)
	}
}

func (s *codexStreamState) requireItem(event wireEvent) (*itemState, error) {
	if event.OutputIndex == nil {
		return nil, errors.New("codex: event is missing output index")
	}
	if *event.OutputIndex < 0 {
		return nil, errors.New("codex: event has negative output index")
	}
	var byID, byIndex *itemState
	if event.ItemID != "" {
		byID = s.items[event.ItemID]
		if byID == nil {
			return nil, fmt.Errorf("codex: event references unknown output item %q", event.ItemID)
		}
	}
	if event.OutputIndex != nil {
		key := eventKey("", *event.OutputIndex)
		byIndex = s.items[key]
		if byIndex == nil {
			return nil, fmt.Errorf("codex: event references unknown output item %q", key)
		}
	}
	if byID != nil && byIndex != nil && byID != byIndex {
		return nil, errors.New("codex: event has conflicting output item identity")
	}
	if byID != nil {
		return byID, nil
	}
	return byIndex, nil
}

func (s *codexStreamState) requireItemType(event wireEvent, expected string) (*itemState, error) {
	item, err := s.requireItem(event)
	if err != nil {
		return nil, err
	}
	if item.item.Type != expected {
		return nil, fmt.Errorf("codex: event for %q item used %q event family", item.item.Type, expected)
	}
	return item, nil
}

func requireMutableItem(eventType string, item *itemState) error {
	if item.completed {
		return fmt.Errorf("codex: %s targets completed output item %q", eventType, item.key)
	}
	return nil
}

func (s *codexStreamState) finishItem(event wireEvent) ([]model.Chunk, error) {
	if event.Item.ID == "" || event.Item.Type == "" || event.OutputIndex == nil {
		return nil, errors.New("codex: completed output item is missing identity, output index, or type")
	}
	if err := validateItemType(event.Item.Type); err != nil {
		return nil, err
	}
	if err := validateCompletedItem(event.Item); err != nil {
		return nil, err
	}
	item, err := s.reconcileItem(event.Item, event.OutputIndex)
	if err != nil {
		return nil, err
	}
	if item.completed {
		if !equalWireItem(item.item, event.Item) {
			return nil, fmt.Errorf("codex: repeated output item completion changes item %q", item.key)
		}
		return nil, nil
	}
	item.item = event.Item
	chunks, err := s.finalizeItem(item)
	if err != nil {
		return nil, err
	}
	item.completed = true
	return chunks, nil
}

func (s *codexStreamState) reconcileItem(incoming wireItem, outputIndex *int) (*itemState, error) {
	if outputIndex != nil && *outputIndex < 0 {
		return nil, errors.New("codex: completed output item has negative output index")
	}
	item, err := s.existingItem(incoming.ID, outputIndex)
	if err != nil {
		return nil, err
	}
	if item == nil {
		index := -1
		if outputIndex != nil {
			index = *outputIndex
		}
		item = newItemState(incoming, index)
	} else if !compatibleWireItem(item.item, incoming) {
		return nil, errors.New("codex: conflicting completed output item")
	}
	if err := s.bindItem(item, incoming.ID, outputIndex); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *codexStreamState) existingItem(id string, outputIndex *int) (*itemState, error) {
	var byID, byIndex *itemState
	if id != "" {
		byID = s.items[id]
	}
	if outputIndex != nil {
		byIndex = s.items[eventKey("", *outputIndex)]
	}
	if byID != nil && byIndex != nil && byID != byIndex {
		return nil, errors.New("codex: conflicting completed output item identity")
	}
	if byID != nil {
		return byID, nil
	}
	return byIndex, nil
}

func (s *codexStreamState) bindItem(item *itemState, id string, outputIndex *int) error {
	if id != "" {
		if existing := s.items[id]; existing != nil && existing != item {
			return errors.New("codex: conflicting completed output item identity")
		}
		s.items[id] = item
	}
	if outputIndex == nil {
		return nil
	}
	if item.outputIndex >= 0 && item.outputIndex != *outputIndex {
		return errors.New("codex: conflicting completed output item index")
	}
	key := eventKey("", *outputIndex)
	if existing := s.items[key]; existing != nil && existing != item {
		return errors.New("codex: conflicting completed output item identity")
	}
	item.outputIndex = *outputIndex
	s.items[key] = item
	return nil
}

func (s *codexStreamState) finalizeItem(item *itemState) ([]model.Chunk, error) {
	switch item.item.Type {
	case wireMessageItem:
		return s.finalizeMessage(item)
	case wireReasoningItem:
		return s.finalizeReasoning(item)
	case wireFunctionCallItem:
		arguments, err := requiredWireString("function arguments", item.item.Arguments)
		if err != nil {
			return nil, err
		}
		return s.finishTool(item, arguments)
	default:
		return nil, fmt.Errorf("codex: unsupported output item type %q", item.item.Type)
	}
}

func (s *codexStreamState) finalizeMessage(item *itemState) ([]model.Chunk, error) {
	var chunks []model.Chunk
	for index, content := range item.item.Content {
		var value *string
		switch content.Type {
		case wireOutputText:
			value = content.Text
		case wireRefusal:
			value = content.Refusal
		default:
			return nil, fmt.Errorf("codex: unsupported message content type %q", content.Type)
		}
		complete, err := requiredWireString(content.Type, value)
		if err != nil {
			return nil, err
		}
		finished, err := s.finishText(item, index, content.Type, complete)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, finished...)
	}
	for index := range item.text {
		if !item.textFinal[index] {
			return nil, fmt.Errorf("codex: message item %q completed with unfinished content", item.key)
		}
	}
	return chunks, nil
}

func (s *codexStreamState) finalizeReasoning(item *itemState) ([]model.Chunk, error) {
	chunks := make([]model.Chunk, 0, len(item.item.Summary)+len(item.item.Content)+1)
	for index, summary := range item.item.Summary {
		if summary.Type != "summary_text" {
			return nil, fmt.Errorf("codex: unsupported reasoning summary type %q", summary.Type)
		}
		complete, err := requiredWireString("reasoning summary", summary.Text)
		if err != nil {
			return nil, err
		}
		finished, err := s.finishThinking(item, reasoningPartKey{kind: wireSummary, index: index}, complete)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, finished...)
	}
	for index, content := range item.item.Content {
		if content.Type != "reasoning_text" {
			return nil, fmt.Errorf("codex: unsupported reasoning content type %q", content.Type)
		}
		complete, err := requiredWireString("reasoning content", content.Text)
		if err != nil {
			return nil, err
		}
		finished, err := s.finishThinking(item, reasoningPartKey{kind: wireContentKey, index: index}, complete)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, finished...)
	}
	if item.item.EncryptedContent != nil && *item.item.EncryptedContent != "" && !item.redactedFinal {
		key := reasoningPartKey{kind: "encrypted"}
		chunks = append(chunks, thinkingChunk("", s.thinkingIndex(item, key), true, []byte(*item.item.EncryptedContent)))
		item.redactedFinal = true
	}
	for key := range item.thinking {
		if !item.thinkingFinal[key] {
			return nil, fmt.Errorf("codex: reasoning item %q completed with unfinished content", item.key)
		}
	}
	return chunks, nil
}

func textEventKind(eventType string) string {
	if strings.HasPrefix(eventType, "response.refusal.") {
		return wireRefusal
	}
	return wireOutputText
}

func requiredStreamIndex(name string, value *int) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("codex: stream event is missing %s index", name)
	}
	if *value < 0 {
		return 0, fmt.Errorf("codex: stream event has negative %s index", name)
	}
	return *value, nil
}

func requiredWireString(name string, value *string) (string, error) {
	if value == nil {
		return "", fmt.Errorf("codex: stream event is missing %s", name)
	}
	return *value, nil
}

func (s *codexStreamState) handleMessagePart(event wireEvent) ([]model.Chunk, error) {
	item, err := s.requireItemType(event, wireMessageItem)
	if err != nil {
		return nil, err
	}
	index, err := requiredStreamIndex("content", event.ContentIndex)
	if err != nil {
		return nil, err
	}
	if event.Part.Type != wireOutputText && event.Part.Type != wireRefusal {
		return nil, fmt.Errorf("codex: unsupported message content type %q", event.Part.Type)
	}
	if strings.HasSuffix(event.Type, ".added") {
		if err := requireMutableItem(event.Type, item); err != nil {
			return nil, err
		}
		if item.textFinal[index] {
			return nil, fmt.Errorf("codex: %s follows completed content for item %q", event.Type, item.key)
		}
		_, err = item.textBuilder(index, event.Part.Type)
		return nil, err
	}
	value := event.Part.Text
	if event.Part.Type == wireRefusal {
		value = event.Part.Refusal
	}
	complete, err := requiredWireString(event.Part.Type, value)
	if err != nil {
		return nil, err
	}
	return s.finishText(item, index, event.Part.Type, complete)
}

func (s *codexStreamState) handleReasoningPart(event wireEvent) ([]model.Chunk, error) {
	item, err := s.requireItemType(event, wireReasoningItem)
	if err != nil {
		return nil, err
	}
	index, err := requiredStreamIndex(wireSummary, event.SummaryIndex)
	if err != nil {
		return nil, err
	}
	if event.Part.Type != "summary_text" {
		return nil, fmt.Errorf("codex: unsupported reasoning summary type %q", event.Part.Type)
	}
	key := reasoningPartKey{kind: wireSummary, index: index}
	if strings.HasSuffix(event.Type, ".added") {
		if err := requireMutableItem(event.Type, item); err != nil {
			return nil, err
		}
		if item.thinkingFinal[key] {
			return nil, fmt.Errorf("codex: %s follows completed reasoning for item %q", event.Type, item.key)
		}
		item.reasoningBuilder(key)
		return nil, nil
	}
	complete, err := requiredWireString("reasoning summary", event.Part.Text)
	if err != nil {
		return nil, err
	}
	return s.finishThinking(item, key, complete)
}

func (i *itemState) textBuilder(index int, kind string) (*strings.Builder, error) {
	if existing := i.textKinds[index]; existing != "" && existing != kind {
		return nil, fmt.Errorf("codex: conflicting content type for item %q index %d", i.key, index)
	}
	i.textKinds[index] = kind
	builder := i.text[index]
	if builder == nil {
		builder = &strings.Builder{}
		i.text[index] = builder
	}
	return builder, nil
}

func (s *codexStreamState) finishText(item *itemState, index int, kind, complete string) ([]model.Chunk, error) {
	if item.completed && !item.textFinal[index] {
		return nil, fmt.Errorf("codex: completed output item %q received new text completion", item.key)
	}
	builder, err := item.textBuilder(index, kind)
	if err != nil {
		return nil, err
	}
	if item.textFinal[index] {
		if complete != builder.String() {
			return nil, fmt.Errorf("codex: conflicting completed text for item %q", item.key)
		}
		return nil, nil
	}
	prefix := builder.String()
	if !strings.HasPrefix(complete, prefix) {
		return nil, fmt.Errorf("codex: completed text conflicts with deltas for item %q", item.key)
	}
	item.textFinal[index] = true
	if suffix := strings.TrimPrefix(complete, prefix); suffix != "" {
		builder.WriteString(suffix)
		return []model.Chunk{textChunk(suffix)}, nil
	}
	return nil, nil
}

func (s *codexStreamState) appendReasoning(event wireEvent, kind string, indexValue *int) ([]model.Chunk, error) {
	item, err := s.requireItemType(event, wireReasoningItem)
	if err != nil {
		return nil, err
	}
	index, err := requiredStreamIndex(kind, indexValue)
	if err != nil {
		return nil, err
	}
	if event.Delta == "" {
		return nil, errors.New("codex: reasoning delta is empty")
	}
	key := reasoningPartKey{kind: kind, index: index}
	if err := requireMutableItem(event.Type, item); err != nil {
		return nil, err
	}
	if item.thinkingFinal[key] {
		return nil, fmt.Errorf("codex: %s follows completed reasoning for item %q", event.Type, item.key)
	}
	builder := item.reasoningBuilder(key)
	builder.WriteString(event.Delta)
	return []model.Chunk{thinkingChunk(event.Delta, s.thinkingIndex(item, key), false, nil)}, nil
}

func (s *codexStreamState) finishReasoningEvent(event wireEvent, kind string, indexValue *int) ([]model.Chunk, error) {
	item, err := s.requireItemType(event, wireReasoningItem)
	if err != nil {
		return nil, err
	}
	index, err := requiredStreamIndex(kind, indexValue)
	if err != nil {
		return nil, err
	}
	complete, err := requiredWireString("reasoning text", event.Text)
	if err != nil {
		return nil, err
	}
	return s.finishThinking(item, reasoningPartKey{kind: kind, index: index}, complete)
}

func (s *codexStreamState) finishThinking(item *itemState, key reasoningPartKey, complete string) ([]model.Chunk, error) {
	if item.completed && !item.thinkingFinal[key] {
		return nil, fmt.Errorf("codex: completed output item %q received new reasoning completion", item.key)
	}
	if item.thinkingFinal[key] {
		if complete != item.reasoningBuilder(key).String() {
			return nil, fmt.Errorf("codex: conflicting completed reasoning for item %q", item.key)
		}
		return nil, nil
	}
	builder := item.reasoningBuilder(key)
	prefix := builder.String()
	if !strings.HasPrefix(complete, prefix) {
		return nil, fmt.Errorf("codex: completed reasoning conflicts with deltas for item %q", item.key)
	}
	if suffix := strings.TrimPrefix(complete, prefix); suffix != "" {
		builder.WriteString(suffix)
	}
	item.thinkingFinal[key] = true
	if builder.Len() == 0 {
		return nil, nil
	}
	return []model.Chunk{thinkingChunk(builder.String(), s.thinkingIndex(item, key), true, nil)}, nil
}

func (s *codexStreamState) thinkingIndex(item *itemState, key reasoningPartKey) int {
	if index, ok := item.thinkingIndexes[key]; ok {
		return index
	}
	index := s.nextThinkingIndex
	s.nextThinkingIndex++
	item.thinkingIndexes[key] = index
	return index
}

func (s *codexStreamState) finishTool(item *itemState, complete string) ([]model.Chunk, error) {
	if item.completed && !item.toolFinal {
		return nil, fmt.Errorf("codex: completed output item %q received new tool completion", item.key)
	}
	if item.toolFinal {
		if complete != item.arguments.String() {
			return nil, fmt.Errorf("codex: conflicting completed arguments for item %q", item.key)
		}
		return nil, nil
	}
	if item.item.Name == "" || item.item.CallID == "" {
		return nil, errors.New("codex: completed function call is missing name or call id")
	}
	prefix := item.arguments.String()
	if !strings.HasPrefix(complete, prefix) {
		return nil, fmt.Errorf("codex: completed function arguments conflict for item %q", item.key)
	}
	if suffix := strings.TrimPrefix(complete, prefix); suffix != "" {
		item.arguments.WriteString(suffix)
	}
	arguments := []byte(item.arguments.String())
	if !jsonObject(arguments) {
		return nil, fmt.Errorf("codex: function call %q has malformed object arguments", item.item.CallID)
	}
	item.toolFinal = true
	return []model.Chunk{model.ToolCallChunk{ToolCall: model.ToolCall{
		Name: tools.Ident(s.codec.CanonicalName(item.item.Name)), Payload: rawjson.Message(append([]byte(nil), arguments...)), ID: item.item.CallID,
	}}}, nil
}

func (s *codexStreamState) finishResponse(eventType string, response wireResponse) ([]model.Chunk, error) { //nolint:maintidx // Terminal reconciliation validates one atomic state transition.
	if s.terminal {
		return nil, errors.New("codex: duplicate terminal response")
	}
	expectedStatus := "completed"
	if eventType == wireIncompleteEvent {
		expectedStatus = "incomplete"
	}
	if response.ID == "" || response.Status != expectedStatus || s.responseConflict(response.ID) {
		return nil, errors.New("codex: terminal response has missing or conflicting identity")
	}
	if s.responseID == "" {
		s.responseID = response.ID
	}
	chunks := make([]model.Chunk, 0)
	finalizedItems := make(map[*itemState]struct{}, len(response.Output))
	for index, output := range response.Output {
		if output.ID == "" || output.Type == "" {
			return nil, errors.New("codex: terminal output item is missing identity or type")
		}
		if err := validateItemType(output.Type); err != nil {
			return nil, err
		}
		if err := validateCompletedItem(output); err != nil {
			return nil, err
		}
		item, err := s.reconcileItem(output, &index)
		if err != nil {
			return nil, err
		}
		if _, duplicate := finalizedItems[item]; duplicate {
			return nil, errors.New("codex: terminal response contains a duplicate output item")
		}
		finalizedItems[item] = struct{}{}
		if item.completed && !equalWireItem(item.item, output) {
			return nil, fmt.Errorf("codex: terminal response changes completed output item %q", item.key)
		}
		item.item = output
		finalized, err := s.finalizeItem(item)
		if err != nil {
			return nil, err
		}
		item.completed = true
		chunks = append(chunks, finalized...)
	}
	seen := make(map[*itemState]struct{})
	for _, item := range s.items {
		if _, duplicate := seen[item]; duplicate {
			continue
		}
		seen[item] = struct{}{}
		if !item.completed {
			return nil, errors.New("codex: terminal response omitted an unfinished output item")
		}
	}
	usage, err := normalizeUsage(response.Usage, s.modelID, s.modelClass)
	if err != nil {
		return nil, err
	}
	chunks = append(chunks, model.UsageChunk{Usage: usage})
	outputLimited := eventType == wireIncompleteEvent && response.IncompleteDetails.Reason == maxOutputTokensReason
	chunks = append(chunks, model.StopChunk{Reason: response.Status, OutputLimited: outputLimited})
	s.terminal = true
	return chunks, nil
}

func (s *codexStreamState) acceptProgress(response *wireResponse, statuses ...string) error {
	if response == nil || response.ID == "" || s.responseConflict(response.ID) {
		return errors.New("codex: progress response has missing or conflicting identity")
	}
	validStatus := false
	for _, status := range statuses {
		if response.Status == status {
			validStatus = true
			break
		}
	}
	if !validStatus {
		return errors.New("codex: progress response has conflicting status")
	}
	s.responseID = response.ID
	return nil
}

func (s *codexStreamState) responseConflict(id string) bool {
	return s.responseID != "" && s.responseID != id
}

func normalizeUsage(usage wireUsage, modelID string, modelClass model.ModelClass) (model.TokenUsage, error) {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.InputTokensDetails.CachedTokens < 0 {
		return model.TokenUsage{}, errors.New("codex: token usage must not be negative")
	}
	if usage.InputTokensDetails.CachedTokens > usage.InputTokens {
		return model.TokenUsage{}, errors.New("codex: cached input tokens exceed input tokens")
	}
	if usage.InputTokens > math.MaxInt-usage.OutputTokens {
		return model.TokenUsage{}, errors.New("codex: token usage total overflows")
	}
	result := model.TokenUsage{
		Model: modelID, ModelClass: modelClass,
		InputTokens:     usage.InputTokens - usage.InputTokensDetails.CachedTokens,
		OutputTokens:    usage.OutputTokens,
		CacheReadTokens: usage.InputTokensDetails.CachedTokens,
	}
	if usage.TotalTokens != 0 {
		result.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return result, nil
}

func (s *codexStreamState) providerEventError(value wireError) error {
	code := value.Code
	if code == "" {
		code = value.Type
	}
	cause := errors.New("codex provider stream error")
	return normalizeStreamProviderError(value.Status, code, value.RequestID, cause, s.credentials)
}

func (i *itemState) reasoningBuilder(key reasoningPartKey) *strings.Builder {
	builder := i.thinking[key]
	if builder == nil {
		builder = &strings.Builder{}
		i.thinking[key] = builder
	}
	return builder
}

func eventKey(id string, outputIndex int) string {
	if id != "" {
		return id
	}
	return "#" + strconv.Itoa(outputIndex)
}

func invalidStreamError() error {
	return model.NewProviderError(
		codexProvider,
		"stream",
		0,
		model.ProviderErrorKindUnknown,
		"",
		"provider returned invalid stream data",
		"",
		false,
		errors.New("invalid Codex stream data"),
	)
}

func compatibleWireItem(existing, completed wireItem) bool {
	pairs := [][2]string{
		{existing.ID, completed.ID},
		{existing.Type, completed.Type},
		{existing.Name, completed.Name},
		{existing.CallID, completed.CallID},
	}
	for _, pair := range pairs {
		if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
			return false
		}
	}
	return true
}

func equalWireItem(left, right wireItem) bool {
	return left.ID == right.ID &&
		left.Type == right.Type &&
		left.Name == right.Name &&
		left.CallID == right.CallID &&
		equalOptionalString(left.Arguments, right.Arguments) &&
		equalOptionalString(left.EncryptedContent, right.EncryptedContent) &&
		equalWireContent(left.Content, right.Content) &&
		equalWireContent(left.Summary, right.Summary)
}

func equalWireContent(left, right []wireContent) bool {
	if (left == nil) != (right == nil) {
		return false
	}
	return slices.EqualFunc(left, right, func(a, b wireContent) bool {
		return a.Type == b.Type && equalOptionalString(a.Text, b.Text) && equalOptionalString(a.Refusal, b.Refusal)
	})
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func validateCompletedItem(item wireItem) error {
	switch item.Type {
	case wireMessageItem:
		if item.Content == nil {
			return errors.New("codex: completed message is missing content")
		}
	case wireReasoningItem:
		if item.Summary == nil && item.Content == nil && item.EncryptedContent == nil {
			return errors.New("codex: completed reasoning item is missing content")
		}
	case wireFunctionCallItem:
		if item.Name == "" || item.CallID == "" || item.Arguments == nil {
			return errors.New("codex: completed function call is missing name, call id, or arguments")
		}
	}
	return nil
}

func textChunk(text string) model.Chunk {
	return model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: text}}}}
}

func thinkingChunk(text string, index int, final bool, redacted []byte) model.Chunk {
	return model.ThinkingChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{
		Text: text, Redacted: append([]byte(nil), redacted...), Index: index, Final: final,
	}}}}
}
