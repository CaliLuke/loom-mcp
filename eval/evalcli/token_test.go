package evalcli_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokenPlanner struct{}

func TestRunTokenLimit(t *testing.T) {
	cfg := config(t)
	cfg.Harness = eval.NewHarnessFunc(func(ctx context.Context, rt *runtime.Runtime) error {
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{
			ID: "root", Planner: tokenPlanner{},
			Workflow:         engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
			PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool",
		})
	}, nil, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"), agenttest.Usage(60, 40))))
	cfg.Scenarios = []eval.Scenario{{ID: "s", Agent: "root", Items: eval.Items(eval.Item{ID: "one", Input: "hello"}), Expect: []expect.Expectation{expect.Completed()}}}
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--repeat", "5", "--max-tokens", "250")
	require.Equal(t, 0, code, diagnostic)
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	assert.Equal(t, "stopped", headers[0].Status)
	assert.Equal(t, eval.StopTokenLimit, headers[0].Stop)
	assert.Equal(t, 300, headers[0].Tokens)
	e, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	require.Len(t, e.Attempts, 5)
	for _, a := range e.Attempts[:3] {
		assert.Equal(t, eval.VerdictPassed, a.Verdict)
		assert.Equal(t, 100, a.Tokens)
	}
	for _, a := range e.Attempts[3:] {
		assert.Equal(t, eval.VerdictNotRun, a.Verdict)
		assert.Equal(t, eval.StopTokenLimit, a.NotRun)
	}
}

func (tokenPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := in.Agent.ModelClient("fixture")
	if !ok {
		return nil, errors.New("missing fixture client")
	}
	response, err := client.Complete(ctx, &model.Request{Model: "scripted", Messages: in.Messages})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &response.Content[0]}}, nil
}

func (tokenPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}
