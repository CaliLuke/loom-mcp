package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/stretchr/testify/require"
)

func TestMemoryToolsetLoadMemoryFallsBackToCurrentRunStore(t *testing.T) {
	store := memoryinmem.New()
	ctx := context.Background()
	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-1",
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "hello"}, map[string]string{"tenant": "acme"}),
		memory.NewEvent(time.Unix(20, 0), memory.PlannerNoteData{Note: "note"}, map[string]string{"tenant": "acme"}),
	))

	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Store:      store,
		MaxResults: 20,
	})
	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.load_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Payload:    rawjson.Message([]byte(`{"scope":"current_run","event_types":["user_message"],"labels":{"tenant":"acme"},"limit":5}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"scope":"current_run"`)
	require.Contains(t, string(encoded), `"user_message"`)
	require.NotContains(t, string(encoded), `"planner_note"`)
}

func TestMemoryToolsetCurrentRunWithSessionIDReturnsRunEventsWithoutSessionLabels(t *testing.T) {
	store := memoryinmem.New()
	ctx := context.Background()
	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-1",
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "sessionless label event"}, nil),
		memory.NewEvent(time.Unix(20, 0), memory.PlannerNoteData{Note: "run scoped note"}, nil),
	))

	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Store:      store,
		MaxResults: 20,
	})
	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.load_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		SessionID:  "sess-1",
		ToolCallID: "memory-1",
		Payload:    rawjson.Message([]byte(`{"scope":"current_run","limit":5}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"scope":"current_run"`)
	require.Contains(t, string(encoded), `"sessionless label event"`)
	require.Contains(t, string(encoded), `"run scoped note"`)
}

func TestMemoryToolsetLoadMemoryUsesConfiguredSearcher(t *testing.T) {
	called := false
	searcher := memory.SearcherFunc(func(_ context.Context, query memory.Query) (memory.QueryResult, error) {
		called = true
		require.Equal(t, "svc.agent", query.AgentID)
		require.Equal(t, "run-1", query.RunID)
		require.Equal(t, "session-1", query.SessionID)
		require.Equal(t, "acme", query.Labels["tenant"])
		require.Equal(t, []memory.EventType{memory.EventUserMessage}, query.Types)
		require.Equal(t, 2, query.Limit)
		return memory.QueryResult{
			Events: []memory.Event{
				memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "from index"}, nil),
			},
		}, nil
	})

	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Searcher:   searcher,
		MaxResults: 2,
	})
	result, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "memory.load_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		SessionID:  "session-1",
		ToolCallID: "memory-1",
		Payload:    rawjson.Message([]byte(`{"scope":"indexed","event_types":["user_message"],"labels":{"tenant":"acme"},"limit":20}`)),
	})
	require.NoError(t, err)
	require.True(t, called)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"from index"`)
	require.Contains(t, string(encoded), `"scope":"indexed"`)
}

func TestMemoryToolsetIndexedSearchUnavailableReturnsStructuredToolError(t *testing.T) {
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{Name: "memory"})
	result, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "memory.load_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Payload:    rawjson.Message([]byte(`{"scope":"indexed","limit":5}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ToolResult.Error)
	require.NotNil(t, result.ToolResult.RetryHint)
	require.Equal(t, planner.RetryReasonUnsupportedOperation, result.ToolResult.RetryHint.Reason)
}

func TestPlanStartActivityInjectsPreloadedMemory(t *testing.T) {
	store := memoryinmem.New()
	ctx := context.Background()
	require.NoError(t, store.AppendEvents(ctx, "service.agent", "run-123",
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "remember me"}, map[string]string{"tenant": "acme"}),
	))

	called := false
	pl := &stubPlanner{start: func(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		called = true
		require.Len(t, input.PreloadedMemory, 1)
		require.Equal(t, memory.EventUserMessage, input.PreloadedMemory[0].Type)
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.Memory = store
	reg := rt.agents["service.agent"]
	reg.Policy.PreloadMemory = &MemoryPreloadPolicy{Scope: MemoryScopeCurrentRun, MaxResults: 5}
	rt.agents["service.agent"] = reg

	out, err := rt.PlanStartActivity(ctx, &PlanActivityInput{
		AgentID: "service.agent",
		RunID:   "run-123",
		RunContext: run.Context{
			RunID:  "run-123",
			Labels: map[string]string{"tenant": "acme"},
		},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, out.Result.FinalResponse)
}

func TestPlanStartActivityPreloadCurrentRunWithSessionIDReturnsRunEvents(t *testing.T) {
	store := memoryinmem.New()
	ctx := context.Background()
	require.NoError(t, store.AppendEvents(ctx, "service.agent", "run-123",
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "preload without session label"}, nil),
	))

	called := false
	pl := &stubPlanner{start: func(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		called = true
		require.Len(t, input.PreloadedMemory, 1)
		require.Equal(t, memory.EventUserMessage, input.PreloadedMemory[0].Type)
		data, err := memory.DecodeUserMessageData(input.PreloadedMemory[0])
		require.NoError(t, err)
		require.Equal(t, "preload without session label", data.Message)
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.Memory = store
	reg := rt.agents["service.agent"]
	reg.Policy.PreloadMemory = &MemoryPreloadPolicy{Scope: MemoryScopeCurrentRun, MaxResults: 5}
	rt.agents["service.agent"] = reg

	out, err := rt.PlanStartActivity(ctx, &PlanActivityInput{
		AgentID: "service.agent",
		RunID:   "run-123",
		RunContext: run.Context{
			RunID:     "run-123",
			SessionID: "sess-1",
		},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, out.Result.FinalResponse)
}
