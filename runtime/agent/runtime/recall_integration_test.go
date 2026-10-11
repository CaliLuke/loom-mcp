package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecallRegistrationNameCheck(t *testing.T) {
	for _, name := range []tools.Ident{"svc.recall_context", "svc.recall.context"} {
		rt := New(WithContextCompaction("agent", ContextCompaction{}))
		err := rt.RegisterAgent(t.Context(), AgentRegistration{ID: "agent", Specs: []tools.ToolSpec{newAnyJSONSpec(name, "svc")}})
		assert.ErrorContains(t, err, "reserved name")
	}
}

func TestRecallWorkflowAllowancesEventsAndReconstruction(t *testing.T) {
	config := ContextCompaction{KeepRecentTokens: 10, ProtectRecentToolTokens: 10, MinSavingTokens: 1, RecallDiscoveryCalls: 2, RecallReadCalls: 1}
	rt := New(WithEngine(inmem.New()), WithContextCompaction("content.agent", config))
	grow := newAnyJSONSpec("test.grow", "test")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{grow}, Inline: true, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		result := "tick"
		if strings.Contains(string(call.Payload), "grow") {
			result = strings.Repeat("growth ", 7000)
		}
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result}), nil
	}}))
	provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 6000}, resolved: "measured"}
	require.NoError(t, rt.RegisterModel("fake", provider))
	step := 0
	var advertised []bool
	decide := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
		step++
		client, ok := agent.ModelClient("fake")
		require.True(t, ok)
		request := &model.Request{Model: "fake", Messages: messages}
		_, err := client.Complete(ctx, request)
		if err != nil {
			return nil, err
		}
		found := false
		for _, tool := range request.Tools {
			found = found || tool.Name == tools.RecallContext.String()
		}
		advertised = append(advertised, found)
		lookup := func(id, payload string) planner.ToolRequest {
			call := planner.ToolRequest{Name: tools.RecallContext, ToolCallID: id, Payload: rawjson.Message(payload)}
			if id == "read-exhausted" || id == "list-rejected" {
				call.Error = &planner.ToolError{Message: "request construction failed"}
			}
			return call
		}
		switch step {
		case 1:
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: grow.Name, ToolCallID: "tick", Payload: rawjson.Message(`{}`)}}}, nil
		case 2:
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{lookup("read-ok", `{"read":{"id":"1","part":"result","offset":0}}`), lookup("read-rejected", `{"read":{"id":"missing","part":"result","offset":0}}`), lookup("read-exhausted", `{"read":null}`), lookup("read-again", `{"read":null}`)}}, nil
		case 3:
			var text strings.Builder
			for _, message := range messages {
				for _, part := range message.Parts {
					if part, ok := part.(model.TextPart); ok {
						text.WriteString(part.Text)
					}
				}
			}
			handle := regexp.MustCompile(`rc-[0-9a-f]{8}`).FindString(text.String())
			require.NotEmpty(t, handle)
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{lookup("list-ok", `{"list":{"handle":"`+handle+`"}}`), lookup("list-rejected", `{"list":{"handle":"rc-deadbeef"}}`), lookup("find-malformed", `{"find":null}`), lookup("list-exhausted", `{"list":null}`), {Name: grow.Name, ToolCallID: "grow", Payload: rawjson.Message(`{"grow":true}`)}}}, nil
		case 4:
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{lookup("read-refilled", `{"read":{"id":"1","part":"result","offset":0}}`), lookup("read-next-episode", `{"read":null}`), lookup("list-refilled", `{"list":{"from":1,"through":1}}`)}}, nil
		default:
			return oracleFinal(), nil
		}
	}
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		return decide(ctx, input.Agent, input.Messages)
	}, resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return decide(ctx, input.Agent, input.Messages)
	}})
	var seed []*model.Message
	for i := 0; i < 30; i++ {
		seed = append(seed, contentMessage(fmt.Sprintf("user %d %s", i, strings.Repeat("seed ", 200))), &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "assistant"}}})
	}
	seed = append(seed, contentMessage("continue"))
	out, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("recall-run"))
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true, true, true, true}, advertised, "first shortened step advertises and stays advertised")
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "recall-run", "recall-run")
	require.NoError(t, err)
	rr := record.Runs[0]
	require.NotNil(t, rr.RecoverySettings)
	assert.False(t, rr.RecoverySettings.DisableRecall)
	var recalled []hooks.ContextRecalledEvent
	episodes := map[string]int{}
	for _, event := range rr.Events {
		if event.Type == hooks.ContextRecalled {
			var call hooks.ContextRecalledEvent
			require.NoError(t, json.Unmarshal(event.Payload, &call))
			recalled = append(recalled, call)
		}
		if event.Type == hooks.RecallBudgetExhausted {
			var exhausted hooks.RecallBudgetExhaustedEvent
			require.NoError(t, json.Unmarshal(event.Payload, &exhausted))
			episodes[exhausted.Allowance]++
		}
	}
	require.Len(t, recalled, 11)
	assert.Equal(t, map[string]int{"reads": 2, "discovery": 1}, episodes)
	outcomes := map[string]int{}
	for _, call := range recalled {
		outcomes[call.Outcome]++
	}
	assert.Equal(t, map[string]int{"ok": 4, "rejected": 3, "exhausted": 4}, outcomes)
	assert.Equal(t, recalled[len(recalled)-1].RemainingReads, rr.Recall.Reads)
	assert.Equal(t, recalled[len(recalled)-1].RemainingDiscovery, rr.Recall.Discovery)
	for _, call := range recalled {
		if call.Kind == recall.List || call.Kind == recall.Find {
			assert.Equal(t, 0, call.RemainingReads, "discovery calls do not consume reads before the refill")
		}
	}
	results, err := LoadRunToolEvents(t.Context(), rt.RunStore, "recall-run", out.ToolEventsRef)
	require.NoError(t, err)
	for _, result := range results {
		if !strings.Contains(result.ToolCallID, "exhausted") && result.ToolCallID != "read-next-episode" {
			continue
		}
		data, err := rt.RunStore.Get(t.Context(), "recall-run", result.Refs.Result)
		require.NoError(t, err)
		var payload struct {
			Code      recall.Code `json:"code"`
			Allowance string      `json:"allowance"`
			Other     int         `json:"other_remaining"`
			Refills   string      `json:"refills_at"`
		}
		require.NoError(t, json.Unmarshal(data, &payload))
		assert.Equal(t, recall.CodeBudgetExhausted, payload.Code)
		assert.Equal(t, "next compaction", payload.Refills)
		if result.ToolCallID == "read-exhausted" {
			assert.Equal(t, "reads", payload.Allowance)
			assert.Equal(t, 3, payload.Other)
		}
		if result.ToolCallID == "list-exhausted" {
			assert.Equal(t, "discovery", payload.Allowance)
			assert.Zero(t, payload.Other)
		}
	}
	refilled := recalled[len(recalled)-3]
	assert.Equal(t, "ok", refilled.Outcome, "WitnessReadAfterRefill")
}

