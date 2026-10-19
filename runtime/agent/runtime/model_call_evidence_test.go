package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCallBudgetWorkflowEvidence(t *testing.T) {
	rt := New(WithoutContextDefault("content.agent"))
	provider := &callInfoClient{calls: make(chan callInfoObservation, 3)}
	require.NoError(t, rt.RegisterModel("budget-model", provider))
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := in.Agent.ModelClient("budget-model")
		require.True(t, ok)
		require.NoError(t, budgetTestInvoke(ctx, client, false))
		require.ErrorIs(t, budgetTestInvoke(ctx, client, false), policy.ErrModelCallLimit)
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"), WithRunMaxModelCalls(1))
	require.Error(t, err)
	var application *engine.ApplicationError
	require.ErrorAs(t, err, &application)
	assert.Equal(t, modelCallLimitErrorType, application.Type)
	assert.True(t, application.NonRetryable)
	assert.Len(t, provider.calls, 1)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	run := record.Runs[0]
	assert.True(t, run.Sealed)
	require.NotNil(t, run.ModelCallLimit)
	assert.Equal(t, 2, run.ModelCallLimit.Call.Seq)
	require.Len(t, run.Calls, 1)
	assert.Equal(t, runrecord.Adopted, run.Calls[0].Class)
	assert.Empty(t, run.Transcript)
	encoded, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(encoded)
	require.NoError(t, err)
	require.Len(t, restored.Runs, 1)
	assert.Equal(t, run.ModelCallLimit, restored.Runs[0].ModelCallLimit)
	assert.Equal(t, run.Calls[0].Started.Key, restored.Runs[0].Calls[0].Started.Key)
}

func TestModelCallBudgetSummaryUsesRunReservations(t *testing.T) {
	rt, input, provider, _ := summaryFixture(t)
	input.MaxModelCalls = 1
	seq, err := rt.RunStore.NextSeq(t.Context(), input.Scope, input.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, seq, "prior planner reservation shares the summary run")
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, policy.ModelCallLimitReason, out.Kind)
	assert.Equal(t, 2, out.ModelCallLimit.Call.Seq)
	assert.Empty(t, provider.requests)
	assert.Empty(t, out.CallKeys)
	assert.Empty(t, out.Facts)
	assert.Nil(t, out.Checkpoint)
	assert.Equal(t, model.TokenUsage{}, out.Usage)
}

func TestModelCallBudgetSummaryRetryConsumesAllowance(t *testing.T) {
	rt, input, provider, _ := summaryFixture(t)
	input.MaxModelCalls = 1
	// A transient failure causes the existing local retry to reserve another slot.
	provider.errors = []error{model.NewProviderError("test", "complete", 503, model.ProviderErrorKindUnavailable, "", "busy", "", true, nil)}
	provider.partialOnError = true
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, 2, out.ModelCallLimit.Call.Seq)
	assert.Len(t, provider.requests, 1)
	assert.Len(t, out.CallKeys, 1)
	assert.Len(t, out.Facts, 1)
	assert.Nil(t, out.Checkpoint)
	assert.Equal(t, provider.response.Usage, out.Usage)
}

func TestModelCallBudgetSummaryAdoptionAndExport(t *testing.T) {
	rt, in, provider, entries := summaryFixture(t)
	provider.errors = []error{model.NewProviderError("test", "complete", 503, model.ProviderErrorKindUnavailable, "", "busy", "", true, nil)}
	provider.partialOnError = true
	base := summaryWorkflowBase(in, entries)
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}}
	input := PlanActivityInput{MaxModelCalls: 1, AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: base.compactionDecision.View, Step: in.Step, ContextSettings: base.contextSettings}
	err := rt.prepareWorkflowSummary(wf, base, &input, time.Time{})
	var application *engine.ApplicationError
	require.ErrorAs(t, err, &application)
	assert.Equal(t, modelCallLimitErrorType, application.Type)
	assert.True(t, application.NonRetryable)
	assert.Equal(t, 1, wf.calls)
	assert.Len(t, provider.requests, 1)
	assert.Nil(t, input.View.Checkpoint)
	assert.Nil(t, base.view().Checkpoint)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	observed := record.Runs[0]
	require.NotNil(t, observed.ModelCallLimit)
	assert.Equal(t, 2, observed.ModelCallLimit.Call.Seq)
	require.Len(t, observed.Calls, 1, "refusal must not create or adopt a phantom call")
	assert.Equal(t, runrecord.Adopted, observed.Calls[0].Class)
	assert.Equal(t, 1, observed.Calls[0].Started.Key.Seq)
	require.Len(t, observed.Events, 1)
	assert.Equal(t, hooks.ContextCompactionSkipped, observed.Events[0].Type)
	var event hooks.ContextCompactionEvent
	require.NoError(t, json.Unmarshal(observed.Events[0].Payload, &event))
	assert.Equal(t, policy.ModelCallLimitReason, event.Kind)
	assert.Equal(t, observed.ModelCallLimit, event.ModelCallLimit)
	assert.Equal(t, provider.response.Usage, event.Usage)
	require.Len(t, event.CallKeys, 1)
	assert.Equal(t, observed.Calls[0].Started.Key, event.CallKeys[0])
	assert.Nil(t, event.Checkpoint)
	encoded, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(encoded)
	require.NoError(t, err)
	require.Len(t, restored.Runs, 1)
	assert.Equal(t, observed.ModelCallLimit, restored.Runs[0].ModelCallLimit)
	assert.Len(t, restored.Runs[0].Calls, 1)
}
