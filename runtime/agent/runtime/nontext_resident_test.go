package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type compatibilityWorkflowContext struct {
	*testWorkflowContext
	scans int
}

func (w *compatibilityWorkflowContext) ExecuteRecordContentActivity(ctx context.Context, call engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	if call.Input.Compatibility != nil {
		w.scans++
	}
	return w.testWorkflowContext.ExecuteRecordContentActivity(ctx, call)
}

func TestNonTextResidentWorkflowBeforePlanner(t *testing.T) {
	for _, tc := range []struct {
		name                                                             string
		qualified, waived, supported, textOnly, explicitGuard, oversized bool
		scans                                                            int
		resident                                                         bool
		reason                                                           string
	}{
		{name: "explicit guarded", explicitGuard: true, resident: true, reason: "provider"},
		{name: "oversized supported PDF", qualified: true, supported: true, oversized: true, scans: 1, resident: true, reason: "too_large"},
		{name: "provider", qualified: true, resident: true, reason: "provider"},
		{name: "exact format rejection", qualified: true, supported: true, scans: 1, resident: true, reason: "too_large"},
		{name: "unqualified", supported: true},
		{name: "explicit waiver", waived: true, supported: true},
		{name: "text only", qualified: true, supported: true, textOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 6)
			base.contextSettings = compaction.Merge(compaction.Settings{Default: true, KeepNonTextResident: true, MaxTier: compaction.Evict}, nil)
			if tc.explicitGuard {
				base.contextSettings.Default = false
				base.contextSettings.Threshold = true
			}
			if tc.waived {
				base.contextSettings.Default = false
				base.contextSettings.Threshold = true
				base.contextSettings.KeepNonTextResident = false
			}
			fact := contextmeasure.RequestFacts{Model: "m", Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, ViewBearing: true, ContextWindow: 200000}
			if tc.qualified {
				fact.Qualified = compaction.Evict
			}
			if tc.supported {
				fact.ToolResultParts = model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 100, DocumentFormats: []model.DocumentFormat{"pdf"}, MaxDocumentBytes: 100}
			}
			started, err := callrecord.Encode(callrecord.Started{Key: fact.Call, Model: fact.Model, Activity: callrecord.ActivityPlanner})
			require.NoError(t, err)
			call := runstore.CallRecord{Key: fact.Call, State: runstore.CallStarted, Started: started}
			require.NoError(t, rt.RunStore.PutCall(t.Context(), "root", call))
			call.State = runstore.CallCompleted
			call.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &fact})
			require.NoError(t, err)
			require.NoError(t, rt.RunStore.PutCall(t.Context(), "root", call))
			base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{fact})
			if tc.oversized {
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.DocumentPart{Name: "report", Format: "pdf", Bytes: make([]byte, 101)}}}), Role: "user", NonText: 51}))
			} else if !tc.textOnly {
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}}), Role: "user", NonText: 1500}))
			}
			wf := &compatibilityWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt}}
			original := base.view()
			for range 2 {
				input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
				require.NoError(t, rt.preparePlanActivity(wf, base, engine.ActivityOptions{}, &input, time.Time{}))
				assert.Equal(t, tc.resident, base.compactionState.Flags.NonTextResident)
				if tc.resident {
					assert.Equal(t, original.TailFrom, input.View.TailFrom)
					assert.Equal(t, original.ClearedThrough, input.View.ClearedThrough)
				}
			}
			assert.Equal(t, tc.scans, wf.scans, "sticky residency must not repeat compatibility work")
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			var diagnostics int
			for _, event := range page.Events {
				if event.Type == hooks.ContextNonTextResident {
					diagnostics++
					var diagnostic hooks.ContextDiagnosticEvent
					require.NoError(t, json.Unmarshal(event.Payload, &diagnostic))
					assert.Equal(t, tc.reason, diagnostic.Reason)
					assert.Equal(t, "m", diagnostic.Model)
				}
			}
			if tc.resident {
				assert.Equal(t, 1, diagnostics, "diagnostic is durable before a planner is scheduled")
			} else {
				assert.Zero(t, diagnostics)
			}
		})
	}
}

func TestNonTextPlannerPayloadBound(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 4)
	base.contextSettings = compaction.Merge(compaction.Settings{Default: true, KeepNonTextResident: true, MaxTier: compaction.Evict}, nil)
	parts := make([]model.Part, 0, 100)
	formats := make([]model.DocumentFormat, 0, 100)
	for i := range 100 {
		format := model.DocumentFormat(fmt.Sprintf("format-%03d-%s", i, strings.Repeat("x", 40)))
		formats = append(formats, format)
		parts = append(parts, model.DocumentPart{Name: "part", Format: format, Bytes: make([]byte, 400)})
	}
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: parts}), Role: "user", NonText: 20000}))
	fact := contextmeasure.RequestFacts{Model: "m", Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, ViewBearing: true, Qualified: compaction.Evict, ContextWindow: 2000000, ToolResultParts: model.ToolResultPartSupport{DocumentFormats: formats, MaxDocumentBytes: 2000}}
	started, err := callrecord.Encode(callrecord.Started{Key: fact.Call, Model: fact.Model, Activity: callrecord.ActivityPlanner})
	require.NoError(t, err)
	record := runstore.CallRecord{Key: fact.Call, State: runstore.CallStarted, Started: started}
	require.NoError(t, rt.RunStore.PutCall(t.Context(), "root", record))
	record.State = runstore.CallCompleted
	record.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &fact})
	require.NoError(t, err)
	require.NoError(t, rt.RunStore.PutCall(t.Context(), "root", record))
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{fact})
	wf := &compatibilityWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt}}
	input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
	require.NoError(t, rt.preparePlanActivity(wf, base, engine.ActivityOptions{}, &input, time.Time{}))
	assert.Equal(t, 1, wf.scans)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	calls := 0
	for _, entry := range input.View.Pending {
		calls += len(entry.Calls)
	}
	assert.LessOrEqual(t, len(data), 4096+400*len(input.View.Pending)+100*calls+40*len(input.Models))
	assert.NotContains(t, string(data), string(formats[0]), "capability and part descriptors must not enter planner envelope")
}

