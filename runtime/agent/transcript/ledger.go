// Package transcript provides a minimal, provider‑precise ledger that records
// the canonical conversation needed to rebuild provider payloads (e.g., Bedrock)
// without leaking provider SDK types into workflow state. The ledger stores
// only the essential, JSON‑friendly parts in the exact order in which they
// must be presented to the provider (thinking → tool_use → tool_result).
//
// Design goals (see AGENTS.md):
//   - Provider‑fidelity: preserve ordering/shape required by providers.
//   - Minimalism: store only what is needed to rebuild payloads exactly.
//   - Stateless API: pure methods that are safe for workflow replay.
//   - Provider‑agnostic at rest: convert to/from provider formats at edges.
package transcript

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

const (
	ledgerPartToolUse    = "tool_use"
	ledgerPartToolResult = "tool_result"
)

type (
	// Part is the canonical provider‑precise content fragment stored by the ledger.
	// Implementations must be one of ThinkingPart, TextPart, CitationsPart,
	// ToolUsePart, or ToolResultPart.
	Part interface {
		isPart()
	}

	// ThinkingPart carries provider reasoning. Exactly one variant must be set:
	// either signed plaintext (Text+Signature) or Redacted bytes. Index tracks
	// the provider content block index when available; Final indicates finalization.
	ThinkingPart struct {
		// Text is provider‑issued plaintext reasoning when available.
		Text string
		// Signature is the provider signature that authenticates Text.
		Signature string
		// Redacted holds provider opaque redacted reasoning bytes.
		Redacted []byte
		// Index is the provider content block index (negative if unknown).
		Index int
		// Final marks the finalization of this reasoning block.
		Final bool
	}

	// TextPart carries assistant or user visible text.
	TextPart struct {
		// Text is visible content intended for users.
		Text string
	}

	// CitationsPart carries assistant text and its provider citation metadata.
	CitationsPart struct {
		Text      string
		Citations []model.Citation
	}

	// ToolUsePart declares a tool invocation by the assistant.
	ToolUsePart struct {
		// ID is the provider tool_use identifier (for correlating tool_result).
		ID string
		// Name is the provider‑visible tool name (sanitized as required).
		Name string
		// Args are the JSON‑encodable tool arguments.
		Args any
	}

	// ToolResultPart communicates a tool result by the user back to the model,
	// correlated via ToolUseID.
	ToolResultPart struct {
		// ToolUseID correlates to a prior assistant ToolUsePart.ID.
		ToolUseID string
		// Content is the JSON‑encodable tool result payload.
		Content any
		// IsError indicates whether the tool invocation failed.
		IsError bool
	}

	// ToolResultSpec describes a single tool_result block for appending user
	// messages in a turn. It is used by AppendUserToolResults to build a single
	// user message containing multiple tool_result parts.
	ToolResultSpec struct {
		ToolUseID string
		Content   any
		IsError   bool
	}

	// Message groups ordered parts under a role for the provider conversation.
	Message struct {
		// Role is one of "assistant", "user", or "system".
		Role string
		// Parts must be in final provider order for this message.
		Parts []Part
		// Meta carries optional provider‑agnostic metadata for diagnostics.
		Meta map[string]any
	}

	// Ledger holds the ordered transcript for the current turn. It records only
	// the minimal set of parts required to rebuild provider payloads with exact
	// ordering (thinking → tool_use → tool_result). It is JSON‑friendly and safe
	// to store in workflow state.
	Ledger struct {
		messages []Message
		// current accumulates the pending assistant message so thinking/text/tool_use
		// can be coalesced before flushing to messages.
		current *Message
	}
)

// NewLedger constructs an empty Ledger ready to record a turn transcript.
func NewLedger() *Ledger {
	return &Ledger{
		messages: make([]Message, 0, 8),
	}
}

// FromModelMessages constructs a ledger initialized with the provided assistant
// messages. Only assistant-role messages contribute to the transcript; other
// roles are ignored.
func FromModelMessages(msgs []*model.Message) *Ledger {
	led := NewLedger()
	for _, msg := range msgs {
		led.AppendAssistantMessage(msg)
	}
	return led
}

