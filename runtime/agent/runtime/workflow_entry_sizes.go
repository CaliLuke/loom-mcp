package runtime

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// measureStructuralEntry resolves only this step's writer-owned content batch.
// Results with canonical stored JSON already carry their measured user role.
func (b *workflowPlanInput) measureStructuralEntry(entry *index.MessageEntry, batch *workflowContentBatch) error {
	if entry.Kind != index.Structural || entry.Role != "" {
		return nil
	}
	if entry.Structural == nil {
		return fmt.Errorf("structural size requires parameters")
	}
	texts := make([]string, len(entry.Structural.Text))
	for i, param := range entry.Structural.Text {
		texts[i] = param.Text
		if param.Hash != "" {
			found := false
			if batch != nil {
				for j, hash := range batch.hashes {
					if hash == param.Hash {
						texts[i] = string(batch.items[j])
						found = true
						break
					}
				}
			}
			if !found {
				return fmt.Errorf("structural size missing writer-owned text %s", param.Hash)
			}
		}
	}
	size, err := index.StructuralSize(*entry, texts, b.finalizeOpenIDs())
	if err != nil {
		return err
	}
	entry.Bytes, entry.NonText, entry.Role = size.Bytes, size.NonText, size.Role
	return nil
}

func (b *workflowPlanInput) finalizeOpenIDs() []string {
	hidden := map[string]bool{}
	for i := len(b.Entries) - 1; i >= 0; i-- {
		entry := b.Entries[i]
		if entry.Structural != nil && entry.Structural.Kind == index.HideBookkeeping {
			for _, id := range entry.ToolUseIDs {
				hidden[id] = true
			}
			continue
		}
		if entry.Kind == index.TypedInput || (entry.Role == "" && entry.Kind == index.Structural) {
			continue
		}
		if entry.Role != model.ConversationRoleAssistant {
			return nil
		}
		var ids []string
		for _, id := range entry.ToolUseIDs {
			if !hidden[id] {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}
