package selvedge

import (
	"context"
	"errors"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	sv "github.com/CaliLuke/selvedge"
)

// ReplayHistory verifies a recorded history using registered workflow definitions.
// It requires neither sealing nor starting the App and executes no activities.
func (e *Engine) ReplayHistory(ctx context.Context, history *sv.RecordedHistory) error {
	err := sv.ReplayHistory(ctx, e.app, history)
	var replayError *sv.ReplayError
	if errors.As(err, &replayError) && errors.Is(replayError.WorkflowErr, api.ErrWorkflowProtocolMismatch) {
		return replayError.WorkflowErr
	}
	return err
}

// ExportHistory reads a portable history from the initialized client store.
func (e *Engine) ExportHistory(ctx context.Context, id string) (*sv.RecordedHistory, error) {
	return sv.ExportHistory(ctx, e.client, id)
}
