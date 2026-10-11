package summarize

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func lenParts(messages []*model.Message) int {
	count := 0
	for _, message := range messages {
		if message != nil {
			count += len(message.Parts)
		}
	}
	return count
}

func normalizePart(part model.Part) (model.Part, error) {
	if part == nil {
		return nil, fmt.Errorf("summarize: nil part")
	}
	value := reflect.ValueOf(part)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, fmt.Errorf("summarize: nil part %T", part)
		}
		return value.Elem().Interface().(model.Part), nil
	}
	return part, nil
}

// partAnnotation matches K11 placeholder metadata using integer decimal sizes.
func partAnnotation(unit Unit) (PartAnnotation, error) {
	part, err := normalizePart(unit.Attachment)
	if err != nil {
		return PartAnnotation{}, err
	}
	annotation := PartAnnotation{MessageID: unit.MessageID, Part: unit.Part, ToolUseID: unit.ToolUseID}
	switch value := part.(type) {
	case model.ImagePart:
		annotation.Kind, annotation.Format, annotation.Bytes = "image", string(value.Format), len(value.Bytes)
	case model.DocumentPart:
		annotation.Kind, annotation.Format, annotation.Bytes = "document", string(value.Format), len(value.Bytes)
		if value.URI != "" {
			annotation.Bytes = len(value.URI)
		}
	default:
		return PartAnnotation{}, fmt.Errorf("summarize: unsupported attachment %T", part)
	}
	return annotation, nil
}

func byteSize(size int) string {
	if size >= 1000000 {
		return decimalSize(size, 100000, "MB")
	}
	if size >= 1000 {
		return decimalSize(size, 100, "KB")
	}
	return fmt.Sprintf("%d bytes", size)
}

func decimalSize(size, divisor int, unit string) string {
	tenths, remainder := size/divisor, size%divisor
	if remainder > divisor/2 || (remainder == divisor/2 && tenths%2 != 0) {
		tenths++
	}
	return fmt.Sprintf("%d.%d %s", tenths/10, tenths%10, unit)
}

func (r *inputRenderer) appendMessages(entry index.MessageEntry, messages []*model.Message) error {
	number := 0
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			number++
			value, err := normalizePart(part)
			if err != nil {
				return err
			}
			unit := Unit{MessageID: entry.ID, Part: number, Label: fmt.Sprintf("[%s message %d]", message.Role, entry.ID)}
			if message.Role == model.ConversationRoleUser {
				unit.Label = "[" + checkpoint.Label(entry.ID, entry.Origin, r.in.CurrentPos) + "]"
			}
			include, err := r.renderPart(entry, &unit, value)
			if err != nil {
				return err
			}
			if include {
				r.evidence.Units = append(r.evidence.Units, unit)
			}
		}
	}
	return nil
}

func (r *inputRenderer) renderPart(entry index.MessageEntry, unit *Unit, part model.Part) (bool, error) {
	switch value := part.(type) {
	case model.TextPart:
		unit.Text = value.Text
	case model.CitationsPart:
		unit.Text = value.Text
	case model.ImagePart:
		value.Bytes = slices.Clone(value.Bytes)
		unit.Attachment = value
		return r.nonText(unit, r.in.AcceptsImages)
	case model.DocumentPart:
		return r.documentPart(unit, value)
	case model.ToolUsePart:
		return r.usePart(entry, unit, value)
	case model.ToolResultPart:
		return r.resultPart(unit, value)
	case model.ThinkingPart, model.CacheCheckpointPart:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported part %T", part)
	}
	return true, nil
}

func (r *inputRenderer) documentPart(unit *Unit, value model.DocumentPart) (bool, error) {
	if value.Text != "" || len(value.Chunks) > 0 {
		unit.Text = value.Text
		if len(value.Chunks) > 0 {
			unit.Text = strings.Join(value.Chunks, "\n")
		}
		return true, nil
	}
	value.Bytes = slices.Clone(value.Bytes)
	unit.Attachment = value
	return r.nonText(unit, r.in.AcceptsDocuments)
}

func (r *inputRenderer) usePart(entry index.MessageEntry, unit *Unit, value model.ToolUsePart) (bool, error) {
	if r.hidden[value.ID] {
		return false, nil
	}
	call, err := r.callUnit(entry, unit.Part, value.ID, value.Name, value.Input)
	if err != nil {
		return false, err
	}
	*unit = call
	return true, nil
}

func (r *inputRenderer) resultPart(unit *Unit, value model.ToolResultPart) (bool, error) {
	if r.hidden[value.ToolUseID] {
		return false, nil
	}
	data, err := json.Marshal(value.Content, json.Deterministic(true))
	if err != nil {
		return false, err
	}
	unit.Text, unit.ToolUseID, unit.IsResult = string(data), value.ToolUseID, true
	unit.Label = fmt.Sprintf("[result message %d, %s]", unit.MessageID, value.ToolUseID)
	return true, nil
}

func (r *inputRenderer) nonText(unit *Unit, accepted bool) (bool, error) {
	if accepted {
		return true, nil
	}
	annotation, err := partAnnotation(*unit)
	if err != nil {
		return false, err
	}
	label := strings.Trim(unit.Label, "[]")
	unit.Text = fmt.Sprintf("[%s not shown: %s, part %d, %s, %s]", annotation.Kind, label, annotation.Part, annotation.Format, byteSize(annotation.Bytes))
	unit.Attachment = nil
	r.evidence.Annotations = append(r.evidence.Annotations, annotation)
	return true, nil
}

// callUnit shares K13 lookup labels; eligible payloads stay complete.
func (r *inputRenderer) callUnit(entry index.MessageEntry, part int, id, name string, input any) (Unit, error) {
	data, err := json.Marshal(input, json.Deterministic(true))
	if err != nil {
		return Unit{}, err
	}
	label := fmt.Sprintf("[call message %d, %s, %s]", entry.ID, id, name)
	if tools.Ident(name) == tools.RecallContext || strings.Contains(name, "recall_context") || slices.Contains(r.in.RecallNames, name) {
		r.lookups[id] = true
		label = fmt.Sprintf("[message %d, %s]", entry.ID, checkpoint.LookupLine(id, "", -1))
	}
	return Unit{MessageID: entry.ID, Part: part, ToolUseID: id, Label: label, Text: string(data)}, nil
}
