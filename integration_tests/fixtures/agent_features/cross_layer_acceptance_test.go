package agentfeatures_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/agents/specialist"
	delegated "example.com/agentfeatures/gen/features/agents/specialist/agenttools/delegated"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type parentDelegationPlanner struct{}

type specialistFinalResultPlanner struct {
	mu     sync.Mutex
	runID  string
	parent string
	callID string
	tool   string
}

type externalAwaitPlanner struct {
	mu          sync.Mutex
	resumeCalls int
	toolOutputs int
}

type awaitingSpecialistPlanner struct {
	started chan struct{}
	once    sync.Once
}

func TestGeneratedCrossLayerInMemoryContracts(t *testing.T) {
	t.Run("child agent links every observable layer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		runGeneratedChildScenario(t, ctx, newFeatureRuntime(t))
	})

	t.Run("clarification and external result share one barrier", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		runGeneratedExternalAwaitScenario(t, ctx, newFeatureRuntime(t))
	})

	t.Run("parent cancellation reaches child agent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		runGeneratedChildCancellationScenario(t, ctx, newFeatureRuntime(t))
	})
}

func runGeneratedChildScenario(t *testing.T, ctx context.Context, fx *featureRuntime) {
	t.Helper()
	childPlanner := &specialistFinalResultPlanner{}
	require.NoError(t, specialist.RegisterSpecialistAgent(ctx, fx.rt, specialist.SpecialistAgentConfig{Planner: childPlanner}))
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: parentDelegationPlanner{}}))
	_, err := fx.rt.CreateSession(ctx, "sess-child")
	require.NoError(t, err)

	out, err := coordinator.NewClient(fx.rt).Run(
		ctx,
		"sess-child",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "delegate"}}}},
		agentsruntime.WithRunID("run-parent-child"),
	)
	require.NoError(t, err)
	require.Equal(t, "child complete", messageText(out.Final))
	require.Len(t, out.ToolEvents, 1)
	require.Nil(t, out.ToolEvents[0].Error)
	require.JSONEq(t, `{"ok":true,"message":"child:loom"}`, string(out.ToolEvents[0].Result))
	link := out.ToolEvents[0].RunLink
	require.NotNil(t, link)
	require.Equal(t, "run-parent-child", link.ParentRunID)
	require.Equal(t, "delegate-1", link.ParentToolCallID)
	require.Equal(t, specialist.AgentID, link.AgentID)

	parent, err := fx.rt.SessionStore.LoadRun(ctx, "run-parent-child")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, parent.Status)
	require.Equal(t, []string{link.RunID}, parent.ChildRunIDs)
	child, err := fx.rt.SessionStore.LoadRun(ctx, link.RunID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, child.Status)
	require.Equal(t, "sess-child", child.SessionID)

	childRunID, parentRunID, parentCallID, toolName := childPlanner.snapshot()
	require.Equal(t, link.RunID, childRunID)
	require.Equal(t, "run-parent-child", parentRunID)
	require.Equal(t, "delegate-1", parentCallID)
	require.Equal(t, delegated.Summarize.String(), toolName)
	require.Equal(t, 1, fx.recorder.count(hooks.ChildRunLinked))
	require.Equal(t, 1, fx.stream.count(stream.EventChildRunLinked))

	page, err := fx.rt.RunEventStore.List(ctx, "run-parent-child", "", 100)
	require.NoError(t, err)
	eventTypes := make([]hooks.EventType, 0, len(page.Events))
	for _, event := range page.Events {
		eventTypes = append(eventTypes, event.Type)
	}
	require.Contains(t, eventTypes, hooks.ChildRunLinked)
	require.Contains(t, eventTypes, hooks.RunCompleted)
}

