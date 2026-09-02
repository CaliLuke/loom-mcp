package agentfeatures_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features"
	"example.com/agentfeatures/gen/features/agents/coordinator"
	coordinatorworkflow "example.com/agentfeatures/gen/features/agents/coordinator/workflow"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	agentdebug "github.com/CaliLuke/loom-mcp/v2/runtime/agent/debug"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedFeatureFixtureRegistersRuntimeSurface(t *testing.T) {
	ctx := context.Background()
	rt := agentsruntime.New(
		agentsruntime.WithMemoryStore(memoryinmem.New()),
		agentsruntime.WithMemoryService(memoryinmem.NewService()),
		agentsruntime.WithMemoryScopeResolver(memory.ScopeResolverFunc(func(_ context.Context, input memory.ScopeInput) (memory.Scope, error) {
			return memory.Scope{
				Namespace:  "agent:" + input.AgentID,
				UserID:     "fixture-user",
				Visibility: input.Visibility,
			}, nil
		})),
		agentsruntime.WithArtifactStore(artifact.NewMemoryStore()),
		agentsruntime.WithNamedInterceptors(map[string]agentsruntime.Interceptor{
			"audit": &auditInterceptor{},
		}),
	)
	registerCoordinatorToolsets(t, ctx, rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))

	toolsets := rt.ListToolsets()
	require.Contains(t, toolsets, "features.artifacts")
	require.Contains(t, toolsets, "features.memory")
	require.Contains(t, toolsets, "features.long_term_memory")
	require.Contains(t, toolsets, "features.skills")
	require.Contains(t, toolsets, "features.workflow")
	require.ElementsMatch(t, []tools.Ident{
		workflow.Draft,
		workflow.MethodEcho,
		workflow.Review,
		workflow.Retry,
		workflow.Publish,
		workflow.Revise,
	}, workflow.Names())
}

func TestGeneratedFeatureInMemoryModelRecovery(t *testing.T) {
	t.Parallel()

	runGeneratedModelRecoveryScenario(
		t,
		context.Background(),
		newFeatureRuntime(t),
		"sess-inmem-recovery",
		"run-inmem-recovery",
	)
}

