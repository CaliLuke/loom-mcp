package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestActivityAttemptContextOnRetry(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	adapter := &Engine{}
	var attempts []int
	activity := func(ctx context.Context) error {
		attempt := engine.ActivityAttemptFromContext(adapter.injectWorkflowContextIntoActivity(ctx))
		attempts = append(attempts, attempt)
		if attempt == 1 {
			return errors.New("retry activity")
		}
		return nil
	}
	env.RegisterActivity(activity)
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
		})
		return workflow.ExecuteActivity(ctx, activity).Get(ctx, nil)
	})
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, []int{1, 2}, attempts)
}

// The SDK owns retry attempts; the engine forwards their increasing identity and
// the finite retry policy. Workflow code cannot reschedule the same SDK attempt.
func TestPlannerRetrySameAttemptPrevented(t *testing.T) {
	env, eng := newStorageTestEnvironment(t)
	var attempts []int
	require.NoError(t, eng.RegisterPlannerActivity(t.Context(), "retry.plan", engine.ActivityOptions{StartToCloseTimeout: time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Millisecond}}, func(ctx context.Context, _ *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
		attempts = append(attempts, engine.ActivityAttemptFromContext(ctx))
		return nil, errors.New("injected planner attempt failure")
	}))
	require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "retry.workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		_, err := wf.ExecutePlannerActivity(wf.Context(), engine.PlannerActivityCall{Name: "retry.plan", Input: &api.PlanActivityInput{RunID: "run"}})
		return nil, err
	}}))
	env.ExecuteWorkflow("retry.workflow", &api.RunInput{RunID: "run"})
	require.ErrorContains(t, env.GetWorkflowError(), "injected planner attempt failure")
	assert.Equal(t, []int{1, 2, 3}, attempts, "StepsEnd: retries advance and fail after MaxAttempts")
}
