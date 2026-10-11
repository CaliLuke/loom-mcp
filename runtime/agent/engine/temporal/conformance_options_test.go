package temporal_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
)

// verifyScheduledActivityOptions reads recorded server history, proving routing
// and timeout/retry translation without inspecting adapter-private state.
func verifyScheduledActivityOptions(t *testing.T, c client.Client, id, name string, want []engine.ActivityOptions) error {
	t.Helper()
	history := c.GetWorkflowHistory(t.Context(), id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	got := make([]engine.ActivityOptions, 0, len(want))
	for history.HasNext() {
		event, err := history.Next()
		require.NoError(t, err)
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil || scheduled.GetActivityType().GetName() != name {
			continue
		}
		retry := scheduled.GetRetryPolicy()
		got = append(got, engine.ActivityOptions{
			Queue:                  scheduled.GetTaskQueue().GetName(),
			ScheduleToStartTimeout: scheduled.GetScheduleToStartTimeout().AsDuration(),
			StartToCloseTimeout:    scheduled.GetStartToCloseTimeout().AsDuration(),
			HeartbeatTimeout:       scheduled.GetHeartbeatTimeout().AsDuration(),
			RetryPolicy:            engine.RetryPolicy{MaxAttempts: int(retry.GetMaximumAttempts()), InitialInterval: retry.GetInitialInterval().AsDuration(), BackoffCoefficient: retry.GetBackoffCoefficient()},
		})
	}
	if !assert.ObjectsAreEqual(want, got) {
		return &enginetest.GapError{Err: fmt.Errorf("scheduled activity options: want %#v, got %#v", want, got)}
	}
	return nil
}
