package temporal

import (
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// workflowTestWorker registers the actual worker wrapper in the SDK environment.
type workflowTestWorker struct {
	*fakeWorker
	env *testsuite.TestWorkflowEnvironment
}

func TestWorkflowProtocolMismatchIsNonRetryable(t *testing.T) {
	mismatch := &api.WorkflowProtocolMismatchError{Want: api.WorkflowProtocolVersion, Got: 0}
	for _, test := range []struct {
		name string
		err  error
	}{
		{"direct", mismatch},
		{"wrapped", fmt.Errorf("workflow input: %w", mismatch)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			eng := newTestEngine(t)
			eng.workerFactory = func(client.Client, string, worker.Options) worker.Worker {
				return &workflowTestWorker{fakeWorker: &fakeWorker{}, env: env}
			}
			attempts := 0
			require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{
				Name: "protocol.workflow",
				Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
					attempts++
					return nil, test.err
				},
			}))
			env.SetStartWorkflowOptions(client.StartWorkflowOptions{RetryPolicy: &temporalsdk.RetryPolicy{MaximumAttempts: 3}})
			env.ExecuteWorkflow("protocol.workflow", &api.RunInput{})
			var appErr *temporalsdk.ApplicationError
			require.ErrorAs(t, env.GetWorkflowError(), &appErr)
			assert.Equal(t, "loom_mcp.workflow_protocol_mismatch", appErr.Type())
			assert.True(t, appErr.NonRetryable())
			assert.Equal(t, test.err.Error(), appErr.Message())
			assert.Equal(t, 1, attempts)
		})
	}
}

func (w *workflowTestWorker) RegisterWorkflowWithOptions(fn any, opts workflow.RegisterOptions) {
	w.env.RegisterWorkflowWithOptions(fn, opts)
}
