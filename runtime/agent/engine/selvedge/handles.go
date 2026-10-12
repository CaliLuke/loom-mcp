package selvedge

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

type workflowHandle struct {
	id     string
	client *sv.App
}

// SignalByID signals the instance; Selvedge uses the same ID for run and workflow.
func (e *Engine) SignalByID(ctx context.Context, id, runID, name string, payload any) error {
	if runID != "" && runID != id {
		return engine.ErrWorkflowNotFound
	}
	return (&workflowHandle{id: id, client: e.client}).Signal(ctx, name, payload)
}

// CancelByID requests durable cancellation.
func (e *Engine) CancelByID(ctx context.Context, id string) error {
	return (&workflowHandle{id: id, client: e.client}).Cancel(ctx)
}

// QueryRunCompletion waits for the recorded terminal output or failure.
func (e *Engine) QueryRunCompletion(ctx context.Context, id string) (*api.RunOutput, error) {
	return (&workflowHandle{id: id, client: e.client}).Wait(ctx)
}
func (h *workflowHandle) Wait(ctx context.Context) (*api.RunOutput, error) {
	result, err := sv.WaitWorkflow[*api.RunOutput](ctx, h.client, h.id)
	if err != nil {
		return nil, byIDError(err)
	}
	return result.Output, normalizeError(result.Error)
}

func (h *workflowHandle) Signal(ctx context.Context, name string, payload any) error {
	return byIDError(h.client.Signal(ctx, h.id, name, payload))
}

func (h *workflowHandle) Cancel(ctx context.Context) error {
	return byIDError(sv.CancelWorkflow(ctx, h.client, h.id, ""))
}
