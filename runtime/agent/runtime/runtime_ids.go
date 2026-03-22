package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

const unknownID = "unknown"

type (
	// PromptRenderHookContext identifies one agent run turn for prompt_rendered hook emission.
	PromptRenderHookContext struct {
		RunID     string
		AgentID   agent.Ident
		SessionID string
		TurnID    string
	}

	promptRenderHookContextKey struct{}
)

// WithPromptRenderHookContext returns ctx stamped with run metadata used by runtime prompt observer callbacks.
func WithPromptRenderHookContext(ctx context.Context, meta PromptRenderHookContext) context.Context {
	return context.WithValue(ctx, promptRenderHookContextKey{}, meta)
}

// withPromptRenderHookContext returns a context stamped with runtime run metadata used by onPromptRendered.
func withPromptRenderHookContext(ctx context.Context, meta PromptRenderHookContext) context.Context {
	return WithPromptRenderHookContext(ctx, meta)
}

// promptRenderHookContextFromContext extracts prompt-render hook metadata.
func promptRenderHookContextFromContext(ctx context.Context) (PromptRenderHookContext, bool) {
	if ctx == nil {
		return PromptRenderHookContext{}, false
	}
	meta, ok := ctx.Value(promptRenderHookContextKey{}).(PromptRenderHookContext)
	if !ok {
		return PromptRenderHookContext{}, false
	}
	return meta, true
}

// hasNonNullJSON reports whether raw contains a non-empty JSON value other than the literal `null`.
func hasNonNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// NestedRunID generates a hierarchical run ID for nested agent execution.
func NestedRunID(parentRunID string, toolName tools.Ident) string {
	if parentRunID == "" {
		parentRunID = unknownID
	}
	return fmt.Sprintf("%s/agent/%s", parentRunID, toolName)
}

// NestedRunIDForToolCall generates a child workflow ID for agent-as-tool runs.
func NestedRunIDForToolCall(parentRunID string, toolName tools.Ident, toolCallID string) string {
	base := NestedRunID(parentRunID, toolName)
	if toolCallID == "" {
		return base
	}
	return fmt.Sprintf("%s/%s", base, nestedRunIDSuffix(toolCallID))
}

func nestedRunIDSuffix(toolCallID string) string {
	if strings.HasPrefix(toolCallID, "tooluse_") {
		return strings.ReplaceAll(toolCallID, "/", "-")
	}
	h := fnv.New64a()
	if _, err := h.Write([]byte(toolCallID)); err != nil {
		panic(fmt.Errorf("hash tool call id: %w", err))
	}
	return fmt.Sprintf("call-%016x", h.Sum64())
}

// generateDeterministicToolCallID creates a replay-safe tool-call ID.
func generateDeterministicToolCallID(runID, turnID string, attempt int, toolName tools.Ident, index int) string {
	if runID == "" {
		runID = unknownID
	}
	if toolName == "" {
		toolName = "tool"
	}
	safeTool := strings.ReplaceAll(string(toolName), ".", "-")
	tid := turnID
	if tid == "" {
		tid = "no-turn"
	}
	return strings.Join([]string{runID, tid, fmt.Sprintf("attempt-%d", attempt), safeTool, strconv.Itoa(index)}, "/")
}

// generateDeterministicAwaitID creates a replay-safe await identifier.
func generateDeterministicAwaitID(runID, turnID string, tool tools.Ident, toolCallID string) string {
	if runID == "" {
		runID = unknownID
	}
	safeTool := strings.ReplaceAll(string(tool), ".", "-")
	if safeTool == "" {
		safeTool = "tool"
	}
	tid := turnID
	if tid == "" {
		tid = "no-turn"
	}
	if toolCallID == "" {
		toolCallID = "no-call"
	}
	return strings.Join([]string{runID, tid, safeTool, "await", toolCallID}, "/")
}
