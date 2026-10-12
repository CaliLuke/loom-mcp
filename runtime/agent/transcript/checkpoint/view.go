package checkpoint

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Project reconstructs a committed view without storing or changing evidence.
func Project(ctx context.Context, source index.Source, entries []index.MessageEntry, view index.ViewRef) ([]*model.Message, error) {
	if view.Checkpoint == nil && view.TailFrom <= 1 && view.ClearedThrough == 0 && view.PreviewThrough == 0 && view.ForceClearThrough == 0 {
		return index.Render(ctx, entries, source)
	}
	from := max(index.MessageID(1), view.TailFrom)
	if from > 1 {
		if err := ValidTail(entries, from); err != nil {
			return nil, err
		}
	}
	message, err := projectCheckpoint(ctx, source, entries, view, from)
	if err != nil {
		return nil, err
	}
	prefix, tail := entries[:from-1], entries[from-1:]
	cleared, err := ResultText(ctx, source, tail, view)
	if err != nil {
		return nil, err
	}
	messages, err := index.RenderTail(ctx, tail, source, PrefixHidden(prefix), cleared)
	if err != nil {
		return nil, err
	}
	systemMessages, err := projectSystems(ctx, source, prefix)
	if err != nil {
		return nil, err
	}
	assembled := transcript.AssembleView(systemMessages, message, messages)
	if message != nil {
		if err := transcript.ValidateView(assembled, transcript.GrammarAnthropic{}); err != nil {
			return nil, err
		}
		if err := transcript.ValidateView(assembled, transcript.GrammarBedrock{}); err != nil {
			return nil, err
		}
	}
	return assembled, nil
}

// clearedText derives placeholders from immutable result facts, never bodies.
func ClearedText(ctx context.Context, source index.Source, entries []index.MessageEntry, through index.MessageID) (map[index.MessageID]string, error) {
	cleared := map[index.MessageID]string{}
	for _, entry := range entries {
		if entry.ID > through {
			break
		}
		if entry.Kind != index.ToolResult || entry.Keep {
			continue
		}
		if entry.Hash == "" || len(entry.ToolUseIDs) != 1 {
			return nil, fmt.Errorf("cleared result %d has no stored result", entry.ID)
		}
		errorText := ""
		if entry.Result != nil && entry.Result.Error != nil {
			param := entry.Result.Error
			errorText = param.Text
			if param.Hash != "" {
				data, err := source(ctx, param.Hash)
				if err != nil {
					return nil, err
				}
				errorText = string(data)
			}
		}
		cleared[entry.ID] = ClearedResult(entry.ContentBytes(), entry.ToolUseIDs[0], firstLine(errorText))
	}
	return cleared, nil
}

// ResultText derives recovery projections with forced clearing taking precedence
// over ordinary clearing and previews. Immutable Keep flags remain unchanged.
func ResultText(ctx context.Context, source index.Source, entries []index.MessageEntry, view index.ViewRef) (map[index.MessageID]string, error) {
	result, err := ClearedText(ctx, source, entries, view.ClearedThrough)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Kind != index.ToolResult {
			continue
		}
		if entry.ID <= view.ForceClearThrough {
			copy := entry
			copy.Keep = false
			forced, err := ClearedText(ctx, source, []index.MessageEntry{copy}, view.ForceClearThrough)
			if err != nil {
				return nil, err
			}
			result[entry.ID] = forced[entry.ID]
		} else if entry.ID > view.ClearedThrough && entry.ID <= view.PreviewThrough {
			content, err := index.ResultContent(ctx, source, entry)
			if err != nil {
				return nil, err
			}
			text, ok := content.(string)
			if !ok {
				data, err := json.Marshal(content, json.Deterministic(true))
				if err != nil {
					return nil, err
				}
				text = string(data)
			}
			result[entry.ID] = HeadTail(text, view.Budget.PreviewTokens*BytesPerToken)
		}
	}
	return result, nil
}

// prefixHidden retains unmatched hidden calls whose results may lie in the tail.
func PrefixHidden(entries []index.MessageEntry) map[string]bool {
	hidden := map[string]bool{}
	for _, entry := range entries {
		if entry.Structural != nil && entry.Structural.Kind == index.HideBookkeeping {
			for _, id := range entry.ToolUseIDs {
				hidden[id] = true
			}
		}
		if ConsumesHiddenResult(entry) {
			for _, id := range entry.ToolUseIDs {
				delete(hidden, id)
			}
		}
	}
	return hidden
}

// consumesHiddenResult mirrors Renderer.appendResult, including structural results.
func ConsumesHiddenResult(entry index.MessageEntry) bool {
	if entry.Kind == index.ToolResult {
		return true
	}
	if entry.Structural == nil {
		return false
	}
	switch entry.Structural.Kind {
	case index.CarriedResult, index.UnknownTool, index.PolicyDenial, index.ConfirmationDenial, index.SynthesizedError, index.Cancellation:
		return true
	case index.FinalizeResults, index.FinalizeHint, index.Reminders, index.HideBookkeeping:
		return false
	default:
		return false
	}
}

func ValidTail(entries []index.MessageEntry, tail index.MessageID) error {
	for _, entry := range entries {
		if _, err := index.RecordedRole(entry); err != nil {
			return err
		}
	}
	cuts := CutPoints(Units(entries, index.RecordedRole, 0))
	if !slices.Contains(cuts, tail) {
		return fmt.Errorf("tail %d is not a cut point", tail)
	}
	return nil
}

func projectCheckpoint(ctx context.Context, source index.Source, entries []index.MessageEntry, view index.ViewRef, from index.MessageID) (*model.Message, error) {
	var message *model.Message
	if view.Checkpoint != nil {
		input, err := FromRecord(ctx, source, *view.Checkpoint, entries, view.ChainPos)
		if err != nil {
			return nil, err
		}
		if input.Through != from-1 {
			return nil, fmt.Errorf("checkpoint through does not match tail")
		}
		input.Pinned = slices.Clone(view.Pinned)
		message, err = Render(input, view.Budget, BudgetLimits(view.Budget), nil)
		if err != nil {
			return nil, err
		}
	} else if from > 1 {
		return nil, fmt.Errorf("committed eviction lacks checkpoint")
	}
	return message, nil
}

func projectSystems(ctx context.Context, source index.Source, prefix []index.MessageEntry) ([]*model.Message, error) {
	var systems []index.MessageEntry
	for _, entry := range prefix {
		if entry.Role == model.ConversationRoleSystem {
			systems = append(systems, entry)
		}
	}
	systemMessages, err := index.Render(ctx, systems, source)
	if err != nil {
		return nil, err
	}
	return systemMessages, nil
}