func TestRecallDisabledAndUnsupportedWorkflow(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled_%v", disabled), func(t *testing.T) {
			rt := New(WithEngine(inmem.New()), WithContextCompaction("content.agent", ContextCompaction{KeepRecentTokens: 10, ProtectRecentToolTokens: 10, MinSavingTokens: 1}), WithContextRecovery("content.agent", ContextRecovery{DisableRecall: disabled}))
			grow := newAnyJSONSpec("test.tick", "test")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{grow}, Inline: true, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "tick"}), nil
			}}))
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 6000}, resolved: "measured"}
			require.NoError(t, rt.RegisterModel("fake", provider))
			step := 0
			decide := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
				step++
				client, ok := agent.ModelClient("fake")
				require.True(t, ok)
				request := &model.Request{Model: "fake", Messages: messages}
				_, err := client.Complete(ctx, request)
				if err != nil {
					return nil, err
				}
				for _, tool := range request.Tools {
					if disabled {
						assert.NotEqual(t, tools.RecallContext.String(), tool.Name)
					}
				}
				switch step {
				case 1:
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: grow.Name, ToolCallID: "tick", Payload: rawjson.Message(`{}`)}}}, nil
				case 2:
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tools.RecallContext, ToolCallID: "lookup", Payload: rawjson.Message(`{"read":{"id":"1","part":"result","offset":0}}`)}}}, nil
				default:
					return oracleFinal(), nil
				}
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				return decide(ctx, in.Agent, in.Messages)
			}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return decide(ctx, in.Agent, in.Messages)
			}})
			seed := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}, model.TextPart{Text: strings.Repeat("original user ", 2000)}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "earlier answer"}}}, contentMessage("continue")}
			out, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("disabled-run"))
			require.NoError(t, err)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, out.RunID, out.RunID)
			require.NoError(t, err)
			rr := record.Runs[0]
			disabledEvents, recalled := 0, 0
			for _, event := range rr.Events {
				switch string(event.Type) {
				case string(hooks.ContextRecallDisabled):
					disabledEvents++
				case string(hooks.ContextRecalled):
					recalled++
					var call hooks.ContextRecalledEvent
					require.NoError(t, json.Unmarshal(event.Payload, &call))
					assert.Equal(t, "unsupported", call.Outcome)
					assert.Equal(t, recall.CodeUnsupportedPart, call.Code)
				}
			}
			if disabled {
				assert.Equal(t, 1, disabledEvents)
				assert.Zero(t, recalled)
				results, err := LoadRunToolEvents(t.Context(), rt.RunStore, out.RunID, out.ToolEventsRef)
				require.NoError(t, err)
				require.Len(t, results, 2)
				assert.Equal(t, tools.ToolUnavailable, results[1].Name)
			} else {
				assert.Zero(t, disabledEvents)
				assert.Equal(t, 1, recalled)
			}
			require.NotNil(t, rr.RecoverySettings)
			assert.Equal(t, disabled, rr.RecoverySettings.DisableRecall)
		})
	}
}

func TestRecallRegistrationFallbackCatalog(t *testing.T) {
	bad := newAnyJSONSpec("svc.recall_context", "svc")
	rt := New(WithContextCompaction("content.agent", ContextCompaction{}))
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "svc", Specs: []tools.ToolSpec{bad}, Inline: true, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return nil, nil
	}}))
	require.ErrorContains(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "content.agent"}), "reserved name")
	rt = New(WithContextCompaction("content.agent", ContextCompaction{}))
	registerContentAgent(t, rt, &stubPlanner{})
	assert.ErrorContains(t, rt.RegisterToolset(ToolsetRegistration{Name: "svc", Specs: []tools.ToolSpec{bad}, Inline: true, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return nil, nil
	}}), "reserved name")
}
