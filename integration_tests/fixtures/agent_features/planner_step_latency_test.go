package agentfeatures_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

// measurePlannerStepLatency uses the separate resume adoption activity from each of 20 real
// executions. Its interval includes scheduling, the activity and workflow resumption.
func measurePlannerStepLatency(t *testing.T, temporalClient client.Client) {
	t.Helper()
	if os.Getenv("LOOM_MCP_MEASURE_PLANNER_STEP") != "1" {
		t.Skip("set LOOM_MCP_MEASURE_PLANNER_STEP=1 for the 20-run non-race experiment")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerHistoryReplayAgent(t, fx)
	durations := make([]time.Duration, 0, 20)
	for i := range 20 {
		sessionID, runID := fmt.Sprintf("sess-step-latency-%d", i), fmt.Sprintf("run-step-latency-%d", i)
		_, err := fx.rt.CreateSession(ctx, sessionID)
		require.NoError(t, err)
		_, err = coordinator.NewClient(fx.rt).Run(ctx, sessionID, []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "record this request"}}}}, agentsruntime.WithRunID(runID))
		require.NoError(t, err)
		iter := temporalClient.GetWorkflowHistory(ctx, runID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		var events []*historypb.HistoryEvent
		for iter.HasNext() {
			event, err := iter.Next()
			require.NoError(t, err)
			events = append(events, event)
		}
		duration, activities, steps := plannerStepRoundTrip(t, events)
		durations = append(durations, duration)
		t.Logf("run=%d round_trip=%s activities=%d planner_steps=%d", i, duration, activities, steps)
	}
	slices.Sort(durations)
	median := (durations[9] + durations[10]) / 2
	t.Logf("planner_step round-trip median over 20 non-race real Temporal runs: %s (min=%s max=%s)", median, durations[0], durations[19])
}

func plannerStepRoundTrip(t *testing.T, events []*historypb.HistoryEvent) (time.Duration, int, int) {
	t.Helper()
	var scheduledID int64
	var start, completed time.Time
	var duration time.Duration
	activities, steps := 0, 0
	for _, event := range events {
		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
			activities++
			if scheduled.ActivityType.Name != "runtime.publish_hook" {
				continue
			}
			for _, envelope := range historyHookInputs(t, scheduled.Input.Payloads[0].Data) {
				if envelope.Type == hooks.PlannerStep {
					steps++
					if steps == 2 && scheduledID == 0 {
						scheduledID = event.EventId
						start = event.EventTime.AsTime()
					}
				}
			}
		}
		if activity := event.GetActivityTaskCompletedEventAttributes(); activity != nil && activity.ScheduledEventId == scheduledID {
			completed = event.EventTime.AsTime()
		}
		if !completed.IsZero() && duration == 0 && event.GetWorkflowTaskStartedEventAttributes() != nil {
			duration = event.EventTime.AsTime().Sub(start)
		}
	}
	assert.Equal(t, 2, steps)
	require.Positive(t, duration)
	return duration, activities, steps
}
