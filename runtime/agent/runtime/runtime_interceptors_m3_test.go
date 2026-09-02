package runtime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type interceptableModelClient struct {
	calls *[]string
}

func (c interceptableModelClient) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	*c.calls = append(*c.calls, "model")
	return &model.Response{
		Content: []model.Message{{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.TextPart{Text: "model:" + req.Model},
			},
		}},
		StopReason: "end_turn",
	}, nil
}

func (c interceptableModelClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return emptyModelStream{}, nil
}

type emptyModelStream struct{}

func (emptyModelStream) Recv() (model.Chunk, error) { return model.Chunk{}, io.EOF }
func (emptyModelStream) Close() error               { return nil }
func (emptyModelStream) Metadata() map[string]any   { return nil }
func (emptyModelStream) Response() *model.Response  { return nil }
func (emptyModelStream) Finalize(primaryErr error) error {
	return primaryErr
}

type closeCountingModelClient struct {
	stream *closeCountingModelStream
}

func (c closeCountingModelClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, errors.New("complete unsupported")
}

func (c closeCountingModelClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return c.stream, nil
}

type closeCountingModelStream struct {
	closeCount int
}

func (s *closeCountingModelStream) Recv() (model.Chunk, error) { return model.Chunk{}, io.EOF }
func (s *closeCountingModelStream) Close() error {
	s.closeCount++
	return nil
}
func (s *closeCountingModelStream) Metadata() map[string]any  { return nil }
func (s *closeCountingModelStream) Response() *model.Response { return nil }
func (s *closeCountingModelStream) Finalize(primaryErr error) error {
	return errors.Join(primaryErr, s.Close())
}

type contractModelClient struct {
	completeResp *model.Response
	completeErr  error
	streamer     model.ValidatedStreamer
	streamErr    error
}

type finalResponseModelPlanner struct{}

type caughtValidationPlanner struct{}

func (finalResponseModelPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("test-model")
	if !ok {
		return nil, errors.New("test model not registered")
	}
	response, err := client.Complete(ctx, &model.Request{RunID: input.RunContext.RunID})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &response.Content[0]}}, nil
}

func (finalResponseModelPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func (caughtValidationPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("test-model")
	if !ok {
		return nil, errors.New("test model not registered")
	}
	if _, err := client.Complete(ctx, &model.Request{RunID: input.RunContext.RunID}); err == nil {
		return nil, errors.New("expected rejected model response")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "planner recovered"}},
	}}}, nil
}

func (caughtValidationPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func (c contractModelClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return c.completeResp, c.completeErr
}

func (c contractModelClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return c.streamer, c.streamErr
}

func TestModelInterceptorsMutateRequestAndResponseOnce(t *testing.T) {
	var calls []string
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
			calls = append(calls, "before-model")
			require.Equal(t, agent.Ident("svc.agent"), input.AgentID)
			req := *input.Request
			req.Model = "rewritten"
			return &BeforeModelDecision{Request: &req}, nil
		},
		AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
			calls = append(calls, "after-model")
			require.NoError(t, input.Err)
			input.Response.Content[0].Parts = []model.Part{model.TextPart{Text: "after"}}
			return &AfterModelDecision{Response: input.Response}, nil
		},
	}))
	rt.models["default"] = interceptableModelClient{calls: &calls}

	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		events:  nil,
		cache:   CachePolicy{},
		turnID:  "turn-1",
		labels:  map[string]string{"tenant": "acme"},
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "original"})
	require.NoError(t, err)
	require.Equal(t, []string{"before-model", "model", "after-model"}, calls)
	require.Equal(t, "after", resp.Content[0].Parts[0].(model.TextPart).Text)
}

func TestEventInterceptorCanDropBeforeRunlogStreamAndBus(t *testing.T) {
	rl := &recordingRunlog{}
	bus := hooks.NewBus()
	store := sessioninmem.New()
	rt := New(
		WithRunEventStore(rl),
		WithHooks(bus),
		WithSessionStore(store),
		WithInterceptors(RuntimeInterceptorFuncs{
			BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				require.Equal(t, hooks.PlannerNote, input.Event.Type())
				return &BeforeEventDecision{Drop: true}, nil
			},
		}),
	)

	var published hooks.Event
	sub, err := bus.Register(hooks.SubscriberFunc(func(_ context.Context, evt hooks.Event) error {
		published = evt
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() })

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}))

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "note", nil), "turn-1")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(context.Background(), input))
	require.Empty(t, rl.events)
	require.Nil(t, published)
}

