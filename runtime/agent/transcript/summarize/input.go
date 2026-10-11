package summarize

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type inputRenderer struct {
	in              Input
	evidence        Evidence
	hidden, lookups map[string]bool
}

// RenderInput projects full eligible canonical evidence without view omissions.
// Contents must come from the authorized access layer: Record, Output and injected
// call fields are never selected. Hidden declarations are scanned beyond the cut.
func RenderInput(in Input) (Evidence, error) {
	if in.From == 0 || in.To <= in.From || in.CurrentPos < 0 {
		return Evidence{}, fmt.Errorf("summarize: invalid input cut or run position")
	}
	if in.Previous != "" && in.SummaryPos < 0 {
		return Evidence{}, fmt.Errorf("summarize: previous summary has no run position")
	}
	r := inputRenderer{in: in, evidence: Evidence{Previous: checkpoint.Qualify(in.Previous, in.SummaryPos, in.CurrentPos)}, hidden: map[string]bool{}, lookups: map[string]bool{}}
	for _, entry := range in.Entries {
		if entry.Structural != nil && entry.Structural.Kind == index.HideBookkeeping {
			for _, id := range entry.ToolUseIDs {
				r.hidden[id] = true
			}
		}
	}
	for _, entry := range in.Entries {
		if entry.ID < in.From || entry.ID >= in.To {
			continue
		}
		if err := r.appendEntry(entry); err != nil {
			return Evidence{}, fmt.Errorf("summarize entry %d: %w", entry.ID, err)
		}
	}
	return r.evidence, nil
}

func (r *inputRenderer) appendEntry(entry index.MessageEntry) error {
	switch entry.Kind {
	case index.Message:
		var message *model.Message
		if err := r.decode(entry.Hash, &message); err != nil {
			return err
		}
		return r.appendMessages(entry, []*model.Message{message})
	case index.Assistant:
		return r.appendAssistant(entry)
	case index.ToolResult:
		return r.appendResult(entry)
	case index.Clarification:
		data, err := r.content(entry.Hash)
		if err != nil {
			return err
		}
		r.evidence.Units = append(r.evidence.Units, Unit{MessageID: entry.ID, Part: 1, Label: "[" + checkpoint.Label(entry.ID, entry.Origin, r.in.CurrentPos) + "]", Text: string(data)})
	case index.Structural:
		return r.appendStructural(entry)
	case index.TypedInput:
		// Typed inputs are planner data, not transcript messages.
	default:
		return fmt.Errorf("unknown entry kind %q", entry.Kind)
	}
	return nil
}

func (r *inputRenderer) appendAssistant(entry index.MessageEntry) error {
	var messages []*model.Message
	if entry.Hash != "" {
		if err := r.decode(entry.Hash, &messages); err != nil {
			return err
		}
	}
	if err := r.appendMessages(entry, messages); err != nil {
		return err
	}
	for i, hash := range entry.Calls {
		// Decode only original model-visible arguments, never executed/injected fields.
		var call struct {
			Name, ToolCallID string
			Payload          rawjson.Message
		}
		if err := r.decode(hash, &call); err != nil {
			return err
		}
		if r.hidden[call.ToolCallID] {
			continue
		}
		unit, err := r.callUnit(entry, lenParts(messages)+i+1, call.ToolCallID, call.Name, call.Payload)
		if err != nil {
			return err
		}
		r.evidence.Units = append(r.evidence.Units, unit)
	}
	return nil
}

func (r *inputRenderer) appendResult(entry index.MessageEntry) error {
	if len(entry.ToolUseIDs) != 1 {
		return fmt.Errorf("result requires one tool use ID")
	}
	id := entry.ToolUseIDs[0]
	if r.hidden[id] {
		return nil
	}
	var text string
	if entry.Hash == "" && entry.Result != nil && entry.Result.Error != nil {
		var err error
		text, err = r.text(*entry.Result.Error)
		if err != nil {
			return err
		}
	} else if entry.Hash != "" {
		data, err := r.content(entry.Hash)
		if err != nil {
			return err
		}
		if !jsontext.Value(data).IsValid() {
			return fmt.Errorf("invalid eligible result JSON")
		}
		text = string(data)
	}
	label := fmt.Sprintf("[result message %d, %s]", entry.ID, id)
	if r.lookups[id] {
		label = fmt.Sprintf("[lookup result message %d, %s]", entry.ID, id)
	}
	r.evidence.Units = append(r.evidence.Units, Unit{MessageID: entry.ID, Part: 1, ToolUseID: id, Label: label, Text: text, IsResult: true})
	return nil
}

func (r *inputRenderer) appendStructural(entry index.MessageEntry) error {
	if entry.Structural == nil {
		return fmt.Errorf("missing structural parameters")
	}
	if entry.Structural.Kind == index.HideBookkeeping {
		return nil
	}
	if len(entry.ToolUseIDs) == 1 && r.hidden[entry.ToolUseIDs[0]] {
		return nil
	}
	messages, err := index.Render(context.Background(), []index.MessageEntry{entry}, func(_ context.Context, hash runstore.Hash) ([]byte, error) {
		return r.content(hash)
	})
	if err != nil {
		return err
	}
	return r.appendMessages(entry, messages)
}

func (r *inputRenderer) content(hash runstore.Hash) ([]byte, error) {
	data, ok := r.in.Contents[hash]
	if !ok || hash == "" {
		return nil, fmt.Errorf("missing eligible content %q", hash)
	}
	return data, nil
}

func (r *inputRenderer) decode(hash runstore.Hash, value any) error {
	data, err := r.content(hash)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (r *inputRenderer) text(param index.TextParameter) (string, error) {
	if param.Hash == "" {
		return param.Text, nil
	}
	data, err := r.content(param.Hash)
	return string(data), err
}
