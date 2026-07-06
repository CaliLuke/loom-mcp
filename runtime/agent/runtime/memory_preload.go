package runtime

import (
	"context"

	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
)

func (r *Runtime) preloadMemory(ctx context.Context, policy *MemoryPreloadPolicy, agentID string, runCtx run.Context, reader memory.Reader) ([]memory.Event, error) {
	if policy == nil {
		return nil, nil
	}
	query := memory.Query{
		AgentID:   agentID,
		RunID:     runCtx.RunID,
		SessionID: runCtx.SessionID,
		Labels:    runCtx.Labels,
		Limit:     policy.MaxResults,
	}
	switch policy.Scope {
	case MemoryScopeCurrentRun:
		if reader == nil {
			return nil, nil
		}
		result := memory.QueryEvents(reader.Events(), query)
		return result.Events, nil
	case MemoryScopeIndexed:
		if r.MemorySearcher == nil {
			return nil, nil
		}
		result, err := r.MemorySearcher.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		return result.Events, nil
	case "":
		return nil, nil
	default:
		return nil, nil
	}
}
