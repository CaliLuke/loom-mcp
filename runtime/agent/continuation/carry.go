// Package continuation builds bounded, immutable session carry and resolves its lineage.
package continuation

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Entry pairs canonical provenance with the final rendered messages. Callers
	// must apply the final view's clearing/preview rules before invoking Carry.
	Entry struct {
		Entry    index.MessageEntry
		Messages []*model.Message
		Form     string
	}
	// Plan accounts for every non-reasoning entry in either Tail or Evicted.
	Plan struct {
		Tail     []Entry
		Evicted  []index.MessageEntry
		TailFrom index.MessageID
		Bytes    int
		Minimal  bool
	}
)

const (
	// Full identifies content carried without truncation.
	Full = "full"
	// Preview identifies bounded text or arguments.
	Preview = "preview"
	// Placeholder identifies permanently cleared results.
	Placeholder = "placeholder"
)

// Cap returns the carry ceiling in bytes at two bytes per token. Unknown model
// budgets use the 60k-token ceiling; explicit recent/protection settings persist.
func Cap(settings compaction.Settings, minB int) int {
	keep, protect := settings.KeepRecentTokens, settings.ProtectRecentToolTokens
	if minB == 0 {
		if keep == 0 {
			keep = 20000
		}
		if protect == 0 {
			protect = 40000
		}
	} else {
		if keep == 0 {
			keep = min(minB/10, 20000)
		}
		if protect == 0 {
			protect = min(minB*15/100, 40000)
		}
	}
	return (keep + protect) * checkpoint.BytesPerToken
}

// Carry drops reasoning, evicts oldest legal units, forces newest results clear,
// then shrinks the remaining text and arguments evenly to the minimum preview.
// It never mutates the caller's messages and never restores a carried form.
func Carry(entries []Entry, view index.ViewRef, capBytes, minPreview int) (Plan, error) {
	plan, metadata, err := prepareCarry(entries, view)
	if err != nil {
		return Plan{}, err
	}

	cuts := checkpoint.CutPoints(checkpoint.Units(metadata, index.RecordedRole, 0))
	if err := plan.measure(); err != nil {
		return Plan{}, err
	}
	if err := plan.evictOldest(cuts, capBytes); err != nil {
		return Plan{}, err
	}
	if err := plan.clearNewestResults(capBytes); err != nil {
		return Plan{}, err
	}
	if plan.Bytes <= capBytes {
		return plan, nil
	}
	original := make([]Entry, len(plan.Tail))
	for i, entry := range plan.Tail {
		original[i] = entry
		original[i].Messages, err = cloneCarryMessages(entry.Messages)
		if err != nil {
			return Plan{}, err
		}
	}
	for limit := max(plan.Bytes, minPreview); plan.Bytes > capBytes; {
		limit = max(limit/2, minPreview)
		if err := previewEntries(plan.Tail, original, limit); err != nil {
			return Plan{}, err
		}
		if err := plan.measure(); err != nil {
			return Plan{}, err
		}
		if limit == minPreview {
			plan.Minimal = true
			break
		}
	}
	return plan, nil
}

// dropUncarriedParts removes reasoning and unsupported non-text content.
func dropUncarriedParts(entry *Entry) {
	entry.Entry.NonText = 0
	if entry.Entry.Result != nil {
		projection := *entry.Entry.Result
		projection.Parts = ""
		entry.Entry.Result = &projection
	}
	for _, message := range entry.Messages {
		parts := make([]model.Part, 0, len(message.Parts))
		for _, part := range message.Parts {
			switch part := part.(type) {
			case model.ToolResultPart:
				if len(part.Parts) > 0 {
					part.Parts = nil
					entry.Form = Preview
				}
				parts = append(parts, part)
			case model.ThinkingPart:
				continue
			case model.ImagePart, model.DocumentPart:
				parts = append(parts, model.TextPart{Text: "[non-text content omitted]"})
				entry.Form = Preview
			default:
				parts = append(parts, part)
			}
		}
		message.Parts = parts
		message.Meta = nil
	}
}

func prepareCarry(entries []Entry, view index.ViewRef) (Plan, []index.MessageEntry, error) {
	plan := Plan{TailFrom: max(view.TailFrom, 1)}
	var metadata []index.MessageEntry
	for _, entry := range entries {
		messages, err := cloneCarryMessages(entry.Messages)
		if err != nil {
			return Plan{}, nil, err
		}
		entry.Messages = messages
		metadata = append(metadata, entry.Entry)
		if entry.Entry.ID < plan.TailFrom {
			continue
		}
		if entry.Form == "" {
			entry.Form = Full
		}
		dropUncarriedParts(&entry)
		plan.Tail = append(plan.Tail, entry)
	}
	return plan, metadata, nil
}

// cloneCarryMessages owns containers and normalizes pointer parts. Carry replaces
// part values, never their nested maps or slices, so those payloads stay read-only.
func cloneCarryMessages(messages []*model.Message) ([]*model.Message, error) {
	out := make([]*model.Message, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			return nil, fmt.Errorf("nil carried message")
		}
		copy := *message
		copy.Parts = make([]model.Part, 0, len(message.Parts))
		for _, part := range message.Parts {
			value := reflect.ValueOf(part)
			if value.Kind() == reflect.Pointer {
				if value.IsNil() {
					return nil, fmt.Errorf("nil carried part")
				}
				normalized, ok := value.Elem().Interface().(model.Part)
				if !ok {
					return nil, fmt.Errorf("invalid carried part %T", part)
				}
				part = normalized
			}
			copy.Parts = append(copy.Parts, part)
		}
		out = append(out, &copy)
	}
	return out, nil
}
func (p *Plan) measure() error {
	p.Bytes = 0
	for _, entry := range p.Tail {
		data, err := json.Marshal(entry.Messages, json.Deterministic(true))
		if err != nil {
			return err
		}
		p.Bytes += len(data)
	}
	return nil
}

// clearNewestResults preserves result identities while dropping even kept bodies.
func (p *Plan) clearNewestResults(capBytes int) error {
	if p.Bytes > capBytes {
		for i := range p.Tail {
			entry := &p.Tail[i]
			for _, message := range entry.Messages {
				for j, part := range message.Parts {
					if result, ok := part.(model.ToolResultPart); ok {
						id := result.ToolUseID
						if entry.Entry.Origin != nil {
							id = fmt.Sprintf("r%d", entry.Entry.Origin.Pos) + " " + id
						}
						result.Parts = nil
						result.Content = checkpoint.ClearedResult(entry.Entry.ContentBytes(), id, "")
						message.Parts[j] = result
						if entry.Form != Preview {
							entry.Form = Placeholder
						}
					}
				}
			}
		}
		if err := p.measure(); err != nil {
			return err
		}
	}
	return nil
}

// evictOldest advances only to closed unit boundaries.
func (p *Plan) evictOldest(cuts []index.MessageID, capBytes int) error {
	for _, cut := range cuts {
		if p.Bytes <= capBytes {
			break
		}
		if cut <= p.TailFrom {
			continue
		}
		first := 0
		for first < len(p.Tail) && p.Tail[first].Entry.ID < cut {
			p.Evicted = append(p.Evicted, p.Tail[first].Entry)
			first++
		}
		p.Tail = slices.Clone(p.Tail[first:])
		p.TailFrom = cut
		if err := p.measure(); err != nil {
			return err
		}
	}
	return nil
}
