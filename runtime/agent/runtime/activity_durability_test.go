package runtime

import (
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityToolRunReportsContentStorageFailures(t *testing.T) {
	baseline, _, err := runDurableFailureScenario(t, true, 0, "", true)
	require.NoError(t, err)
	require.NotEmpty(t, baseline.operations)
	for i, operation := range baseline.operations {
		t.Run(fmt.Sprintf("%02d_%s", i+1, operation), func(t *testing.T) {
			_, log, err := runDurableFailureScenario(t, true, i+1, "", true)
			if err != nil {
				require.ErrorIs(t, err, assert.AnError)
				return
			}
			// Activity failures can be returned to the planner as tool errors;
			// the recorded tool result must expose the failure before resuming.
			page, err := log.Store.List(t.Context(), "root", "", 100)
			require.NoError(t, err)
			var failures int
			for _, event := range page.Events {
				if event.Type != hooks.ToolResultReceived {
					continue
				}
				decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: event.Type, RunID: event.RunID, Payload: event.Payload})
				require.NoError(t, err)
				result := decoded.(*hooks.ToolResultReceivedEvent)
				require.NotNil(t, result.Error)
				require.NotNil(t, result.Error.Cause)
				assert.Contains(t, result.Error.Cause.Error(), assert.AnError.Error())
				assert.Empty(t, result.ResultJSON)
				failures++
			}
			assert.Equal(t, 1, failures)
		})
	}
}
