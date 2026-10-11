package checkpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryResultProjection(t *testing.T) {
	body := `"` + strings.Repeat("abcdefgh", 200) + `"`
	reads := 0
	source := func(context.Context, runstore.Hash) ([]byte, error) {
		reads++
		return []byte(body), nil
	}
	entries := []index.MessageEntry{{ID: 2, Kind: index.ToolResult, Batch: 1, Hash: "result", ToolUseIDs: []string{"call"}, Bytes: len(body) + 4, Keep: true}}
	view := index.ViewRef{PreviewThrough: 2, Budget: index.RenderBudget{PreviewTokens: 128}}
	first, err := ResultText(t.Context(), source, entries, view)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(first[2]), 256)
	assert.Contains(t, first[2], "not shown")
	second, err := ResultText(t.Context(), source, entries, view)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	view.ForceClearThrough = 2
	forced, err := ResultText(t.Context(), source, entries, view)
	require.NoError(t, err)
	assert.Equal(t, ClearedResult(len(body)+4, "call", ""), forced[2])
	assert.Equal(t, 2, reads, "forced Keep placeholders never load bodies")
	assert.True(t, entries[0].Keep)
}
