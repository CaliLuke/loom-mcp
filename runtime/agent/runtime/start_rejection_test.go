package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rejectingStartEngine struct {
	stubEngine
	err    error
	calls  int
	cancel context.CancelFunc
}

func (e *rejectingStartEngine) StartWorkflow(context.Context, engine.WorkflowStartRequest) (engine.WorkflowHandle, error) {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	return nil, e.err
}

func TestStartRunSettlesOnlyDefiniteRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status session.RunStatus
		cancel bool
	}{
		{name: "rejected", cause: engine.ErrWorkflowStartRejected, status: session.RunStatusFailed},
		{name: "canceled after rejection", cause: engine.ErrWorkflowStartRejected, status: session.RunStatusFailed, cancel: true},
		{name: "unknown", cause: errors.New("transport lost"), status: session.RunStatusPending},
		{name: "deadline", cause: context.DeadlineExceeded, status: session.RunStatusPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := &rejectingStartEngine{err: tc.cause}
			rt := newStartRejectionRuntime(eng)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				eng.cancel = cancel
			}
			_, err := rt.CreateSession(ctx, "session")
			require.NoError(t, err)
			_, err = rt.MustClient("service.agent").Start(ctx, "session", nil, WithRunID("rejected-run"))
			require.ErrorIs(t, err, ErrWorkflowStartFailed)
			require.ErrorIs(t, err, tc.cause)
			got, err := rt.SessionStore.LoadRun(context.Background(), "rejected-run")
			require.NoError(t, err)
			assert.Equal(t, tc.status, got.Status)
		})
	}
}

