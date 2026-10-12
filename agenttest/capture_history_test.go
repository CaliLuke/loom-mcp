package agenttest_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureRejectsHistoryClientBeforeModelCall(t *testing.T) {
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("summary")))
	require.NoError(t, err)
	client := script.Client()
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("model", client); err != nil {
			return err
		}
		registered, ok := rt.ModelClient("model")
		require.True(t, ok)
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{
			ID: "root", Planner: capturePlanner{},
			Policy:           runtime.RunPolicy{History: runtime.Compress(registered, runtime.HistoryCompressionConfig{CompressAtTurns: 3, KeepMaxTurns: 1})},
			Workflow:         engine.WorkflowDefinition{Name: "capture.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
			PlanActivityName: "capture.plan", ResumeActivityName: "capture.resume", ExecuteToolActivity: "capture.tool",
		})
	})
	_, err = h.CaptureFirstRequest(t.Context(), "root", agenttest.Input{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "first"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "first answer"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "second"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "second answer"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "third"}}},
	})
	assert.Empty(t, script.Requests(), "history must not call the pre-gate client")
	var historyErr *agenttest.CaptureHistoryError
	require.ErrorAs(t, err, &historyErr)
	assert.EqualValues(t, "root", historyErr.AgentID)
	assert.Contains(t, err.Error(), "History policy")
	assert.Contains(t, err.Error(), "ungated model client")
}

func TestCaptureHistoryPolicyGuard(t *testing.T) {
	for _, tc := range []struct {
		name        string
		policyAgent agent.Ident
		pure        bool
	}{
		{name: "no history"},
		{name: "pure history", policyAgent: "root", pure: true},
		{name: "custom history", policyAgent: "root"},
		{name: "other agent history", policyAgent: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("done")))
			require.NoError(t, err)
			client := script.Client()
			policyCalls := 0
			policy := runtime.HistoryPolicy(func(_ context.Context, messages []*model.Message, _ []*model.ToolDefinition) ([]*model.Message, error) {
				policyCalls++
				return messages, nil
			})
			if tc.pure {
				policy = runtime.KeepRecentTurns(1)
			}
			h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
				if err := rt.RegisterModel("model", client); err != nil {
					return err
				}
				for _, id := range []agent.Ident{"root", "other"} {
					var history runtime.HistoryPolicy
					if id == tc.policyAgent {
						history = policy
					}
					name := string(id)
					if err := rt.RegisterAgent(ctx, runtime.AgentRegistration{
						ID: id, Planner: capturePlanner{}, Policy: runtime.RunPolicy{History: history},
						Workflow:         engine.WorkflowDefinition{Name: name + ".workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
						PlanActivityName: name + ".plan", ResumeActivityName: name + ".resume", ExecuteToolActivity: name + ".tool",
					}); err != nil {
						return err
					}
				}
				return nil
			})
			request, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("hello"))
			assert.Zero(t, policyCalls, "capture must reject before invoking any History policy")
			assert.Empty(t, script.Requests(), "capture must never consume the provider")
			if tc.policyAgent == "" {
				require.NoError(t, err)
				assert.Equal(t, "model", request.ModelID())
				assert.NotNil(t, request.Source())
				return
			}
			var historyErr *agenttest.CaptureHistoryError
			require.ErrorAs(t, err, &historyErr)
			assert.Equal(t, tc.policyAgent, historyErr.AgentID)
			assert.Contains(t, err.Error(), string(tc.policyAgent))
			assert.Contains(t, err.Error(), "History policy")
			if tc.name == "custom history" {
				result, runErr := h.Run(t.Context(), "root", agenttest.User("normal"))
				require.NoError(t, runErr)
				require.NoError(t, result.RunErr)
				assert.Positive(t, policyCalls, "ordinary Run must still execute the History policy")
				assert.Len(t, script.Requests(), 1)
			}
		})
	}
}
