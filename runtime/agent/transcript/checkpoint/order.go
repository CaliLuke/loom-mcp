package checkpoint

import (
	"cmp"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// OrderKey orders original user messages by run position, then original ID.
type OrderKey struct {
	Pos int
	ID  index.MessageID
}

// Key resolves provenance; nil origins belong to the current run.
func Key(id index.MessageID, origin *index.Origin, currentPos int) OrderKey {
	if origin != nil {
		return OrderKey{Pos: origin.Pos, ID: origin.ID}
	}
	return OrderKey{Pos: currentPos, ID: id}
}

// Compare returns the chronological ordering of two keys.
func Compare(a, b OrderKey) int {
	if order := cmp.Compare(a.Pos, b.Pos); order != 0 {
		return order
	}
	return cmp.Compare(a.ID, b.ID)
}

// Label renders a local or qualified original message reference.
func Label(id index.MessageID, origin *index.Origin, currentPos int) string {
	key := Key(id, origin, currentPos)
	if key.Pos == currentPos {
		return fmt.Sprintf("message %d", key.ID)
	}
	return fmt.Sprintf("r%d message %d", key.Pos, key.ID)
}
