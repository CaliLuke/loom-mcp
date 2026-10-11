package compaction

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRecallRefill(t *testing.T) {
	for _, tc := range []struct{ remaining, initial, want int }{{20, 20, 30}, {0, 20, 10}, {59, 20, 60}, {60, 20, 60}, {0, 1, 1}, {2, 1, 3}, {0, 3, 2}, {8, 3, 9}} {
		assert.Equal(t, tc.want, Refill(tc.remaining, tc.initial))
	}
}

func TestRecallAllowancesIndependent(t *testing.T) {
	settings := Settings{RecallDiscoveryCalls: 3, RecallReadCalls: 1}
	state := NewRecall(settings)
	for _, read := range []bool{false, false, false, false} {
		accepted := state.Charge(read)
		if accepted {
			assert.GreaterOrEqual(t, state.Discovery, 0)
		}
		assert.Equal(t, 1, state.Reads)
	}
	assert.False(t, state.Charge(false))
	assert.True(t, state.Charge(true))
	assert.False(t, state.Charge(true))
	state = state.Refilled(settings)
	assert.Equal(t, RecallAllowances{Discovery: 2, Reads: 1}, state)
	assert.True(t, state.Charge(true), "WitnessReadAfterRefill")
	assert.Equal(t, 2, state.Discovery)
	assert.Equal(t, RecallAllowances{Discovery: 20, Reads: 20}, NewRecall(Settings{}))
}
