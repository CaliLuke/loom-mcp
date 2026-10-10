package inmem

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerActivityAttemptContext(t *testing.T) {
	e := New().(*eng)
	ctx := engine.WithActivityAttempt(context.Background(), 7)
	w := e.newWorkflowContext(ctx, "run")
	require.NoError(t, e.RegisterPlannerActivity(ctx, "planner", engine.ActivityOptions{}, func(ctx context.Context, _ *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
		assert.Equal(t, 1, engine.ActivityAttemptFromContext(ctx))
		return &api.PlanActivityOutput{}, nil
	}))
	_, err := w.ExecutePlannerActivity(ctx, engine.PlannerActivityCall{Name: "planner", Input: &api.PlanActivityInput{}})
	require.NoError(t, err)
}
