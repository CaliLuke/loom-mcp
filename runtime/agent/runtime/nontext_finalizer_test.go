package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonTextResidencyDeadlineThenFinalizer(t *testing.T) {
	for _, exact := range []bool{false, true} {
		name := "direct support"
		if exact {
			name = "activity compatibility"
		}
		t.Run(name, func(t *testing.T) {
			runResidencyFinalizerBoundary(t, "finalizer", exact)
		})
	}
}

func TestNonTextResidencyRejectsUnrelatedLaterPlanner(t *testing.T) {
	for _, transition := range []string{"ordinary", "stale finalizer", "wrong reason", "larger gap"} {
		t.Run(transition, func(t *testing.T) {
			runResidencyFinalizerBoundary(t, transition, false)
		})
	}
}

// runResidencyFinalizerBoundary uses the workflow clock to expire the active
// deadline after protected preparation but before its planner can be scheduled.
func runResidencyFinalizerBoundary(t *testing.T, transition string, exact bool) {
	t.Helper()
	rt := New(WithContextQualification(Qualification{Config: "qualified-test", Models: []string{"measured"}, Tier: compaction.Evict}))
	base := compactionFixture(t, rt.RunStore, 2)
	base.protocolVersion = api.WorkflowProtocolVersion
	base.AgentID = "agent"
	base.RunContext = run.Context{RootRunID: "root", RunID: "run", Attempt: 1}
	base.contextSettings = compaction.Merge(compaction.Settings{Default: true, KeepNonTextResident: true}, nil)
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "user", Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}}), NonText: 1500}))
	if transition == "stale finalizer" {
		require.NoError(t, base.structural(index.FinalizeResults, nil))
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeHint, ProtocolVersion: index.TemplateProtocolVersion, Reason: string(planner.TerminationReasonTimeBudget)}}))
	}
	provider := &residencyFactsClient{defaultFactsClient: &defaultFactsClient{recordingFactsClient: &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 100000}, resolved: "measured"}}}
	if exact {
		provider.support = model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 100}
	}
	require.NoError(t, rt.RegisterModel("fake", provider))
	reg := AgentRegistration{ID: "agent", ResumeActivityName: "resume", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
		if err != nil {
			return nil, err
		}
		return assistantCommitResult(), nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}}}
	rt.agents["agent"] = reg
	wf := &seedBudgetWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}, now: time.Unix(0, 0)}
	out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{})
	require.NoError(t, err)
	base.adopt(out)
	require.Equal(t, 1, base.step)
	wf.duringHook = func(call engine.HookActivityCall) {
		if call.Input.Type == hooks.ContextResidencyAdopted {
			wf.now = wf.now.Add(2 * time.Second)
		}
	}
	out, err = rt.runPlanActivity(wf, base, "resume", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, wf.Now().Add(time.Second))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, out)
	require.True(t, base.compactionState.Flags.NonTextResident)
	require.Len(t, wf.plannerCalls, 1, "expired prepared step2 must never schedule")
	input := &RunInput{AgentID: "agent", RunID: "run", RootRunID: "root"}
	if transition == "ordinary" || transition == "stale finalizer" {
		out, err = rt.runPlanActivity(wf, base, "resume", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{})
		require.NoError(t, err)
		base.adopt(out)
	} else {
		reason := planner.TerminationReasonTimeBudget
		if transition == "wrong reason" {
			reason = planner.TerminationReasonFailureCap
		}
		if transition == "larger gap" {
			base.step++
		}
		final, err := rt.finalizeWithPlanner(wf, reg, input, base, nil, model.TokenUsage{}, policy.CapsState{}, 1, "", nil, reason, wf.Now().Add(10*time.Second))
		require.NoError(t, err)
		require.NotNil(t, final)
		require.NotNil(t, wf.plannerCalls[1].Input.Finalize)
	}
	require.Len(t, wf.plannerCalls, 2)
	expectedStep := 3
	if transition == "larger gap" {
		expectedStep = 4
	}
	assert.Equal(t, expectedStep, wf.plannerCalls[1].Input.Step)
	require.NoError(t, rt.sealWorkflowRecord(wf, input, base))
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	if transition != "finalizer" {
		require.ErrorContains(t, err, "residency adoption planner step mismatch")
		return
	}
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	assert.True(t, record.Runs[0].Sealed)
	assert.True(t, record.Runs[0].NonTextResident)
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
}
