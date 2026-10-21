package agentfeatures_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
)

// waitForHooks synchronizes with recorded state and diagnoses terminal or deadline failures.
func (r *hookRecorder) waitForHooks(ctx context.Context, runID string, kinds ...hooks.EventType) error {
	for {
		r.mu.Lock()
		var observed []hooks.EventType
		for _, event := range r.events {
			if event.RunID() == runID {
				observed = append(observed, event.Type())
			}
		}
		ready := true
		for _, kind := range kinds {
			ready = ready && slices.Contains(observed, kind)
		}
		if r.changed == nil {
			r.changed = make(chan struct{})
		}
		changed := r.changed
		r.mu.Unlock()
		if ready {
			return nil
		}
		if slices.Contains(observed, hooks.RunCompleted) {
			return fmt.Errorf("run %s completed before hooks %v; observed %v", runID, kinds, observed)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("run %s waiting for hooks %v; observed %v: %w", runID, kinds, observed, ctx.Err())
		}
	}
}
