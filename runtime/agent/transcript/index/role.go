package index

import (
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// RecordedRole supplies Units with the role recorded by the content writer.
// It never resolves content, so pure decisions use only immutable index facts.
func RecordedRole(entry MessageEntry) (model.ConversationRole, error) {
	switch entry.Role {
	case "", model.ConversationRoleUser, model.ConversationRoleAssistant, model.ConversationRoleSystem:
		return entry.Role, nil
	default:
		return "", fmt.Errorf("index entry %d has invalid recorded role %q", entry.ID, entry.Role)
	}
}
