package runtime

import (
	"context"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
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
		query.SessionID = ""
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

func (r *Runtime) preloadLongTermMemory(ctx context.Context, policy *LongTermMemoryPreloadPolicy, agentID string, runCtx run.Context, messages []*model.Message) ([]memory.Entry, error) {
	if policy == nil || r.MemoryService == nil {
		return nil, nil
	}
	queryText := latestUserText(messages)
	if queryText == "" {
		return nil, nil
	}
	visibility := policy.Visibility
	if visibility == "" {
		visibility = memory.VisibilityUser
	}
	scoped, err := resolveToolScopedMemory(ctx, r.MemoryService, r.MemoryScopeResolver, memory.ScopeInput{
		AgentID:    agentID,
		SessionID:  runCtx.SessionID,
		RunID:      runCtx.RunID,
		Visibility: visibility,
		Labels:     runCtx.Labels,
		Payload: map[string]any{
			memoryPayloadQueryKey: queryText,
		},
	})
	if err != nil {
		return nil, err
	}
	result, err := scoped.Search(ctx, queryText, stripMemoryScopeLabels(runCtx.Labels), policy.MaxResults)
	if err != nil {
		return nil, err
	}
	entries := make([]memory.Entry, 0, len(result.Hits))
	for _, hit := range result.Hits {
		entries = append(entries, hit.Entry)
	}
	return entries, nil
}

func latestUserText(messages []*model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != model.ConversationRoleUser {
			continue
		}
		var parts []string
		for _, part := range msg.Parts {
			text, ok := part.(model.TextPart)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(text.Text); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		return ""
	}
	return ""
}
