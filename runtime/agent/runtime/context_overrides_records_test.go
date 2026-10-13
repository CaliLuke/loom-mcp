package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestContextFingerprintSummaryRecords(t *testing.T) {
	rt, in, _, _ := summaryFixture(t)
	start := &RunInput{AgentID: in.AgentID, RunID: in.RunID, RootRunID: in.Scope}
	rt.stampContextCompaction(start)
	require.NoError(t, rt.prepareContextStart(t.Context(), start))
	in.ContextSettings = start.ContextSettings
	in.ContextFingerprint = start.ContextFingerprint
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.NotNil(t, out.Checkpoint)
	data, err := rt.RunStore.Get(t.Context(), in.Scope, *out.Checkpoint)
	require.NoError(t, err)
	var record index.CheckpointRecord
	require.NoError(t, json.Unmarshal(data, &record))
	assert.Equal(t, index.Fingerprint(start.ContextFingerprint), record.Fingerprint)
	calls, err := rt.RunStore.ListCalls(t.Context(), in.Scope, in.RunID)
	require.NoError(t, err)
	require.NotEmpty(t, calls)
	for _, call := range calls {
		started, err := callrecord.DecodeStarted(call.Started)
		require.NoError(t, err)
		assert.Equal(t, start.ContextFingerprint, started.Context)
	}
}

func TestContextOverrideChildPinning(t *testing.T) {
	rt := New(WithContextCompaction("parent", ContextCompaction{}), WithContextCompaction("child", ContextCompaction{}))
	parent := &RunInput{AgentID: "parent", RootRunID: "root", RunID: "root"}
	WithContextOverrides(ContextOverrides{ContextWindow: new(8000)})(parent)
	rt.stampContextCompaction(parent)
	require.NoError(t, rt.prepareContextStart(t.Context(), parent))
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", ContextStart: &api.ContextStartInput{AgentID: "child", Settings: resolvedContextSettings(&ContextCompaction{}, ContextRecovery{})}})
	require.NoError(t, err)
	require.NotNil(t, out.ContextStart)
	assert.Zero(t, out.ContextStart.Settings.ContextWindow)
	assert.NotEqual(t, parent.ContextFingerprint, out.ContextStart.Fingerprint)
	assert.Equal(t, "child.context.summary", string(out.ContextStart.Settings.Prompt.Ref.ID))
}

func TestContextOverrideWorkflowRevalidation(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{compactionState: compaction.State{ValidateBudgets: true}, contextSettings: compaction.Merge(compaction.Settings{Threshold: true, KeepRecentTokens: 10000}, nil)}
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	in := PlanActivityInput{RunID: "run"}
	prepareCompaction(base, &in)
	require.NoError(t, rt.adoptCompaction(wf, base, in, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{{Model: "large", ViewBearing: true, ContextWindow: 200000}}}, index.ViewRef{}))
	prepareCompaction(base, &in)
	err := rt.adoptCompaction(wf, base, in, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 8000}}}, index.ViewRef{})
	var application *engine.ApplicationError
	require.ErrorAs(t, err, &application)
	assert.True(t, application.NonRetryable)
	assert.Contains(t, err.Error(), "ResidentTokens")
}

func TestContextCompactCanonicalTelemetry(t *testing.T) {
	recorder := &runtimeTelemetryRecorder{}
	rt := New(WithTracer(recorder))
	for step, kind := range []hooks.EventType{hooks.ContextCompacted, hooks.ContextCompactionSkipped} {
		event := hooks.NewContextCompactionEvent("run", "agent", "session", step, kind, "threshold", "evict", 2, 3, 10000, 4000, nil, time.Unix(100, 0))
		event.Fingerprint = "fingerprint"
		event.EffectiveWindow = 8000
		event.Pretend = true
		event.Latency = int64(time.Duration(step) * time.Second)
		require.NoError(t, rt.publishHook(t.Context(), event, "turn"))
		require.NoError(t, rt.publishHook(t.Context(), event, "turn"))
	}
	spans := recorder.spanRecords()
	require.Len(t, spans, 2)
	for step, span := range spans {
		assert.Equal(t, trace.SpanKindInternal, span.startConfig.SpanKind())
		assert.Equal(t, time.Unix(100, 0), span.endConfig.Timestamp())
		assert.Equal(t, time.Unix(100, 0).Add(-time.Duration(step)*time.Second), span.startConfig.Timestamp())
		for _, field := range []string{"reason", "tier", "outcome", "kind", "units", "calls", "before", "after", "checkpoint", "fingerprint", "step", "model", "chunks", "effective_window", "pretend", "usage.input_tokens", "usage.output_tokens", "usage.total_tokens", "usage.cache_read_tokens", "usage.cache_write_tokens"} {
			assert.Contains(t, span.attributes, "loom_mcp.context."+field)
		}
		for field, value := range map[string]string{"agent_id": "agent", "run_id": "run", "session_id": "session", "turn_id": "turn"} {
			assert.Equal(t, value, span.attributes["loom_mcp."+field].AsString())
		}
		assert.Equal(t, "context.compact", span.name)
		assert.Equal(t, "fingerprint", span.attributes["loom_mcp.context.fingerprint"].AsString())
		assert.Equal(t, int64(8000), span.attributes["loom_mcp.context.effective_window"].AsInt64())
		assert.True(t, span.ended)
	}
}

func TestContextOverrideChildWorkflow(t *testing.T) {
	rt := New(WithContextCompaction("content.agent", ContextCompaction{}), WithContextCompaction("child.agent", ContextCompaction{}))
	var childSettings compaction.Settings
	var childFingerprint string
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "child.agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}}, Workflow: engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "child.queue", Handler: func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		childSettings = in.ContextSettings
		childFingerprint = in.ContextFingerprint
		return rt.ExecuteWorkflow(w, in)
	}}, PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool"}))
	registration := NewAgentToolsetRegistration(rt, AgentToolConfig{Name: "delegate", AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"}})
	spec := newAnyJSONSpec("delegate.child", "delegate")
	spec.IsAgentTool = true
	spec.AgentID = "child.agent"
	registration.Specs = append(registration.Specs, spec)
	require.NoError(t, rt.RegisterToolset(registration))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"), WithContextOverrides(ContextOverrides{ContextWindow: new(8000)}))
	require.NoError(t, err)
	assert.Zero(t, childSettings.ContextWindow)
	assert.NotEmpty(t, childFingerprint)
	assert.Equal(t, "child.agent.context.summary", string(childSettings.Prompt.Ref.ID))
}

func TestContextCompactEffectiveWindowExcludesReserve(t *testing.T) {
	state, err := compaction.ObserveEffective(compaction.State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 10000, OutputReserve: 1000}}, compaction.Settings{})
	require.NoError(t, err)
	event := stampCompactionEvent(&workflowPlanInput{compactionState: state}, &hooks.ContextCompactionEvent{})
	assert.Equal(t, 10000, event.EffectiveWindow)
}
