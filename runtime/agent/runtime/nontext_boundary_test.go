package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type residencyFactsClient struct {
	*defaultFactsClient
	support model.ToolResultPartSupport
}

func (c *residencyFactsClient) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return c.support
}

type residencyBoundaryCase struct {
	registration  string
	interception  string
	support       model.ToolResultPartSupport
	window        int
	resident      bool
	reason        string
	scans         int
	failure       string
	terminalFirst bool
}

func TestNonTextInitialAttachmentWaitsForObservedFacts(t *testing.T) {
	for _, registration := range []string{"explicit", "inherited threshold"} {
		for _, supported := range []bool{false, true} {
			name := registration + "/zero support"
			tc := residencyBoundaryCase{registration: registration, window: 100000, resident: true, reason: "provider"}
			if supported {
				name = registration + "/supporting"
				tc.support = model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 100}
				tc.resident, tc.reason, tc.scans = false, "", 1
			}
			t.Run(name, func(t *testing.T) {
				runResidencyBoundary(t, tc)
			})
		}
	}
}

func TestNonTextDiagnosticInterceptionPreservesCanonicalResidency(t *testing.T) {
	for _, interception := range []string{"drop", "rewrite"} {
		for _, tc := range []residencyBoundaryCase{
			{window: 100000, resident: true, reason: "provider"},
			{window: 6000, support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 100}, resident: true, reason: "too_large"},
			{window: 100000, support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 100}, resident: true, reason: "too_large", scans: 1},
		} {
			tc.interception = interception
			t.Run(interception+"/"+tc.reason+"/"+fmt.Sprint(tc.support.ImageFormats), func(t *testing.T) {
				runResidencyBoundary(t, tc)
			})
		}
	}
}

func TestNonTextFailedPlannerPreservesResidency(t *testing.T) {
	for _, interception := range []string{"drop", "rewrite"} {
		for _, failure := range []string{"before model", "after model"} {
			for _, tc := range []residencyBoundaryCase{
				{window: 100000, resident: true, reason: "provider"},
				{window: 6000, support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 100}, resident: true, reason: "too_large"},
				{window: 100000, support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 100}, resident: true, reason: "too_large", scans: 1},
			} {
				tc.interception, tc.failure = interception, failure
				t.Run(interception+"/"+failure+"/"+tc.reason+"/"+fmt.Sprint(tc.support.ImageFormats), func(t *testing.T) {
					runResidencyBoundary(t, tc)
				})
			}
		}
	}
}

func TestNonTextTerminalFirstStepDoesNotInventDecision(t *testing.T) {
	runResidencyBoundary(t, residencyBoundaryCase{window: 100000, terminalFirst: true})
}

