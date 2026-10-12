package selvedge

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWorkflow(t *testing.T, e *Engine, name string, handler engine.WorkflowFunc) error {
	t.Helper()
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: name, Handler: handler}))
	require.NoError(t, e.SealRegistration(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	h, err := e.StartWorkflow(ctx, engine.WorkflowStartRequest{ID: name, Workflow: name, Input: &api.RunInput{RunID: name}})
	require.NoError(t, err)
	_, err = h.Wait(ctx)
	return err
}

func TestAwaitContextContract(t *testing.T) {
	e := newTestEngine(t)
	err := runWorkflow(t, e, "await", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		assert.Nil(t, w.Context().Done())
		if w.Await(w.Context(), nil) == nil {
			return nil, errors.New("nil Await predicate was accepted")
		}
		if w.Await(context.Background(), func() bool {
			return true
		}) == nil {
			return nil, errors.New("foreign Go context was accepted")
		}
		foreign, _ := w.WithCancel()
		if w.Await(foreign.Context(), func() bool {
			return true
		}) == nil {
			return nil, errors.New("foreign workflow scope was accepted")
		}
		goctx, cancel := context.WithCancel(w.Context())
		defer cancel()
		if w.Await(goctx, func() bool {
			return true
		}) == nil {
			return nil, errors.New("cancelable Go context was accepted")
		}
		cancel()
		if !errors.Is(w.Await(goctx, func() bool {
			return true
		}), context.Canceled) {
			return nil, errors.New("canceled Go context did not return cancellation")
		}
		scoped, scopeCancel := w.WithCancel()
		scopeCancel()
		if !errors.Is(scoped.Await(scoped.Context(), func() bool {
			return false
		}), context.Canceled) {
			return nil, errors.New("canceled scope did not return cancellation")
		}
		detached := w.Detached()
		assert.NoError(t, detached.Await(detached.Context(), func() bool {
			return true
		}))
		return &api.RunOutput{}, nil
	})
	require.NoError(t, err)
}

func TestNonPositiveTimerReady(t *testing.T) {
	e := newTestEngine(t)
	err := runWorkflow(t, e, "timer", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		goctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, duration := range []time.Duration{0, -time.Second} {
			future, err := w.NewTimer(goctx, duration)
			if err != nil {
				return nil, err
			}
			assert.True(t, future.IsReady())
			expiry, err := future.Get(goctx)
			if err != nil {
				return nil, err
			}
			assert.False(t, expiry.IsZero())
			repeated, err := future.Get(goctx)
			if err != nil {
				return nil, errors.New("repeated timer Get failed")
			}
			assert.Equal(t, expiry, repeated)
		}
		return &api.RunOutput{}, nil
	})
	require.NoError(t, err)
}

func TestActivityContextHelpers(t *testing.T) {
	e := newTestEngine(t)
	var calls atomic.Int32
	require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "probe", engine.ActivityOptions{HeartbeatTimeout: time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 3}}, func(ctx context.Context, _ *api.ToolInput) (*api.ToolOutput, error) {
		calls.Add(1)
		assert.True(t, engine.IsActivityContext(ctx))
		assert.Positive(t, engine.ActivityAttemptFromContext(ctx))
		assert.Nil(t, engine.WorkflowContextFromContext(ctx))
		assert.Zero(t, engine.ActivityHeartbeatTimeout(ctx))
		assert.False(t, engine.RecordActivityHeartbeat(ctx, "ignored"))
		return nil, engine.NewNonRetryableApplicationError("no retry", "terminal", nil, nil)
	}))
	err := runWorkflow(t, e, "activity", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		future, err := w.ExecuteToolActivityAsync(w.Context(), engine.ToolActivityCall{Name: "probe", Input: &api.ToolInput{}, Options: engine.ActivityOptions{HeartbeatTimeout: time.Second}})
		if err != nil {
			return nil, err
		}
		_, first := future.Get(w.Context())
		_, second := future.Get(w.Context())
		assert.EqualError(t, second, first.Error())
		return nil, first
	})
	var app *engine.ApplicationError
	require.ErrorAs(t, err, &app)
	assert.Equal(t, "terminal", app.Type)
	assert.EqualValues(t, 1, calls.Load())
}

