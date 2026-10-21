package agentfeatures_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// testTemporalTransferDispatchLatency isolates server dispatch from planner/tool work.
// Forty serial activities exhaust the transfer poller's initial burst, exposing
// its sustained scheduling cost. The median distinguishes repeated throttling
// from an isolated task-delivery delay.
func testTemporalTransferDispatchLatency(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	const queue = "transfer-dispatch-budget"
	t.Cleanup(func() {
		if t.Failed() {
			logTemporalHistory(t, temporalClient, queue)
		}
	})
	w := worker.New(temporalClient, queue, worker.Options{})
	w.RegisterWorkflow(transferDispatchWorkflow)
	w.RegisterActivity(transferDispatchActivity)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)
	handle, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "transfer-dispatch-budget",
		TaskQueue: queue,
	}, transferDispatchWorkflow)
	require.NoError(t, err)
	var durations []time.Duration
	require.NoError(t, handle.Get(ctx, &durations))
	require.Len(t, durations, 40)
	slices.Sort(durations)
	median := (durations[19] + durations[20]) / 2
	t.Logf("40 serial no-op activity dispatches: median=%s min=%s max=%s", median, durations[0], durations[39])
	assert.Less(t, median, 50*time.Millisecond, "dispatch must avoid the default 20-polls/s throttle")
}

func transferDispatchWorkflow(ctx workflow.Context) ([]time.Duration, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	durations := make([]time.Duration, 0, 40)
	for range 40 {
		started := workflow.Now(ctx)
		if err := workflow.ExecuteActivity(ctx, transferDispatchActivity).Get(ctx, nil); err != nil {
			return nil, err
		}
		durations = append(durations, workflow.Now(ctx).Sub(started))
	}
	return durations, nil
}

func transferDispatchActivity(context.Context) error {
	return nil
}

// requireTimingQualification keeps latency contracts explicit on a dedicated host.
func requireTimingQualification(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_MCP_TIMING_QUALIFICATION") != "1" {
		t.Skip("run make test-timing-qualification for latency contracts")
	}
}
