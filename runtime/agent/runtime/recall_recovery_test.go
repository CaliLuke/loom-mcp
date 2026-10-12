package runtime

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecallRecoveryRecordsOnlyAddedToolBytes(t *testing.T) {
	base := workflowPlanInput{}
	advertised := PlanActivityInput{View: index.ViewRef{PreviewThrough: 1}}
	prepareRecall(&base, &advertised)
	definition, err := contextmeasure.Measure(&model.Request{Tools: advertised.ExtraTools}, model.ContextLimits{}, nil)
	require.NoError(t, err)
	require.Positive(t, definition.TextBytes)
	for _, tc := range []struct {
		name          string
		input         PlanActivityInput
		already, none bool
		want          int
	}{
		{name: "first recovery", want: definition.TextBytes},
		{name: "already advertised", already: true},
		{name: "recall disabled", input: PlanActivityInput{ContextSettings: compaction.ContextSettings{DisableRecall: true}}},
		{name: "tools disabled", input: PlanActivityInput{ToolsDisabled: true}},
		{name: "finalize", input: PlanActivityInput{Finalize: &planner.Termination{}}},
		{name: "correction disables tools", input: PlanActivityInput{Recovery: &api.ModelRecovery{DisableTools: true}}},
		{name: "request disables tools", none: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New()}
			provider := &recordingTestClient{store: store, response: &model.Response{}}
			view := []*model.Message{contentMessage("view")}
			recorder := NewCallRecording(store, "root", index.ViewRef{}, view, 1)
			recorder.configureOverflow(&tc.input)
			req := &model.Request{Messages: view}
			if tc.already {
				req.Tools = advertised.ExtraTools
			}
			if tc.none {
				req.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeNone}
			}
			measured, err := contextmeasure.Measure(req, model.ContextLimits{}, view)
			require.NoError(t, err)
			client := &callRecordingClient{inner: provider, recording: recorder}
			_, err = client.Complete(recordingCallContext(1), req)
			require.NoError(t, err)
			calls, err := store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			require.NotNil(t, completion.Facts)
			data, err := callrecord.Encode(completion.Facts)
			require.NoError(t, err)
			var costs struct {
				RecoveryToolBytes int `json:"recovery_tool_bytes"`
			}
			require.NoError(t, json.Unmarshal(data, &costs))
			assert.Equal(t, tc.want, costs.RecoveryToolBytes)
			assert.Equal(t, measured.TextBytes, completion.Facts.TextBytes, "failed request size remains its actual measurement")
		})
	}
}
