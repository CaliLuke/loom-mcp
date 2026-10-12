package agenttest_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type delegatePlanner struct{}

func TestCaptureChildFirstDiagnostic(t *testing.T) {
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("must not run")))
	require.NoError(t, err)
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("child", script.Client()); err != nil {
			return err
		}
		return registerChildHarness(harnessPlanner{id: "child"})(ctx, rt)
	})
	_, err = h.CaptureFirstRequest(t.Context(), "parent", nil)
	require.NotErrorIs(t, err, agenttest.ErrCaptureNoModelCall)
	require.ErrorContains(t, err, "child run")
	assert.Equal(t, 1, script.Remaining())
}

func TestHarnessChildAwaitReported(t *testing.T) {
	h := agenttest.New(t, registerChildHarness(harnessPlanner{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "child-question", Question: "which child?"}))}), agenttest.WithRunTimeout(time.Second))
	_, err := h.Run(t.Context(), "parent", nil)
	require.ErrorIs(t, err, agenttest.ErrUnansweredAwait)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "child-question")
}

func TestStubMissingToolsetDiagnostic(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), func(ctx context.Context, rt *runtime.Runtime) error {
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "root", Planner: delegatePlanner{}, Specs: []tools.ToolSpec{echoSpec()}, Workflow: engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool"})
	})
	require.NoError(t, err)
	agenttest.Stub(h, echoTool())
	assert.ErrorContains(t, h.Close(), "no registered toolset")
}

func registerChildHarness(child harnessPlanner) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if err := registerHarnessAgent("child", child)(ctx, rt); err != nil {
			return err
		}
		spec := tools.ToolSpec{Name: "delegate.child", Toolset: "delegate", IsAgentTool: true, AgentID: "child", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
		reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "delegate", AgentID: "child", Route: runtime.AgentRoute{ID: "child", WorkflowName: "child.workflow", DefaultTaskQueue: "queue"}})
		reg.Specs = []tools.ToolSpec{spec}
		if err := rt.RegisterToolset(reg); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "parent", Planner: delegatePlanner{}, Specs: []tools.ToolSpec{spec}, Workflow: engine.WorkflowDefinition{Name: "parent.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "parent.plan", ResumeActivityName: "parent.resume", ExecuteToolActivity: "parent.tool"})
	}
}

func (delegatePlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "delegate.child", ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
}
func (delegatePlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return captureFinal(), nil
}
