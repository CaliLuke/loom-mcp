package agentfeatures_test

import (
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

const (
	historyTimeBudget         = 10 * time.Second
	workflowBudgetHistoryFile = "testdata/workflow_protocol_v13_budget.json"
)

// recordBudgetWorkflowHistory uses the existing external-await scenario to
// record a normal terminal resume whose adoption crosses the active budget.
func recordBudgetWorkflowHistory(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyBudgetAdoption(t))
	runGeneratedExternalAwaitScenario(t, ctx, fx, historyTimeBudget)
	history := recordCompletedWorkflowHistory(t, ctx, temporalClient, eng, "run-external-await", workflowBudgetHistoryFile)
	assertHistoryBudgetAdoption(t, history)
}

// historyBudgetAdoption delays only the recorder's final planner adoption.
func historyBudgetAdoption(t *testing.T) agentsruntime.RuntimeOption {
	t.Helper()
	return func(options *agentsruntime.Options) {
		subscription, err := options.Hooks.Register(hooks.SubscriberFunc(delayHistoryTerminalAdoption))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, subscription.Close())
		})
	}
}

// delayHistoryTerminalAdoption crosses the active budget in the final adoption
// activity, after the planner has already returned its terminal answer.
func delayHistoryTerminalAdoption(ctx context.Context, event hooks.Event) error {
	step, ok := event.(*hooks.PlannerStepEvent)
	if !ok || step.Step != 2 {
		return nil
	}
	timer := time.NewTimer(historyTimeBudget + time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// assertHistoryBudgetAdoption proves the recording exercises RB1: the answer
// arrives within its capped planner timeout, adoption alone exceeds the entire
// active budget, and completion follows without another planner call.
func assertHistoryBudgetAdoption(t *testing.T, history *historypb.History) {
	t.Helper()
	var runInput api.RunInput
	require.NoError(t, json.Unmarshal(history.Events[0].GetWorkflowExecutionStartedEventAttributes().Input.Payloads[0].Data, &runInput, jsonopts.FormatDurationAsNano(true)))
	require.NotNil(t, runInput.Policy)
	require.Equal(t, historyTimeBudget, runInput.Policy.TimeBudget)
	schedules := make(map[int64]*historypb.HistoryEvent)
	var terminalStep int
	var adoption *historypb.HistoryEvent
	var adoptionCompleted bool
	for _, event := range history.Events {
		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
			schedules[event.EventId] = event
			if scheduled.ActivityType.Name == "features.coordinator.resume" {
				var input api.PlanActivityInput
				require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
				assert.Nil(t, input.Finalize, "no finalizer may replace the in-hand terminal answer")
				assert.Zero(t, terminalStep, "no planner call after the terminal answer")
			}
			if scheduled.ActivityType.Name == "runtime.publish_hook" && terminalStep != 0 {
				for _, input := range historyHookInputs(t, scheduled.Input.Payloads[0].Data) {
					if input.Type == hooks.PlannerStep {
						var step hooks.PlannerStepEvent
						require.NoError(t, json.Unmarshal(input.Payload, &step))
						assert.Equal(t, terminalStep, step.Step)
						adoption = event
					}
				}
			}
		}
		completed := event.GetActivityTaskCompletedEventAttributes()
		if completed == nil {
			continue
		}
		scheduledEvent := schedules[completed.ScheduledEventId]
		require.NotNil(t, scheduledEvent)
		scheduled := scheduledEvent.GetActivityTaskScheduledEventAttributes()
		if scheduled.ActivityType.Name == "features.coordinator.resume" {
			var output api.PlanActivityOutput
			require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &output, jsonopts.FormatDurationAsNano(true)))
			if output.Result != nil && output.Result.FinalResponse != nil {
				assert.Empty(t, output.Result.ToolCalls)
				assert.Nil(t, output.Result.Await)
				var input api.PlanActivityInput
				require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
				terminalStep = input.Step
				assert.Less(t, event.EventTime.AsTime().Sub(scheduledEvent.EventTime.AsTime()), scheduled.StartToCloseTimeout.AsDuration())
			}
		}
		if adoption != nil && completed.ScheduledEventId == adoption.EventId {
			assert.Greater(t, event.EventTime.AsTime().Sub(adoption.EventTime.AsTime()), runInput.Policy.TimeBudget)
			adoptionCompleted = true
		}
	}
	assert.Equal(t, 2, terminalStep)
	assert.True(t, adoptionCompleted, "terminal adoption must complete after crossing the active budget")
}
