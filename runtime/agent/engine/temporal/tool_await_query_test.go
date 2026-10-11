package temporal

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporalQueryToolAwaits(t *testing.T) {
	env, eng := newStorageTestEnvironment(t)
	cli := &rootQueryTestClient{Client: eng.client, env: env}
	eng.client = cli
	want := []planner.AwaitExternalTools{{ID: "await", Items: []planner.AwaitToolItem{{Name: "test.answer", ToolCallID: "call"}}}}
	require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "await.workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		if err := wf.SetQueryHandler("tool_awaits", func() ([]planner.AwaitExternalTools, error) { return want, nil }); err != nil {
			return nil, err
		}
		return &api.RunOutput{}, nil
	}}))
	env.ExecuteWorkflow("await.workflow", &api.RunInput{RunID: "parent"})
	require.NoError(t, env.GetWorkflowError())
	querier, ok := any(eng).(interface {
		QueryToolAwaits(context.Context, string) ([]planner.AwaitExternalTools, error)
	})
	require.True(t, ok, "engine must expose pending tool awaits to detached clients")
	got, err := querier.QueryToolAwaits(t.Context(), "parent")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "tool_awaits", cli.queryType)
	assert.Equal(t, "parent", cli.workflowID)
}
