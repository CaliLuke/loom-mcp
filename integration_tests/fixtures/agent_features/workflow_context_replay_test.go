package agentfeatures_test

import (
	"bytes"
	"crypto/sha256"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

// TestGeneratedFeatureContextOverrideHistory checks real recorded settings, not
// current registration defaults, own planner and summarizer activity inputs.
func TestGeneratedFeatureContextOverrideHistory(t *testing.T) {
	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	for _, filename := range []string{workflowHistoryFile, workflowSummaryHistoryFile, workflowBudgetHistoryFile} {
		t.Run(filename, func(t *testing.T) {
			data, err := root.ReadFile(filename)
			require.NoError(t, err)
			history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
			require.NoError(t, err)
			var start api.RunInput
			require.NoError(t, json.Unmarshal(history.Events[0].GetWorkflowExecutionStartedEventAttributes().Input.Payloads[0].Data, &start, jsonopts.FormatDurationAsNano(true)))
			assert.Equal(t, api.WorkflowProtocolVersion, start.WorkflowProtocolVersion)
			assert.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(compaction.Canonical(start.ContextSettings))), start.ContextFingerprint)
			assert.NotEmpty(t, start.ContextSettings.Prompt.Template)
			assert.NotEmpty(t, start.ContextSettings.Prompt.Ref.ID)
			if filename != workflowBudgetHistoryFile {
				require.NotNil(t, start.Policy)
				require.NotNil(t, start.Policy.Context)
				require.NotNil(t, start.Policy.Context.ContextWindow)
				assert.Equal(t, 5000, *start.Policy.Context.ContextWindow)
				assert.Equal(t, 5000, start.ContextSettings.ContextWindow)
			}
			planners := 0
			for _, event := range history.Events {
				scheduled := event.GetActivityTaskScheduledEventAttributes()
				if scheduled == nil {
					continue
				}
				switch scheduled.ActivityType.Name {
				case "features.coordinator.plan", "features.coordinator.resume":
					var input api.PlanActivityInput
					require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
					assert.Equal(t, start.ContextFingerprint, input.View.ContextFingerprint)
					planners++
				case "features.coordinator.summarize_context":
					var input api.SummarizeContextInput
					require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
					assert.Equal(t, start.ContextFingerprint, input.ContextFingerprint)
					assert.Equal(t, start.ContextSettings, input.ContextSettings)
				}
			}
			assert.Positive(t, planners)
		})
	}
}
