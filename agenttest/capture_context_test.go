package agenttest_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureRegisteredContextModels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy runtime.RunPolicy
	}{
		{name: "default"},
		{name: "default summary", policy: runtime.RunPolicy{ContextDefault: &runtime.ContextDefault{ContextModel: &runtime.ContextModel{ID: "summary"}}}},
		{name: "explicit summary", policy: runtime.RunPolicy{ContextCompaction: &runtime.ContextCompaction{ContextModel: &runtime.ContextModel{ID: "summary"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelScript, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("unused")))
			require.NoError(t, err)
			summaryScript, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("unused summary")))
			require.NoError(t, err)
			h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
				if err := rt.RegisterModel("model", modelScript.Client()); err != nil {
					return err
				}
				if err := rt.RegisterModel("summary", summaryScript.Client()); err != nil {
					return err
				}
				return rt.RegisterAgent(ctx, runtime.AgentRegistration{
					ID: "root", Planner: capturePlanner{}, Policy: tc.policy,
					Workflow:         engine.WorkflowDefinition{Name: "capture.context", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
					PlanActivityName: "capture.plan", ResumeActivityName: "capture.resume", ExecuteToolActivity: "capture.tool",
				})
			})
			request, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("capture context"))
			require.NoError(t, err)
			assert.Equal(t, "model", request.ModelID())
			assert.NotNil(t, request.Source())
			assert.Equal(t, 1, modelScript.Remaining())
			assert.Equal(t, 1, summaryScript.Remaining())
		})
	}
}
