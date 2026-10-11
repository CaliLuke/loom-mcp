package checkpoint

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

// TestOrderKey checks original run ordering and qualified labels.
func TestOrderKey(t *testing.T) {
	for _, tt := range []struct {
		origin *index.Origin
		id     index.MessageID
		key    OrderKey
		label  string
	}{
		{&index.Origin{Pos: 1, ID: 57}, 200, OrderKey{Pos: 1, ID: 57}, "r1 message 57"},
		{&index.Origin{Pos: 2, ID: 5}, 201, OrderKey{Pos: 2, ID: 5}, "r2 message 5"},
		{nil, 5, OrderKey{Pos: 3, ID: 5}, "message 5"},
		{&index.Origin{Pos: 3, ID: 6}, 202, OrderKey{Pos: 3, ID: 6}, "message 6"},
	} {
		assert.Equal(t, tt.key, Key(tt.id, tt.origin, 3))
		assert.Equal(t, tt.label, Label(tt.id, tt.origin, 3))
	}
	assert.Negative(t, Compare(OrderKey{Pos: 1, ID: 57}, OrderKey{Pos: 2, ID: 5}))
	assert.Negative(t, Compare(OrderKey{Pos: 2, ID: 5}, OrderKey{Pos: 3, ID: 5}))
	assert.Zero(t, Compare(OrderKey{Pos: 2, ID: 5}, OrderKey{Pos: 2, ID: 5}))
}
