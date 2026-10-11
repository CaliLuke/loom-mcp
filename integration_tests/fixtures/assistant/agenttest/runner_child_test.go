package agenttest_test

import (
	"context"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerParentPlanner struct{}

func TestRunnerChildUsageFixture(t *testing.T) {
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		if err := generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: fixturePlanner{}}); err != nil {
			return err
		}
		spec := tools.ToolSpec{Name: "delegate.child", Toolset: "delegate", IsAgentTool: true, AgentID: "assistant.assistant_runtime", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
		reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "delegate", AgentID: "assistant.assistant_runtime", Route: runtime.AgentRoute{ID: "assistant.assistant_runtime", WorkflowName: generated.WorkflowName, DefaultTaskQueue: generated.DefaultTaskQueue}})
		reg.Specs = []tools.ToolSpec{spec}
		if err := rt.RegisterToolset(reg); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "parent", Planner: runnerParentPlanner{}, Specs: []tools.ToolSpec{spec}, Workflow: engine.WorkflowDefinition{Name: "parent.workflow", TaskQueue: "parent", Handler: rt.ExecuteWorkflow}, PlanActivityName: "parent.plan", ResumeActivityName: "parent.resume", ExecuteToolActivity: "parent.tool"})
	}
	s := eval.Scenario{ID: "child", Agent: "parent", Items: eval.Items(eval.Item{ID: "one", Input: "delegate"}), Expect: []expect.Expectation{expect.Completed()}}
	r, err := eval.Run(t.Context(), eval.Config{Harness: eval.NewHarnessFunc(register, nil, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("delegate"), agenttest.Usage(60, 40)), agenttest.Respond(agenttest.Text("child"), agenttest.Usage(60, 40)), agenttest.Respond(agenttest.Text("parent final"), agenttest.Usage(60, 40)))), Scenarios: []eval.Scenario{s}})
	require.NoError(t, err)
	a := r.Attempts[0]
	require.NoError(t, a.HarnessErr)
	require.NoError(t, a.Outcome.Err)
	assert.Equal(t, eval.VerdictPassed, a.Verdict())
	assert.Equal(t, 300, a.Tokens)
	require.Len(t, a.Runs(), 1)
	assert.Len(t, a.Runs()[0].Export.Runs, 2)
}

func (runnerParentPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if _, err := completeFixtureTurn(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "delegate.child", ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
}
func (runnerParentPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return completeFixtureTurn(ctx, in.Agent, in.Messages)
}