// ValidateBedrock verifies critical Bedrock constraints when thinking is enabled:
//   - Any assistant message that contains tool_use must start with thinking.
//   - For each user message containing tool_result, the immediately prior assistant
//     message must contain at least as many tool_use blocks.
//
// It returns a descriptive error when a constraint is violated.
func ValidateBedrock(messages []*model.Message, thinkingEnabled bool) error {
	if len(messages) == 0 {
		return nil
	}
	for i, m := range messages {
		if !isAssistantMessage(m) {
			continue
		}
		if !messageHasToolUse(m) {
			continue
		}
		if err := validateAssistantToolUseMessage(messages, i, m, thinkingEnabled); err != nil {
			return err
		}
	}
	return nil
}

// BuildMessagesFromEvents reconstructs provider-ready messages from durable
// memory events by replaying them through a Ledger. It returns messages in the
// canonical provider order (assistant thinking -> text -> tool_use; user tool_result).
func BuildMessagesFromEvents(events []memory.Event) ([]*model.Message, error) {
	l := NewLedger()
	var pendingResults []ToolResultSpec
	var toolOrder []string
	for _, e := range events {
		nextResults, err := applyLedgerEvent(l, e, pendingResults, &toolOrder)
		if err != nil {
			return nil, err
		}
		pendingResults = nextResults
	}
	flushPendingToolResults(l, pendingResults, toolOrder)
	return l.BuildMessages(), nil
}

// UnmarshalJSON customizes Message decoding so that Parts (which contain
// interface implementations) can be reconstructed from stored JSON.
func (m *Message) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role  string           `json:"Role"`  //nolint:tagliatelle
		Parts []jsontext.Value `json:"Parts"` //nolint:tagliatelle
		Meta  map[string]any   `json:"Meta"`  //nolint:tagliatelle
	}
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	m.Role = tmp.Role
	m.Meta = tmp.Meta
	if len(tmp.Parts) == 0 {
		m.Parts = nil
		return nil
	}
	m.Parts = make([]Part, 0, len(tmp.Parts))
	for i, raw := range tmp.Parts {
		part, err := decodeLedgerPart(raw)
		if err != nil {
			return fmt.Errorf("decode parts[%d]: %w", i, err)
		}
		m.Parts = append(m.Parts, part)
	}
	return nil
}

// MarshalJSON serializes committed messages and the pending assistant message
// so a Ledger can be stored in workflow state without losing turn progress.
func (l *Ledger) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	type ledgerJSON struct {
		Messages []Message `json:"messages"`
		Current  *Message  `json:"current,omitempty"`
	}
	return json.Marshal(ledgerJSON{
		Messages: l.messages,
		Current:  l.current,
	}, json.FormatNilMapAsNull(true), json.FormatNilSliceAsNull(true))
}

// UnmarshalJSON restores committed messages and pending assistant progress.
func (l *Ledger) UnmarshalJSON(data []byte) error {
	type ledgerJSON struct {
		Messages []Message `json:"messages"`
		Current  *Message  `json:"current,omitempty"`
	}
	var decoded ledgerJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	l.messages = decoded.Messages
	l.current = decoded.Current
	return nil
}

// AppendThinking records a structured thinking block and ensures it appears at
// the head of the current assistant message. When a message is not yet open,
// a new assistant message is started.
func (l *Ledger) AppendThinking(tp ThinkingPart) {
	if l.current == nil {
		l.current = &Message{Role: string(model.ConversationRoleAssistant), Parts: make([]Part, 0, 2)}
	}
	// Ensure all thinking parts stay at the head of the message.
	// Insert this block directly after any existing leading thinking parts.
	if len(l.current.Parts) == 0 {
		l.current.Parts = append(l.current.Parts, tp)
		return
	}
	// Find the end of the leading thinking run (may be zero).
	i := 0
	for i < len(l.current.Parts) {
		if _, ok := l.current.Parts[i].(ThinkingPart); ok {
			i++
			continue
		}
		break
	}
	// Insert tp at position i (which may be 0 to prepend).
	l.current.Parts = append(
		l.current.Parts[:i],
		append(
			[]Part{
				tp,
			},
			l.current.Parts[i:]...,
		)...,
	)
}

// AppendText appends assistant text to the current assistant message. When no
// assistant message is open, a new one is started.
func (l *Ledger) AppendText(text string) {
	if text == "" {
		return
	}
	if l.current == nil {
		l.current = &Message{Role: string(model.ConversationRoleAssistant), Parts: make([]Part, 0, 1)}
	}
	// Coalesce sequential text deltas to avoid storing one part per chunk.
	// This preserves provider-visible ordering while reducing workflow state size.
	if n := len(l.current.Parts); n > 0 {
		if last, ok := l.current.Parts[n-1].(TextPart); ok {
			last.Text += text
			l.current.Parts[n-1] = last
			return
		}
	}
	l.current.Parts = append(l.current.Parts, TextPart{Text: text})
}

