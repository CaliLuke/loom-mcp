package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
)

type scopedLongTermMemory struct {
	service memory.Service
	scope   memory.Scope
}

// AddRunToMemory ingests a run snapshot into the configured long-term memory service.
func (r *Runtime) AddRunToMemory(ctx context.Context, input memory.IngestRunInput) (memory.IngestResult, error) {
	if r.MemoryService == nil {
		return memory.IngestResult{}, fmt.Errorf("runtime: memory service is not configured")
	}
	if len(input.Events) == 0 {
		if r.Memory == nil {
			return memory.IngestResult{}, fmt.Errorf("runtime: memory store is required to load run events")
		}
		snapshot, err := r.Memory.LoadRun(ctx, input.AgentID, input.RunID)
		if err != nil {
			return memory.IngestResult{}, err
		}
		input.Events = snapshot.Events
	}
	return r.MemoryService.IngestRun(ctx, input)
}

// AddEventsToMemory ingests event deltas into the configured long-term memory service.
func (r *Runtime) AddEventsToMemory(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error) {
	if r.MemoryService == nil {
		return memory.IngestResult{}, fmt.Errorf("runtime: memory service is not configured")
	}
	if len(input.Events) == 0 {
		return memory.IngestResult{}, nil
	}
	return r.MemoryService.IngestEvents(ctx, input)
}

// PutMemoryEntry writes a direct long-term memory entry.
func (r *Runtime) PutMemoryEntry(ctx context.Context, input memory.PutEntryInput) (memory.Entry, error) {
	if r.MemoryService == nil {
		return memory.Entry{}, fmt.Errorf("runtime: memory service is not configured")
	}
	return r.MemoryService.PutEntry(ctx, input)
}

func resolveToolScopedMemory(ctx context.Context, service memory.Service, resolver memory.ScopeResolver, input memory.ScopeInput) (*scopedLongTermMemory, error) {
	if service == nil {
		return nil, fmt.Errorf("runtime: memory service is not configured")
	}
	if resolver == nil {
		resolver = memory.ScopeResolverFunc(defaultResolveMemoryScope)
	}
	scope, err := resolver.ResolveMemoryScope(ctx, input)
	if err != nil {
		return nil, err
	}
	return &scopedLongTermMemory{
		service: service,
		scope:   scope,
	}, nil
}

func (m *scopedLongTermMemory) Search(ctx context.Context, query string, labels map[string]string, limit int) (memory.SearchResult, error) {
	if m == nil || m.service == nil {
		return memory.SearchResult{}, fmt.Errorf("runtime: memory service is not configured")
	}
	if err := m.validateScope(); err != nil {
		return memory.SearchResult{}, err
	}
	return m.service.Search(ctx, memory.SearchQuery{
		Scope:  m.scope,
		Query:  query,
		Labels: labels,
		Limit:  limit,
	})
}

func (m *scopedLongTermMemory) validateScope() error {
	if m == nil {
		return fmt.Errorf("memory scope is required")
	}
	return validateMemoryScope(m.scope)
}
