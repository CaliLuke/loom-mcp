package compaction

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

func TestEstimateResidentSystems(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Role: "system", Bytes: 9000, NonText: 100}, {ID: 2, Role: "user", Bytes: 8000}, {ID: 3, Role: "system", Bytes: 6000}, {ID: 4, Role: "assistant", Bytes: 300}}
	text, parts := viewSizes(State{}, entries, index.ViewRef{TailFrom: 4})
	assert.Equal(t, 15300, text)
	assert.Equal(t, 100, parts)
}

func TestObserveExhaustedReserve(t *testing.T) {
	for _, reserve := range []int{6000, 6001} {
		state := Observe(State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 6000, OutputReserve: reserve}})
		assert.True(t, state.Flags.BudgetTooSmall)
		assert.False(t, state.Flags.LimitsUnknown)
		assert.False(t, state.Models["m"].Known)
		assert.Equal(t, None, Decide(state, nil, index.ViewRef{}, Settings{}).Tier)
	}
}
