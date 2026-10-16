package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingToolMemoryStore struct{ memory.Store }

func TestMemoryToolRejectsInvalidCallsWithoutReadingStorage(t *testing.T) {
	for _, tc := range []struct{ name, tool, payload, want string }{
		{"nil", "", "", "request is nil"},
		{"unknown tool", "unknown", "{}", "unknown memory tool"},
		{"invalid load JSON", "load_memory", "{", "unexpected EOF"},
		{"invalid search JSON", "search_memory", "{", "unexpected EOF"},
		{"invalid load scope", "load_memory", `{"scope":"future"}`, "unknown memory scope"},
		{"empty search", "search_memory", `{"query":"  "}`, "search query is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{Store: failingToolMemoryStore{}})
			var call *planner.ToolRequest
			if tc.tool != "" {
				call = &planner.ToolRequest{Name: tools.Ident("memory." + tc.tool), Payload: rawjson.Message(tc.payload)}
			}
			result, err := reg.Execute(t.Context(), call)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
		})
	}
}

func TestMemoryToolUnavailableSourcesReturnActionableToolResults(t *testing.T) {
	for _, tc := range []struct {
		name, tool, payload, want string
		cfg                       MemoryToolsetConfig
	}{
		{"transcript not enabled", "load_memory", `{"scope":"current_run"}`, "not enabled", MemoryToolsetConfig{Sources: []memory.ToolSource{memory.ToolSourceLongTerm}}},
		{"transcript missing store", "load_memory", `{}`, "WithMemoryStore", MemoryToolsetConfig{}},
		{"index not enabled", "load_memory", `{"scope":"indexed"}`, "not enabled", MemoryToolsetConfig{Sources: []memory.ToolSource{memory.ToolSourceTranscript}}},
		{"index missing searcher", "load_memory", `{"scope":"indexed"}`, "WithMemorySearcher", MemoryToolsetConfig{}},
		{"long term not enabled", "search_memory", `{"query":"find"}`, "not enabled", MemoryToolsetConfig{}},
		{"long term missing service", "search_memory", `{"query":"find"}`, "WithMemoryService", MemoryToolsetConfig{Sources: []memory.ToolSource{memory.ToolSourceLongTerm}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewMemoryToolsetRegistration(tc.cfg)
			result, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: tools.Ident("memory." + tc.tool), ToolCallID: "call", Payload: rawjson.Message(tc.payload)})
			require.NoError(t, err)
			require.NotNil(t, result.ToolResult)
			require.NotNil(t, result.ToolResult.Error)
			assert.Contains(t, result.ToolResult.Error.Error(), tc.want)
			assert.Equal(t, "call", result.ToolResult.ToolCallID)
			require.NotNil(t, result.ToolResult.RetryHint)
			assert.Equal(t, planner.RetryReasonUnsupportedOperation, result.ToolResult.RetryHint.Reason)
			assert.True(t, result.ToolResult.RetryHint.RestrictToTool)
		})
	}
}

func TestMemoryToolPropagatesStoreFailure(t *testing.T) {
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{Store: failingToolMemoryStore{}})
	result, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: "memory.load_memory", AgentID: "agent", RunID: "run"})
	require.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, result)
}

func (s failingToolMemoryStore) LoadRun(context.Context, string, string) (memory.Snapshot, error) {
	return memory.Snapshot{}, assert.AnError
}