func runGeneratedExternalAwaitScenario(t *testing.T, ctx context.Context, fx *featureRuntime) {
	t.Helper()
	plan := &externalAwaitPlanner{}
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: plan}))
	_, err := fx.rt.CreateSession(ctx, "sess-external-await")
	require.NoError(t, err)

	handle, err := coordinator.NewClient(fx.rt).Start(
		ctx,
		"sess-external-await",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "await"}}}},
		agentsruntime.WithRunID("run-external-await"),
		agentsruntime.WithRunTimeBudget(time.Second),
	)
	require.NoError(t, err)
	if !assert.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitClarification) == 1 &&
			fx.recorder.count(hooks.AwaitExternalTools) == 1
	}, 5*time.Second, 10*time.Millisecond) {
		waitCtx, cancelWait := context.WithTimeout(ctx, 2*time.Second)
		defer cancelWait()
		out, waitErr := handle.Wait(waitCtx)
		require.FailNowf(t, "external-input barrier was not published", "run output: %#v; wait error: %v", out, waitErr)
	}

	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{
		RunID:  "run-external-await",
		ID:     "clarify-topic",
		Answer: "use loom",
	}))
	require.NoError(t, fx.rt.ProvideToolResults(ctx, &api.ToolResultsSet{
		RunID: "run-external-await",
		ID:    "external-draft",
		Results: []*api.ProvidedToolResult{{
			Name:       workflow.Draft,
			ToolCallID: "external-draft-1",
			Result:     rawjson.Message([]byte(`{"ok":true,"approved":true}`)),
		}},
	}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "external inputs complete", messageText(out.Final))
	resumeCalls, toolOutputs := plan.snapshot()
	require.Equal(t, 1, resumeCalls)
	require.Equal(t, 1, toolOutputs)
	require.Equal(t, 1, fx.recorder.count(hooks.AwaitClarification))
	require.Equal(t, 1, fx.recorder.count(hooks.AwaitExternalTools))
	require.Equal(t, 1, fx.stream.count(stream.EventAwaitClarification))
	require.Equal(t, 1, fx.stream.count(stream.EventAwaitExternalTools))

	runMeta, err := fx.rt.SessionStore.LoadRun(ctx, "run-external-await")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, runMeta.Status)
	page, err := fx.rt.RunEventStore.List(ctx, "run-external-await", "", 100)
	require.NoError(t, err)
	eventTypes := make([]hooks.EventType, 0, len(page.Events))
	for _, event := range page.Events {
		eventTypes = append(eventTypes, event.Type)
	}
	require.Contains(t, eventTypes, hooks.AwaitClarification)
	require.Contains(t, eventTypes, hooks.AwaitExternalTools)
	require.Contains(t, eventTypes, hooks.ToolResultReceived)
	require.Contains(t, eventTypes, hooks.RunCompleted)
}

func runGeneratedChildCancellationScenario(t *testing.T, ctx context.Context, fx *featureRuntime) {
	t.Helper()
	childPlanner := &awaitingSpecialistPlanner{started: make(chan struct{})}
	require.NoError(t, specialist.RegisterSpecialistAgent(ctx, fx.rt, specialist.SpecialistAgentConfig{Planner: childPlanner}))
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: parentDelegationPlanner{}}))
	_, err := fx.rt.CreateSession(ctx, "sess-child-cancel")
	require.NoError(t, err)

	handle, err := coordinator.NewClient(fx.rt).Start(
		ctx,
		"sess-child-cancel",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "cancel child"}}}},
		agentsruntime.WithRunID("run-parent-cancel"),
	)
	require.NoError(t, err)
	select {
	case <-childPlanner.started:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	childRunID := fx.recorder.childRunID("run-parent-cancel")
	require.NotEmpty(t, childRunID)
	require.Eventually(t, func() bool {
		return fx.recorder.countForRun(hooks.AwaitTypedInput, childRunID) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, fx.rt.CancelRun(ctx, "run-parent-cancel"))
	_, err = handle.Wait(ctx)
	require.Error(t, err)
	require.Eventually(t, func() bool {
		parent, parentErr := fx.rt.SessionStore.LoadRun(ctx, "run-parent-cancel")
		return parentErr == nil && parent.Status == session.RunStatusCanceled
	}, 5*time.Second, 20*time.Millisecond)
	var childEngineStatus engine.RunStatus
	require.Eventually(t, func() bool {
		status, statusErr := fx.rt.Engine.QueryRunStatus(ctx, childRunID)
		if statusErr != nil {
			return false
		}
		childEngineStatus = status
		switch status {
		case engine.RunStatusCompleted, engine.RunStatusFailed, engine.RunStatusCanceled, engine.RunStatusTimedOut:
			return true
		case engine.RunStatusPending, engine.RunStatusRunning, engine.RunStatusPaused:
			return false
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, engine.RunStatusCanceled, childEngineStatus)
	require.Eventually(t, func() bool {
		snapshot, snapshotErr := fx.rt.GetRunSnapshot(ctx, childRunID)
		return snapshotErr == nil && snapshot.Status == run.StatusCanceled
	}, 10*time.Second, 20*time.Millisecond)
	child, err := fx.rt.SessionStore.LoadRun(ctx, childRunID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCanceled, child.Status)
}

func (parentDelegationPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	call := delegated.NewSummarizeCall(
		&delegated.SummarizePayload{Topic: "loom"},
		delegated.WithToolCallID("delegate-1"),
	)
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{call}, ExpectedChildren: 1}, nil
}

func (parentDelegationPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if input == nil || len(input.ToolOutputs) != 1 {
		return nil, errors.New("parent planner expected one child output")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "child complete"}},
	}}}, nil
}

