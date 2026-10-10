package temporal

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

type rootQueryTestClient struct {
	client.Client
	env                          *testsuite.TestWorkflowEnvironment
	workflowID, runID, queryType string
	argumentCount, calls         int
}

func TestTemporalQueryRootRunID(t *testing.T) {
	for _, tc := range []struct {
		name       string
		workflowID string
		value      any
		queryErr   error
		want       string
		wantErr    string
		decodeErr  bool
	}{
		{name: "root scope", workflowID: "parent", value: "root", want: "root"},
		{name: "query failure", workflowID: "parent", queryErr: errors.New("scope unavailable"), wantErr: "scope unavailable"},
		{name: "invalid result type", workflowID: "parent", value: 42, decodeErr: true},
		{name: "missing workflow id", value: "root", wantErr: "workflow id is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, eng := newStorageTestEnvironment(t)
			cli := &rootQueryTestClient{Client: eng.client, env: env}
			eng.client = cli
			require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "root.workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
				if err := wf.SetQueryHandler(api.QueryRootRunID, func() (any, error) {
					return tc.value, tc.queryErr
				}); err != nil {
					return nil, err
				}
				return &api.RunOutput{RunID: "parent"}, nil
			}}))
			env.ExecuteWorkflow("root.workflow", &api.RunInput{RunID: "parent"})
			require.NoError(t, env.GetWorkflowError())
			root, err := eng.QueryRootRunID(t.Context(), tc.workflowID)
			switch {
			case tc.wantErr != "":
				require.ErrorContains(t, err, tc.wantErr)
			case tc.decodeErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, root)
			if tc.workflowID == "" {
				assert.Zero(t, cli.calls)
			} else {
				assert.Equal(t, 1, cli.calls)
				assert.Equal(t, tc.workflowID, cli.workflowID)
				assert.Empty(t, cli.runID, "root scope queries the latest run")
				assert.Equal(t, api.QueryRootRunID, cli.queryType)
				assert.Zero(t, cli.argumentCount)
			}
		})
	}
}

// QueryWorkflow delegates to the SDK's registered query handler and converter,
// recording the client routing parameters without replacing the query result.
func (c *rootQueryTestClient) QueryWorkflow(_ context.Context, workflowID, runID, queryType string, args ...any) (converter.EncodedValue, error) {
	c.workflowID, c.runID, c.queryType = workflowID, runID, queryType
	c.argumentCount, c.calls = len(args), c.calls+1
	return c.env.QueryWorkflow(queryType, args...)
}