// AppendAssistantMessage appends one already-owned canonical assistant message
// without changing its part order, metadata, or citation structure.
func (l *Ledger) AppendAssistantMessage(message *model.Message) {
	if message == nil || message.Role != model.ConversationRoleAssistant || len(message.Parts) == 0 {
		return
	}
	l.flushAssistant()
	canonical := Message{
		Role:  string(message.Role),
		Parts: make([]Part, 0, len(message.Parts)),
		Meta:  message.Meta,
	}
	for _, part := range message.Parts {
		switch value := part.(type) {
		case model.ThinkingPart:
			canonical.Parts = append(canonical.Parts, thinkingPartFromModel(value))
		case model.TextPart:
			canonical.Parts = append(canonical.Parts, TextPart{Text: value.Text})
		case model.CitationsPart:
			canonical.Parts = append(canonical.Parts, CitationsPart{Text: value.Text, Citations: value.Citations})
		case model.ToolUsePart:
			canonical.Parts = append(canonical.Parts, ToolUsePart{ID: value.ID, Name: value.Name, Args: value.Input})
		case *model.ThinkingPart:
			if value != nil {
				canonical.Parts = append(canonical.Parts, thinkingPartFromModel(*value))
			}
		case *model.TextPart:
			if value != nil {
				canonical.Parts = append(canonical.Parts, TextPart{Text: value.Text})
			}
		case *model.CitationsPart:
			if value != nil {
				canonical.Parts = append(canonical.Parts, CitationsPart{Text: value.Text, Citations: value.Citations})
			}
		case *model.ToolUsePart:
			if value != nil {
				canonical.Parts = append(canonical.Parts, ToolUsePart{ID: value.ID, Name: value.Name, Args: value.Input})
			}
		}
	}
	if len(canonical.Parts) > 0 {
		l.messages = append(l.messages, canonical)
	}
}

// DeclareToolUse appends a tool_use to the current assistant message. The
// caller is responsible for flushing the assistant message at the end of the
// turn so that subsequent user tool_result messages can correlate to the full
// set of tool_use blocks.
func (l *Ledger) DeclareToolUse(id, name string, args any) {
	if l.current == nil {
		l.current = &Message{Role: string(model.ConversationRoleAssistant), Parts: make([]Part, 0, 1)}
	}
	l.current.Parts = append(l.current.Parts, ToolUsePart{
		ID:   id,
		Name: name,
		Args: args,
	})
}

// FlushAssistant finalizes the current assistant message (if any) and appends
// it to the ledger. It is safe to call when no assistant message is open.
func (l *Ledger) FlushAssistant() {
	l.flushAssistant()
}

// AppendUserToolResults appends a single user message containing tool_result
// parts for the provided specs, preserving their order. Specs with empty
// ToolUseID are ignored.
func (l *Ledger) AppendUserToolResults(results []ToolResultSpec) {
	if len(results) == 0 {
		return
	}
	parts := make([]Part, 0, len(results))
	for _, r := range results {
		if r.ToolUseID == "" {
			continue
		}
		parts = append(parts, ToolResultPart(r))
	}
	if len(parts) == 0 {
		return
	}
	l.messages = append(l.messages, Message{Role: "user", Parts: parts})
}

// BuildMessages converts the ledger, including the current assistant message,
// to provider-agnostic model messages without mutating ledger state. This keeps
// the method safe for workflow query handlers.
func (l *Ledger) BuildMessages() []*model.Message {
	messageCount := len(l.messages)
	if l.current != nil && len(l.current.Parts) > 0 {
		messageCount++
	}
	if messageCount == 0 {
		return nil
	}
	out := make([]*model.Message, 0, messageCount)
	for i := range l.messages {
		msg := buildModelMessage(l.messages[i])
		if len(msg.Parts) > 0 {
			out = append(out, msg)
		}
	}
	if l.current != nil && len(l.current.Parts) > 0 {
		msg := buildModelMessage(*l.current)
		if len(msg.Parts) > 0 {
			out = append(out, msg)
		}
	}
	return out
}