func TestEventInterceptorReplacementPersistsMutatedEventTypeEverywhere(t *testing.T) {
	rl := &recordingRunlog{}
	bus := hooks.NewBus()
	store := sessioninmem.New()
	sink := &recordingStreamSink{}
	replacement := hooks.NewAssistantMessageEvent("run-1", "svc.agent", "sess-1", "mutated assistant", nil)
	replacementInput, err := hooks.EncodeToHookInput(replacement, "turn-1")
	require.NoError(t, err)
	rt := New(
		WithRunEventStore(rl),
		WithHooks(bus),
		WithSessionStore(store),
		WithStream(sink),
		WithInterceptors(RuntimeInterceptorFuncs{
			BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				require.Equal(t, hooks.PlannerNote, input.Event.Type())
				return &BeforeEventDecision{
					Event:   replacement,
					Payload: replacementInput.Payload.RawMessage(),
				}, nil
			},
		}),
	)

	var published hooks.Event
	sub, err := bus.Register(hooks.SubscriberFunc(func(_ context.Context, evt hooks.Event) error {
		published = evt
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() })

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}))

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "original note", nil), "turn-1")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(context.Background(), input))

	require.Len(t, rl.events, 1)
	require.Equal(t, hooks.AssistantMessage, rl.events[0].Type)
	require.NotNil(t, published)
	require.Equal(t, hooks.AssistantMessage, published.Type())
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 1)
	require.Equal(t, stream.EventAssistantReply, streamEvents[0].Type())
}

func TestModelInterceptorStreamShortCircuitResponseDoesNotReturnNilStreamer(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
			return &BeforeModelDecision{
				Response: &model.Response{
					Content: []model.Message{{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "short-circuited"}},
					}},
					StopReason: "end_turn",
				},
			}, nil
		},
	}))
	var calls []string
	rt.models["default"] = interceptableModelClient{calls: &calls}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model response short-circuit is unsupported for streaming")
	require.Nil(t, streamer)
	require.Empty(t, calls)
}

func TestModelInterceptorStreamAfterErrorClosesOpenedStreamer(t *testing.T) {
	afterErr := errors.New("after blocked")
	opened := &closeCountingModelStream{}
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, afterErr
		},
	}))
	rt.models["default"] = closeCountingModelClient{stream: opened}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, afterErr)
	require.Nil(t, streamer)
	require.Equal(t, 1, opened.closeCount)
}

func TestModelInterceptorAfterModelEmptyDecisionCannotClearModelError(t *testing.T) {
	modelErr := errors.New("provider failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{}, nil
		},
	}))
	rt.models["default"] = contractModelClient{completeErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, modelErr)
	require.Nil(t, resp)
}

func TestModelInterceptorAfterModelReplacementResponseClearsModelError(t *testing.T) {
	modelErr := errors.New("provider failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{
				Response: &model.Response{
					Content: []model.Message{{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "replacement"}},
					}},
					StopReason: "end_turn",
				},
			}, nil
		},
	}))
	rt.models["default"] = contractModelClient{completeErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "replacement", resp.Content[0].Parts[0].(model.TextPart).Text)
}

func TestPlanActivityKeepsValidAfterModelRecoveryReplacement(t *testing.T) {
	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolIdentity,
		errors.New("private invalid model output"),
		model.ResponseEvidence{Present: true, ByteCount: 64, Fingerprint: [32]byte{1}},
		nil,
	)
	require.NoError(t, err)
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
			require.ErrorIs(t, input.Err, rejected)
			return &AfterModelDecision{Response: &model.Response{
				Content: []model.Message{{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "repaired"}},
				}},
				StopReason: "end_turn",
			}}, nil
		},
	}))
	rt.models["test-model"] = contractModelClient{completeErr: rejected}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: finalResponseModelPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-after-model-repair",
		RunContext: run.Context{RunID: "run-after-model-repair"},
	})
	require.NoError(t, err)
	require.Nil(t, out.Recovery)
	require.NotNil(t, out.Result)
	require.Equal(t, "repaired", out.Result.FinalResponse.Message.Parts[0].(model.TextPart).Text)
}

func TestBeforeModelShortCircuitUsesEffectiveToolPolicyForRecovery(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
			require.Equal(t, &model.CacheOptions{AfterSystem: true, AfterTools: true}, input.Request.Cache)
			toolNames := make([]string, 0, len(input.Request.Tools))
			for _, definition := range input.Request.Tools {
				toolNames = append(toolNames, definition.Name)
			}
			require.ElementsMatch(t, []string{"allowed", tools.ToolUnavailable.String()}, toolNames)
			return &BeforeModelDecision{Response: &model.Response{
				ToolCalls:  []model.ToolCall{{Name: "blocked", Payload: []byte(`{}`)}},
				StopReason: "tool_use",
			}}, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	rt.agents["svc.agent"] = AgentRegistration{
		ID:      "svc.agent",
		Planner: &modelToolPolicyPlanner{},
		Policy: RunPolicy{Cache: CachePolicy{
			AfterSystem: true,
			AfterTools:  true,
		}},
	}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-before-model-policy",
		RunContext:       run.Context{RunID: "run-before-model-policy"},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"allowed"},
	})
	require.NoError(t, err)
	require.Nil(t, out.Result)
	require.NotNil(t, out.Recovery)
	require.Equal(t, model.OutputValidationToolIdentity, out.Recovery.Kind)
	require.Contains(t, out.Recovery.Correction, "allowed")
	require.NotContains(t, out.Recovery.Correction, "blocked")
}

