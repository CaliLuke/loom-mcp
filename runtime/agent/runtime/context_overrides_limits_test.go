package runtime

import (
	"encoding/json/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextLimitsPartialAndMergedOverrides(t *testing.T) {
	t.Run("output only", func(t *testing.T) {
		assert.NotPanics(t, func() { New(WithContextLimits(map[string]model.ContextLimits{"m": {MaxOutputTokens: 10}})) })
	})
	for _, window := range []int{49, 50, 99} {
		t.Run(strconv.Itoa(window), func(t *testing.T) {
			rt := New(WithContextLimits(map[string]model.ContextLimits{"m": {ContextWindow: window}}))
			require.NoError(t, rt.RegisterModel("m", contextLimitsClient{}))
			client, ok := rt.ModelClient("m")
			require.True(t, ok)
			_, _, known := client.(model.ContextInfo).ContextLimits(t.Context(), &model.Request{})
			assert.False(t, known)
		})
	}
}

func TestContextInvalidOverrideFactsAndDiagnostic(t *testing.T) {
	rt := New(WithContextLimits(map[string]model.ContextLimits{"m": {ContextWindow: 50}}))
	require.NoError(t, rt.RegisterModel("m", contextLimitsClient{}))
	client, ok := rt.ModelClient("m")
	require.True(t, ok)
	messages := []*model.Message{contentMessage("hello")}
	recording := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, messages, 1)
	facts, limits, err := recording.measureRequest(t.Context(), &model.Request{Messages: messages}, client, runstore.CallKey{}, "m")
	require.NoError(t, err)
	assert.True(t, facts.LimitsInvalid)
	assert.Zero(t, facts.ContextWindow)
	assert.True(t, limits.InvalidOverride)
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	base := &workflowPlanInput{}
	for range 2 {
		require.NoError(t, rt.adoptCompaction(wf, base, PlanActivityInput{RunID: "run"}, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{facts}}, index.ViewRef{}))
	}
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, hooks.ContextLimitsUnknown, page.Events[0].Type)
	var diagnostic hooks.ContextDiagnosticEvent
	require.NoError(t, json.Unmarshal(page.Events[0].Payload, &diagnostic))
	assert.Equal(t, "invalid_override", diagnostic.Reason)
	assert.Equal(t, "m", diagnostic.Model)
}

func TestContextGuardUsesRealLimitsAndMargin(t *testing.T) {
	for _, tc := range []struct {
		name          string
		window, bytes int
		margin        Ratio
		reject        bool
	}{
		{"zero at boundary", 100, 300, Ratio{Num: 0, Den: 1}, false}, {"zero above boundary", 100, 303, Ratio{Num: 0, Den: 1}, true}, {"half at boundary", 100, 450, Ratio{Num: 1, Den: 2}, false}, {"half above boundary", 100, 453, Ratio{Num: 1, Den: 2}, true}, {"zero", 100, 360, Ratio{Num: 0, Den: 1}, true}, {"half", 100, 360, Ratio{Num: 1, Den: 2}, false}, {"pretend", 200000, 60000, Ratio{Num: 1, Den: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New()}
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: store, response: &model.Response{}}, limits: model.ContextLimits{ContextWindow: tc.window}, resolved: "m"}
			messages := []*model.Message{contentMessage(strings.Repeat("x", tc.bytes))}
			recording := NewCallRecording(store, "root", index.ViewRef{}, messages, 1)
			settings := compaction.Merge(compaction.Settings{ContextWindow: 8000}, &ContextOverrides{GuardMargin: &tc.margin})
			recording.configureOverflow(&PlanActivityInput{ContextSettings: settings, Models: []api.ModelContext{{Model: "m", Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Calibrated: true, Budget: 7000}}})
			_, err := (&callRecordingClient{inner: provider, recording: recording}).Complete(recordingCallContext(1), &model.Request{Messages: messages})
			if tc.reject {
				require.ErrorIs(t, err, ErrContextBudget)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestContextInvalidLimitsFailThresholdAndReport(t *testing.T) {
	rt := New()
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	base := &workflowPlanInput{contextSettings: compaction.Merge(compaction.Settings{Threshold: true}, nil)}
	err := rt.adoptCompaction(wf, base, PlanActivityInput{RunID: "run"}, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{{ViewBearing: true, Model: "invalid", LimitsInvalid: true}}}, index.ViewRef{})
	var invalid *ContextSettingsError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "ContextWindow", invalid.Field)
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	var diagnostic hooks.ContextDiagnosticEvent
	require.NoError(t, json.Unmarshal(page.Events[0].Payload, &diagnostic))
	assert.Equal(t, "invalid_override", diagnostic.Reason)
	assert.Equal(t, "invalid", diagnostic.Model)
}

// TestContextGuardMarginWorkflow covers the run-option to planner-command boundary.
func TestContextGuardMarginWorkflow(t *testing.T) {
	for _, margin := range []Ratio{{Num: 0, Den: 1}, {Num: 1, Den: 2}} {
		t.Run(strconv.Itoa(margin.Num), func(t *testing.T) {
			rt := New()
			rt.agents["agent"] = AgentRegistration{ID: "agent", PlanActivityName: "plan"}
			input := &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion}
			WithContextOverrides(ContextOverrides{GuardMargin: &margin})(input)
			rt.stampContextCompaction(input)
			require.NoError(t, rt.prepareContextStart(t.Context(), input))
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*PlanActivityOutput{{Result: oracleFinal()}}}
			_, err := rt.ExecuteWorkflow(wf, input)
			require.NoError(t, err)
			require.Len(t, wf.plannerCalls, 1)
			assert.Equal(t, contextmeasure.Rate{Tokens: margin.Num, Bytes: margin.Den}, wf.plannerCalls[0].Input.ContextSettings.GuardMargin)
		})
	}
}
