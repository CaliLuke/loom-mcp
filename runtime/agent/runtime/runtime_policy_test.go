package runtime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureModelClient struct {
	tools []string
}

func (c *captureModelClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	c.tools = nil
	for _, tool := range req.Tools {
		c.tools = append(c.tools, tool.Name)
	}
	return &model.Response{
		Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "done"}},
		}},
	}, nil
}

func (c *captureModelClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, io.EOF
}

type modelToolPolicyPlanner struct{}

func (p *modelToolPolicyPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("default")
	if !ok {
		return nil, errors.New("default model not configured")
	}
	resp, err := client.Complete(ctx, &model.Request{
		Tools: []*model.ToolDefinition{
			{Name: "allowed", InputSchema: map[string]any{"type": "object"}},
			{Name: "blocked", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeAny},
	})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{
		FinalResponse: &planner.FinalResponse{Message: &resp.Content[0]},
	}, nil
}

func (p *modelToolPolicyPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, nil
}

func TestPolicyAllowlistTrimsToolExecution(t *testing.T) {
	recorder := &recordingHooks{}
	rt := &Runtime{
		Bus:           recorder,
		Policy:        &stubPolicyEngine{decision: policy.Decision{AllowedTools: []tools.Ident{tools.Ident("allowed")}}},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}
	rt.toolsets = map[string]ToolsetRegistration{"svc.tools": {
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return Executed(&planner.ToolResult{
				Name:   call.Name,
				Result: map[string]any{"ok": true},
			}), nil
		}}}
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"allowed": newAnyJSONSpec("allowed", "svc.tools"), "blocked": newAnyJSONSpec("blocked", "svc.tools")}
	wfCtx := &testWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		asyncResult: ToolOutput{Payload: []byte("null")},
		planResult: &planner.PlanResult{
			FinalResponse: &planner.FinalResponse{
				Message: &model.Message{
					Role:  "assistant",
					Parts: []model.Part{model.TextPart{Text: "done"}},
				},
			},
		},
		hasPlanResult: true,
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}, Agent: newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID})}
	initial := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tools.Ident("allowed")}, {Name: tools.Ident("blocked")}}}
	out, err := rt.runLoop(wfCtx, AgentRegistration{
		ID:                  input.AgentID,
		Planner:             &stubPlanner{},
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}, input, base, initial, nil, model.TokenUsage{}, policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 5}, time.Time{}, time.Time{}, 2, "turn-1", nil, nil, 0)
	require.NoError(t, err)
	require.Len(t, out.ToolEvents, 1)
	require.Equal(t, tools.Ident("allowed"), out.ToolEvents[0].Name)
	var scheduled []tools.Ident
	for _, evt := range recorder.events {
		if e, ok := evt.(*hooks.ToolCallScheduledEvent); ok {
			scheduled = append(scheduled, e.ToolName)
		}
	}
	require.Equal(t, []tools.Ident{tools.Ident("allowed")}, scheduled)
}

// TestApplyPolicyEnvelopeFiltersStrictly pins the semantics of an active
// tool-policy envelope: the allowlist is authoritative and an empty allowlist
// means no tool call may execute (issue #116). The historical bug treated an
// empty envelope allowlist as "unrestricted", letting DisableTools decisions
// pass every planner tool call through to execution.
func TestApplyPolicyEnvelopeFiltersStrictly(t *testing.T) {
	candidates := []planner.ToolRequest{
		{Name: tools.Ident("allowed")},
		{Name: tools.Ident("blocked")},
	}
	cases := []struct {
		name    string
		allowed []tools.Ident
		want    []tools.Ident
	}{
		{
			name:    "empty allowlist blocks every call",
			allowed: nil,
			want:    []tools.Ident{},
		},
		{
			name:    "allowlist keeps only listed calls",
			allowed: []tools.Ident{tools.Ident("allowed")},
			want:    []tools.Ident{tools.Ident("allowed")},
		},
		{
			name:    "allowlist covering all calls keeps them",
			allowed: []tools.Ident{tools.Ident("allowed"), tools.Ident("blocked")},
			want:    []tools.Ident{tools.Ident("allowed"), tools.Ident("blocked")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &Runtime{logger: telemetry.NoopLogger{}}
			base := &planner.PlanInput{RunContext: run.Context{RunID: "run-1"}}
			input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
			envelope := toolPolicyEnvelope{Active: true, Allowed: tc.allowed}
			result, err := rt.applyPolicy(context.Background(), base, input, candidates, policy.CapsState{}, "turn-1", nil, envelope)
			require.NoError(t, err)
			assert.Equal(t, tc.want, toolHandles(result.AllowedCalls))
		})
	}
}

func TestApplyPolicyEnvelopePreservesToolUnavailable(t *testing.T) {
	rt := &Runtime{logger: telemetry.NoopLogger{}}
	base := &planner.PlanInput{RunContext: run.Context{RunID: "run-1"}}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
	candidates := []planner.ToolRequest{
		{Name: tools.Ident("allowed")},
		{Name: tools.ToolUnavailable},
	}

	result, err := rt.applyPolicy(context.Background(), base, input, candidates, policy.CapsState{}, "turn-1", nil, toolPolicyEnvelope{
		Active:  true,
		Allowed: []tools.Ident{tools.Ident("allowed")},
	})
	require.NoError(t, err)
	assert.Equal(t, []tools.Ident{tools.Ident("allowed"), tools.ToolUnavailable}, toolHandles(result.AllowedCalls))
}

