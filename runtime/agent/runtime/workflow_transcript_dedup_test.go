package runtime

// workflow_transcript_dedup_test.go verifies that the streamed assistant
// transcript (thinking/text) of a planner turn is appended to the provider
// conversation exactly once, even when the turn records multiple tool_use
// batches (immediate execution plus confirmation-gated calls).

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunLoopRecordsAssistantTranscriptOncePerTurn(t *testing.T) {
	cases := []struct {
		name      string
		toolCalls []planner.ToolRequest
		confirmed []tools.Ident
	}{
		{
			name: "normal tool plus confirmation gated tool",
			toolCalls: []planner.ToolRequest{
				{Name: tools.Ident("tool.plain"), ToolCallID: "call-plain"},
				{Name: tools.Ident("tool.confirm"), ToolCallID: "call-confirm"},
			},
			confirmed: []tools.Ident{"tool.confirm"},
		},
		{
			name: "multiple confirmation gated tools",
			toolCalls: []planner.ToolRequest{
				{Name: tools.Ident("tool.confirm"), ToolCallID: "call-confirm-1"},
				{Name: tools.Ident("tool.confirm2"), ToolCallID: "call-confirm-2"},
			},
			confirmed: []tools.Ident{"tool.confirm", "tool.confirm2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTranscriptDedupRuntime(tc.toolCalls, tc.confirmed)
			wfCtx := &testWorkflowContext{ctx: context.Background(), asyncResult: ToolOutput{Payload: []byte("null")}}
			wfCtx.confirmCh = make(chan *api.ConfirmationDecision, len(tc.confirmed))
			wfCtx.ensureSignals()
			for range tc.confirmed {
				wfCtx.confirmCh <- &api.ConfirmationDecision{Approved: true, RequestedBy: "tester"}
			}
			wfCtx.hasPlanResult = true
			wfCtx.planResult = &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "done"}},
			}}}

			input := &RunInput{AgentID: "svc.agent", RunID: "run-1"}
			base := &planner.PlanInput{
				RunContext: run.Context{RunID: input.RunID},
				Agent:      newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID}),
			}
			initial := &planner.PlanResult{ToolCalls: tc.toolCalls}
			turnTranscript := []*model.Message{{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "turn-reasoning"}},
			}}
			ctrl := interrupt.NewController(wfCtx)

			_, err := rt.runLoop(wfCtx, AgentRegistration{
				ID:                  input.AgentID,
				Planner:             &stubPlanner{},
				ExecuteToolActivity: "execute",
				ResumeActivityName:  "resume",
			}, input, base, initial, turnTranscript, model.TokenUsage{}, policy.CapsState{}, time.Time{}, time.Time{}, 2, "turn-1", nil, ctrl, 0)
			require.NoError(t, err)

			assert.Equal(t, 1, countAssistantTextParts(base.Messages, "turn-reasoning"), "streamed assistant transcript must be recorded exactly once")
			for _, call := range tc.toolCalls {
				assert.Equal(t, 1, countToolUseParts(base.Messages, call.ToolCallID), "tool_use %s must be declared exactly once", call.ToolCallID)
			}
		})
	}
}

// newTranscriptDedupRuntime builds a minimal Runtime with anyJSON tool specs for
// the given calls and confirmation overrides for the confirmed tool idents.
func newTranscriptDedupRuntime(toolCalls []planner.ToolRequest, confirmed []tools.Ident) *Runtime {
	rt := &Runtime{
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
		toolsets: map[string]ToolsetRegistration{"svc.ts": {
			Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}), nil
			},
		}},
	}
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{}
	for _, call := range toolCalls {
		rt.toolSpecs[call.Name] = newAnyJSONSpec(call.Name, "svc.ts")
	}
	confirm := map[tools.Ident]*ToolConfirmation{}
	for _, name := range confirmed {
		confirm[name] = &ToolConfirmation{
			Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
				return "Confirm?", nil
			},
			DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) {
				return map[string]string{"summary": "denied"}, nil
			},
		}
	}
	rt.toolConfirmation = &ToolConfirmationConfig{Confirm: confirm}
	return rt
}

// countAssistantTextParts counts assistant-message text parts equal to text.
func countAssistantTextParts(msgs []*model.Message, text string) int {
	count := 0
	for _, msg := range msgs {
		if msg == nil || msg.Role != model.ConversationRoleAssistant {
			continue
		}
		for _, part := range msg.Parts {
			if tp, ok := part.(model.TextPart); ok && tp.Text == text {
				count++
			}
		}
	}
	return count
}

// countToolUseParts counts tool_use parts declaring the given tool call ID.
func countToolUseParts(msgs []*model.Message, toolCallID string) int {
	count := 0
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		for _, part := range msg.Parts {
			if tu, ok := part.(model.ToolUsePart); ok && tu.ID == toolCallID {
				count++
			}
		}
	}
	return count
}
