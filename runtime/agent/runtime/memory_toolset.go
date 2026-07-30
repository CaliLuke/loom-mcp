package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
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
		// Service performs long-term memory entry search.
		Service memory.Service
		// ScopeResolver derives tenant/user routing for long-term memory.
		ScopeResolver memory.ScopeResolver
		// Sources selects which memory tools and transcript scopes are enabled.
		Sources []memory.ToolSource
		// Visibility is the widest long-term memory visibility allowed by this toolset.
		Visibility memory.Visibility
		// MaxResults caps memory responses. Zero uses DefaultMemoryToolResultLimit.
		// Set UnlimitedToolsetLimit to disable the runtime ceiling.
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

	searchMemoryPayload struct {
		Query  string            `json:"query"`
		Labels map[string]string `json:"labels,omitempty"`
		Limit  int               `json:"limit,omitempty"`
	}

	searchMemoryResult struct {
		Hits      []searchMemoryHit `json:"hits"`
		Truncated bool              `json:"truncated"`
	}

	searchMemoryHit struct {
		Content string            `json:"content"`
		Author  string            `json:"author,omitempty"`
		Labels  map[string]string `json:"labels,omitempty"`
		Score   float64           `json:"score,omitempty"`
		Snippet string            `json:"snippet,omitempty"`
	}
)

const (
	defaultMemoryToolsetName = "memory"
	memoryToolLoad           = "load_memory"
	memoryToolSearch         = "search_memory"
	memoryPayloadQueryKey    = "query"
	memoryNamespaceLabel     = "memory.namespace"
	memoryUserIDLabel        = "memory.user_id"

	// DefaultMemoryToolResultLimit bounds memory tool results when MaxResults is unset.
	DefaultMemoryToolResultLimit = 100
	// UnlimitedToolsetLimit disables built-in runtime ceilings for toolset limits.
	UnlimitedToolsetLimit = -1
)

// NewMemoryToolsetRegistration exposes memory lookup as ordinary model tools.
func NewMemoryToolsetRegistration(cfg MemoryToolsetConfig) ToolsetRegistration {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = defaultMemoryToolsetName
	}
	specs := make([]tools.ToolSpec, 0, 2)
	if memoryToolsetCanLoadMemory(cfg) {
		specs = append(specs, memoryToolSpec(name, memoryToolLoad, "Load bounded memory events for the current run or indexed memory."))
	}
	if memorySourcesAllow(cfg.Sources, memory.ToolSourceLongTerm) && cfg.Service != nil {
		specs = append(specs, memoryToolSpec(name, memoryToolSearch, "Search bounded long-term memory entries."))
	}
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for loading bounded run, indexed, and long-term memory.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return executeMemoryTool(ctx, cfg, call)
		},
		Specs: specs,
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
	case memoryToolSearch:
		var payload searchMemoryPayload
		if err := decodeMemoryPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeSearchMemory(ctx, cfg, call, payload)
	default:
		return nil, fmt.Errorf("unknown memory tool %q", call.Name)
	}
}

