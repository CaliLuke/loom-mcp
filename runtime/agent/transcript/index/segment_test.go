package index_test

import (
	"context"
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSegmentGolden(t *testing.T) {
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.Message, Hash: "sha256:caller", Role: "user", Bytes: 42},
		{ID: 2, Kind: index.Assistant, Hash: "sha256:transcript", Calls: []runstore.Hash{"sha256:call"}, Role: "assistant", ToolUseIDs: []string{"call-1"}},
		{ID: 3, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping, ProtocolVersion: 5}, ToolUseIDs: []string{"call-1"}},
	}
	seg, err := index.NewSegment("", "", entries)
	require.NoError(t, err)
	data, hash, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	want, err := os.ReadFile("testdata/segment.json")
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(data))
	assert.Equal(t, string(want), string(data)+"\n", "exact wire bytes")
	hashes, err := os.ReadFile("testdata/segment_hashes.json")
	require.NoError(t, err)
	var expected []runstore.Hash
	require.NoError(t, json.Unmarshal(hashes, &expected))
	assert.Equal(t, expected, []runstore.Hash{seg.Entries[0].Chain, seg.Entries[1].Chain, seg.Entries[2].Chain, hash})
	var decoded index.IndexSegment
	require.NoError(t, json.Unmarshal(data, &decoded))
	again, againHash, err := index.EncodeSegment(decoded)
	require.NoError(t, err)
	assert.Equal(t, data, again)
	assert.Equal(t, hash, againHash)
	assert.Empty(t, entries[0].Chain, "construction does not mutate caller entries")
}

func TestWalkReconstructsCommittedEntries(t *testing.T) {
	ctx := context.Background()
	store := inmem.New()
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return store.Get(ctx, "root", hash)
	}
	first, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: "seed"}})
	require.NoError(t, err)
	head := putSegment(t, store, first)
	second, err := index.NewSegment(head, first.Entries[0].Chain, []index.MessageEntry{{ID: 2, Kind: index.TypedInput, Hash: "answer", InputID: "input-1"}})
	require.NoError(t, err)
	next := putSegment(t, store, second)
	assert.Equal(t, next, putSegment(t, store, second), "crash after store retries identical bytes")
	got, err := index.Walk(ctx, source, head)
	require.NoError(t, err)
	assert.Equal(t, first.Entries, got, "stored but unadopted segment remains orphaned")
	got, err = index.Walk(ctx, source, next)
	require.NoError(t, err)
	assert.Equal(t, append(first.Entries, second.Entries...), got)
}

func putSegment(t *testing.T, store *inmem.Store, seg index.IndexSegment) runstore.Hash {
	t.Helper()
	data, expected, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	hash, err := store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	assert.Equal(t, expected, hash)
	return hash
}
