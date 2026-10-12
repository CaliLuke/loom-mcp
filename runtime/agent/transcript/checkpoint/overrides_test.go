package checkpoint

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

func TestContextPinOverrides(t *testing.T) {
	budget := index.RenderBudget{KeepUserTokens: 1000, MaxPins: 1, MaxPinnedParts: 1}
	limits := BudgetLimits(budget)
	assert.Equal(t, 1, limits.MaxPins)
	assert.Equal(t, 1, limits.MaxPinnedParts)
	pins := SelectPins([]UserMessage{user(1, "first"), user(2, "second")}, budget, limits)
	assert.Equal(t, []index.MessageID{1}, pins)
}
