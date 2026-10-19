package statefulevaluation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryStateContract(t *testing.T) {
	state := newDiscoveryState()
	key := discoveryKey{Agent: "a", Session: "one"}
	for _, name := range []string{"one", "two", "three", "four", "five", "six"} {
		require.NoError(t, state.reveal(key, "generation-1", descriptor{Name: name, InputSchema: []byte(`{"type":"object"}`)}))
	}
	assert.Equal(t, []string{"two", "three", "four", "five", "six"}, state.names(key, "generation-1"))
	before := state.bytes(key, "generation-1")
	require.NoError(t, state.reveal(key, "generation-1", descriptor{Name: "three", InputSchema: []byte(`{"type":"object"}`)}))
	assert.Equal(t, before, state.bytes(key, "generation-1"))
	assert.Equal(t, []string{"two", "three", "four", "five", "six"}, state.names(key, "generation-1"))
	assert.Empty(t, state.names(discoveryKey{Agent: "b", Session: "one"}, "generation-1"))
	assert.Empty(t, state.names(discoveryKey{Agent: "a", Session: "two"}, "generation-1"))
	assert.Empty(t, state.names(key, "generation-2"))
	require.NoError(t, state.reveal(key, "generation-2", descriptor{Name: "new", Description: strings.Repeat("x", 5000)}))
	require.NoError(t, state.reveal(key, "generation-2", descriptor{Name: "newer", Description: strings.Repeat("y", 5000)}))
	assert.Equal(t, []string{"newer"}, state.names(key, "generation-2"))
	assert.LessOrEqual(t, state.bytes(key, "generation-2"), 8192)
	err := state.reveal(key, "generation-2", descriptor{Name: "oversized", Description: strings.Repeat("z", 8193)})
	require.ErrorIs(t, err, errDescriptorTooLarge)
	assert.Equal(t, []string{"newer"}, state.names(key, "generation-2"), "oversized admission cannot evict valid state")
}