func buildModelMessage(m Message) *model.Message {
	msg := &model.Message{
		Role:  model.ConversationRole(m.Role),
		Parts: make([]model.Part, 0, len(m.Parts)),
		Meta:  m.Meta,
	}
	hadThinking, emittedThinking := appendLedgerParts(msg, m.Parts)
	if hadThinking && !emittedThinking {
		msg.Parts = append([]model.Part{redactedThinkingPart()}, msg.Parts...)
	}
	return msg
}

func appendLedgerParts(msg *model.Message, parts []Part) (bool, bool) {
	var hadThinking bool
	var emittedThinking bool
	for _, p := range parts {
		had, emitted := appendLedgerPart(msg, p)
		hadThinking = hadThinking || had
		emittedThinking = emittedThinking || emitted
	}
	return hadThinking, emittedThinking
}

func appendLedgerPart(msg *model.Message, p Part) (bool, bool) {
	switch v := p.(type) {
	case ThinkingPart:
		return true, appendLedgerThinkingPart(msg, v)
	case TextPart:
		msg.Parts = append(msg.Parts, model.TextPart{Text: v.Text})
	case CitationsPart:
		msg.Parts = append(msg.Parts, model.CitationsPart{Text: v.Text, Citations: v.Citations})
	case ToolUsePart:
		msg.Parts = append(msg.Parts, model.ToolUsePart{ID: v.ID, Name: v.Name, Input: v.Args})
	case ToolResultPart:
		msg.Parts = append(msg.Parts, model.ToolResultPart{ToolUseID: v.ToolUseID, Content: v.Content, IsError: v.IsError})
	}
	return false, false
}

func appendLedgerThinkingPart(msg *model.Message, v ThinkingPart) bool {
	if len(v.Redacted) > 0 {
		msg.Parts = append(msg.Parts, model.ThinkingPart{
			Redacted: append([]byte(nil), v.Redacted...),
			Index:    v.Index,
			Final:    v.Final,
		})
		return true
	}
	if v.Text != "" && v.Signature != "" {
		msg.Parts = append(msg.Parts, model.ThinkingPart{
			Text:      v.Text,
			Signature: v.Signature,
			Index:     v.Index,
			Final:     v.Final,
		})
		return true
	}
	return false
}

func redactedThinkingPart() model.ThinkingPart {
	return model.ThinkingPart{
		Redacted: []byte("redacted"),
		Final:    true,
	}
}

// IsEmpty reports whether the ledger currently holds any committed or pending parts.
func (l *Ledger) IsEmpty() bool {
	if l == nil {
		return true
	}
	if l.current != nil && len(l.current.Parts) > 0 {
		return false
	}
	return len(l.messages) == 0
}

func decodeLedgerPart(raw jsontext.Value) (Part, error) {
	var legacyText string
	if err := json.Unmarshal(raw, &legacyText); err == nil {
		return TextPart{Text: legacyText}, nil
	}
	obj, err := decodeLedgerPartObject(raw)
	if err != nil {
		return nil, err
	}
	if hasAnyKey(obj, "Signature", "Redacted", "Index", "Final") {
		return decodeLedgerThinkingPart(raw)
	}
	if _, ok := obj["ToolUseID"]; ok {
		return decodeLedgerToolResultPart(raw)
	}
	if _, ok := obj["Name"]; ok {
		return decodeLedgerToolUsePart(raw)
	}
	if _, ok := obj["Citations"]; ok {
		return decodeLedgerCitationsPart(raw)
	}
	if _, ok := obj["Text"]; ok {
		return decodeLedgerTextPart(raw)
	}
	return nil, errors.New("unknown part shape")
}

func decodeLedgerPartObject(raw jsontext.Value) (map[string]jsontext.Value, error) {
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode part object: %w", err)
	}
	if len(obj) == 0 {
		return nil, errors.New("empty part payload")
	}
	return obj, nil
}

func decodeLedgerThinkingPart(raw jsontext.Value) (Part, error) {
	var thinking ThinkingPart
	if err := json.Unmarshal(raw, &thinking); err != nil {
		return nil, fmt.Errorf("decode ThinkingPart: %w", err)
	}
	return thinking, nil
}

func decodeLedgerToolResultPart(raw jsontext.Value) (Part, error) {
	var result ToolResultPart
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode ToolResultPart: %w", err)
	}
	if result.ToolUseID == "" {
		return nil, errors.New("ToolResultPart requires ToolUseID")
	}
	return result, nil
}