func TestPreparePrePlanToolPolicyDoesNotCapAdvertisedTools(t *testing.T) {
	rt := &Runtime{
		Bus:           noopHooks{},
		Policy:        &stubPolicyEngine{decision: policy.Decision{}},
		RunEventStore: runloginmem.New(),
		logger:        telemetry.NoopLogger{},
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			tools.Ident("one"): newAnyJSONSpec("one", "svc.tools"),
			tools.Ident("two"): newAnyJSONSpec("two", "svc.tools"),
		},
	}
	input := &RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		Policy:  &PolicyOverrides{PerTurnMaxToolCalls: 1},
	}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}}

	result, err := rt.preparePrePlanToolPolicy(context.Background(), AgentRegistration{ID: input.AgentID}, input, base, policy.CapsState{
		MaxToolCalls:       1,
		RemainingToolCalls: 1,
	}, "turn-1")
	require.NoError(t, err)
	assert.Equal(t, []tools.Ident{tools.Ident("one"), tools.Ident("two")}, result.Envelope.Allowed)
}

func TestMergeCapsIgnoresDeprecatedExpiresAt(t *testing.T) {
	current := policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 4}
	decision := policy.CapsState{
		RemainingToolCalls: 3,
		ExpiresAt:          time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	got := mergeCaps(current, decision)

	assert.Equal(t, 3, got.RemainingToolCalls)
	assert.True(t, got.ExpiresAt.IsZero(), "deprecated wall-clock expiry must not become a second runtime deadline") //nolint:staticcheck // Verify the legacy field remains inert.
}

// TestDisableToolsPolicyBlocksToolExecution drives the workflow loop end to end
// with a policy engine that returns DisableTools. The pre-plan policy envelope
// must come back active with an empty allowlist, and a planner that emits tool
// calls anyway must have them rejected instead of executed (issue #116).
func TestDisableToolsPolicyBlocksToolExecution(t *testing.T) {
	recorder := &recordingHooks{}
	executed := false
	rt := &Runtime{
		Bus:           recorder,
		Policy:        &stubPolicyEngine{decision: policy.Decision{DisableTools: true}},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}
	rt.toolsets = map[string]ToolsetRegistration{"svc.tools": {
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			executed = true
			return Executed(&planner.ToolResult{
				Name:   call.Name,
				Result: map[string]any{"ok": true},
			}), nil
		}}}
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"allowed": newAnyJSONSpec("allowed", "svc.tools")}
	wfCtx := &testWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}, Agent: newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID})}
	reg := AgentRegistration{
		ID:                  input.AgentID,
		Planner:             &stubPlanner{},
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}

	// Derive the envelope exactly like the workflow start path does.
	policyResult, err := rt.preparePrePlanToolPolicy(wfCtx.Context(), reg, input, base, policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 5}, "turn-1")
	require.NoError(t, err)
	require.True(t, policyResult.Envelope.Active)
	require.Empty(t, policyResult.Envelope.Allowed)

	// A planner that violates DisableTools by emitting tool calls anyway.
	initial := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tools.Ident("allowed")}}}
	out, err := rt.runLoop(wfCtx, reg, input, base, initial, nil, model.TokenUsage{}, policyResult.Caps, time.Time{}, time.Time{}, 2, "turn-1", nil, nil, 0, policyResult.Envelope)
	require.Error(t, err)
	require.ErrorContains(t, err, "no tools allowed for execution")
	assert.Nil(t, out)
	assert.False(t, executed, "tool must not execute when policy disabled tools")
	for _, evt := range recorder.events {
		if e, ok := evt.(*hooks.ToolCallScheduledEvent); ok {
			t.Fatalf("unexpected tool call scheduled for %q despite DisableTools", e.ToolName)
		}
	}
}

