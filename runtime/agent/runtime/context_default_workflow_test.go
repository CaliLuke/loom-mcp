package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultFactsClient struct{ *recordingFactsClient }

func (*defaultFactsClient) ProviderConfiguration(context.Context, *model.Request) string {
	return "qualified-test"
}

func TestContextDefaultQualifiedViewsMatchExplicit(t *testing.T) {
	requests := make(map[string][][]byte)
	for _, variant := range []string{"explicit", "qualified", "unqualified", "off"} {
		t.Run(variant, func(t *testing.T) {
			rt := New(WithContextQualification(compaction.Qualification{Config: "qualified-test", Models: []string{"measured"}, Tier: compaction.Evict}))
			base := compactionFixture(t, rt.RunStore, 30)
			base.AgentID = "agent"
			base.RunContext = run.Context{RootRunID: "root", RunID: "run", Attempt: 1}
			settings := compaction.Settings{Default: true, KeepNonTextResident: true, KeepRecentTokens: 30, ProtectRecentToolTokens: 40, MinSavingTokens: 20, MaxTier: compaction.Evict, TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}}
			if variant == "explicit" {
				settings.Default = false
				settings.Threshold = true
				settings.KeepNonTextResident = false
				base.compaction = &ContextCompaction{}
			}
			if variant == "off" {
				settings.Default = false
			}
			base.contextSettings = settings
			provider := &defaultFactsClient{recordingFactsClient: &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 6000}, resolved: "measured"}}
			observed := &observedDefaultClient{recordingFactsClient: provider.recordingFactsClient}
			caps := model.Capabilities{Info: provider}
			if variant != "unqualified" {
				caps.ProviderConfiguration = provider
			}
			client := model.WithCapabilitySet(observed, caps)
			require.NoError(t, rt.RegisterModel("fake", client))
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
				client, ok := input.Agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: input.Messages})
				if err != nil {
					return nil, err
				}
				return assistantCommitResult(), nil
			}}}
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
			for range 3 {
				input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
				out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{})
				require.NoError(t, err)
				base.adopt(out)
			}
			requests[variant] = observed.requests
			if variant == "unqualified" {
				for _, request := range observed.requests {
					assert.NotContains(t, string(request), "recall_context")
				}
				page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
				require.NoError(t, err)
				for _, event := range page.Events {
					assert.NotEqual(t, hooks.ContextCompacted, event.Type)
					assert.NotEqual(t, hooks.ContextCompactionSkipped, event.Type)
				}
			}
			if variant == "qualified" {
				assert.Equal(t, compaction.Evict, compaction.ObservedQualifiedTier(base.compactionState))
			}
		})
	}
	assert.Equal(t, requests["explicit"], requests["qualified"])
	assert.Equal(t, requests["off"], requests["unqualified"])
	require.Len(t, requests["qualified"], 3)
	assert.Less(t, len(requests["qualified"][1]), len(requests["qualified"][0]))
	assert.Equal(t, requests["unqualified"][0], requests["unqualified"][1])
}

type observedDefaultClient struct {
	*recordingFactsClient
	requests [][]byte
}

func (c *observedDefaultClient) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	data, err := json.Marshal(request, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	c.requests = append(c.requests, data)
	return c.recordingFactsClient.Complete(ctx, request)
}