func TestAgentFeatureMethodBackedDispatcher(t *testing.T) {
	ctx := context.Background()
	fx := newFeatureRuntime(t)
	svc := &methodBackedFeatureService{}
	client := features.NewClient(features.NewEchoTopicEndpoint(svc))
	exec := coordinatorworkflow.NewCoordinatorWorkflowExec(coordinatorworkflow.WithClient(client))

	registerCoordinatorToolsets(t, ctx, fx.rt, exec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{}))

	out, err := fx.rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-method-backed-dispatcher",
		SessionID:  "sess-method-backed-dispatcher",
		ToolName:   workflow.MethodEcho,
		ToolCallID: "method-echo",
		Payload:    rawjson.Message([]byte(`{"topic":"loom"}`)),
		Labels:     map[string]string{"tenant": "acme"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true,"message":"echo:loom"}`, string(out.Payload))
	require.Equal(t, "loom", svc.topic)
}

func TestGeneratedFeatureRunPublishesAwaitAndResumesWithTypedInput(t *testing.T) {
	ctx := context.Background()
	fx := newFeatureRuntime(t)
	exec := newRecordingWorkflowExecutor()
	runGeneratedFeatureAwaitScenario(t, ctx, fx, exec, "sess-1", "run-generated-agent-features", 0, time.Second)
}

func runGeneratedFeatureAwaitScenario(
	t *testing.T,
	ctx context.Context,
	fx *featureRuntime,
	exec *recordingWorkflowExecutor,
	sessionID string,
	runID string,
	answerDelay time.Duration,
	awaitTimeout time.Duration,
	runOpts ...agentsruntime.RunOption,
) {
	t.Helper()
	rt := fx.rt
	exec.failRetryOnce = true
	registerCoordinatorToolsets(t, ctx, rt, exec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))
	_, err := rt.CreateSession(ctx, sessionID)
	require.NoError(t, err)

	runOpts = append(runOpts, agentsruntime.WithRunID(runID))
	handle, err := rt.MustClient(coordinator.AgentID).Start(
		ctx,
		sessionID,
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ship it"}}}},
		runOpts...,
	)
	require.NoError(t, err)
	if !assert.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitTypedInput) == 1
	}, awaitTimeout, 10*time.Millisecond) {
		waitCtx, cancelWait := context.WithTimeout(ctx, 2*time.Second)
		defer cancelWait()
		out, waitErr := handle.Wait(waitCtx)
		require.FailNowf(t, "typed-input await was not published", "run output: %#v; wait error: %v", out, waitErr)
	}
	require.NoError(t, rt.PauseRun(ctx, &api.PauseRequest{
		RunID:       runID,
		Reason:      "fixture-review",
		RequestedBy: "fixture-reviewer",
	}))
	require.NoError(t, rt.ResumeRun(ctx, &api.ResumeRequest{
		RunID:       runID,
		Notes:       "continue fixture",
		RequestedBy: "fixture-reviewer",
	}))
	if answerDelay > 0 {
		timer := time.NewTimer(answerDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case <-timer.C:
		}
	}

	err = rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
		RunID:   runID,
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"content-type":"application/json"}`)),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1
	}, awaitTimeout, 10*time.Millisecond)
	require.NoError(t, rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{
		RunID:       runID,
		Approved:    true,
		RequestedBy: "fixture-reviewer",
	}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)

	require.Equal(t, coordinator.AgentID, out.AgentID)
	require.Equal(t, runID, out.RunID)
	require.Equal(t, "generated workflow complete", messageText(out.Final))
	require.ElementsMatch(t, []string{"draft", "review", "retry#1", "retry#2", "publish"}, exec.toolCallIDs())
	require.Contains(t, toolEventCallIDs(out.ToolEvents), "publish")
	retryEvent := toolEventByCallID(out.ToolEvents, "retry#1")
	require.NotNil(t, retryEvent)
	require.NotNil(t, retryEvent.Error)
	require.NotNil(t, retryEvent.RetryHint)
	require.Equal(t, workflow.Retry, retryEvent.RetryHint.Tool)
	require.NotNil(t, toolEventByCallID(out.ToolEvents, "retry#2"))
	require.Len(t, publishArtifacts(out.ToolEvents), 1)
	require.GreaterOrEqual(t, fx.audit.beforeRunCount(), 1)
	require.GreaterOrEqual(t, fx.audit.beforeToolCount(), 5)
	require.GreaterOrEqual(t, fx.recorder.count(hooks.ToolResultReceived), 5)
	require.Equal(t, 1, fx.recorder.count(hooks.AwaitConfirmation))
	require.Equal(t, 1, fx.recorder.count(hooks.ToolAuthorization))
	require.GreaterOrEqual(t, fx.recorder.count(hooks.RunPaused), 3)
	require.GreaterOrEqual(t, fx.recorder.count(hooks.RunResumed), 3)

	runMeta, err := rt.SessionStore.LoadRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, runMeta.Status)
	require.Equal(t, sessionID, runMeta.SessionID)
	page, err := rt.RunEventStore.List(ctx, runID, "", 100)
	require.NoError(t, err)
	eventTypes := make([]hooks.EventType, 0, len(page.Events))
	for _, event := range page.Events {
		eventTypes = append(eventTypes, event.Type)
	}
	require.Contains(t, eventTypes, hooks.AwaitTypedInput)
	require.Contains(t, eventTypes, hooks.AwaitConfirmation)
	require.Contains(t, eventTypes, hooks.ToolAuthorization)
	require.Contains(t, eventTypes, hooks.RunPaused)
	require.Contains(t, eventTypes, hooks.RunCompleted)
	require.Equal(t, 1, fx.stream.count(stream.EventAwaitTypedInput))
	require.Equal(t, 1, fx.stream.count(stream.EventAwaitConfirmation))
	require.Equal(t, 1, fx.stream.count(stream.EventToolAuthorization))
	require.Equal(t, 1, fx.stream.count(stream.EventRunStreamEnd))
}