type qualifiedOverflowProvider struct{ *overflowProvider }

func (*qualifiedOverflowProvider) ProviderConfiguration(context.Context, *model.Request) string {
	return "qualified-overflow"
}

func TestNonTextResidentDefaultOverflowStillRecovers(t *testing.T) {
	rt := New(WithContextQualification(Qualification{Config: "qualified-overflow", Models: []string{"small"}, Tier: compaction.Evict}))
	provider := &qualifiedOverflowProvider{&overflowProvider{window: 8000, realWindow: 8000}}
	require.NoError(t, rt.RegisterModel("fake", provider))
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{newAnyJSONSpec("test.next", "test")}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}}), nil
	}}))
	call := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) error {
		client, ok := agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: messages})
		return err
	}
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		if err := call(ctx, in.Agent, in.Messages); err != nil {
			return nil, err
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "test.next", ToolCallID: "next", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		if err := call(ctx, in.Agent, in.Messages); err != nil {
			return nil, err
		}
		return oracleFinal(), nil
	}})
	seed := []*model.Message{{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}, model.TextPart{Text: strings.Repeat("history", 9000)}}}, {Role: "assistant", Parts: []model.Part{model.TextPart{Text: "noted"}}}, contentMessage("continue")}
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("resident-overflow"))
	require.NoError(t, err)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "resident-overflow", "resident-overflow")
	require.NoError(t, err)
	resident, recovered, compacted := 0, 0, 0
	for _, event := range record.Runs[0].Events {
		switch {
		case event.Type == hooks.ContextNonTextResident:
			resident++
		case event.Type == hooks.ContextOverflow:
			recovered++
		case event.Type == hooks.ContextCompacted:
			var adoption hooks.ContextCompactionEvent
			require.NoError(t, json.Unmarshal(event.Payload, &adoption))
			if adoption.Reason != "overflow" {
				compacted++
			}
		}
	}
	assert.Equal(t, 1, resident)
	assert.Positive(t, recovered)
	assert.Zero(t, compacted)
	assert.True(t, record.Runs[0].NonTextResident)
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	_, err = runrecord.FromExport(data)
	require.NoError(t, err)
}

func TestNonTextResidentRechecksOriginalsAfterEviction(t *testing.T) {
	for _, change := range []string{"late PDF", "new narrower model", "smaller budget"} {
		t.Run(change, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 30)
			entries := base.Entries
			base.Entries = nil
			entries[0].Hash = compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}})
			entries[0].NonText = 1500
			for _, entry := range entries {
				require.NoError(t, base.appendEntry(entry))
			}
			base.contextSettings = compaction.Merge(compaction.Settings{Default: true, KeepNonTextResident: true, KeepRecentTokens: 30, MinSavingTokens: 20, MaxTier: compaction.Evict, TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}}, nil)
			fact := contextmeasure.RequestFacts{Model: "wide", Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, ViewBearing: true, Qualified: compaction.Evict, ContextWindow: 200000, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png", "jpeg"}, MaxImageBytes: 100}}
			persistResidentCall(t, rt.RunStore, fact)
			base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{fact})
			wf := &compatibilityWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt}}
			first := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
			require.NoError(t, rt.preparePlanActivity(wf, base, engine.ActivityOptions{}, &first, time.Time{}))
			require.Greater(t, first.View.TailFrom, index.MessageID(1), "supporting attachment is eligible for eviction")
			require.Equal(t, 1, wf.scans)
			base.compactionView = first.View
			base.compactionState = base.compactionDecision.State
			switch change {
			case "late PDF":
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "user", Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.DocumentPart{Name: "later", Format: "pdf", Bytes: []byte{1}}}}), NonText: 1}))
			case "new narrower model":
				fact.Model, fact.Call.Seq = "narrow", 2
				fact.ToolResultParts.ImageFormats = []model.ImageFormat{"jpeg"}
				persistResidentCall(t, rt.RunStore, fact)
				base.compactionState = compaction.Observe(base.compactionState, []contextmeasure.RequestFacts{fact})
			case "smaller budget":
				fact.ContextWindow = 6000
				base.compactionState = compaction.Observe(base.compactionState, []contextmeasure.RequestFacts{fact})
			}
			second := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
			require.NoError(t, rt.preparePlanActivity(wf, base, engine.ActivityOptions{}, &second, time.Time{}))
			assert.True(t, base.compactionState.Flags.NonTextResident)
			assert.Equal(t, first.View.TailFrom, second.View.TailFrom, "new residency prevents further threshold eviction")
			if change == "smaller budget" {
				assert.Equal(t, 1, wf.scans, "budget narrowing uses the unchanged compatibility evidence")
			} else {
				assert.Equal(t, 2, wf.scans)
			}
		})
	}
}

func persistResidentCall(t *testing.T, store runstore.Store, fact contextmeasure.RequestFacts) {
	t.Helper()
	started, err := callrecord.Encode(callrecord.Started{Key: fact.Call, Model: fact.Model, Activity: callrecord.ActivityPlanner})
	require.NoError(t, err)
	record := runstore.CallRecord{Key: fact.Call, State: runstore.CallStarted, Started: started}
	require.NoError(t, store.PutCall(t.Context(), "root", record))
	record.State = runstore.CallCompleted
	record.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &fact})
	require.NoError(t, err)
	require.NoError(t, store.PutCall(t.Context(), "root", record))
}
