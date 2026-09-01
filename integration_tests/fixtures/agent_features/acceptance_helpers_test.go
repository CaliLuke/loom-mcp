package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"sync"
	"testing"

	"example.com/agentfeatures/gen/features"
	"example.com/agentfeatures/gen/features/agents/coordinator"
	agentworkflow "example.com/agentfeatures/gen/features/agents/coordinator/workflow"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type featureRuntime struct {
	rt        *agentsruntime.Runtime
	recorder  *hookRecorder
	audit     *auditInterceptor
	memory    memory.Store
	longTerm  memory.Service
	artifacts artifact.Store
	stream    *streamRecorder
}

type recordingWorkflowExecutor struct {
	mu            sync.Mutex
	calls         []planner.ToolRequest
	failRetryOnce bool
	retryFailed   bool
}

type methodBackedFeatureService struct {
	topic string
}

func newRecordingWorkflowExecutor() *recordingWorkflowExecutor {
	return &recordingWorkflowExecutor{}
}

func (s *methodBackedFeatureService) EchoTopic(ctx context.Context, payload *features.MethodEchoPayload) (*features.MethodEchoResult, error) {
	s.topic = payload.Topic
	return &features.MethodEchoResult{
		OK:      true,
		Message: "echo:" + payload.Topic,
	}, nil
}

func (e *recordingWorkflowExecutor) Execute(ctx context.Context, meta *agentsruntime.ToolCallMeta, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, *call)
	if e.failRetryOnce && call.Name == workflow.Retry && !e.retryFailed {
		e.retryFailed = true
		e.mu.Unlock()
		return nil, errors.New("retry needs corrected input")
	}
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
	case workflow.Revise:
		result = &workflow.ReviseResult{OK: true, Approved: &approved}
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

func TestGeneratedToolCallBuilderReturnsCodecError(t *testing.T) {
	original := agentworkflow.DraftPayloadCodec
	t.Cleanup(func() {
		agentworkflow.DraftPayloadCodec = original
	})
	agentworkflow.DraftPayloadCodec = tools.JSONCodec[*agentworkflow.DraftPayload]{
		ToJSON: func(*agentworkflow.DraftPayload) ([]byte, error) {
			return nil, errors.New("encode draft payload")
		},
		FromJSON: original.FromJSON,
	}

	require.NotPanics(t, func() {
		call := agentworkflow.NewDraftCall(&agentworkflow.DraftPayload{Topic: "draft"})
		require.Equal(t, agentworkflow.Draft, call.Name)
		require.Empty(t, call.Payload)
		require.NotNil(t, call.Error)
		require.Equal(t, "encode draft payload", call.Error.Message)
	})
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

func (e *recordingWorkflowExecutor) toolNames() []tools.Ident {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]tools.Ident, 0, len(e.calls))
	for _, call := range e.calls {
		names = append(names, call.Name)
	}
	return names
}

type namedInterceptorPlanner struct {
	t             *testing.T
	wantPreloaded string
	wantLongTerm  string
}

func (p namedInterceptorPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	if p.wantPreloaded != "" {
		encoded, err := json.Marshal(input.PreloadedMemory)
		require.NoError(p.t, err)
		require.Contains(p.t, string(encoded), p.wantPreloaded)
	}
	if p.wantLongTerm != "" {
		encoded, err := json.Marshal(input.PreloadedMemoryEntries)
		require.NoError(p.t, err)
		require.Contains(p.t, string(encoded), p.wantLongTerm)
	}
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
		Payload:    []byte(`{"topic":"loom"}`),
	}}}, nil
}

func (namedInterceptorPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{
		Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "interceptors complete"}}},
	}}, nil
}

type auditInterceptor struct {
	mu          sync.Mutex
	label       string
	order       *[]string
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
	if a.order != nil && a.label != "" {
		*a.order = append(*a.order, a.label)
	}
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

type streamRecorder struct {
	mu     sync.Mutex
	events []stream.Event
}

func (r *streamRecorder) Send(ctx context.Context, event stream.Event) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}

func (r *streamRecorder) Close(ctx context.Context) error {
	return nil
}

func (r *streamRecorder) count(kind stream.EventType) int {
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

func newFeatureRuntime(t *testing.T, opts ...agentsruntime.RuntimeOption) *featureRuntime {
	t.Helper()
	bus := hooks.NewBus()
	recorder := &hookRecorder{}
	_, err := bus.Register(recorder)
	require.NoError(t, err)
	mem := memoryinmem.New()
	longTerm := memoryinmem.NewService()
	artifacts := artifact.NewMemoryStore()
	streams := &streamRecorder{}
	audit := &auditInterceptor{}
	base := []agentsruntime.RuntimeOption{
		agentsruntime.WithEngine(engineinmem.New()),
		agentsruntime.WithSessionStore(sessioninmem.New()),
		agentsruntime.WithRunEventStore(runloginmem.New()),
		agentsruntime.WithMemoryStore(mem),
		agentsruntime.WithMemoryService(longTerm),
		agentsruntime.WithMemoryScopeResolver(memory.ScopeResolverFunc(func(_ context.Context, input memory.ScopeInput) (memory.Scope, error) {
			return memory.Scope{
				Namespace:  "agent:" + input.AgentID,
				UserID:     "fixture-user",
				Visibility: input.Visibility,
			}, nil
		})),
		agentsruntime.WithArtifactStore(artifacts),
		agentsruntime.WithHooks(bus),
		agentsruntime.WithStream(streams),
		agentsruntime.WithNamedInterceptors(map[string]agentsruntime.Interceptor{
			"audit": audit,
		}),
	}
	base = append(base, opts...)
	return &featureRuntime{
		rt:        agentsruntime.New(base...),
		recorder:  recorder,
		audit:     audit,
		memory:    mem,
		longTerm:  longTerm,
		artifacts: artifacts,
		stream:    streams,
	}
}

func registerCoordinatorToolsets(t *testing.T, ctx context.Context, rt *agentsruntime.Runtime, exec agentsruntime.ToolCallExecutor) {
	t.Helper()
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, rt, coordinator.WithWorkflowExecutor(exec)))
	delegated, err := coordinator.NewCoordinatorDelegatedAgentToolsetRegistration(rt, "")
	require.NoError(t, err)
	require.NoError(t, rt.RegisterToolset(delegated))
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

func toolEventByCallID(events []*api.ToolEvent, callID string) *api.ToolEvent {
	for _, event := range events {
		if event.ToolCallID == callID {
			return event
		}
	}
	return nil
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