func TestQueryHandlerAdapter(t *testing.T) {
	e := newTestEngine(t)
	expected := []planner.AwaitExternalTools{{}}
	err := runWorkflow(t, e, "queries", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		for _, invalid := range []any{42, func() {}, func(int) (string, error) {
			return "", nil
		}, func() (string, int) {
			return "", 0
		}, (func() (string, error))(nil)} {
			//nolint:testifylint // FailNow would exit the workflow coroutine rather than this test.
			assert.Error(t, w.SetQueryHandler("invalid", invalid))
		}
		if err := w.SetQueryHandler(api.QueryRootRunID, func() (string, error) {
			return "root", nil
		}); err != nil {
			return nil, err
		}
		if err := w.SetQueryHandler(engine.QueryToolAwaits, func() ([]planner.AwaitExternalTools, error) {
			return expected, nil
		}); err != nil {
			return nil, err
		}
		assert.NoError(t, w.SetQueryHandler("removed", func() (int, error) {
			return 1, nil
		}))
		assert.NoError(t, w.SetQueryHandler("removed", nil))
		return &api.RunOutput{}, nil
	})
	require.NoError(t, err)
	root, err := e.QueryRootRunID(t.Context(), "queries")
	require.NoError(t, err)
	assert.Equal(t, "root", root)
	awaits, err := e.QueryToolAwaits(t.Context(), "queries")
	require.NoError(t, err)
	assert.Equal(t, expected, awaits)
	_, err = sv.Query[int](t.Context(), e.app, "queries", "removed")
	var missing *sv.QueryNotFoundError
	require.ErrorAs(t, err, &missing)
	client, err := NewClient(Options{Client: e.client})
	require.NoError(t, err)
	_, err = client.QueryRootRunID(t.Context(), "queries")
	require.ErrorIs(t, err, ErrQueriesNeedWorker)
	_, err = client.QueryToolAwaits(t.Context(), "queries")
	require.ErrorIs(t, err, ErrQueriesNeedWorker)
	_, err = sv.StartWorkflowByName(t.Context(), e.client, "not-registered", nil, sv.WithInstanceID("unknown-definition"))
	require.NoError(t, err)
	_, err = e.QueryRootRunID(t.Context(), "unknown-definition")
	var unregistered *sv.WorkflowNotRegisteredError
	assert.ErrorAs(t, err, &unregistered)
}

func TestStartClassificationAndLostAck(t *testing.T) {
	e := newTestEngine(t)
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "holding", Handler: func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		_, err := w.PauseRequests().Receive(w.Context())
		return &api.RunOutput{RunID: in.RunID}, err
	}}))
	require.NoError(t, e.SealRegistration(t.Context()))
	req := engine.WorkflowStartRequest{ID: "duplicate", Workflow: "holding", Input: &api.RunInput{RunID: "original"}}
	h, err := e.StartWorkflow(t.Context(), req)
	require.NoError(t, err)
	duplicate, err := e.StartWorkflow(t.Context(), req)
	assert.Nil(t, duplicate)
	require.ErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
	require.NotErrorIs(t, err, engine.ErrWorkflowStartRejected)
	require.NoError(t, h.Signal(t.Context(), api.SignalPause, &api.PauseRequest{}))
	_, err = h.Wait(t.Context())
	require.NoError(t, err)
	_, err = e.StartWorkflow(t.Context(), req)
	require.ErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
	require.NotErrorIs(t, err, engine.ErrWorkflowStartRejected)
	req.ID = "encode"
	req.Input = &api.RunInput{ToolArgs: rawjson.Message(`{`)}
	_, err = e.StartWorkflow(t.Context(), req)
	require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
	require.NotErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
	lost := errors.New("injected lost acknowledgement")
	e.startWorkflow = func(ctx context.Context, app *sv.App, name string, input any, opts ...sv.StartOption) (string, error) {
		id, err := sv.StartWorkflowByName(ctx, app, name, input, opts...)
		if err != nil {
			return id, err
		}
		return id, lost
	}
	req.ID = "lost-ack"
	req.Input = &api.RunInput{}
	_, err = e.StartWorkflow(t.Context(), req)
	require.ErrorIs(t, err, lost)
	require.NotErrorIs(t, err, engine.ErrWorkflowStartRejected)
	require.NotErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
	persisted, err := sv.Describe(t.Context(), e.client, req.ID)
	require.NoError(t, err)
	assert.Equal(t, req.ID, persisted.InstanceID)
}