func executeLoadMemory(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest, payload loadMemoryPayload) (*ToolExecutionResult, error) {
	scope := payload.Scope
	if scope == "" {
		scope = defaultLoadMemoryScope(cfg.Sources)
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
	var unsupported *ToolExecutionResult
	switch scope {
	case MemoryScopeCurrentRun:
		result, unsupported, err = executeCurrentRunMemory(ctx, cfg, call, query)
	case MemoryScopeIndexed:
		result, unsupported, err = executeIndexedMemory(ctx, cfg, call, query)
	default:
		return nil, fmt.Errorf("unknown memory scope %q", scope)
	}
	if unsupported != nil {
		return unsupported, nil
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

func executeCurrentRunMemory(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest, query memory.Query) (memory.QueryResult, *ToolExecutionResult, error) {
	if !memorySourcesAllow(cfg.Sources, memory.ToolSourceTranscript) {
		return memory.QueryResult{}, unsupportedMemorySource(call, "current-run transcript memory is not enabled for this toolset"), nil
	}
	if cfg.Store == nil {
		return memory.QueryResult{}, unsupportedMemorySource(call, "Configure runtime.WithMemoryStore to enable current-run memory loading."), nil
	}
	snapshot, err := cfg.Store.LoadRun(ctx, string(call.AgentID), call.RunID)
	if err != nil {
		return memory.QueryResult{}, nil, err
	}
	query.SessionID = ""
	return memory.QueryEvents(snapshot.Events, query), nil, nil
}

func executeIndexedMemory(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest, query memory.Query) (memory.QueryResult, *ToolExecutionResult, error) {
	if !memorySourcesAllow(cfg.Sources, memory.ToolSourceIndexedTranscript) {
		return memory.QueryResult{}, unsupportedMemorySource(call, "indexed transcript memory is not enabled for this toolset"), nil
	}
	if cfg.Searcher == nil {
		return memory.QueryResult{}, unsupportedMemorySearch(call), nil
	}
	result, err := cfg.Searcher.Query(ctx, query)
	return result, nil, err
}

func executeSearchMemory(ctx context.Context, cfg MemoryToolsetConfig, call *planner.ToolRequest, payload searchMemoryPayload) (*ToolExecutionResult, error) {
	queryText := strings.TrimSpace(payload.Query)
	if queryText == "" {
		return nil, fmt.Errorf("memory search query is required")
	}
	if unavailable := longTermMemoryUnavailable(cfg, call); unavailable != nil {
		return unavailable, nil
	}
	visibility := effectiveMemoryVisibilityCeiling(cfg.Visibility)
	scoped, err := resolveToolScopedMemory(ctx, cfg.Service, cfg.ScopeResolver, memory.ScopeInput{
		AgentID:    string(call.AgentID),
		SessionID:  call.SessionID,
		RunID:      call.RunID,
		Visibility: visibility,
		Labels:     call.Labels,
		Payload: map[string]any{
			memoryPayloadQueryKey: queryText,
		},
	})
	if err != nil {
		return nil, err
	}
	if err := scoped.validateScope(); err != nil {
		return unsupportedMemorySource(call, err.Error()), nil
	}
	result, err := scoped.Search(ctx, queryText, searchMemoryLabels(payload.Labels), memoryResultLimit(payload.Limit, cfg.MaxResults))
	if err != nil {
		return nil, err
	}
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Result: searchMemoryResult{
			Hits:      modelMemoryHits(result.Hits),
			Truncated: result.Truncated,
		},
	}), nil
}

func effectiveMemoryVisibilityCeiling(ceiling memory.Visibility) memory.Visibility {
	if ceiling != "" {
		return ceiling
	}
	return memory.VisibilityUser
}

func longTermMemoryUnavailable(cfg MemoryToolsetConfig, call *planner.ToolRequest) *ToolExecutionResult {
	if !memorySourcesAllow(cfg.Sources, memory.ToolSourceLongTerm) {
		return unsupportedMemorySource(call, "long-term memory is not enabled for this toolset")
	}
	if cfg.Service == nil {
		return unsupportedMemorySource(call, "Configure runtime.WithMemoryService to enable long-term memory search.")
	}
	return nil
}

func unsupportedMemorySearch(call *planner.ToolRequest) *ToolExecutionResult {
	return unsupportedMemorySource(call, "Configure runtime.WithMemorySearcher to enable indexed memory search.")
}

func unsupportedMemorySource(call *planner.ToolRequest, message string) *ToolExecutionResult {
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      planner.NewToolError(message),
		RetryHint: &planner.RetryHint{
			Reason:         planner.RetryReasonUnsupportedOperation,
			Tool:           call.Name,
			RestrictToTool: true,
			Message:        message,
		},
	})
}

func memoryResultLimit(requested, configured int) int {
	return toolsetLimit(requested, configured, DefaultMemoryToolResultLimit)
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

func memorySourcesAllow(sources []memory.ToolSource, source memory.ToolSource) bool {
	if len(sources) == 0 {
		return source == memory.ToolSourceTranscript || source == memory.ToolSourceIndexedTranscript
	}
	for _, candidate := range sources {
		if candidate == source {
			return true
		}
	}
	return false
}

func memoryToolsetCanLoadMemory(cfg MemoryToolsetConfig) bool {
	return memorySourcesAllow(cfg.Sources, memory.ToolSourceTranscript) && cfg.Store != nil ||
		memorySourcesAllow(cfg.Sources, memory.ToolSourceIndexedTranscript) && cfg.Searcher != nil
}

func defaultLoadMemoryScope(sources []memory.ToolSource) MemoryScope {
	transcript := memorySourcesAllow(sources, memory.ToolSourceTranscript)
	indexed := memorySourcesAllow(sources, memory.ToolSourceIndexedTranscript)
	if indexed && !transcript {
		return MemoryScopeIndexed
	}
	return MemoryScopeCurrentRun
}

func validateMemoryScope(scope memory.Scope) error {
	if strings.TrimSpace(scope.Namespace) == "" {
		return fmt.Errorf("memory namespace is required")
	}
	switch scope.Visibility {
	case memory.VisibilityUser:
		if strings.TrimSpace(scope.UserID) == "" {
			return fmt.Errorf("user-scoped memory requires a user id")
		}
	case memory.VisibilityShared:
	case "":
		return fmt.Errorf("memory visibility is required")
	default:
		return fmt.Errorf("unknown memory visibility %q", scope.Visibility)
	}
	return nil
}

func stripMemoryScopeLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		if key == memoryNamespaceLabel || key == memoryUserIDLabel {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func searchMemoryLabels(labels map[string]string) map[string]string {
	return stripMemoryScopeLabels(labels)
}

func modelMemoryHits(hits []memory.SearchHit) []searchMemoryHit {
	if len(hits) == 0 {
		return nil
	}
	out := make([]searchMemoryHit, 0, len(hits))
	for _, hit := range hits {
		content := strings.TrimSpace(hit.Entry.Content)
		if content == "" {
			continue
		}
		out = append(out, searchMemoryHit{
			Content: content,
			Author:  strings.TrimSpace(hit.Entry.Author),
			Labels:  stripMemoryScopeLabels(hit.Entry.Labels),
			Score:   hit.Score,
			Snippet: strings.TrimSpace(hit.Snippet),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
