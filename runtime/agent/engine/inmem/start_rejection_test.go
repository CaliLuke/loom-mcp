package inmem

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartWorkflowClassifiesDefiniteRejections(t *testing.T) {
	t.Parallel()

	eng := New()
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "registered",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			return &api.RunOutput{}, nil
		},
	}))

	tests := []struct {
		name string
		ctx  context.Context
		req  engine.WorkflowStartRequest
	}{
		{
			name: "canceled before acceptance",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			req: engine.WorkflowStartRequest{ID: "canceled", Workflow: "registered"},
		},
		{name: "workflow lookup", ctx: context.Background(), req: engine.WorkflowStartRequest{ID: "missing", Workflow: "missing"}},
		{name: "request validation", ctx: context.Background(), req: engine.WorkflowStartRequest{Workflow: "registered"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := eng.StartWorkflow(tt.ctx, tt.req)
			require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
			if errors.Is(tt.ctx.Err(), context.Canceled) {
				assert.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestStartWorkflowDoesNotClassifyDuplicateIDAsRejected(t *testing.T) {
	t.Parallel()

	eng := New()
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "registered",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			return &api.RunOutput{}, nil
		},
	}))
	_, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "same", Workflow: "registered"})
	require.NoError(t, err)

	_, err = eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "same", Workflow: "registered"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, engine.ErrWorkflowStartRejected)
}