func TestByIDErrors(t *testing.T) {
	e := newTestEngine(t)
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "status", Handler: func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		switch in.RunID {
		case "completed":
			return &api.RunOutput{RunID: in.RunID}, nil
		case "failed":
			return nil, errors.New("workflow failed")
		default:
			_, err := w.PauseRequests().Receive(w.Context())
			return nil, err
		}
	}}))
	require.NoError(t, e.SealRegistration(t.Context()))
	for _, id := range []string{"unknown", "completed", "failed", "canceled", "running"} {
		if id != "unknown" {
			h, err := e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: id, Workflow: "status", Input: &api.RunInput{RunID: id}})
			require.NoError(t, err)
			if id == "canceled" {
				require.NoError(t, h.Cancel(t.Context()))
			}
			if id != "running" {
				_, err = h.Wait(t.Context())
				if id == "completed" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		}
		status, err := e.QueryRunStatus(t.Context(), id)
		if id == "unknown" {
			require.ErrorIs(t, err, engine.ErrWorkflowNotFound)
		} else {
			require.NoError(t, err)
			assert.Equal(t, engine.RunStatus(id), status)
		}
		signalErr := e.SignalByID(t.Context(), id, "", api.SignalResume, &api.ResumeRequest{})
		cancelErr := e.CancelByID(t.Context(), id)
		switch id {
		case "unknown":
			require.ErrorIs(t, signalErr, engine.ErrWorkflowNotFound)
			require.ErrorIs(t, cancelErr, engine.ErrWorkflowNotFound)
			_, err = e.QueryRunCompletion(t.Context(), id)
			require.ErrorIs(t, err, engine.ErrWorkflowNotFound)
		case "completed", "failed":
			require.ErrorIs(t, signalErr, engine.ErrWorkflowCompleted)
			require.ErrorIs(t, cancelErr, engine.ErrWorkflowCompleted)
		case "canceled":
			assert.NoError(t, cancelErr)
		case "running":
			assert.NoError(t, signalErr)
			assert.NoError(t, cancelErr)
		}
		assert.ErrorIs(t, e.SignalByID(t.Context(), id, "wrong-run", api.SignalResume, nil), engine.ErrWorkflowNotFound)
	}
}

func TestWorkflowProtocolFailureWait(t *testing.T) {
	e := newTestEngine(t)
	expected := fmt.Errorf("history mismatch: %w", api.ErrWorkflowProtocolMismatch)
	err := runWorkflow(t, e, "protocol", func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
		return nil, expected
	})
	var app *engine.ApplicationError
	require.ErrorAs(t, err, &app)
	assert.Equal(t, "loom_mcp.workflow_protocol_mismatch", app.Type)
	assert.Equal(t, expected.Error(), app.Message)
}

func TestPayloadRoundTrip(t *testing.T) {
	// The shared conformance body checks all nine activity input/output pairs and
	// all six signals. This adds polymorphic model parts to both workflow edges.
	e := newTestEngine(t)
	message := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}, model.ImagePart{Format: "png", Bytes: []byte{0, 1, 255}}}}
	expected := &api.RunInput{RunID: "payload", Messages: []*model.Message{message}, ToolArgs: rawjson.Message(`{"nested":[true,42]}`)}
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "payload", Handler: func(_ engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		assert.Equal(t, expected, in)
		return &api.RunOutput{RunID: in.RunID, Final: in.Messages[0]}, nil
	}}))
	require.NoError(t, e.SealRegistration(t.Context()))
	h, err := e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "payload", Workflow: "payload", Input: expected})
	require.NoError(t, err)
	out, err := h.Wait(t.Context())
	require.NoError(t, err)
	assert.Equal(t, message, out.Final)
}

func TestChildDuplicateClassification(t *testing.T) {
	e := newTestEngine(t)
	registerEcho(t, e)
	err := runWorkflow(t, e, "parent", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		request := engine.ChildWorkflowRequest{ID: "same-child", Workflow: "echo", Input: &api.RunInput{RunID: "child"}}
		child, err := w.StartChildWorkflow(w.Context(), request)
		if err != nil {
			return nil, err
		}
		_, err = child.Get(w.Context())
		if err != nil {
			return nil, err
		}
		_, err = w.StartChildWorkflow(w.Context(), request)
		//nolint:testifylint // FailNow would exit the workflow coroutine rather than this test.
		assert.ErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
		assert.NotErrorIs(t, err, engine.ErrWorkflowStartRejected)
		return nil, err
	})
	require.Error(t, err)
}
func TestRegistrationAndUnregisteredActivity(t *testing.T) {
	e := newTestEngine(t)
	require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{}))
	registerEcho(t, e)
	require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "echo", Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
		return nil, nil
	}}))
	handler := func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
		return &api.ToolOutput{}, nil
	}
	require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "registered", engine.ActivityOptions{}, handler))
	require.Error(t, e.RegisterExecuteToolActivity(t.Context(), "registered", engine.ActivityOptions{}, handler))
	require.Error(t, e.RegisterExecuteToolActivity(t.Context(), "", engine.ActivityOptions{}, handler))
	err := runWorkflow(t, e, "validation", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		for _, call := range []engine.ToolActivityCall{{Input: &api.ToolInput{}}, {Name: "registered"}, {Name: "missing", Input: &api.ToolInput{}}} {
			_, err := w.ExecuteToolActivity(w.Context(), call)
			if err == nil {
				return nil, errors.New("invalid activity call was accepted")
			}
		}
		_, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: "registered", Input: &api.ToolInput{}, Options: engine.ActivityOptions{Queue: "no-poller", StartToCloseTimeout: time.Nanosecond, ScheduleToStartTimeout: time.Nanosecond}})
		return &api.RunOutput{}, err
	})
	require.NoError(t, err)
	require.Error(t, e.RegisterExecuteToolActivity(t.Context(), "late", engine.ActivityOptions{}, handler))
}
