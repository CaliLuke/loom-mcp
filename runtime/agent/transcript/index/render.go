package index

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
)

// Renderer incrementally applies immutable entries within one run. It is not concurrency-safe.
type Renderer struct {
	ctx      context.Context
	source   Source
	messages []*model.Message
	batch    MessageID
	parts    []model.Part
	hidden   map[string]bool
	cleared  map[MessageID]string
}

// Render resolves entries in order without mutating them or their content.
// Record hashes and typed-input payloads are never read. HistoryPolicy, provider
// validation and recovery corrections remain the planner activity's concern.
func Render(ctx context.Context, entries []MessageEntry, source Source) ([]*model.Message, error) {
	r := Renderer{ctx: ctx, source: source, hidden: make(map[string]bool)}
	if err := r.Append(ctx, entries, source); err != nil {
		return nil, err
	}
	return r.Messages()
}

// RenderTail renders a selected suffix with hidden calls inherited from its
// prefix and precomputed cleared-result text. Neither map is mutated.
func RenderTail(ctx context.Context, entries []MessageEntry, source Source, hidden map[string]bool, cleared map[MessageID]string) ([]*model.Message, error) {
	r := Renderer{hidden: maps.Clone(hidden), cleared: cleared}
	if err := r.Append(ctx, entries, source); err != nil {
		return nil, err
	}
	return r.Messages()
}

// Append renders only new entries. Discard the renderer after an error.
func (r *Renderer) Append(ctx context.Context, entries []MessageEntry, source Source) error {
	r.ctx, r.source = ctx, source
	defer func() {
		r.ctx, r.source = nil, nil
	}()
	if r.hidden == nil {
		r.hidden = make(map[string]bool)
	}
	for _, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return fmt.Errorf("render entry %d: %w", entry.ID, err)
		}
		if entry.Batch == 0 || entry.Batch != r.batch {
			r.flushResults()
		}
		if err := r.appendEntry(entry); err != nil {
			return fmt.Errorf("render entry %d: %w", entry.ID, err)
		}
	}
	r.flushResults()
	return nil
}

// Messages returns an owned snapshot using the same JSON boundary as Temporal.
func (r *Renderer) Messages() ([]*model.Message, error) {
	data, err := json.Marshal(r.messages, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	var out []*model.Message
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Renderer) appendEntry(entry MessageEntry) error {
	switch entry.Kind {
	case Message:
		var message *model.Message
		if err := r.decode(entry.Hash, &message); err != nil {
			return err
		}
		if entry.Origin != nil && entry.Batch != 0 {
			// Carried result bytes are already projected model parts. Preserve the
			// original batch without interpreting them as raw result bodies again.
			r.batch = entry.Batch
			r.parts = append(r.parts, message.Parts...)
		} else {
			r.messages = append(r.messages, message)
		}
	case Assistant:
		return r.appendAssistant(entry)
	case ToolResult:
		return r.appendResult(entry, nil)
	case Clarification:
		text, err := r.text(TextParameter{Hash: entry.Hash})
		if err != nil {
			return err
		}
		if text != "" {
			r.appendText(model.ConversationRoleUser, text)
		}
	case TypedInput:
		// The loader derives planner typed inputs separately; never model content.
	case Structural:
		return r.appendStructural(entry)
	}
	return nil
}

func (r *Renderer) appendAssistant(entry MessageEntry) error {
	var messages []*model.Message
	if entry.Hash != "" {
		if err := r.decode(entry.Hash, &messages); err != nil {
			return err
		}
	}
	if len(messages) == 0 && len(entry.Calls) == 0 {
		return nil
	}
	var target *model.Message
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		// recordAssistantTurn's clone creates a non-nil empty Parts slice and
		// normalizes empty metadata. Loaded messages already own their contents.
		if msg.Parts == nil {
			msg.Parts = []model.Part{}
		}
		if len(msg.Meta) == 0 {
			msg.Meta = nil
		}
		r.messages = append(r.messages, msg)
		if msg.Role == model.ConversationRoleAssistant {
			target = msg
		}
	}
	if target == nil {
		target = &model.Message{Role: model.ConversationRoleAssistant}
		r.messages = append(r.messages, target)
	}
	for _, hash := range entry.Calls {
		// Decode only the model-eligible call fields. Using rawjson.Message
		// preserves the concrete Input type used by recordAssistantTurn.
		var call struct {
			Name       string
			ToolCallID string
			Payload    rawjson.Message
		}
		if err := r.decode(hash, &call); err != nil {
			return err
		}
		target.Parts = append(target.Parts, model.ToolUsePart{ID: call.ToolCallID, Name: call.Name, Input: call.Payload})
	}
	return nil
}

// ResultContent projects a result exactly as Render does, without loading an
// omitted body. The returned value is suitable for deterministic previewing.
func ResultContent(ctx context.Context, source Source, entry MessageEntry) (any, error) {
	r := Renderer{ctx: ctx, source: source}
	return r.resultContent(entry, nil)
}

func (r *Renderer) appendResult(entry MessageEntry, inline []byte) error {
	if entry.Batch == 0 || len(entry.ToolUseIDs) != 1 {
		return fmt.Errorf("tool result requires a batch and one tool use ID")
	}
	r.batch = entry.Batch
	id := entry.ToolUseIDs[0]
	if r.hidden[id] {
		delete(r.hidden, id)
		return nil
	}
	if text, ok := r.cleared[entry.ID]; ok {
		r.parts = append(r.parts, model.ToolResultPart{ToolUseID: id, Content: text, IsError: entry.Result != nil && entry.Result.Error != nil})
		return nil
	}
	content, err := r.resultContent(entry, inline)
	if err != nil {
		return err
	}
	r.parts = append(r.parts, model.ToolResultPart{ToolUseID: id, Content: content, IsError: entry.Result != nil && entry.Result.Error != nil})
	return nil
}

// resultContent applies the immutable projection without loading omitted bodies.
func (r *Renderer) resultContent(entry MessageEntry, inline []byte) (any, error) {
	projection := ResultProjection{}
	if entry.Result != nil {
		projection = *entry.Result
	}
	preview, err := r.text(projection.Preview)
	if err != nil {
		return nil, err
	}
	var errorText string
	if projection.Error != nil {
		errorText, err = r.text(*projection.Error)
		if err != nil {
			return nil, err
		}
	}
	var content any
	if entry.OmitsResultContent() {
		content, err = transcript.OmittedToolResultContent(preview, projection.Bounds)
	} else {
		data := inline
		if entry.Hash != "" {
			data, err = r.source(r.ctx, entry.Hash)
			if err != nil {
				return nil, err
			}
		}
		content, err = transcript.ProjectToolResultContent(rawjson.Message(data), projection.Bounds, preview, errorText)
	}
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (r *Renderer) flushResults() {
	if len(r.parts) > 0 {
		r.messages = append(r.messages, &model.Message{Role: model.ConversationRoleUser, Parts: r.parts})
	}
	r.parts = nil
	r.batch = 0
}

func (r *Renderer) appendText(role model.ConversationRole, text string) {
	r.messages = append(r.messages, &model.Message{Role: role, Parts: []model.Part{model.TextPart{Text: text}}})
}

func (r *Renderer) text(param TextParameter) (string, error) {
	if param.Hash == "" {
		return param.Text, nil
	}
	data, err := r.source(r.ctx, param.Hash)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *Renderer) decode(hash runstore.Hash, value any) error {
	if hash == "" {
		return fmt.Errorf("missing content hash")
	}
	data, err := r.source(r.ctx, hash)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
