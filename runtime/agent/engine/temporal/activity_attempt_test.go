package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

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
