package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowSmallBudgetOverflowFailsWithoutRecovery(t *testing.T) {
	for _, bearing := range []bool{true, false} {
		t.Run(stringBool(bearing), func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 3)
			provider := model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "context_too_long", "provider rejected the request", "request", false, nil)
			client := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, err: provider}, limits: model.ContextLimits{ContextWindow: 200}, resolved: "small"}
			require.NoError(t, rt.RegisterModel("fake", client))
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
				messages := input.Messages
				if !bearing {
					messages = []*model.Message{contentMessage("unrelated")}
				}
				client, ok := input.Agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Messages: messages})
				return nil, err
			}}}
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
			caps := policy.CapsState{}
			out, err := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
			workflowErr := err
			assert.Nil(t, out)
			var typed *ContextOverflowError
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, "budget_too_small", typed.Reason)
			require.ErrorIs(t, err, ErrContextBudgetExceeded)
			require.ErrorIs(t, err, checkpointBudgetError())
			var original *model.ProviderError
			require.ErrorAs(t, err, &original)
			assert.Equal(t, provider.Message(), original.Message())
			assert.Equal(t, provider.RequestID(), original.RequestID())
			assert.Contains(t, err.Error(), "52 bytes short")
			assert.Contains(t, err.Error(), provider.Message())
			assert.Len(t, wf.plannerCalls, 1)
			assert.Equal(t, policy.CapsState{}, caps)
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			counts := map[hooks.EventType]int{}
			for _, event := range page.Events {
				counts[event.Type]++
			}
			assert.Equal(t, 1, counts[hooks.ContextOverflow])
			assert.Equal(t, 1, counts[hooks.ContextBudgetTooSmall])
			assert.Zero(t, counts[hooks.PlannerStep])
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, 1)
			assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
			assert.Equal(t, base.IndexHead, record.Runs[0].IndexHead)
			assert.Empty(t, record.Runs[0].Presentations)
			var application *engine.ApplicationError
			require.ErrorAs(t, workflowErr, &application)
			assert.True(t, application.NonRetryable)
		})
	}
}

func checkpointBudgetError() error {
	return (&compaction.BudgetShortfallError{}).Unwrap()
}