func TestGeneratedFeatureRunPersistsArtifactsMemorySkillsAndDebugState(t *testing.T) {
	ctx := context.Background()
	var indexedQuery memory.Query
	fx := newFeatureRuntime(t, agentsruntime.WithMemorySearcher(memory.SearcherFunc(func(_ context.Context, query memory.Query) (memory.QueryResult, error) {
		indexedQuery = query
		return memory.QueryResult{Events: []memory.Event{
			memory.NewEvent(time.Unix(30, 0), memory.UserMessageData{Message: "indexed memory"}, map[string]string{"tenant": "acme"}),
		}}, nil
	})))
	rt := fx.rt
	exec := newRecordingWorkflowExecutor()
	registerCoordinatorToolsets(t, ctx, rt, exec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))
	_, err := rt.CreateSession(ctx, "sess-state")
	require.NoError(t, err)

	runID := "run-generated-agent-features-state"
	require.NoError(t, fx.memory.AppendEvents(ctx, string(coordinator.AgentID), runID,
		memory.NewEvent(time.Unix(20, 0), memory.PlannerNoteData{Note: "seed memory"}, nil),
	))
	handle, err := coordinator.NewClient(rt).Start(
		ctx,
		"sess-state",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "publish state"}}}},
		agentsruntime.WithRunID(runID),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitTypedInput) == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
		RunID:   runID,
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"content-type":"application/json"}`)),
	}))
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{
		RunID:       runID,
		Approved:    true,
		RequestedBy: "fixture-reviewer",
	}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	refs := publishArtifacts(out.ToolEvents)
	require.Len(t, refs, 1)

	listArtifacts, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.artifacts.list_artifacts",
		ToolCallID: "list-artifacts",
		Payload:    rawjson.Message([]byte(`{"mime_type":"text/plain"}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(listArtifacts.Payload), refs[0].ID)
	require.NotContains(t, string(listArtifacts.Payload), "published")

	loadArtifact, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.artifacts.load_artifact",
		ToolCallID: "load-artifact",
		Payload:    rawjson.Message([]byte(`{"id":"` + refs[0].ID + `","max_bytes":5}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"content":"publi","mime_type":"text/plain","truncated":true,"size_bytes":9}`, string(loadArtifact.Payload))

	loadMemory, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.memory.load_memory",
		ToolCallID: "load-memory",
		Payload:    rawjson.Message([]byte(`{"scope":"current_run","limit":5}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(loadMemory.Payload), "seed memory")

	indexedMemory, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.memory.load_memory",
		ToolCallID: "indexed-memory",
		Payload:    rawjson.Message([]byte(`{"scope":"indexed","event_types":["user_message"],"labels":{"tenant":"acme"},"limit":50}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(indexedMemory.Payload), "indexed memory")
	require.Equal(t, string(coordinator.AgentID), indexedQuery.AgentID)
	require.Equal(t, runID, indexedQuery.RunID)
	require.Equal(t, "sess-state", indexedQuery.SessionID)
	require.Equal(t, map[string]string{"tenant": "acme"}, indexedQuery.Labels)
	require.Equal(t, []memory.EventType{memory.EventUserMessage}, indexedQuery.Types)
	require.Equal(t, 20, indexedQuery.Limit)

	_, err = fx.longTerm.PutEntry(ctx, memory.PutEntryInput{
		Scope: memory.Scope{
			Namespace:  "agent:" + string(coordinator.AgentID),
			UserID:     "fixture-user",
			Visibility: memory.VisibilityUser,
		},
		Content: "long-term fixture memory",
		Author:  "user",
		Labels:  map[string]string{"tenant": "acme"},
	})
	require.NoError(t, err)
	longTermMemory, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.long_term_memory.search_memory",
		ToolCallID: "search-memory",
		Payload:    rawjson.Message([]byte(`{"query":"fixture memory","labels":{"tenant":"acme"},"limit":50}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(longTermMemory.Payload), "long-term fixture memory")

	listSkills, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.skills.list_skills",
		ToolCallID: "list-skills",
		Payload:    rawjson.Message([]byte(`{}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(listSkills.Payload), "release-check")
	require.Contains(t, string(listSkills.Payload), `"allowed_tools":["shell"]`)
	require.Contains(t, string(listSkills.Payload), `"preload":"on_start"`)
	require.Contains(t, string(listSkills.Payload), `"reload":"per_call"`)
	require.Contains(t, string(listSkills.Payload), `"preloaded":true`)

	loadSkill, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      runID,
		SessionID:  "sess-state",
		ToolName:   "features.skills.load_skill",
		ToolCallID: "load-skill",
		Payload:    rawjson.Message([]byte(`{"skill":"release-check"}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(loadSkill.Payload), `"reloaded":true`)

	srv, err := agentdebug.NewServer(agentdebug.Config{Runtime: rt})
	require.NoError(t, err)
	for path, want := range map[string][]string{
		"/runs/" + runID + "/await": {
			`"ID":"approval"`,
			`"content-type"`,
		},
		"/runs/" + runID + "/memory": {
			"seed memory",
			"workflow.publish",
			refs[0].ID,
		},
		"/runs/" + runID + "/artifacts": {
			refs[0].ID,
			"publish.txt",
		},
		"/runs/" + runID + "/workflow": {
			`"id":"publish"`,
			`"tool_name":"workflow.publish"`,
			`"status":"completed"`,
		},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Contains(t, rec.Body.String(), `"data"`, path)
		for _, text := range want {
			require.Contains(t, rec.Body.String(), text, path)
		}
	}
}

func TestGeneratedFeatureRunAppliesNamedInterceptorsAndRetryReflect(t *testing.T) {
	ctx := context.Background()
	var order []string
	audit := &auditInterceptor{label: "audit", order: &order}
	fx := newFeatureRuntime(t,
		agentsruntime.WithInterceptors(agentsruntime.ToolInterceptorFuncs{
			BeforeToolFunc: func(ctx context.Context, input *agentsruntime.BeforeToolInput) (*agentsruntime.BeforeToolDecision, error) {
				order = append(order, "runtime")
				return nil, nil
			},
		}),
		agentsruntime.WithNamedInterceptors(map[string]agentsruntime.Interceptor{
			"audit": audit,
		}),
	)
	rt := fx.rt
	require.NoError(t, rt.RegisterModel("test-model", modelClientFunc(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{
			Content:    []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "model ok"}}}},
			StopReason: "end_turn",
		}, nil
	})))
	exec := newRecordingWorkflowExecutor()
	exec.failRetryOnce = true
	registerCoordinatorToolsets(t, ctx, rt, exec)
	require.NoError(t, fx.memory.AppendEvents(ctx, string(coordinator.AgentID), "run-interceptors",
		memory.NewEvent(time.Unix(40, 0), memory.UserMessageData{Message: "preload memory"}, nil),
	))
	_, err := fx.longTerm.PutEntry(ctx, memory.PutEntryInput{
		Scope: memory.Scope{
			Namespace:  "agent:" + string(coordinator.AgentID),
			UserID:     "fixture-user",
			Visibility: memory.VisibilityUser,
		},
		Content: "check interceptors long-term entry",
		Author:  "user",
	})
	require.NoError(t, err)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{
		Planner: namedInterceptorPlanner{t: t, wantPreloaded: "preload memory", wantLongTerm: "long-term entry"},
	}))
	_, err = rt.CreateSession(ctx, "sess-interceptors")
	require.NoError(t, err)

	retryOut, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-interceptors",
		SessionID:  "sess-interceptors",
		ToolName:   workflow.Retry,
		ToolCallID: "retry#1",
		Payload:    rawjson.Message([]byte(`{}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, retryOut.RetryHint)
	require.Equal(t, workflow.Retry, retryOut.RetryHint.Tool)
	require.Equal(t, []string{"runtime", "audit"}, order[:2])

	out, err := rt.MustClient(coordinator.AgentID).Run(
		ctx,
		"sess-interceptors",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "check interceptors"}}}},
		agentsruntime.WithRunID("run-interceptors"),
	)
	require.NoError(t, err)
	require.Equal(t, "interceptors complete", messageText(out.Final))
	require.GreaterOrEqual(t, audit.beforeRunCount(), 1)
	require.GreaterOrEqual(t, audit.beforeToolCount(), 1)
	require.GreaterOrEqual(t, audit.beforeModelCount(), 1)
	require.GreaterOrEqual(t, audit.beforeEventCount(), 1)
}