func TestBeforeModelShortCircuitSeesToolFreeFinalAnswerRecovery(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
			require.Empty(t, input.Request.Tools)
			require.Nil(t, input.Request.ToolChoice)
			return &BeforeModelDecision{Response: &model.Response{
				Content: []model.Message{{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "replacement"}},
				}},
				StopReason: "end_turn",
			}}, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: &modelToolPolicyPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-before-model-tool-free",
		RunContext:       run.Context{RunID: "run-before-model-tool-free"},
		ToolPolicyActive: true,
		Recovery: &api.ModelRecovery{
			Kind:         model.OutputValidationStructuredOutput,
			Correction:   "replace the invalid final answer",
			DisableTools: true,
		},
	})
	require.NoError(t, err)
	require.Nil(t, out.Recovery)
	require.NotNil(t, out.Result)
}

func TestBeforeModelReplacementCannotBroadenEffectiveToolPolicy(t *testing.T) {
	tests := []struct {
		name            string
		allowed         []tools.Ident
		expectedCatalog []tools.Ident
	}{
		{name: "allowlist", allowed: []tools.Ident{"allowed"}, expectedCatalog: []tools.Ident{"allowed", tools.ToolUnavailable}},
		{name: "tool free", expectedCatalog: []tools.Ident{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{
				BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
					return &BeforeModelDecision{
						Request: &model.Request{
							Tools: []*model.ToolDefinition{
								{Name: "allowed", InputSchema: map[string]any{"type": "object"}},
								{Name: "blocked", InputSchema: map[string]any{"type": "object"}},
							},
							ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "blocked"},
						},
						Response: &model.Response{
							ToolCalls:  []model.ToolCall{{Name: "blocked", Payload: []byte(`{}`)}},
							StopReason: "tool_use",
						},
					}, nil
				},
			}))
			rt.models["default"] = contractModelClient{}
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: &modelToolPolicyPlanner{}}

			out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
				AgentID:          "svc.agent",
				RunID:            "run-interceptor-policy",
				RunContext:       run.Context{RunID: "run-interceptor-policy"},
				ToolPolicyActive: true,
				AllowedTools:     test.allowed,
			})
			require.NoError(t, err)
			require.NotNil(t, out.Recovery)
			require.Equal(t, test.expectedCatalog, out.Recovery.ToolCatalog)
			require.NotContains(t, out.Recovery.Correction, "blocked")
		})
	}
}

func TestPlanActivityKeepsPlannerResultAfterCaughtModelValidation(t *testing.T) {
	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolIdentity,
		errors.New("private invalid model output"),
		model.ResponseEvidence{Present: true, ByteCount: 64, Fingerprint: [32]byte{1}},
		nil,
	)
	require.NoError(t, err)
	rt := New()
	rt.models["test-model"] = contractModelClient{completeErr: rejected}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: caughtValidationPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-caught-validation",
		RunContext: run.Context{RunID: "run-caught-validation"},
	})
	require.NoError(t, err)
	require.Nil(t, out.Recovery)
	require.NotNil(t, out.Result)
	require.Equal(t, "planner recovered", out.Result.FinalResponse.Message.Parts[0].(model.TextPart).Text)
}

func TestModelInterceptorsCannotBypassRequestContract(t *testing.T) {
	request := &model.Request{Tools: []*model.ToolDefinition{{
		Name:        "advertised",
		InputSchema: map[string]any{"type": "object"},
	}}}
	injected := &model.Response{
		ToolCalls:  []model.ToolCall{{Name: tools.Ident("hidden"), Payload: []byte(`{}`)}},
		StopReason: "tool_use",
	}
	tests := []struct {
		name        string
		interceptor RuntimeInterceptorFuncs
		inner       model.Client
	}{
		{
			name: "before short circuit",
			interceptor: RuntimeInterceptorFuncs{BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
				return &BeforeModelDecision{Response: injected}, nil
			}},
			inner: contractModelClient{},
		},
		{
			name: "after replacement",
			interceptor: RuntimeInterceptorFuncs{AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
				input.Request.Tools = append(input.Request.Tools, &model.ToolDefinition{Name: "hidden", InputSchema: map[string]any{"type": "object"}})
				return &AfterModelDecision{Response: injected}, nil
			}},
			inner: contractModelClient{completeResp: &model.Response{
				Content:    []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "safe"}}}},
				StopReason: "end_turn",
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &modelRecoveryRecorder{}
			client := newModelInterceptedClient(test.inner, []Interceptor{test.interceptor}, "agent", "run", "session", "turn", "model", recorder, nil)
			_, err := client.Complete(context.Background(), request)
			require.Error(t, err)
			var validationErr *model.OutputValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, model.OutputValidationToolIdentity, validationErr.Kind())
			recovery, recoveryErr := recorder.recovery(err, 1)
			require.NoError(t, recoveryErr)
			require.Equal(t, model.OutputValidationToolIdentity, recovery.Kind)
			require.Contains(t, recovery.Correction, "advertised")
			require.NotContains(t, recovery.Correction, "hidden")
		})
	}
}

