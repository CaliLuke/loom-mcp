package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// MemoryToolsetConfig configures model-facing memory tools.
	MemoryToolsetConfig struct {
		// Name is the toolset name used to construct canonical tool IDs.
		Name string
		// Store reads current-run memory when no Searcher is configured.
		Store memory.Store
		// Searcher performs indexed or cross-run memory lookup.
		Searcher memory.Searcher
		// MaxResults caps load_memory responses. Zero means no configured cap.
		MaxResults int
	}

	loadMemoryPayload struct {
		Scope      MemoryScope        `json:"scope,omitempty"`
		EventTypes []memory.EventType `json:"event_types,omitempty"`
		Labels     map[string]string  `json:"labels,omitempty"`
		Limit      int                `json:"limit,omitempty"`
	}

	loadMemoryResult struct {
		Events    []memory.Event `json:"events"`
		Truncated bool           `json:"truncated"`
		Scope     MemoryScope    `json:"scope"`
	}
)

const (
	defaultMemoryToolsetName = "memory"
	memoryToolLoad           = "load_memory"
)

// NewMemoryToolsetRegistration exposes memory lookup as an ordinary model tool.
func NewMemoryToolsetRegistration(cfg MemoryToolsetConfig) ToolsetRegistration {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = defaultMemoryToolsetName
	}
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for loading bounded run and indexed memory.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return executeMemoryTool(ctx, cfg, call)
		},
		Specs: []tools.ToolSpec{
			memoryToolSpec(name, memoryToolLoad, "Load bounded memory events for the current run or indexed memory."),
		},
	}
}

func executeMemoryTool(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	if call == nil {
		return nil, fmt.Errorf("memory tool request is nil")
	}
	switch call.Name.Tool() {
	case memoryToolLoad:
		var payload loadMemoryPayload
		if err := decodeMemoryPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeLoadMemory(ctx, cfg, call, payload)
	default:
		return nil, fmt.Errorf("unknown memory tool %q", call.Name)
	}
}

func executeLoadMemory(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest, payload loadMemoryPayload) (*ToolExecutionResult, error) {
	scope := payload.Scope
	if scope == "" {
		scope = MemoryScopeCurrentRun
	}
	query := memory.Query{
		AgentID:   string(call.AgentID),
		RunID:     call.RunID,
		SessionID: call.SessionID,
		Labels:    payload.Labels,
		Types:     payload.EventTypes,
		Limit:     memoryResultLimit(payload.Limit, cfg.MaxResults),
	}

	var result memory.QueryResult
	var err error
	switch {
	case cfg.Searcher != nil:
		result, err = cfg.Searcher.Query(ctx, query)
	case scope == MemoryScopeCurrentRun:
		if cfg.Store == nil {
			return nil, fmt.Errorf("memory store is required")
		}
		snapshot, loadErr := cfg.Store.LoadRun(ctx, string(call.AgentID), call.RunID)
		if loadErr != nil {
			return nil, loadErr
		}
		result = memory.QueryEvents(snapshot.Events, query)
	case scope == MemoryScopeIndexed:
		return unsupportedMemorySearch(call), nil
	default:
		return nil, fmt.Errorf("unknown memory scope %q", scope)
	}
	if err != nil {
		return nil, err
	}
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Result: loadMemoryResult{
			Events:    result.Events,
			Truncated: result.Truncated,
			Scope:     scope,
		},
	}), nil
}

func unsupportedMemorySearch(call *planner.ToolRequest) *ToolExecutionResult {
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      planner.NewToolError("indexed memory search is unavailable"),
		RetryHint: &planner.RetryHint{
			Reason:         planner.RetryReasonUnsupportedOperation,
			Tool:           call.Name,
			RestrictToTool: true,
			Message:        "Configure runtime.WithMemorySearcher to enable indexed memory search.",
		},
	})
}

func memoryResultLimit(requested, configured int) int {
	if configured > 0 && (requested <= 0 || requested > configured) {
		return configured
	}
	return requested
}

func decodeMemoryPayload(call *planner.ToolRequest, out any) error {
	if len(call.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(call.Payload.RawMessage(), out)
}

func memoryToolSpec(toolset, tool, description string) tools.ToolSpec {
	id := tools.Ident(toolset + "." + tool)
	return tools.ToolSpec{
		Name:        id,
		Toolset:     toolset,
		Description: description,
		Payload:     tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:      tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}
}