func TestGeneratedFeatureRunBranchesToReviseWhenCaseDoesNotMatch(t *testing.T) {
	ctx := context.Background()
	fx := newFeatureRuntime(t)
	rt := fx.rt
	exec := newRecordingWorkflowExecutor()
	registerCoordinatorToolsets(t, ctx, rt, exec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))
	_, err := rt.CreateSession(ctx, "sess-revise")
	require.NoError(t, err)

	runID := "run-generated-agent-features-revise"
	handle, err := coordinator.NewClient(rt).Start(
		ctx,
		"sess-revise",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "revise it"}}}},
		agentsruntime.WithRunID(runID),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitTypedInput) == 1
	}, time.Second, 10*time.Millisecond)

	err = rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
		RunID:   runID,
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"content-type":"text/plain"}`)),
	})
	require.NoError(t, err)
	_, err = handle.Wait(ctx)
	require.NoError(t, err)

	require.Contains(t, exec.toolNames(), workflow.Revise)
	require.NotContains(t, exec.toolNames(), workflow.Publish)

	srv, err := agentdebug.NewServer(agentdebug.Config{Runtime: rt})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/runs/"+runID+"/workflow", nil)
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"id":"revise"`)
	require.Contains(t, rec.Body.String(), `"tool_name":"workflow.revise"`)
	require.Contains(t, rec.Body.String(), `"status":"completed"`)
	require.NotContains(t, rec.Body.String(), `"id":"publish"`)
}
