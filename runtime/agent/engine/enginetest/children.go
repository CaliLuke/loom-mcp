package enginetest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

func testChildSuccess(t *testing.T, f Fixture) error {
	return testChild(t, f, false)
}

func testChildFailure(t *testing.T, f Fixture) error {
	return testChild(t, f, true)
}

func testChild(t *testing.T, f Fixture, fail bool) error {
	ctx := f.ctx
	childInput := &api.RunInput{RunID: request(f).ID + ".child", RootRunID: rootRunID, ParentRunID: request(f).ID, Labels: map[string]string{childName: labelValue}}
	registerWorkflow(t, f, childName, func(_ engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		if err := checkEqual(childInput, in); err != nil {
			return nil, err
		}
		if fail {
			return nil, errors.New(applicationFailure)
		}
		return success(), nil
	})
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		child, err := w.StartChildWorkflow(w.Context(), engine.ChildWorkflowRequest{ID: childInput.RunID, Workflow: childName, TaskQueue: f.TaskQueue, Input: childInput,
			RetryPolicy: engine.RetryPolicy{MaxAttempts: 1}})
		if err != nil {
			return nil, err
		}
		out, err := child.Get(w.Context())
		if err != nil {
			return nil, err
		}
		if !child.IsReady() {
			return nil, errors.New("child result returned before IsReady")
		}
		return out, checkEqual(success(), out)
	})
	h := start(t, f, ctx)
	if fail {
		_, err := h.Wait(ctx)
		return checkApplicationFailure(err)
	}
	return waitSuccess(ctx, h)
}

func testParentCancelsChild(t *testing.T, f Fixture) error {
	ctx := f.ctx
	entered, _ := activityBarrier(t, f, false)
	cleaned := registerCleanup(t, f)
	registerWorkflow(t, f, childName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		err := announce(w)
		if err == nil {
			_, err = w.ResumeRequests().Receive(w.Context())
		}
		if err == nil {
			return nil, errors.New("parent cancellation did not reach child")
		}
		if cleanupErr := cleanup(w.Detached()); cleanupErr != nil {
			return nil, cleanupErr
		}
		return nil, err
	})
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		child, err := w.StartChildWorkflow(w.Context(), engine.ChildWorkflowRequest{ID: request(f).ID + ".child", Workflow: childName, TaskQueue: f.TaskQueue,
			Input: &api.RunInput{RootRunID: rootRunID}, RetryPolicy: engine.RetryPolicy{MaxAttempts: 1}})
		if err != nil {
			return nil, err
		}
		return child.Get(w.Context())
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	require.NoError(t, h.Cancel(ctx))
	receive(t, ctx, cleaned)
	_, err := h.Wait(ctx)
	return checkCanceled(err)
}