func TestStartRunPreservesExistingIdentity(t *testing.T) {
	eng := &rejectingStartEngine{err: engine.ErrWorkflowStartRejected}
	rt := newStartRejectionRuntime(eng)
	ctx := context.Background()
	_, err := rt.CreateSession(ctx, "session")
	require.NoError(t, err)
	original := session.RunMeta{RunID: "existing", AgentID: "original.agent", SessionID: "session", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), Metadata: map[string]any{"original": true}}
	require.NoError(t, rt.SessionStore.UpsertRun(ctx, original))
	before, err := rt.SessionStore.LoadRun(ctx, original.RunID)
	require.NoError(t, err)
	_, err = rt.MustClient("service.agent").Start(ctx, "session", nil, WithRunID(original.RunID))
	require.ErrorIs(t, err, session.ErrRunAlreadyExists)
	assert.Zero(t, eng.calls)
	after, err := rt.SessionStore.LoadRun(ctx, original.RunID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func newStartRejectionRuntime(eng engine.Engine) *Runtime {
	return &Runtime{
		Engine: eng,
		logger: telemetry.NoopLogger{}, metrics: telemetry.NoopMetrics{}, tracer: telemetry.NoopTracer{},
		SessionStore: sessioninmem.New(),
		agents:       map[agent.Ident]AgentRegistration{"service.agent": {ID: "service.agent", Workflow: engine.WorkflowDefinition{Name: "service.workflow", TaskQueue: "svc.queue"}}},
	}
}

type rejectingChildContext struct {
	*testWorkflowContext
	err    error
	calls  int
	parent *rejectingChildContext
	cancel context.CancelFunc
}

func (w *rejectingChildContext) StartChildWorkflow(context.Context, engine.ChildWorkflowRequest) (engine.ChildWorkflowHandle, error) {
	w.calls++
	if w.cancel != nil {
		w.cancel()
	}
	if w.parent != nil {
		w.parent.calls++
	}
	return nil, w.err
}

func (w *rejectingChildContext) WithCancel() (engine.WorkflowContext, func()) {
	derived, cancel := w.testWorkflowContext.WithCancel()
	return &rejectingChildContext{testWorkflowContext: derived.(*testWorkflowContext), err: w.err, parent: w, cancel: w.cancel}, cancel
}

func TestAgentChildImmediateRejectionSettlesReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status session.RunStatus
		cancel bool
	}{
		{name: "definite", cause: engine.ErrWorkflowStartRejected, status: session.RunStatusFailed},
		{name: "canceled definite", cause: engine.ErrWorkflowStartRejected, status: session.RunStatusFailed, cancel: true},
		{name: "uncertain", cause: errors.New("unknown child start"), status: session.RunStatusPending},
	} {
		for _, dispatch := range []string{"batch", "executor", "direct"} {
			t.Run(tc.name+"/"+dispatch, func(t *testing.T) {
				rt := newStartRejectionRuntime(&stubEngine{})
				rt.RunEventStore = runloginmem.New()
				rt.Bus = &recordingHooks{}
				rt.toolsets = make(map[string]ToolsetRegistration)
				rt.toolSpecs = make(map[tools.Ident]tools.ToolSpec)
				cfg := AgentToolConfig{
					AgentID: "child.agent", Name: "svc.child",
					Route:            AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "queue"},
					AgentToolContent: AgentToolContent{Prompt: func(tools.Ident, any) string { return "child request" }},
				}
				reg := NewAgentToolsetRegistration(rt, cfg)
				rt.toolsets[reg.Name] = reg
				toolID := tools.Ident("svc.child.invoke")
				spec := newAnyJSONSpec(toolID, reg.Name)
				spec.IsAgentTool = true
				spec.AgentID = string(cfg.AgentID)
				rt.toolSpecs[toolID] = spec
				parent := &run.Context{RunID: "parent", SessionID: "session", TurnID: "turn"}
				seedParentRun(t, rt.SessionStore, parent.RunID, parent.SessionID)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wf := &rejectingChildContext{testWorkflowContext: &testWorkflowContext{ctx: ctx, hookRuntime: rt}, err: tc.cause}
				if tc.cancel {
					wf.cancel = cancel
				}
				call := planner.ToolRequest{Name: toolID, RunID: parent.RunID, SessionID: parent.SessionID, TurnID: parent.TurnID, ToolCallID: "call"}
				var err error
				switch dispatch {
				case "batch":
					_, _, err = rt.executeToolCalls(wf, "execute", engine.ActivityOptions{}, "parent.agent", parent, nil, []planner.ToolRequest{call}, 0, nil, time.Time{})
				case "direct":
					_, err = rt.ExecuteAgentChildWithRoute(wf, cfg.Route, nil, buildNestedAgentRunContext(call))
				case "executor":
					_, err = reg.Execute(engine.WithWorkflowContext(wf.Context(), wf), &call)
				}
				require.ErrorIs(t, err, tc.cause)
				assert.Equal(t, 1, wf.calls)
				childID := NestedRunIDForToolCall(parent.RunID, toolID, call.ToolCallID)
				child, err := rt.SessionStore.LoadRun(context.Background(), childID)
				require.NoError(t, err)
				assert.Equal(t, tc.status, child.Status)
				storedParent, err := rt.SessionStore.LoadRun(context.Background(), parent.RunID)
				require.NoError(t, err)
				assert.Equal(t, []string{childID}, storedParent.ChildRunIDs)
			})
		}
	}
}

type failedRejectionStore struct {
	session.Store
	err error
}

func (s *failedRejectionStore) RejectRun(context.Context, string, string) error {
	return s.err
}

func TestStartRunReportsRejectionPersistenceFailure(t *testing.T) {
	rt := newStartRejectionRuntime(&rejectingStartEngine{err: engine.ErrWorkflowStartRejected})
	persistenceErr := errors.New("storage unavailable")
	rt.SessionStore = &failedRejectionStore{Store: rt.SessionStore, err: persistenceErr}
	_, err := rt.CreateSession(context.Background(), "session")
	require.NoError(t, err)
	_, err = rt.MustClient("service.agent").Start(context.Background(), "session", nil)
	require.ErrorIs(t, err, ErrWorkflowStartFailed)
	require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
	require.ErrorIs(t, err, persistenceErr)
}
