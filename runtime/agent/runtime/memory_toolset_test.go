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

func TestMemoryToolsetCurrentRunUsesStoreWhenSearcherConfigured(t *testing.T) {
	store := memoryinmem.New()
	ctx := context.Background()
	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-1",
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "from store"}, nil),
	))
	called := false
	searcher := memory.SearcherFunc(func(_ context.Context, query memory.Query) (memory.QueryResult, error) {
		called = true
		return memory.QueryResult{
			Events: []memory.Event{
				memory.NewEvent(time.Unix(20, 0), memory.UserMessageData{Message: "from index"}, nil),
			},
		}, nil
	})

	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Store:      store,
		Searcher:   searcher,
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
	require.False(t, called)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"from store"`)
	require.NotContains(t, string(encoded), `"from index"`)
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

func TestMemoryToolsetSearchMemoryUsesResolvedUserScope(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	scope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   scope,
		Content: "Loom memory prefers explicit tenant routing",
		Author:  "user",
		Labels:  map[string]string{"project": "loom"},
	})
	require.NoError(t, err)

	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
		MaxResults: 5,
	})
	require.Len(t, reg.Specs, 1)
	require.Equal(t, "memory.search_memory", string(reg.Specs[0].Name))

	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Labels:     map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:    rawjson.Message([]byte(`{"query":"tenant routing","labels":{"project":"loom"},"limit":10}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "explicit tenant routing")
	require.Contains(t, string(encoded), `"project":"loom"`)
	require.NotContains(t, string(encoded), "memory.namespace")
	require.NotContains(t, string(encoded), "Scope")
	require.NotContains(t, string(encoded), "Sources")
	require.NotContains(t, string(encoded), "Metadata")
}

func TestMemoryToolsetSearchMemoryRejectsMissingUserScope(t *testing.T) {
	service := memoryinmem.NewService()
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
	})

	missingUser, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Payload:    rawjson.Message([]byte(`{"query":"tenant routing"}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, missingUser.ToolResult.Error)
	require.Equal(t, planner.RetryReasonUnsupportedOperation, missingUser.ToolResult.RetryHint.Reason)
}

func TestMemoryToolsetSearchMemoryIgnoresPayloadVisibility(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	userScope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}
	sharedScope := memory.Scope{Namespace: "workspace-a", Visibility: memory.VisibilityShared}
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   userScope,
		Content: "user scoped tenant routing",
		Author:  "user",
	})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   sharedScope,
		Content: "shared tenant routing",
		Author:  "system",
	})
	require.NoError(t, err)
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
	})

	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-2",
		Labels:     map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:    rawjson.Message([]byte(`{"query":"tenant routing","visibility":"shared"}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)
	got, ok := result.ToolResult.Result.(searchMemoryResult)
	require.True(t, ok)
	require.Len(t, got.Hits, 1)
	require.Equal(t, "user scoped tenant routing", got.Hits[0].Content)
	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "visibility")
}

func TestMemoryToolsetSearchMemoryDefaultVisibilityIgnoresSharedPayload(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser},
		Content: "default user tenant routing",
		Author:  "user",
	})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", Visibility: memory.VisibilityShared},
		Content: "default shared tenant routing",
		Author:  "system",
	})
	require.NoError(t, err)
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:    "memory",
		Service: service,
		Sources: []memory.ToolSource{memory.ToolSourceLongTerm},
	})

	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Labels:     map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:    rawjson.Message([]byte(`{"query":"tenant routing","visibility":"shared"}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)
	got, ok := result.ToolResult.Result.(searchMemoryResult)
	require.True(t, ok)
	require.Len(t, got.Hits, 1)
	require.Equal(t, "default user tenant routing", got.Hits[0].Content)
	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "visibility")
}

func TestMemoryToolsetSearchMemoryPayloadLabelsCannotOverrideScope(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser},
		Content: "authorized tenant memory",
		Author:  "user",
	})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-b", UserID: "user-2", Visibility: memory.VisibilityUser},
		Content: "other tenant memory",
		Author:  "user",
	})
	require.NoError(t, err)
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
	})

	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Labels:     map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:    rawjson.Message([]byte(`{"query":"tenant memory","labels":{"memory.namespace":"workspace-b","memory.user_id":"user-2"}}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)
	got, ok := result.ToolResult.Result.(searchMemoryResult)
	require.True(t, ok)
	require.Len(t, got.Hits, 1)
	require.Equal(t, "authorized tenant memory", got.Hits[0].Content)
}

