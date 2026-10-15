package inmem

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypedActivityRegistrationsRejectInvalidAndDuplicateHandlers(t *testing.T) {
	t.Run("finish", func(t *testing.T) {
		eng := New()
		handler := func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error) {
			return nil, assert.AnError
		}
		require.ErrorContains(t, eng.RegisterFinishRunActivity(t.Context(), "", engine.ActivityOptions{}, handler), "name is required")
		require.ErrorContains(t, eng.RegisterFinishRunActivity(t.Context(), "activity", engine.ActivityOptions{}, nil), "handler is required")
		require.NoError(t, eng.RegisterFinishRunActivity(t.Context(), "activity", engine.ActivityOptions{}, handler))
		replacement := func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error) {
			return &api.FinishRunOutput{}, nil
		}
		require.ErrorContains(t, eng.RegisterFinishRunActivity(t.Context(), "activity", engine.ActivityOptions{}, replacement), "already registered")
		require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			_, err := wf.ExecuteFinishRunActivity(wf.Context(), engine.FinishRunActivityCall{Name: "activity", Input: &api.FinishRunInput{}})
			return nil, err
		}}))
		handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
		require.NoError(t, err)
		_, err = handle.Wait(t.Context())
		require.ErrorIs(t, err, assert.AnError, "rejected duplicate must not replace the original handler")
	})

	t.Run("content", func(t *testing.T) {
		eng := New()
		handler := func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error) {
			return nil, assert.AnError
		}
		require.ErrorContains(t, eng.RegisterRecordContentActivity(t.Context(), "", engine.ActivityOptions{}, handler), "name is required")
		require.ErrorContains(t, eng.RegisterRecordContentActivity(t.Context(), "activity", engine.ActivityOptions{}, nil), "handler is required")
		require.NoError(t, eng.RegisterRecordContentActivity(t.Context(), "activity", engine.ActivityOptions{}, handler))
		replacement := func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error) {
			return &api.RecordContentOutput{}, nil
		}
		require.ErrorContains(t, eng.RegisterRecordContentActivity(t.Context(), "activity", engine.ActivityOptions{}, replacement), "already registered")
		require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			_, err := wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{Name: "activity", Input: &api.RecordContentInput{}})
			return nil, err
		}}))
		handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
		require.NoError(t, err)
		_, err = handle.Wait(t.Context())
		require.ErrorIs(t, err, assert.AnError, "rejected duplicate must not replace the original handler")
	})

	t.Run("child", func(t *testing.T) {
		eng := New()
		handler := func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
			return nil, assert.AnError
		}
		require.ErrorContains(t, eng.RegisterChildDispatchActivity(t.Context(), "", engine.ActivityOptions{}, handler), "name is required")
		require.ErrorContains(t, eng.RegisterChildDispatchActivity(t.Context(), "activity", engine.ActivityOptions{}, nil), "handler is required")
		require.NoError(t, eng.RegisterChildDispatchActivity(t.Context(), "activity", engine.ActivityOptions{}, handler))
		replacement := func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
			return &api.ChildDispatchOutput{}, nil
		}
		require.ErrorContains(t, eng.RegisterChildDispatchActivity(t.Context(), "activity", engine.ActivityOptions{}, replacement), "already registered")
		require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			_, err := wf.ExecuteChildDispatchActivity(wf.Context(), engine.ChildDispatchActivityCall{Name: "activity", Input: &api.ChildDispatchInput{}})
			return nil, err
		}}))
		handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
		require.NoError(t, err)
		_, err = handle.Wait(t.Context())
		require.ErrorIs(t, err, assert.AnError, "rejected duplicate must not replace the original handler")
	})

	t.Run("recall", func(t *testing.T) {
		eng := New()
		handler := func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
			return nil, assert.AnError
		}
		require.ErrorContains(t, eng.RegisterRecallActivity(t.Context(), "", engine.ActivityOptions{}, handler), "name is required")
		require.ErrorContains(t, eng.RegisterRecallActivity(t.Context(), "activity", engine.ActivityOptions{}, nil), "handler is required")
		require.NoError(t, eng.RegisterRecallActivity(t.Context(), "activity", engine.ActivityOptions{}, handler))
		replacement := func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
			return &api.RecallActivityOutput{}, nil
		}
		require.ErrorContains(t, eng.RegisterRecallActivity(t.Context(), "activity", engine.ActivityOptions{}, replacement), "already registered")
		require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			_, err := wf.ExecuteRecallActivity(wf.Context(), engine.RecallActivityCall{Name: "activity", Input: &api.RecallActivityInput{}})
			return nil, err
		}}))
		handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
		require.NoError(t, err)
		_, err = handle.Wait(t.Context())
		require.ErrorIs(t, err, assert.AnError, "rejected duplicate must not replace the original handler")
	})
}