// runResidencyBoundary drives the actual first model observation, tool turn,
// next planner and seal. The provider is fake; stores, recording and hooks are real.
func runResidencyBoundary(t *testing.T, tc residencyBoundaryCase) *runrecord.RunRecord {
	t.Helper()
	eng := &contentBatchEngine{Engine: engineinmem.New()}
	plannerFailure := errors.New("terminal planner failure")
	var diagnostics atomic.Int32
	rt := New(WithEngine(eng), WithContextQualification(Qualification{Config: "qualified-test", Models: []string{"measured"}, Tier: compaction.Evict}), WithInterceptors(RuntimeInterceptorFuncs{
		BeforeEventFunc: func(_ context.Context, in *BeforeEventInput) (*BeforeEventDecision, error) {
			assert.NotEqual(t, hooks.ContextResidencyAdopted, in.Event.Type(), "protected adoption bypasses ordinary interception")
			if in.Event.Type() != hooks.ContextNonTextResident {
				return nil, nil
			}
			diagnostics.Add(1)
			if tc.interception == "error" {
				return nil, plannerFailure
			}
			diagnostic := in.Event.(*hooks.ContextDiagnosticEvent)
			assert.Equal(t, tc.reason, diagnostic.Reason)
			assert.Equal(t, "measured", diagnostic.Model)
			if tc.interception == "drop" {
				return &BeforeEventDecision{Drop: true}, nil
			}
			if tc.interception == "rewrite" {
				rewritten := *diagnostic
				rewritten.Reason, rewritten.Model = "application explanation", "application label"
				payload, err := json.Marshal(&rewritten)
				if err != nil {
					return nil, err
				}
				return &BeforeEventDecision{Event: &rewritten, Payload: payload}, nil
			}
			return nil, nil
		},
	}))
	if tc.interception == "append failure" {
		rt.RunEventStore = residencyRejectedLog{Store: rt.RunEventStore, err: plannerFailure}
	}
	provider := &residencyFactsClient{defaultFactsClient: &defaultFactsClient{recordingFactsClient: &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: tc.window}, resolved: "measured"}}, support: tc.support}
	require.NoError(t, rt.RegisterModel("fake", provider))
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{newAnyJSONSpec("test.next", "test")}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}}), nil
	}}))
	invoke := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) error {
		client, ok := agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: messages})
		return err
	}
	reg := contextRegistration(rt)
	reg.Planner = &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		assert.Zero(t, diagnostics.Load(), "unobserved support cannot establish residency")
		assert.Zero(t, residencyScanCount(eng), "no compatibility request without recorded call identities")
		if err := invoke(ctx, in.Agent, in.Messages); err != nil {
			return nil, err
		}
		if tc.terminalFirst {
			return oracleFinal(), nil
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "test.next", ToolCallID: "next", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		if tc.resident {
			assert.EqualValues(t, 1, diagnostics.Load(), "residency is decided before the affected planner")
		} else {
			assert.Zero(t, diagnostics.Load())
		}
		assert.Equal(t, tc.scans, residencyScanCount(eng))
		if tc.failure == "before model" {
			return nil, plannerFailure
		}
		if err := invoke(ctx, in.Agent, in.Messages); err != nil {
			return nil, err
		}
		if tc.failure == "after model" {
			return nil, plannerFailure
		}
		return oracleFinal(), nil
	}}
	if tc.registration == "explicit" {
		reg.Policy.ContextCompaction = &ContextCompaction{KeepNonTextResident: true}
	}
	require.NoError(t, rt.RegisterAgent(t.Context(), reg))
	_, err := rt.CreateSession(t.Context(), "session")
	require.NoError(t, err)
	options := []RunOption{WithRunID("boundary")}
	if tc.registration == "inherited threshold" {
		options = append(options, WithContextOverrides(ContextOverrides{Threshold: new(true)}))
	}
	_, err = rt.MustClient(reg.ID).Run(t.Context(), "session", []*model.Message{{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}}}, options...)
	if tc.failure != "" || tc.interception == "error" || tc.interception == "append failure" {
		require.ErrorIs(t, err, plannerFailure)
	} else {
		require.NoError(t, err)
	}
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "boundary", "boundary")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	assert.True(t, record.Runs[0].Sealed)
	assert.Equal(t, tc.resident, record.Runs[0].NonTextResident)
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	assert.Equal(t, tc.resident, restored.Runs[0].NonTextResident)
	// Exported projections cannot override canonical evidence, even without a diagnostic.
	restored.Runs[0].NonTextResident = !tc.resident
	tampered, err := restored.Export().Marshal()
	require.NoError(t, err)
	_, err = runrecord.FromExport(tampered)
	require.ErrorContains(t, err, "non-text residency differs")
	head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	if tc.failure != "" || tc.interception == "error" || tc.interception == "append failure" || tc.resident && tc.registration == "" {
		assert.Nil(t, head)
	} else {
		require.NotNil(t, head)
		assert.Equal(t, "boundary", head.ID)
	}
	for _, event := range record.Runs[0].Events {
		if event.Type != hooks.ContextNonTextResident {
			continue
		}
		assert.NotEqual(t, "drop", tc.interception)
		if tc.interception == "rewrite" {
			var diagnostic hooks.ContextDiagnosticEvent
			require.NoError(t, json.Unmarshal(event.Payload, &diagnostic))
			assert.Equal(t, "application explanation", diagnostic.Reason)
		}
	}
	return record
}

func residencyScanCount(eng *contentBatchEngine) int {
	eng.mu.Lock()
	defer eng.mu.Unlock()
	count := 0
	for _, input := range eng.records {
		if input.Compatibility != nil {
			count++
		}
	}
	return count
}

type residencyRejectedLog struct {
	runlog.Store
	err error
}

func (s residencyRejectedLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	if event.Type == hooks.ContextResidencyAdopted {
		return runlog.AppendResult{}, s.err
	}
	return s.Store.Append(ctx, event)
}

func TestNonTextResidencyCommitPrecedesDiagnosticFailure(t *testing.T) {
	t.Run("diagnostic fails after adoption", func(t *testing.T) {
		runResidencyBoundary(t, residencyBoundaryCase{window: 100000, resident: true, reason: "provider", interception: "error"})
	})
	t.Run("failed append is not adoption", func(t *testing.T) {
		runResidencyBoundary(t, residencyBoundaryCase{window: 100000, interception: "append failure"})
	})
}