func decodeLedgerToolUsePart(raw jsontext.Value) (Part, error) {
	var use ToolUsePart
	if err := json.Unmarshal(raw, &use); err != nil {
		return nil, fmt.Errorf("decode ToolUsePart: %w", err)
	}
	if use.Name == "" {
		return nil, errors.New("ToolUsePart requires Name")
	}
	return use, nil
}

func decodeLedgerTextPart(raw jsontext.Value) (Part, error) {
	var text TextPart
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, fmt.Errorf("decode TextPart: %w", err)
	}
	return text, nil
}

func decodeLedgerCitationsPart(raw jsontext.Value) (Part, error) {
	var citations CitationsPart
	if err := json.Unmarshal(raw, &citations); err != nil {
		return nil, fmt.Errorf("decode CitationsPart: %w", err)
	}
	return citations, nil
}

func isAssistantMessage(m *model.Message) bool {
	return m != nil && m.Role == model.ConversationRoleAssistant
}

func messageHasToolUse(m *model.Message) bool {
	for _, p := range m.Parts {
		if _, ok := p.(model.ToolUsePart); ok {
			return true
		}
	}
	return false
}

func validateAssistantToolUseMessage(messages []*model.Message, index int, m *model.Message, thinkingEnabled bool) error {
	if len(m.Parts) == 0 {
		return fmt.Errorf("bedrock: assistant message[%d] is empty where tool_use present", index)
	}
	if thinkingEnabled {
		if _, ok := m.Parts[0].(model.ThinkingPart); !ok {
			return fmt.Errorf(
				"bedrock: assistant message[%d] with tool_use must start with thinking (parts: %s)",
				index,
				summarizeParts(m.Parts),
			)
		}
	}
	next, err := nextToolResultMessage(messages, index)
	if err != nil {
		return err
	}
	return validateToolHandshake(m, next)
}

func nextToolResultMessage(messages []*model.Message, index int) (*model.Message, error) {
	nextIndex := index + 1
	if nextIndex >= len(messages) {
		return nil, errors.New("bedrock: expected user tool_result following assistant tool_use")
	}
	next := messages[nextIndex]
	if next == nil || next.Role != model.ConversationRoleUser {
		return nil, errors.New("bedrock: expected user tool_result following assistant tool_use")
	}
	return next, nil
}

func validateToolHandshake(assistant, user *model.Message) error {
	useIDs := toolUseIDs(assistant.Parts)
	resIDs := toolResultIDs(user.Parts)
	if len(resIDs) > len(useIDs) {
		return errors.New("bedrock: tool_result count exceeds prior assistant tool_use count")
	}
	for id := range resIDs {
		if _, ok := useIDs[id]; !ok {
			return errors.New("bedrock: tool_result id does not match prior assistant tool_use id")
		}
	}
	return nil
}

func toolUseIDs(parts []model.Part) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, p := range parts {
		if tu, ok := p.(model.ToolUsePart); ok && tu.ID != "" {
			ids[tu.ID] = struct{}{}
		}
	}
	return ids
}

func toolResultIDs(parts []model.Part) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, p := range parts {
		if tr, ok := p.(model.ToolResultPart); ok && tr.ToolUseID != "" {
			ids[tr.ToolUseID] = struct{}{}
		}
	}
	return ids
}

func applyLedgerEvent(l *Ledger, e memory.Event, pendingResults []ToolResultSpec, toolOrder *[]string) ([]ToolResultSpec, error) {
	switch e.Type {
	case memory.EventAssistantMessage:
		return pendingResults, applyAssistantMessageEvent(l, e)
	case memory.EventToolCall:
		return pendingResults, applyToolCallEvent(l, e, toolOrder)
	case memory.EventToolResult:
		return appendToolResultEvent(pendingResults, e)
	case memory.EventThinking:
		return pendingResults, applyThinkingEvent(l, e)
	case memory.EventPlannerNote, memory.EventUserMessage:
		return pendingResults, nil
	default:
		return pendingResults, nil
	}
}

func applyAssistantMessageEvent(l *Ledger, e memory.Event) error {
	data, err := memory.DecodeAssistantMessageData(e)
	if err != nil {
		return fmt.Errorf("transcript: decode %s event: %w", e.Type, err)
	}
	if data.Message != "" {
		l.AppendText(data.Message)
	}
	return nil
}

