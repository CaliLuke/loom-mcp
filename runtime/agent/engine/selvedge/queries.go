package selvedge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	sv "github.com/CaliLuke/selvedge"
)

// ErrQueriesNeedWorker rejects queries without local workflow definitions.
var ErrQueriesNeedWorker = errors.New("selvedge: queries need a worker-mode engine with the workflow registered")

// QueryRunStatus returns the durable status of an instance.
func (e *Engine) QueryRunStatus(ctx context.Context, id string) (engine.RunStatus, error) {
	description, err := sv.Describe(ctx, e.client, id)
	if err != nil {
		return "", byIDError(err)
	}
	switch description.Status {
	case sv.StatusRunning:
		return engine.RunStatusRunning, nil
	case sv.StatusCompleted:
		return engine.RunStatusCompleted, nil
	case sv.StatusFailed:
		return engine.RunStatusFailed, nil
	case sv.StatusCancelled:
		return engine.RunStatusCanceled, nil
	default:
		return "", fmt.Errorf("selvedge: unsupported workflow status %q", description.Status)
	}
}

// QueryRootRunID evaluates the workflow's registered root query.
func (e *Engine) QueryRootRunID(ctx context.Context, id string) (string, error) {
	return query[string](ctx, e, id, api.QueryRootRunID)
}

// QueryToolAwaits evaluates the registered tool result barrier query.
func (e *Engine) QueryToolAwaits(ctx context.Context, id string) ([]planner.AwaitExternalTools, error) {
	return query[[]planner.AwaitExternalTools](ctx, e, id, engine.QueryToolAwaits)
}

func query[T any](ctx context.Context, e *Engine, id, name string) (T, error) {
	var out T
	if e.app == nil {
		return out, ErrQueriesNeedWorker
	}
	data, err := sv.Query[json.RawMessage](ctx, e.app, id, name)
	if err != nil {
		return out, byIDError(err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}
