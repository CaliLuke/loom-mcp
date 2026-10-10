package index_test

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepPayloadFaultShapes mirrors the formal model's entry-unit bounds.
// The whole PlanActivityInput byte bound belongs to the workflow wiring phase.
func TestStepPayloadFaultShapes(t *testing.T) {
	for _, fault := range []string{"none", "seed_in_pending", "pending_not_flushed", "accumulated_history", "head_before_store", "segment_prev_wrong"} {
		t.Run(fault, func(t *testing.T) {
			store := inmem.New()
			source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
				return store.Get(ctx, "root", hash)
			}
			seedEntries := make([]index.MessageEntry, 12)
			for i := range seedEntries {
				seedEntries[i] = index.MessageEntry{ID: index.MessageID(i + 1), Kind: index.Message, Hash: "seed"}
			}
			seed, err := index.NewSegment("", "", seedEntries)
			require.NoError(t, err)
			head := putSegment(t, store, seed)
			view := index.ViewRef{IndexHead: head, Pending: []index.MessageEntry{{ID: 13, Kind: index.TypedInput, Hash: "answer", InputID: "typed"}}}
			committed := 12
			inputUnits := 1
			switch fault {
			case "seed_in_pending":
				view.IndexHead = ""
				view.Pending = append(seed.Entries, view.Pending...)
				inputUnits = len(view.Pending)
				committed = 0
			case "accumulated_history":
				inputUnits += 20 // typed + tool-output history copied to input
			case "pending_not_flushed", "head_before_store", "segment_prev_wrong", "none":
			}
			bounded := inputUnits <= 2 // one step's maximum entries in this model
			assert.Equal(t, fault != "seed_in_pending" && fault != "accumulated_history", bounded)
			if fault == "seed_in_pending" || fault == "accumulated_history" {
				return // These violate InputBounded, not Reconstructs in the TLA model.
			}
			if fault != "pending_not_flushed" {
				seg, err := index.NewSegment(view.IndexHead, seed.Entries[11].Chain, view.Pending)
				require.NoError(t, err)
				if fault == "segment_prev_wrong" {
					seg.Prev = nil
					// Deliberately bypass EncodeSegment validation to model corrupt data.
					data, err := json.Marshal(seg)
					require.NoError(t, err)
					view.IndexHead, err = store.Put(t.Context(), "root", data)
					require.NoError(t, err)
				} else {
					view.IndexHead = putSegment(t, store, seg)
				}
				committed++
			}
			if fault == "head_before_store" {
				committed += 2 // Adopt counted the next assistant/call before storage.
			}
			got, err := index.Walk(t.Context(), source, view.IndexHead)
			if fault == "segment_prev_wrong" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, fault != "head_before_store", len(got) == committed)
			if fault == "pending_not_flushed" {
				// As in StepPayload.tla, Reconstructs still holds for the OLD head.
				// Failing to flush violates the next step's pending-input bound.
				view.Pending = append(view.Pending, index.MessageEntry{ID: 14}, index.MessageEntry{ID: 15})
				assert.Greater(t, len(view.Pending), 2)
			}
		})
	}
}

func TestWalkRejectsBrokenChains(t *testing.T) {
	for _, fault := range []string{"missing", "wrong prev", "wrong chain", "changed entry", "changed bytes", "gap"} {
		t.Run(fault, func(t *testing.T) {
			store := inmem.New()
			source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
				return store.Get(ctx, "root", hash)
			}
			first, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: "one"}})
			require.NoError(t, err)
			firstHead := putSegment(t, store, first)
			second, err := index.NewSegment(firstHead, first.Entries[0].Chain, []index.MessageEntry{{ID: 2, Kind: index.Message, Hash: "two"}})
			require.NoError(t, err)
			secondHead := putSegment(t, store, second)
			third, err := index.NewSegment(secondHead, second.Entries[0].Chain, []index.MessageEntry{{ID: 3, Kind: index.Message, Hash: "three"}})
			require.NoError(t, err)
			switch fault {
			case "missing":
				missing := runstore.Hash("missing")
				third.Prev = &missing
			case "wrong prev":
				third.Prev = &firstHead
			case "wrong chain":
				third.Entries[0].Chain = "wrong"
			case "changed entry":
				third.Entries[0].Hash = "replacement"
			case "gap":
				third.First = 4
				third.Entries[0].ID = 4
			case "changed bytes":
				source = func(context.Context, runstore.Hash) ([]byte, error) {
					return []byte(`{}`), nil
				}
			}
			head := putSegment(t, store, third)
			_, err = index.Walk(t.Context(), source, head)
			require.Error(t, err)
		})
	}
}

func TestSegmentOwnershipAndChainBinding(t *testing.T) {
	entry := index.MessageEntry{ID: 1, Kind: index.Assistant, Calls: []runstore.Hash{"call"}, ToolUseIDs: []string{"id"}}
	seg, err := index.NewSegment("", "", []index.MessageEntry{entry})
	require.NoError(t, err)
	entry.Calls[0] = "changed"
	entry.ToolUseIDs[0] = "changed"
	assert.Equal(t, runstore.Hash("call"), seg.Entries[0].Calls[0])
	assert.Equal(t, "id", seg.Entries[0].ToolUseIDs[0])
	changed, err := index.ComputeChain("predecessor", seg.Entries[0])
	require.NoError(t, err)
	assert.NotEqual(t, changed, seg.Entries[0].Chain)
	changed, err = index.ComputeChain("", entry)
	require.NoError(t, err)
	assert.NotEqual(t, changed, seg.Entries[0].Chain)
}
