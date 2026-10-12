package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/continuation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

func (r *Runtime) continuationChain(ctx context.Context, scope, runID string) (*continuation.Chain, error) {
	store, ok := r.SessionStore.(session.ContinuationStore)
	if !ok || scope != runID {
		return nil, nil
	}
	start, err := r.continuationEvent(ctx, runID, hooks.ContinuationStarted)
	if err != nil || start == nil {
		return nil, err
	}
	meta, err := r.SessionStore.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return &continuation.Chain{RunID: runID, SessionID: meta.SessionID, Prev: start.Prev, Pos: start.Pos, Sessions: r.SessionStore, Continuations: store, Content: r.RunStore, HeadOf: r.continuationHead, Authorize: r.recallAuthorizer}, nil
}

func (r *Runtime) continuationHead(ctx context.Context, runID, scope string) (runstore.Hash, error) {
	if scope != runID {
		return "", fmt.Errorf("continuation is not a root")
	}
	cursor := ""
	for {
		page, err := r.RunEventStore.List(ctx, runID, cursor, 100)
		if err != nil {
			return "", err
		}
		for _, event := range page.Events {
			if event.Type == hooks.RunSealed {
				var seal hooks.RunSealedEvent
				if err := json.Unmarshal(event.Payload, &seal); err != nil {
					return "", err
				}
				return seal.IndexHead, nil
			}
		}
		if page.NextCursor == "" {
			return "", fmt.Errorf("continuation lacks sealed index")
		}
		cursor = page.NextCursor
	}
}