func TestAfterModelCannotMutateRecoverySchemaSnapshot(t *testing.T) {
	t.Parallel()

	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolArguments,
		errors.New("private invalid tool arguments"),
		model.ResponseEvidence{Present: true, ByteCount: 23},
		nil,
	)
	require.NoError(t, err)
	request := &model.Request{Tools: []*model.ToolDefinition{{
		Name: "orders.create",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account_id": map[string]any{"type": "string"},
			},
		},
	}}}
	interceptor := RuntimeInterceptorFuncs{AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
		properties := input.Request.Tools[0].InputSchema.(map[string]any)["properties"].(map[string]any)
		properties["private_after_hook"] = map[string]any{"type": "string"}
		return nil, nil
	}}
	recorder := &modelRecoveryRecorder{}
	client := newModelInterceptedClient(
		contractModelClient{completeErr: rejected},
		[]Interceptor{interceptor},
		"agent",
		"run",
		"session",
		"turn",
		"model",
		recorder,
		nil,
	)

	_, err = client.Complete(context.Background(), request)
	require.ErrorIs(t, err, rejected)
	recovery, recoveryErr := recorder.recovery(err, 1)
	require.NoError(t, recoveryErr)
	require.Contains(t, recovery.Correction, "account_id:string")
	require.NotContains(t, recovery.Correction, "private_after_hook")
}

func TestModelInterceptorAfterModelEnforcesNonNilResponseContract(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model complete contract violation")
	require.Nil(t, resp)
}

func TestModelInterceptorAfterModelStreamCannotClearModelError(t *testing.T) {
	modelErr := errors.New("stream failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{}, nil
		},
	}))
	rt.models["default"] = contractModelClient{streamErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, modelErr)
	require.Nil(t, streamer)
}

func TestModelInterceptorRejectsAfterModelResponseReplacementForStream(t *testing.T) {
	providerErr := errors.New("stream setup failed")
	tests := []struct {
		name      string
		streamErr error
	}{
		{name: "successful setup"},
		{name: "setup error is preserved", streamErr: providerErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opened := &closeCountingModelStream{}
			interceptor := RuntimeInterceptorFuncs{AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
				return &AfterModelDecision{Response: &model.Response{
					Content:    []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "replacement"}}}},
					StopReason: "end_turn",
				}}, nil
			}}
			client := newModelInterceptedClient(
				contractModelClient{streamer: opened, streamErr: test.streamErr},
				[]Interceptor{interceptor},
				"agent", "run", "session", "turn", "model",
				nil,
				nil,
			)

			streamer, err := client.Stream(context.Background(), &model.Request{})
			require.ErrorContains(t, err, "response replacement is unsupported for streaming")
			if test.streamErr != nil {
				require.ErrorIs(t, err, test.streamErr)
			}
			require.Nil(t, streamer)
			require.Equal(t, 1, opened.closeCount)
		})
	}
}

func TestModelInterceptorAfterModelStreamEnforcesNonNilStreamerContract(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model stream contract violation")
	require.Nil(t, streamer)
}

func TestRunInterceptorsOrderAndErrorShortCircuit(t *testing.T) {
	var calls []string
	runErr := errors.New("blocked")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeRunFunc: func(_ context.Context, input *BeforeRunInput) (*BeforeRunDecision, error) {
			calls = append(calls, "before")
			require.Equal(t, agent.Ident("svc.agent"), input.AgentID)
			return nil, runErr
		},
		AfterRunFunc: func(context.Context, *AfterRunInput) (*AfterRunDecision, error) {
			calls = append(calls, "after")
			return nil, nil
		},
	}))
	_, err := runBeforeRunInterceptors(context.Background(), rt.interceptors, RunInput{AgentID: "svc.agent", RunID: "run-1"}, run.Context{RunID: "run-1"})
	require.ErrorIs(t, err, runErr)
	require.Equal(t, []string{"before"}, calls)
}

var _ runlog.Store = (*recordingRunlog)(nil)
