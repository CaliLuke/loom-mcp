package summaryeval

import (
	"bytes"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShowGolden(t *testing.T) {
	e := &Experiment{Header: Header{ID: "replay", Status: "complete", Tokens: 10, Calls: 1}, Lines: []Line{{Key: "run:2/prompt=v2/1", Cut: Cut{RunID: "run", Step: 2, From: 1, To: 4, Origin: "summary", EvictedBytes: 100}, Verification: Verification{Status: "verified"}, Old: Side{Text: "old", Bytes: 3}, New: Side{Text: "new", Bytes: 3}}}}
	var out bytes.Buffer
	require.NoError(t, Show(&out, e, false))
	assert.Equal(t, "Compaction replay replay (complete): 1 cuts, 10 tokens, 1 calls\n\nrun:2/prompt=v2/1 — verified summary messages 1-4\nnot_run= error=\nold: kind=\"\" bytes=3 compression=33.33x rendered=0 tokens=0 chunks=0 latency=0s cached=false\nold\nnew: kind=\"\" bytes=3 compression=33.33x rendered=0 tokens=0 chunks=0 latency=0s cached=false\nnew\n", out.String())
	b, err := ListJSON(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"format":"loom-mcp.eval.compaction.list","replays":[],"version":1}`, string(b))
	b, err = ShowJSON(e)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"format":"loom-mcp.eval.compaction.show"`)
	assert.Contains(t, string(b), `"version":1`)
	e.Lines[0].New.Text = strings.Repeat("line\n", 21)
	out.Reset()
	require.NoError(t, Show(&out, e, false))
	assert.Contains(t, out.String(), "use --full")
	out.Reset()
	require.NoError(t, Show(&out, e, true))
	assert.NotContains(t, out.String(), "use --full")
}

func TestUnjudgedVariantCostAggregate(t *testing.T) {
	lines := []Line{{Variant: "prompt=v2", Verification: Verification{Status: "verified"}, Old: Side{Bytes: 12}, New: Side{Bytes: 8, Usage: model.TokenUsage{TotalTokens: 9}, Chunks: 1}}}
	a := Aggregate(lines)
	require.Len(t, a, 1)
	assert.Equal(t, 1, a[0].Cuts)
	assert.Equal(t, 12, a[0].OldBytes)
	assert.Equal(t, 8, a[0].NewBytes)
	assert.Equal(t, 9, a[0].Tokens)
}
