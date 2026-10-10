package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Index segments use the run-store Mongo implementation's 15 MiB content cap.
// The reserve covers the predecessor, first ID and JSON array punctuation.
const indexSegmentMaxBytes = 15 * 1024 * 1024

func storeIndexSegments(ctx context.Context, store runstore.Store, root string, head, chain runstore.Hash, entries []index.MessageEntry) (runstore.Hash, []index.MessageEntry, error) {
	var committed []index.MessageEntry
	for len(entries) > 0 {
		size, n := 1024, 0
		for n < len(entries) {
			data, err := json.Marshal(entries[n], json.Deterministic(true))
			if err != nil {
				return "", nil, err
			}
			// Chain may still be empty on input.
			if size+len(data)+100 > indexSegmentMaxBytes {
				break
			}
			size += len(data) + 100
			n++
		}
		if n == 0 {
			return "", nil, fmt.Errorf("index entry exceeds content cap: %w", runstore.ErrContentTooLarge)
		}
		seg, err := index.NewSegment(head, chain, entries[:n])
		if err != nil {
			return "", nil, err
		}
		data, hash, err := index.EncodeSegment(seg)
		if err != nil {
			return "", nil, err
		}
		stored, err := store.Put(ctx, root, data)
		if err != nil {
			return "", nil, err
		}
		if stored != hash {
			return "", nil, fmt.Errorf("run store returned incorrect segment hash")
		}
		head, chain = hash, seg.Entries[n-1].Chain
		committed = append(committed, seg.Entries...)
		entries = entries[n:]
	}
	return head, committed, nil
}