func TestMemoryToolsetSearchMemoryReturnsModelFacingHits(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser},
		Content: "model facing tenant memory",
		Author:  "user",
		Sources: []memory.SourceRef{{
			Kind:      memory.SourceDirect,
			AgentID:   "svc.agent",
			SessionID: "session-1",
			RunID:     "run-1",
		}},
		Labels:   map[string]string{"project": "loom", "memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Metadata: map[string]any{"secret": "do-not-return"},
	})
	require.NoError(t, err)
	reg := NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
	})

	result, err := reg.Execute(ctx, &planner.ToolRequest{
		Name:       "memory.search_memory",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "memory-1",
		Labels:     map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:    rawjson.Message([]byte(`{"query":"tenant memory"}`)),
	})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)

	encoded, err := json.Marshal(result.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"content":"model facing tenant memory"`)
	require.Contains(t, string(encoded), `"author":"user"`)
	require.Contains(t, string(encoded), `"project":"loom"`)
	require.NotContains(t, string(encoded), "workspace-a")
	require.NotContains(t, string(encoded), "user-1")
	require.NotContains(t, string(encoded), "session-1")
	require.NotContains(t, string(encoded), "do-not-return")
	require.NotContains(t, string(encoded), "sources")
	require.NotContains(t, string(encoded), "metadata")
	require.NotContains(t, string(encoded), "scope")
	require.NotContains(t, string(encoded), "visibility")
}

func TestExecuteToolActivitySearchMemoryPreservesRuntimeLabels(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser},
		Content: "activity tenant memory",
		Author:  "user",
	})
	require.NoError(t, err)
	rt := newTestRuntimeWithPlanner("svc.agent", &stubPlanner{})
	require.NoError(t, rt.RegisterToolset(NewMemoryToolsetRegistration(MemoryToolsetConfig{
		Name:       "memory",
		Service:    service,
		Sources:    []memory.ToolSource{memory.ToolSourceLongTerm},
		Visibility: memory.VisibilityUser,
	})))

	out, err := rt.ExecuteToolActivity(ctx, &ToolInput{
		AgentID:     "svc.agent",
		RunID:       "run-1",
		ToolsetName: "memory",
		ToolName:    "memory.search_memory",
		ToolCallID:  "memory-1",
		Labels:      map[string]string{"memory.namespace": "workspace-a", "memory.user_id": "user-1"},
		Payload:     rawjson.Message([]byte(`{"query":"tenant memory"}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Contains(t, string(out.Payload), "activity tenant memory")
	require.NotContains(t, string(out.Payload), "visibility")
	require.NotContains(t, string(out.Payload), "memory.namespace")
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

func TestPlanStartActivityInjectsLongTermMemoryFromHistoryFilteredLatestUserText(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	scope := memory.Scope{Namespace: "agent:service.agent", UserID: "user-1", Visibility: memory.VisibilityUser}
	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   scope,
		Content: "filtered question matches long-term entry",
		Author:  "user",
	})
	require.NoError(t, err)

	called := false
	pl := &stubPlanner{start: func(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		called = true
		require.Len(t, input.Messages, 1)
		require.Len(t, input.PreloadedMemoryEntries, 1)
		require.Equal(t, "filtered question matches long-term entry", input.PreloadedMemoryEntries[0].Content)
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.MemoryService = service
	rt.MemoryScopeResolver = memory.ScopeResolverFunc(defaultResolveMemoryScope)
	reg := rt.agents["service.agent"]
	reg.Policy.History = func(_ context.Context, _ []*model.Message, _ []*model.ToolDefinition) ([]*model.Message, error) {
		return []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "filtered question"}}}}, nil
	}
	reg.Policy.PreloadLongTermMemory = &LongTermMemoryPreloadPolicy{Visibility: memory.VisibilityUser, MaxResults: 5}
	rt.agents["service.agent"] = reg

	out, err := rt.PlanStartActivity(ctx, &PlanActivityInput{
		AgentID: "service.agent",
		RunID:   "run-123",
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "unfiltered question"}}},
		},
		RunContext: run.Context{
			RunID:  "run-123",
			Labels: map[string]string{"memory.user_id": "user-1"},
		},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, out.Result.FinalResponse)
}

func TestPlanResumeActivitySkipsLongTermPreloadForToolResultOnlyUserMessage(t *testing.T) {
	ctx := context.Background()
	service := memoryinmem.NewService()
	called := false
	pl := &stubPlanner{resume: func(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
		called = true
		require.Empty(t, input.PreloadedMemoryEntries)
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.MemoryService = service
	rt.MemoryScopeResolver = memory.ScopeResolverFunc(defaultResolveMemoryScope)
	reg := rt.agents["service.agent"]
	reg.Policy.PreloadLongTermMemory = &LongTermMemoryPreloadPolicy{Visibility: memory.VisibilityUser, MaxResults: 5}
	rt.agents["service.agent"] = reg

	out, err := rt.PlanResumeActivity(ctx, &PlanActivityInput{
		AgentID: "service.agent",
		RunID:   "run-123",
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"ok": true}}}},
		},
		RunContext: run.Context{
			RunID:  "run-123",
			Labels: map[string]string{"memory.user_id": "user-1"},
		},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, out.Result.FinalResponse)
}

func TestPlanStartActivityLongTermPreloadRejectsInvalidResolvedScope(t *testing.T) {
	ctx := context.Background()
	pl := &stubPlanner{start: func(_ context.Context, _ *planner.PlanInput) (*planner.PlanResult, error) {
		t.Fatal("planner should not run with invalid memory preload scope")
		return nil, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.MemoryService = memoryinmem.NewService()
	rt.MemoryScopeResolver = memory.ScopeResolverFunc(func(context.Context, memory.ScopeInput) (memory.Scope, error) {
		return memory.Scope{Namespace: "workspace-a", Visibility: memory.VisibilityUser}, nil
	})
	reg := rt.agents["service.agent"]
	reg.Policy.PreloadLongTermMemory = &LongTermMemoryPreloadPolicy{Visibility: memory.VisibilityUser, MaxResults: 5}
	rt.agents["service.agent"] = reg

	_, err := rt.PlanStartActivity(ctx, &PlanActivityInput{
		AgentID: "service.agent",
		RunID:   "run-123",
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "find saved memory"}}},
		},
		RunContext: run.Context{RunID: "run-123"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user-scoped memory requires a user id")
}
