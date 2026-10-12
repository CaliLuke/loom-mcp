package checkpoint

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

func TestCarryUsersCopiesOnlyRenderedText(t *testing.T) {
	in := Input{ID: "record", CurrentPos: 1, Users: []UserMessage{user(1, strings.Repeat("secret full user ", 1000)), user(2, "correction")}, Pinned: []index.MessageID{1}}
	budget := index.RenderBudget{KeepUserTokens: 256}
	pins, lines := CarryUsers(in, budget)
	assert.Len(t, pins, 1)
	assert.NotEmpty(t, lines)
	assert.LessOrEqual(t, len(pins[0].Text), budget.KeepUserTokens)
	assert.NotContains(t, pins[0].Text, strings.Repeat("secret full user ", 1000))
}