func (p *specialistFinalResultPlanner) PlanStart(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	if input == nil {
		return nil, errors.New("specialist planner input is required")
	}
	p.mu.Lock()
	p.runID = input.RunContext.RunID
	p.parent = input.RunContext.ParentRunID
	p.callID = input.RunContext.ParentToolCallID
	p.tool = string(input.RunContext.Tool)
	p.mu.Unlock()
	return &planner.PlanResult{FinalToolResult: &planner.FinalToolResult{
		Result: rawjson.Message([]byte(`{"ok":true,"message":"child:loom"}`)),
	}}, nil
}

func (*specialistFinalResultPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected specialist resume")
}

func (p *specialistFinalResultPlanner) snapshot() (string, string, string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runID, p.parent, p.callID, p.tool
}

func (p *externalAwaitPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{Await: planner.NewAwait(
		planner.AwaitClarificationItem(&planner.AwaitClarification{
			ID:            "clarify-topic",
			Question:      "Which topic?",
			MissingFields: []string{"topic"},
		}),
		planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{
			ID: "external-draft",
			Items: []planner.AwaitToolItem{{
				Name:       workflow.Draft,
				ToolCallID: "external-draft-1",
				Payload:    rawjson.Message([]byte(`{"topic":"loom"}`)),
			}},
		}),
	)}, nil
}

func (p *externalAwaitPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if input == nil {
		return nil, errors.New("external await resume input is required")
	}
	p.mu.Lock()
	p.resumeCalls++
	p.toolOutputs = len(input.ToolOutputs)
	p.mu.Unlock()
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "external inputs complete"}},
	}}}, nil
}

func (p *externalAwaitPlanner) snapshot() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resumeCalls, p.toolOutputs
}

func (p *awaitingSpecialistPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	p.once.Do(func() {
		close(p.started)
	})
	return &planner.PlanResult{Await: planner.NewAwait(
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{
			ID:     "child-input",
			Title:  "Child input",
			Schema: rawjson.Message([]byte(`{"type":"object"}`)),
		}),
	)}, nil
}

func (*awaitingSpecialistPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected awaiting specialist resume")
}

func (r *hookRecorder) childRunID(parentRunID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		linked, ok := event.(*hooks.ChildRunLinkedEvent)
		if ok && linked.RunID() == parentRunID {
			return linked.ChildRunID
		}
	}
	return ""
}

func (r *hookRecorder) countForRun(kind hooks.EventType, runID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, event := range r.events {
		if event.Type() == kind && event.RunID() == runID {
			count++
		}
	}
	return count
}