// TestAwaitResumeReappliesToolPolicy covers issue #117: resuming after an await
// (here a runtime-owned tool pause answered by a clarification) must re-run the
// pre-plan tool policy and stamp the fresh envelope on the resume request, so
// the post-await planner turn does not advertise policy-hidden tools.
func TestAwaitResumeReappliesToolPolicy(t *testing.T) {
	askTool := tools.Ident("inline.ts.ask")
	extraTool := tools.Ident("inline.ts.extra")
	rt := &Runtime{
		Bus:           noopHooks{},
		Policy:        &stubPolicyEngine{decision: policy.Decision{AllowedTools: []tools.Ident{askTool}}},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
		SessionStore:  sessioninmem.New(),
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			askTool:   newAnyJSONSpec(askTool, "inline.ts"),
			extraTool: newAnyJSONSpec(extraTool, "inline.ts"),
		},
		toolsets: map[string]ToolsetRegistration{
			"inline.ts": {
				Inline:       true,
				DispatchMode: DispatchInline,
				Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					return &ToolExecutionResult{
						ToolResult: &planner.ToolResult{
							Name:       call.Name,
							ToolCallID: call.ToolCallID,
							Result:     map[string]any{"phase": "awaiting_input"},
						},
						Pause: &ToolPause{
							Clarification: &ToolPauseClarification{
								ID:       "clar-1",
								Question: "Which one?",
							},
						},
					}, nil
				},
			},
		},
	}

	// The resume planner activity returns a FinalResponse so the loop ends
	// after one pause/answer cycle.
	wfCtx := &testWorkflowContext{
		ctx:           context.Background(),
		hookRuntime:   rt,
		hasPlanResult: true,
		planResult: &planner.PlanResult{
			FinalResponse: &planner.FinalResponse{
				Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "done"}},
				},
			},
		},
	}
	wfCtx.ensureSignals()
	ctrl := interrupt.NewController(wfCtx)
	wfCtx.clarifyCh <- &api.ClarificationAnswer{ID: "clar-1", Answer: "the first one"}

	input := &RunInput{AgentID: "svc.agent", RunID: "run-1", SessionID: "sess-1", TurnID: "turn-1"}
	_, err := rt.CreateSession(context.Background(), input.SessionID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, rt.SessionStore.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   string(input.AgentID),
		RunID:     input.RunID,
		SessionID: input.SessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))
	base := &planner.PlanInput{
		RunContext: run.Context{RunID: input.RunID, SessionID: input.SessionID, TurnID: input.TurnID, Attempt: 1},
		Agent:      newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID}),
	}
	initial := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: askTool}}}

	// Stale pre-await envelope: broader than what the policy engine now allows.
	staleEnvelope := toolPolicyEnvelope{Active: true, Allowed: []tools.Ident{askTool, extraTool}}
	_, err = rt.runLoop(
		wfCtx,
		AgentRegistration{
			ID:                  input.AgentID,
			Planner:             &stubPlanner{},
			ExecuteToolActivity: "execute",
			ResumeActivityName:  "resume",
		},
		input, base, initial, nil, model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 4, RemainingToolCalls: 4},
		time.Time{}, time.Time{}, 2, input.TurnID, nil, ctrl, 0, staleEnvelope,
	)
	require.NoError(t, err)

	require.Equal(t, "resume", wfCtx.lastPlannerCall.Name)
	require.NotNil(t, wfCtx.lastPlannerCall.Input)
	resumeReq := wfCtx.lastPlannerCall.Input
	assert.True(t, resumeReq.ToolPolicyActive, "post-await resume must carry an active tool policy")
	assert.Equal(t, []tools.Ident{askTool}, resumeReq.AllowedTools, "post-await resume must carry the freshly decided allowlist, not the stale pre-await envelope")
	assert.Equal(t, 3, resumeReq.PolicyCaps.RemainingToolCalls, "post-await resume must carry the current caps state")
}

func TestPlanStartActivityFiltersModelVisibleTools(t *testing.T) {
	modelClient := &captureModelClient{}
	rt := &Runtime{
		agents: map[agent.Ident]AgentRegistration{
			"svc.agent": {
				ID:      "svc.agent",
				Planner: &modelToolPolicyPlanner{},
			},
		},
		models: map[string]model.Client{
			"default": modelClient,
		},
		Bus:           hooks.NewBus(),
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-1",
		RunContext:       run.Context{RunID: "run-1"},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"allowed"},
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, []string{"allowed"}, modelClient.tools)
	require.True(t, out.ToolPolicyActive)
	require.Equal(t, []tools.Ident{tools.Ident("allowed")}, out.AllowedTools)
}

func TestPlanStartActivityReturnsHistoryPolicyError(t *testing.T) {
	sentinel := errors.New("history policy unavailable")
	rt := &Runtime{
		agents: map[agent.Ident]AgentRegistration{
			"svc.agent": {
				ID:      "svc.agent",
				Planner: &stubPlanner{},
				Policy: RunPolicy{
					History: func(context.Context, []*model.Message, []*model.ToolDefinition) ([]*model.Message, error) {
						return nil, sentinel
					},
				},
			},
		},
		Bus:           hooks.NewBus(),
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	_, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-1",
		RunContext: run.Context{RunID: "run-1"},
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}},
	})

	require.ErrorIs(t, err, sentinel)
}

func TestPlanResumeActivityReturnsHistoryPolicyError(t *testing.T) {
	sentinel := errors.New("history policy unavailable")
	rt := &Runtime{
		agents: map[agent.Ident]AgentRegistration{
			"svc.agent": {
				ID:      "svc.agent",
				Planner: &stubPlanner{},
				Policy: RunPolicy{
					History: func(context.Context, []*model.Message, []*model.ToolDefinition) ([]*model.Message, error) {
						return nil, sentinel
					},
				},
			},
		},
		Bus:           hooks.NewBus(),
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	_, err := rt.PlanResumeActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-1",
		RunContext: run.Context{RunID: "run-1"},
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}},
	})

	require.ErrorIs(t, err, sentinel)
}
