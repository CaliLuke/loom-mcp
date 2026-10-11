package runtime

import (
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type overflowProvider struct {
	window, realWindow int
	resolved           string
	calls              int
	usage              bool
	failFirst          bool
	sizes              []int
}

type overflowCounter struct {
	*overflowProvider
	afterSend bool
}

func (p *overflowProvider) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	facts, err := contextmeasure.Measure(req, model.ContextLimits{}, nil)
	if err != nil {
		return nil, err
	}
	size := overflow.Size(facts)
	p.calls++
	p.sizes = append(p.sizes, size)
	if size > p.realWindow || (p.failFirst && p.calls == 1) {
		return nil, model.NewProviderError("fake", "complete", 400, model.ProviderErrorKindContextOverflow, "context_length_exceeded", "request too large", "request", false, nil)
	}
	response := &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}
	if p.usage {
		response.Usage = model.TokenUsage{InputTokens: size, TotalTokens: size}
	}
	return response, nil
}

func (*overflowProvider) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (p *overflowProvider) ContextLimits(context.Context, *model.Request) (model.ContextLimits, string, bool) {
	resolved := p.resolved
	if resolved == "" {
		resolved = "small"
	}
	return model.ContextLimits{ContextWindow: p.window}, resolved, p.window > 0
}

func (p *overflowCounter) CountTokens(_ context.Context, req *model.Request) (model.TokenCount, error) {
	facts, err := contextmeasure.Measure(req, model.ContextLimits{}, nil)
	if err != nil {
		return model.TokenCount{}, err
	}
	return model.TokenCount{InputTokens: overflow.Size(facts), Exact: !p.afterSend || p.calls > 0}, nil
}