func applyToolCallEvent(l *Ledger, e memory.Event, toolOrder *[]string) error {
	data, err := memory.DecodeToolCallData(e)
	if err != nil {
		return fmt.Errorf("transcript: decode %s event: %w", e.Type, err)
	}
	payload, err := data.Input()
	if err != nil {
		return fmt.Errorf("transcript: decode tool_call %q payload: %w", data.ToolCallID, err)
	}
	l.DeclareToolUse(data.ToolCallID, string(data.ToolName), payload)
	*toolOrder = append(*toolOrder, data.ToolCallID)
	return nil
}

func appendToolResultEvent(pendingResults []ToolResultSpec, e memory.Event) ([]ToolResultSpec, error) {
	data, err := memory.DecodeToolResultData(e)
	if err != nil {
		return nil, fmt.Errorf("transcript: decode %s event: %w", e.Type, err)
	}
	content, err := ProjectToolResultContent(data.ResultJSON, data.Bounds, data.Preview, data.ErrorMessage)
	if err != nil {
		return nil, fmt.Errorf("transcript: reconstruct tool_result %q: %w", data.ToolCallID, err)
	}
	return append(pendingResults, ToolResultSpec{
		ToolUseID: data.ToolCallID,
		Content:   content,
		IsError:   data.ErrorMessage != "",
	}), nil
}

func applyThinkingEvent(l *Ledger, e memory.Event) error {
	data, err := memory.DecodeThinkingData(e)
	if err != nil {
		return fmt.Errorf("transcript: decode %s event: %w", e.Type, err)
	}
	l.AppendThinking(ThinkingPart{
		Text:      data.Text,
		Signature: data.Signature,
		Redacted:  data.Redacted,
		Index:     data.ContentIndex,
		Final:     data.Final,
	})
	return nil
}

func flushPendingToolResults(l *Ledger, pendingResults []ToolResultSpec, toolOrder []string) {
	if len(pendingResults) == 0 {
		return
	}
	l.FlushAssistant()
	l.AppendUserToolResults(orderToolResults(pendingResults, toolOrder))
}

func orderToolResults(pendingResults []ToolResultSpec, toolOrder []string) []ToolResultSpec {
	byID := make(map[string]ToolResultSpec, len(pendingResults))
	for _, r := range pendingResults {
		if r.ToolUseID == "" {
			continue
		}
		byID[r.ToolUseID] = r
	}
	ordered := make([]ToolResultSpec, 0, len(byID))
	for _, id := range toolOrder {
		if r, ok := byID[id]; ok {
			ordered = append(ordered, r)
			delete(byID, id)
		}
	}
	for _, r := range byID {
		ordered = append(ordered, r)
	}
	return ordered
}

func hasAnyKey(obj map[string]jsontext.Value, keys ...string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

func (ThinkingPart) isPart()   {}
func (TextPart) isPart()       {}
func (CitationsPart) isPart()  {}
func (ToolUsePart) isPart()    {}
func (ToolResultPart) isPart() {}

func thinkingPartFromModel(v model.ThinkingPart) ThinkingPart {
	out := ThinkingPart{
		Text:      v.Text,
		Signature: v.Signature,
		Index:     v.Index,
		Final:     v.Final,
	}
	if len(v.Redacted) > 0 {
		out.Redacted = append([]byte(nil), v.Redacted...)
	}
	return out
}

func (l *Ledger) flushAssistant() {
	if l.current == nil || len(l.current.Parts) == 0 {
		l.current = nil
		return
	}
	l.messages = append(l.messages, *l.current)
	l.current = nil
}

// summarizeParts returns a compact string showing the types of parts in a
// message, e.g. "[thinking, text, tool_use, tool_use]". Used in diagnostics.
func summarizeParts(parts []model.Part) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		switch p.(type) {
		case model.ThinkingPart:
			names[i] = model.ChunkTypeThinking
		case model.TextPart:
			names[i] = model.ChunkTypeText
		case model.CitationsPart:
			names[i] = model.ChunkTypeText
		case model.ToolUsePart:
			names[i] = ledgerPartToolUse
		case model.ToolResultPart:
			names[i] = ledgerPartToolResult
		default:
			names[i] = fmt.Sprintf("%T", p)
		}
	}
	return "[" + strings.Join(names, ", ") + "]"
}
