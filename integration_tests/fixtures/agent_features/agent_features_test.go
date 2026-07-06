package agentfeatures_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/runtime/agent/runtime"
	"github.com/stretchr/testify/require"
)

func TestGeneratedAgentFeatureFixtureRunsEndToEnd(t *testing.T) {
	ctx := context.Background()
	rt, recorder, audit := newFeatureRuntime(t)
	exec := newRecordingWorkflowExecutor()
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, rt, coordinator.WithWorkflowExecutor(exec)))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))
	_, err := rt.CreateSession(ctx, "sess-1")
	require.NoError(t, err)

	runID := "run-generated-agent-features"
	handle, err := rt.MustClient(coordinator.AgentID).Start(
		ctx,
		"sess-1",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ship it"}}}},
		agentsruntime.WithRunID(runID),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return recorder.count(hooks.AwaitTypedInput) == 1
	}, time.Second, 10*time.Millisecond)

	err = rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
		RunID:   runID,
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"approved":true}`)),
	})
	require.NoError(t, err)
	out, err := handle.Wait(ctx)
	require.NoError(t, err)

	require.Equal(t, coordinator.AgentID, out.AgentID)
	require.Equal(t, runID, out.RunID)
	require.Equal(t, "generated workflow complete", messageText(out.Final))
	require.ElementsMatch(t, []string{"draft", "review", "retry#1", "retry#2", "publish"}, exec.toolCallIDs())
	require.Contains(t, toolEventCallIDs(out.ToolEvents), "publish")
	require.Len(t, publishArtifacts(out.ToolEvents), 1)
	require.GreaterOrEqual(t, audit.beforeRunCount(), 1)
	require.GreaterOrEqual(t, audit.beforeToolCount(), 5)
	require.GreaterOrEqual(t, recorder.count(hooks.ToolResultReceived), 5)
}

func TestGeneratedAgentToolsetsUseRuntimeStores(t *testing.T) {
	ctx := context.Background()
	mem := memoryinmem.New()
	artifacts := artifact.NewMemoryStore()
	rt := agentsruntime.New(
		agentsruntime.WithMemoryStore(mem),
		agentsruntime.WithArtifactStore(artifacts),
		agentsruntime.WithNamedInterceptors(map[string]agentsruntime.Interceptor{
			"audit": &auditInterceptor{},
		}),
	)
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{}))

	ref, err := artifacts.Save(ctx, artifact.SaveInput{
		AgentID:    string(coordinator.AgentID),
		RunID:      "run-toolsets",
		ToolCallID: "producer",
		Name:       "report.txt",
		MimeType:   "text/plain",
		Body:       []byte("hello world"),
	})
	require.NoError(t, err)
	require.NoError(t, mem.AppendEvents(ctx, string(coordinator.AgentID), "run-toolsets",
		memory.NewEvent(time.Unix(20, 0), memory.PlannerNoteData{Note: "seed memory"}, nil),
	))

	listArtifacts, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-toolsets",
		SessionID:  "sess-1",
		ToolName:   "features.artifacts.list_artifacts",
		ToolCallID: "list-artifacts",
		Payload:    rawjson.Message([]byte(`{"mime_type":"text/plain"}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(listArtifacts.Payload), ref.ID)

	loadArtifact, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-toolsets",
		SessionID:  "sess-1",
		ToolName:   "features.artifacts.load_artifact",
		ToolCallID: "load-artifact",
		Payload:    rawjson.Message([]byte(`{"id":"` + ref.ID + `","max_bytes":5}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"content":"hello","mime_type":"text/plain","truncated":true,"size_bytes":11}`, string(loadArtifact.Payload))

	loadMemory, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-toolsets",
		SessionID:  "sess-1",
		ToolName:   "features.memory.load_memory",
		ToolCallID: "load-memory",
		Payload:    rawjson.Message([]byte(`{"scope":"current_run","limit":5}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(loadMemory.Payload), "seed memory")

	listSkills, err := rt.ExecuteToolActivity(ctx, &agentsruntime.ToolInput{
		AgentID:    coordinator.AgentID,
		RunID:      "run-toolsets",
		SessionID:  "sess-1",
		ToolName:   "features.skills.list_skills",
		ToolCallID: "list-skills",
		Payload:    rawjson.Message([]byte(`{}`)),
	})
	require.NoError(t, err)
	require.Contains(t, string(listSkills.Payload), "release-check")
}

func TestGeneratedNamedInterceptorsApplyToRunToolModelAndEventPaths(t *testing.T) {
	ctx := context.Background()
	rt, _, audit := newFeatureRuntime(t)
	require.NoError(t, rt.RegisterModel("test-model", modelClientFunc(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "model ok"}}}}}, nil
	})))
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, rt, coordinator.CoordinatorAgentConfig{
		Planner: namedInterceptorPlanner{},
	}))
	_, err := rt.CreateSession(ctx, "sess-interceptors")
	require.NoError(t, err)

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

type recordingWorkflowExecutor struct {
	mu    sync.Mutex
	calls []planner.ToolRequest
}

func newRecordingWorkflowExecutor() *recordingWorkflowExecutor {
	return &recordingWorkflowExecutor{}
}

func (e *recordingWorkflowExecutor) Execute(ctx context.Context, meta *agentsruntime.ToolCallMeta, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, *call)
	e.mu.Unlock()

	approved := true
	var result any
	switch call.Name {
	case workflow.Draft:
		result = &workflow.DraftResult{OK: true, Approved: &approved}
	case workflow.Review:
		result = &workflow.ReviewResult{OK: true, Approved: &approved}
	case workflow.Retry:
		result = &workflow.RetryResult{OK: true, Approved: &approved}
	case workflow.Publish:
		result = &workflow.PublishResult{OK: true, Approved: &approved}
	default:
		return nil, errors.New("unexpected workflow tool: " + string(call.Name))
	}
	toolResult := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Result:     result,
	}
	if call.Name == workflow.Publish {
		toolResult.Artifacts = []artifact.Content{{
			Ref: artifact.Ref{
				Name:     "publish.txt",
				MimeType: "text/plain",
			},
			Body: []byte("published"),
		}}
	}
	return agentsruntime.Executed(toolResult), nil
}

func (e *recordingWorkflowExecutor) toolCallIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.calls))
	for _, call := range e.calls {
		ids = append(ids, call.ToolCallID)
	}
	return ids
}

type namedInterceptorPlanner struct{}

func (namedInterceptorPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("test-model")
	if !ok {
		return nil, errors.New("test model not registered")
	}
	if _, err := client.Complete(ctx, &model.Request{RunID: input.RunContext.RunID}); err != nil {
		return nil, err
	}
	input.Events.PlannerThought(ctx, "named interceptor event", nil)
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{
		Name:       workflow.Draft,
		ToolCallID: "draft",
		Payload:    rawjson.Message([]byte(`{"topic":"loom"}`)),
	}}}, nil
}

func (namedInterceptorPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{
		Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "interceptors complete"}}},
	}}, nil
}

type auditInterceptor struct {
	mu          sync.Mutex
	beforeRun   int
	beforeTool  int
	beforeModel int
	beforeEvent int
}

func (a *auditInterceptor) BeforeRun(ctx context.Context, input *agentsruntime.BeforeRunInput) (*agentsruntime.BeforeRunDecision, error) {
	a.mu.Lock()
	a.beforeRun++
	a.mu.Unlock()
	return nil, nil
}

func (a *auditInterceptor) BeforeTool(ctx context.Context, input *agentsruntime.BeforeToolInput) (*agentsruntime.BeforeToolDecision, error) {
	a.mu.Lock()
	a.beforeTool++
	a.mu.Unlock()
	return nil, nil
}

func (a *auditInterceptor) BeforeModel(ctx context.Context, input *agentsruntime.BeforeModelInput) (*agentsruntime.BeforeModelDecision, error) {
	a.mu.Lock()
	a.beforeModel++
	a.mu.Unlock()
	return nil, nil
}

func (a *auditInterceptor) BeforeEvent(ctx context.Context, input *agentsruntime.BeforeEventInput) (*agentsruntime.BeforeEventDecision, error) {
	a.mu.Lock()
	a.beforeEvent++
	a.mu.Unlock()
	return nil, nil
}

func (a *auditInterceptor) AfterRun(ctx context.Context, input *agentsruntime.AfterRunInput) (*agentsruntime.AfterRunDecision, error) {
	return nil, nil
}

func (a *auditInterceptor) AfterTool(ctx context.Context, input *agentsruntime.AfterToolInput) (*agentsruntime.AfterToolDecision, error) {
	return nil, nil
}

func (a *auditInterceptor) AfterModel(ctx context.Context, input *agentsruntime.AfterModelInput) (*agentsruntime.AfterModelDecision, error) {
	return nil, nil
}

func (a *auditInterceptor) AfterEvent(ctx context.Context, input *agentsruntime.AfterEventInput) (*agentsruntime.AfterEventDecision, error) {
	return nil, nil
}

func (a *auditInterceptor) beforeRunCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beforeRun
}

func (a *auditInterceptor) beforeToolCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beforeTool
}

func (a *auditInterceptor) beforeModelCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beforeModel
}

func (a *auditInterceptor) beforeEventCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beforeEvent
}

type hookRecorder struct {
	mu     sync.Mutex
	events []hooks.Event
}

func (r *hookRecorder) HandleEvent(ctx context.Context, event hooks.Event) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}

func (r *hookRecorder) count(kind hooks.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, event := range r.events {
		if event.Type() == kind {
			count++
		}
	}
	return count
}

type modelClientFunc func(context.Context, *model.Request) (*model.Response, error)

func (f modelClientFunc) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	return f(ctx, req)
}

func (f modelClientFunc) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	return nil, errors.New("stream unsupported")
}

func newFeatureRuntime(t *testing.T) (*agentsruntime.Runtime, *hookRecorder, *auditInterceptor) {
	t.Helper()
	bus := hooks.NewBus()
	recorder := &hookRecorder{}
	_, err := bus.Register(recorder)
	require.NoError(t, err)
	audit := &auditInterceptor{}
	rt := agentsruntime.New(
		agentsruntime.WithMemoryStore(memoryinmem.New()),
		agentsruntime.WithArtifactStore(artifact.NewMemoryStore()),
		agentsruntime.WithHooks(bus),
		agentsruntime.WithNamedInterceptors(map[string]agentsruntime.Interceptor{
			"audit": audit,
		}),
	)
	return rt, recorder, audit
}

func messageText(msg *model.Message) string {
	if msg == nil {
		return ""
	}
	var text string
	for _, part := range msg.Parts {
		if p, ok := part.(model.TextPart); ok {
			text += p.Text
		}
	}
	return text
}

func toolEventCallIDs(events []*api.ToolEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ToolCallID)
	}
	return ids
}

func publishArtifacts(events []*api.ToolEvent) []artifact.Ref {
	for _, event := range events {
		if event.ToolCallID == "publish" {
			return event.Artifacts
		}
	}
	return nil
}

var _ model.Streamer = emptyStreamer{}

type emptyStreamer struct{}

func (emptyStreamer) Recv() (model.Chunk, error) { return model.Chunk{}, io.EOF }
func (emptyStreamer) Close() error               { return nil }
func (emptyStreamer) Metadata() map[string]any   { return nil }
