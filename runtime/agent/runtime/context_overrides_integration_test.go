package runtime

import (
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextOverridePretendWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold bool
		tier      compaction.Tier
		compacted bool
	}{
		{"threshold", true, compaction.Evict, true}, {"baseline", false, "", false}, {"clear", true, compaction.Clear, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithContextCompaction("content.agent", ContextCompaction{KeepRecentTokens: 30}))
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 200000}, resolved: "real"}
			require.NoError(t, rt.RegisterModel("fake", provider))
			spec := newAnyJSONSpec("test.small", "test")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "result"}), nil
			}}))
			invoke := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) error {
				client, ok := agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Messages: messages, MaxTokens: 1000})
				return err
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				if err := invoke(ctx, in.Agent, in.Messages); err != nil {
					return nil, err
				}
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, nil
			}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				if err := invoke(ctx, in.Agent, in.Messages); err != nil {
					return nil, err
				}
				return oracleFinal(), nil
			}})
			overrides := ContextOverrides{ContextWindow: new(8000), Threshold: new(tc.threshold), RecallDiscoveryCalls: new(3), RecallReadCalls: new(3)}
			if tc.tier != "" {
				overrides.MaxTier = &tc.tier
			}
			seed := []*model.Message{contentMessage(strings.Repeat("x", 60000)), {Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}, contentMessage("go")}
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("root"), WithContextOverrides(overrides))
			require.NoError(t, err, "the 20k-token request must use the real 200k guard window")
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
			require.NoError(t, err)
			rr := record.Runs[0]
			wantRecall := 3
			if tc.compacted {
				wantRecall = 5
			}
			assert.Equal(t, wantRecall, rr.Recall.Discovery)
			assert.Equal(t, wantRecall, rr.Recall.Reads)
			require.NotEmpty(t, rr.ContextFingerprint)
			for _, call := range rr.Calls {
				assert.Equal(t, rr.ContextFingerprint, call.Started.Context)
				assert.Equal(t, 200000, call.Completion.Facts.ContextWindow)
			}
			assert.Equal(t, tc.compacted, len(rr.Checkpoints) > 0)
			for _, cp := range rr.Checkpoints {
				assert.Equal(t, rr.ContextFingerprint, string(cp.Record.Fingerprint))
			}
			page, err := rt.RunEventStore.List(t.Context(), "root", "", 100)
			require.NoError(t, err)
			found := false
			for _, event := range page.Events {
				if event.Type == hooks.ContextCompacted {
					var compact hooks.ContextCompactionEvent
					require.NoError(t, json.Unmarshal(event.Payload, &compact))
					assert.Equal(t, "threshold", compact.Reason)
					assert.Equal(t, 8000, compact.EffectiveWindow)
					assert.Equal(t, rr.ContextFingerprint, compact.Fingerprint)
					found = true
				}
			}
			assert.Equal(t, tc.compacted, found)
		})
	}
}

func TestContextPretendProviderOverflow(t *testing.T) {
	rt := New()
	provider := &overflowProvider{window: 200000, realWindow: 200000, failFirst: true}
	require.NoError(t, rt.RegisterModel("fake", provider))
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: in.Messages, MaxTokens: 1000})
		if err != nil {
			return nil, err
		}
		return oracleFinal(), nil
	}})
	seed := []*model.Message{contentMessage(strings.Repeat("x", 60000)), {Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}, contentMessage("go")}
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("root"), WithContextOverrides(ContextOverrides{ContextWindow: new(8000)}))
	require.NoError(t, err)
	assert.Equal(t, 2, provider.calls)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
	require.NoError(t, err)
	found := false
	for _, event := range record.Runs[0].Events {
		if event.Type != hooks.ContextOverflow {
			continue
		}
		var overflow hooks.ContextOverflowEvent
		require.NoError(t, json.Unmarshal(event.Payload, &overflow, jsonopts.FormatDurationAsNano(true)))
		require.NotNil(t, overflow.Recomputed)
		assert.Equal(t, 200000, overflow.Failure.Failed.ContextWindow)
		assert.Equal(t, compaction.RenderBudget(*record.Runs[0].ContextSettings, 7000, 0), overflow.Recomputed.View.Budget)
		found = true
	}
	assert.True(t, found)
}
