package runtime

import (
	"context"
	"encoding/json/v2"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type continuationEvent struct {
	InputHash runstore.Hash `json:"input_hash,omitzero"`
	ID        string        `json:"id,omitzero"`
	Prev      *string       `json:"prev"`
	Pos       int           `json:"pos"`
	Head      *string       `json:"head,omitzero"`
	Outcome   string        `json:"outcome"`
	Reason    string        `json:"reason,omitzero"`
	Seeded    int           `json:"seeded,omitzero"`
	SeedRef   runstore.Hash `json:"seed_ref,omitzero"`
}

func (r *Runtime) appendContinuationEvent(ctx context.Context, runID, sessionID string, kind hooks.EventType, at time.Time, value continuationEvent) error {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return err
	}
	_, err = r.RunEventStore.Append(ctx, &runlog.Event{RunID: runID, SessionID: sessionID, Type: kind, EventKey: string(kind) + ":" + runID, Timestamp: at.UTC(), Payload: data})
	return err
}

// continuationEvent reads canonical decisions before retrying activities, so an
// acknowledgement loss cannot change the recorded predecessor or outcome.
func (r *Runtime) continuationEvent(ctx context.Context, runID string, kind hooks.EventType) (*continuationEvent, error) {
	cursor := ""
	for {
		page, err := r.RunEventStore.List(ctx, runID, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, event := range page.Events {
			if event.Type == kind {
				var out continuationEvent
				if err := json.Unmarshal(event.Payload, &out); err != nil {
					return nil, err
				}
				return &out, nil
			}
		}
		if page.NextCursor == "" {
			return nil, nil
		}
		cursor = page.NextCursor
	}
}
