// Package checkpoint provides pure context-checkpoint selection and rendering.
// It has no workflow, activity, store, or provider side effects.
package checkpoint

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Unit is an inclusive atomic entry range; a cut cannot split it.
	Unit struct {
		From, Through   index.MessageID
		StartsAssistant bool
		openCalls       bool
	}
	// RoleOf loads the role of a Message entry from its canonical content.
	RoleOf func(index.MessageEntry) (model.ConversationRole, error)
	// UnitTokens measures a complete tail unit in estimated tokens.
	UnitTokens func(Unit) int
)

// Units groups entries into atomic batches and one optional open-await suffix.
func Units(entries []index.MessageEntry, role RoleOf, openFrom index.MessageID) []Unit {
	var units []Unit
	pending := map[string]bool{}
	for _, entry := range entries {
		assistant, user := unitStart(entry, role)
		inOpen := openFrom != 0 && entry.ID > openFrom
		if len(units) == 0 || (!inOpen && (assistant || user || entry.ID == openFrom)) {
			units = append(units, Unit{From: entry.ID, Through: entry.ID, StartsAssistant: assistant})
		}
		unit := &units[len(units)-1]
		unit.Through = entry.ID
		if assistant {
			for _, id := range entry.ToolUseIDs {
				pending[id] = true
			}
		} else if entry.Kind == index.ToolResult || (user && entry.Kind == index.Message) || isStructuralResult(entry.Structural) {
			for _, id := range entry.ToolUseIDs {
				delete(pending, id)
			}
		}
		if entry.Structural != nil && entry.Structural.Kind == index.FinalizeResults {
			clear(pending)
		}
		unit.openCalls = len(pending) > 0
	}
	return units
}

// CutPoints returns tail starts between units, never before the first unit.
func CutPoints(units []Unit) []index.MessageID {
	var cuts []index.MessageID
	for i := 1; i < len(units); i++ {
		if units[i].StartsAssistant && !units[i-1].openCalls {
			cuts = append(cuts, units[i].From)
		}
	}
	return cuts
}

// ChooseTail keeps the newest unit even when it exceeds keepRecent.
func ChooseTail(units []Unit, tokens UnitTokens, keepRecent int) index.MessageID {
	if len(units) == 0 {
		return 0
	}
	first := len(units) - 1
	spent := tokens(units[first])
	for first > 0 {
		cost := tokens(units[first-1])
		if spent+cost > keepRecent {
			break
		}
		spent += cost
		first--
	}
	cuts := CutPoints(units)
	for _, cut := range cuts {
		if cut >= units[first].From {
			return cut
		}
	}
	// A newest user unit has no cut of its own; keep it with the latest safe prefix.
	if len(cuts) > 0 {
		return cuts[len(cuts)-1]
	}
	return units[0].From
}

// unitStart reads roles only for stored Message entries. A role-load failure
// violates Units' resolved-content precondition and panics with the cause.
func unitStart(entry index.MessageEntry, role RoleOf) (assistant, user bool) {
	switch entry.Kind {
	case index.Assistant:
		return true, false
	case index.Clarification:
		return false, true
	case index.Message:
		value, err := role(entry)
		if err != nil {
			panic(fmt.Errorf("unit message %d role: %w", entry.ID, err))
		}
		return value == model.ConversationRoleAssistant, value == model.ConversationRoleUser
	case index.ToolResult, index.TypedInput, index.Structural:
		return false, false
	default:
		return false, false
	}
}

// isStructuralResult excludes bookkeeping removal: hiding a call is not its result.
func isStructuralResult(entry *index.StructuralEntry) bool {
	if entry == nil {
		return false
	}
	switch entry.Kind {
	case index.UnknownTool, index.PolicyDenial, index.ConfirmationDenial, index.SynthesizedError, index.Cancellation, index.FinalizeResults:
		return true
	case index.FinalizeHint, index.Reminders, index.HideBookkeeping:
		return false
	default:
		return false
	}
}