func TestWorkflowActivityCallsRejectMissingNamesInputsAndRegistrations(t *testing.T) {
	t.Run("finish", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			call       engine.FinishRunActivityCall
		}{
			{"name", "name is required", engine.FinishRunActivityCall{Input: &api.FinishRunInput{}}},
			{"input", "input is required", engine.FinishRunActivityCall{Name: "missing"}},
			{"registration", "not registered", engine.FinishRunActivityCall{Name: "missing", Input: &api.FinishRunInput{}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				eng := New()
				require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
					_, err := wf.ExecuteFinishRunActivity(wf.Context(), tc.call)
					return nil, err
				}}))
				handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
				require.NoError(t, err)
				result, err := handle.Wait(t.Context())
				require.ErrorContains(t, err, tc.want)
				assert.Nil(t, result)
			})
		}
	})

	t.Run("content", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			call       engine.RecordContentActivityCall
		}{
			{"name", "name is required", engine.RecordContentActivityCall{Input: &api.RecordContentInput{}}},
			{"input", "input is required", engine.RecordContentActivityCall{Name: "missing"}},
			{"registration", "not registered", engine.RecordContentActivityCall{Name: "missing", Input: &api.RecordContentInput{}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				eng := New()
				require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
					_, err := wf.ExecuteRecordContentActivity(wf.Context(), tc.call)
					return nil, err
				}}))
				handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
				require.NoError(t, err)
				result, err := handle.Wait(t.Context())
				require.ErrorContains(t, err, tc.want)
				assert.Nil(t, result)
			})
		}
	})

	t.Run("child", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			call       engine.ChildDispatchActivityCall
		}{
			{"name", "name is required", engine.ChildDispatchActivityCall{Input: &api.ChildDispatchInput{}}},
			{"input", "input is required", engine.ChildDispatchActivityCall{Name: "missing"}},
			{"registration", "not registered", engine.ChildDispatchActivityCall{Name: "missing", Input: &api.ChildDispatchInput{}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				eng := New()
				require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
					_, err := wf.ExecuteChildDispatchActivity(wf.Context(), tc.call)
					return nil, err
				}}))
				handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
				require.NoError(t, err)
				result, err := handle.Wait(t.Context())
				require.ErrorContains(t, err, tc.want)
				assert.Nil(t, result)
			})
		}
	})

	t.Run("recall", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			call       engine.RecallActivityCall
		}{
			{"name", "name is required", engine.RecallActivityCall{Input: &api.RecallActivityInput{}}},
			{"input", "input is required", engine.RecallActivityCall{Name: "missing"}},
			{"registration", "not registered", engine.RecallActivityCall{Name: "missing", Input: &api.RecallActivityInput{}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				eng := New()
				require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
					_, err := wf.ExecuteRecallActivity(wf.Context(), tc.call)
					return nil, err
				}}))
				handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "run", Workflow: "workflow", Input: &api.RunInput{}})
				require.NoError(t, err)
				result, err := handle.Wait(t.Context())
				require.ErrorContains(t, err, tc.want)
				assert.Nil(t, result)
			})
		}
	})
}
