package runtime

import (
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryStorageFailureNeverPublishesSuccessfulCheckpoint(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	trace := &continuationStorageFault{Store: rt.RunStore}
	rt.RunStore = trace
	client.store = trace
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.NotNil(t, out.Checkpoint)
	require.NotEmpty(t, trace.operations)
	for position, operation := range trace.operations {
		t.Run(fmt.Sprintf("%02d_%s", position+1, operation), func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			failed := &continuationStorageFault{Store: rt.RunStore, failAt: position + 1}
			rt.RunStore = failed
			client.store = failed
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.GreaterOrEqual(t, len(failed.operations), position+1, "fault must be reached")
			if err != nil {
				require.ErrorIs(t, err, assert.AnError)
				assert.Nil(t, out)
			} else {
				require.NotNil(t, out)
				assert.Nil(t, out.Checkpoint, "a failed storage operation cannot publish an accepted checkpoint")
				assert.NotEmpty(t, out.Kind)
			}
		})
	}
}
