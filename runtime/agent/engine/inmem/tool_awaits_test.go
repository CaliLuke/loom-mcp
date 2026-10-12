package inmem

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/require"
)

func TestQueryToolAwaitsRequiresWorkflowID(t *testing.T) {
	_, err := New().(engine.ToolAwaitQuerier).QueryToolAwaits(t.Context(), "")
	require.ErrorContains(t, err, "workflow id is required")
}
