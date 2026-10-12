package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// continuationEarlier collapses only the predecessor's own receipt chain.
// Its From link already contains earlier counts; rendering never traverses it.
func (r *Runtime) continuationEarlier(ctx context.Context, previous session.Continuation) (index.EarlierReceipt, error) {
	earlier := index.EarlierReceipt{Runs: 1, First: previous.Pos, Last: previous.Pos}
	hash := previous.Checkpoint
	seen := map[runstore.Hash]bool{}
	for hash != nil {
		if seen[*hash] {
			return earlier, fmt.Errorf("cyclic continuation checkpoint")
		}
		seen[*hash] = true
		data, err := r.RunStore.Get(ctx, previous.ID, *hash)
		if err != nil {
			return earlier, err
		}
		var record index.CheckpointRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return earlier, err
		}
		count, err := strconv.Atoi(strconv.FormatUint(uint64(record.Through), 10))
		if err != nil {
			return earlier, err
		}
		earlier.Messages = max(earlier.Messages, count)
		for _, line := range record.Receipt {
			if line.Kind == "call" {
				earlier.Calls++
			}
			if line.Kind == "assistant" {
				earlier.Assistants++
			}
		}
		if record.From != nil {
			earlier.Runs += record.Earlier.Runs
			earlier.Messages += record.Earlier.Messages
			earlier.Calls += record.Earlier.Calls
			earlier.Assistants += record.Earlier.Assistants
			if record.Earlier.First > 0 {
				earlier.First = record.Earlier.First
			}
		}
		hash = record.Prev
	}
	return earlier, nil
}

func (r *Runtime) continuationBudget(ctx context.Context, runID string) (index.RenderBudget, error) {
	cursor := ""
	for {
		page, err := r.RunEventStore.List(ctx, runID, cursor, 100)
		if err != nil {
			return index.RenderBudget{}, err
		}
		for _, event := range page.Events {
			if event.Type == hooks.RunSealed {
				var seal hooks.RunSealedEvent
				if err := json.Unmarshal(event.Payload, &seal); err != nil {
					return index.RenderBudget{}, err
				}
				if seal.FinalView != nil {
					return seal.FinalView.Budget, nil
				}
			}
		}
		if page.NextCursor == "" {
			return index.RenderBudget{}, nil
		}
		cursor = page.NextCursor
	}
}