func TestOverflowInMemoryEngine(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                                  string
		unknown, exact, afterSend, rewrite, unrelated, single, multiple, overhead, modelSwitch, historyPolicy bool
		window, realWindow                                                                                    int
		failure                                                                                               error
		first, second                                                                                         string
	}{
		{name: "history policy loaded view", historyPolicy: true, window: 8000, realWindow: 8000, first: "provider"},
		{name: "uncalibrated no usage", window: 8000, realWindow: 8000, first: "provider"},
		{name: "unknown limits", unknown: true, realWindow: 8000, first: "provider"},
		{name: "exact before calibration", exact: true, window: 8000, realWindow: 8000, first: "guard"},
		{name: "guard cannot satisfy advisory budget", exact: true, single: true, window: 8000, realWindow: 60000, first: "guard"},
		{name: "model switch", multiple: true, modelSwitch: true, window: 8000, realWindow: 8000, first: "provider"},
		{name: "later call overflows all keys adopted", multiple: true, window: 8000, realWindow: 8000, first: "provider"},
		{name: "rewriting planner", rewrite: true, window: 8000, realWindow: 8000, failure: ErrContextOverflowNoProgress, first: "provider", second: "no_progress"},
		{name: "not view bearing", unrelated: true, window: 8000, realWindow: 8000, failure: ErrContextOverflowNoProgress, first: "provider"},
		{name: "minimal view", single: true, window: 8000, realWindow: 8000, failure: ErrContextBudgetExceeded, first: "provider"},
		{name: "guard after provider then used guard sends", exact: true, afterSend: true, overhead: true, window: 8000, realWindow: 100000, first: "provider", second: "guard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithEngine(inmem.New()))
			provider := &overflowProvider{window: tc.window, realWindow: tc.realWindow, failFirst: tc.afterSend}
			var client model.Client = provider
			if tc.exact {
				client = &overflowCounter{overflowProvider: provider, afterSend: tc.afterSend}
			}
			require.NoError(t, rt.RegisterModel("fake", client))
			if tc.modelSwitch {
				require.NoError(t, rt.RegisterModel("large", &overflowProvider{window: 120000, realWindow: 120000, usage: true, resolved: "large"}))
			}
			var original []*model.Message
			decide := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
				if original == nil {
					original = cloneMessages(messages)
				}
				if tc.rewrite {
					messages = original
				}
				if tc.unrelated {
					messages = []*model.Message{contentMessage(strings.Repeat("other", 15000))}
				}
				if tc.overhead {
					messages = append([]*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: strings.Repeat("fixed", 10000)}}}}, messages...)
				}
				client, ok := agent.ModelClient("fake")
				require.True(t, ok)
				if tc.multiple {
					auxiliary := []*model.Message{contentMessage("small auxiliary")}
					auxiliaryClient := client
					if tc.modelSwitch {
						auxiliaryClient, ok = agent.ModelClient("large")
						require.True(t, ok)
						auxiliary = messages
					}
					_, err := auxiliaryClient.Complete(ctx, &model.Request{Messages: auxiliary})
					if err != nil {
						return nil, err
					}
				}
				_, err := client.Complete(ctx, &model.Request{Messages: messages})
				if err != nil {
					return nil, err
				}
				return oracleFinal(), nil
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
				return decide(ctx, input.Agent, input.Messages)
			}, resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return decide(ctx, input.Agent, input.Messages)
			}})
			historyCalls := 0
			if tc.historyPolicy {
				registration := rt.agents["content.agent"]
				registration.Policy.History = func(_ context.Context, messages []*model.Message, _ []*model.ToolDefinition) ([]*model.Message, error) {
					historyCalls++
					return messages[:len(messages)-1], nil
				}
				rt.agents["content.agent"] = registration
			}
			seed := []*model.Message{contentMessage(strings.Repeat("history", 9000))}
			if !tc.single {
				seed = append(seed, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "earlier response"}}}, contentMessage("continue"))
			}
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("overflow-run"))
			runErr := err
			if tc.historyPolicy {
				assert.GreaterOrEqual(t, historyCalls, 2)
			}
			if tc.failure != nil {
				require.ErrorIs(t, err, tc.failure)
			} else {
				require.NoError(t, err)
			}
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "overflow-run", "overflow-run")
			require.NoError(t, err)
			rr := record.Runs[0]
			require.NotNil(t, rr.ContextSettings)
			assert.False(t, rr.ContextSettings.Threshold)
			assert.Equal(t, contextmeasure.Rate{Tokens: 1, Bytes: 10}, rr.ContextSettings.GuardMargin)
			var overflows []hooks.ContextOverflowEvent
			for _, event := range rr.Events {
				if event.Type == hooks.ContextOverflow {
					var overflow hooks.ContextOverflowEvent
					require.NoError(t, json.Unmarshal(event.Payload, &overflow, jsonopts.FormatDurationAsNano(true)))
					overflows = append(overflows, overflow)
				}
			}
			require.NotEmpty(t, overflows)
			assert.Equal(t, tc.first, overflows[0].Source)
			if tc.second != "" {
				require.GreaterOrEqual(t, len(overflows), 2)
				assert.Equal(t, tc.second, overflows[1].Source)
			}
			for _, call := range rr.Calls {
				assert.Equal(t, runrecord.Adopted, call.Class)
			}
			if tc.multiple {
				assert.Len(t, overflows[0].CallKeys, 2)
				if tc.modelSwitch {
					assert.Equal(t, 1, overflows[0].Failure.Ordinal)
					assert.Equal(t, "small", overflows[0].Failure.Failed.Model)
					assert.Equal(t, "small", rr.Calls[1].Completion.Facts.Model)
				} else {
					assert.Equal(t, 2, overflows[0].Failure.Ordinal)
				}
			}
			if tc.rewrite {
				var typed *ContextOverflowError
				require.ErrorAs(t, runErr, &typed)
				require.Len(t, typed.Calls, 2)
				assert.Equal(t, []runstore.CallKey{rr.Calls[0].Record.Key, rr.Calls[1].Record.Key}, typed.Calls)
				assert.Contains(t, runErr.Error(), fmt.Sprint(typed.Calls[0]))
				assert.Contains(t, runErr.Error(), fmt.Sprint(typed.Calls[1]))
				assert.Contains(t, overflows[1].Outcome, "no_progress")
				assert.Contains(t, rr.Calls[1].Completion.Error.Message, ErrContextOverflowNoProgress.Error())
				assert.Len(t, rr.Calls, 2)
				assert.Contains(t, rr.Calls[1].Completion.Error.Message, "no progress")
			}
			if tc.single && errors.Is(tc.failure, ErrContextBudgetExceeded) {
				var typed *ContextOverflowError
				require.ErrorAs(t, runErr, &typed)
				assert.Equal(t, index.MessageID(1), typed.FirstMessage)
				assert.Zero(t, typed.FixedTokens)
			}
			if tc.failure == nil {
				assert.LessOrEqual(t, provider.sizes[len(provider.sizes)-1], provider.realWindow)
			}
			for _, call := range rr.Calls {
				assert.NotEqual(t, runstore.CallStarted, call.Record.State)
			}
		})
	}
}
