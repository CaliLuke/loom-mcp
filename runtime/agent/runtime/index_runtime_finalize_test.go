package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeIndexOracleFinalize(t *testing.T) {
	for _, reason := range []planner.TerminationReason{"", planner.TerminationReasonTimeBudget, planner.TerminationReasonToolCap, planner.TerminationReasonFailureCap, planner.TerminationReasonOutcomeUnknown} {
		for _, pending := range []bool{false, true} {
			label := "no_calls"
			count := 4
			if pending {
				label = "pending_calls"
				count = 5
			}
			t.Run(string(reason)+"/"+label, func(t *testing.T) {
				rt := New(WithEngine(engineinmem.New()))
				prefix := "TestIndexOracleFinalize/" + string(reason) + "/" + label
				expected := frozenIndexMessages(t, fmt.Sprintf("%s/%d/workflow_finalize.go synthetic results and versioned hint", prefix, count))
				seed := frozenIndexMessages(t, prefix+"/2/workflow.go base init")
				if pending {
					seed = append(seed, expected[1])
				}
				invoked := false
				registerContentAgent(t, rt, &stubPlanner{resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
					invoked = true
					assertIndexMessages(t, expected, in.Messages)
					assert.Empty(t, in.TypedInputs, "finalize still omits typed answers")
					return oracleFinal(), nil
				}})
				answerRef, err := rt.RunStore.Put(t.Context(), "root", []byte(`{"answer":true}`))
				require.NoError(t, err)
				// Drive the real finalization helper and registered in-memory planner activity
				// directly so each terminal reason is deterministic, including pending calls.
				require.NoError(t, rt.Engine.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "oracle.finalize", Handler: func(w engine.WorkflowContext, input *api.RunInput) (*api.RunOutput, error) {
					stored, err := seedWorkflowIndex(w, input)
					if err != nil {
						return nil, err
					}
					base := &workflowPlanInput{AgentID: input.AgentID, RunContext: run.Context{RootRunID: "root", RunID: "root", TurnID: "turn"}, Entries: stored.Entries, IndexHead: stored.IndexHead, Committed: len(stored.Entries)}
					if err := base.appendEntry(index.MessageEntry{Kind: index.TypedInput, InputID: "typed", Hash: answerRef}); err != nil {
						return nil, err
					}
					_, _, err = rt.runFinalizationPlan(w, rt.agents[input.AgentID], input, base, model.TokenUsage{}, policy.CapsState{}, 2, "turn", reason, time.Time{})
					return &api.RunOutput{}, err
				}}))
				handle, err := rt.Engine.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "oracle", Workflow: "oracle.finalize", Input: &api.RunInput{AgentID: "content.agent", RootRunID: "root", RunID: "root", Messages: seed}})
				require.NoError(t, err)
				_, err = handle.Wait(t.Context())
				require.NoError(t, err)
				assert.True(t, invoked)
			})
		}
	}
}

func TestRuntimeIndexOracleResumeMessages(t *testing.T) {
	bus := &recordingHooks{ch: make(chan hooks.Event, 128)}
	rt := New(WithEngine(engineinmem.New()), WithHooks(bus))
	spec := newAnyJSONSpec("test.lookup", "test")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "ok"}), nil
	}}))
	prefix := "TestIndexOracleClarificationResumeAndTypedScript/"
	seed := frozenIndexMessages(t, prefix+"4/workflow_clarification.go empty answer")
	expected := cloneMessages(frozenIndexMessages(t, prefix+"6/workflow_interrupts.go applyResumeMessages"))
	expected = append(expected, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "lookup", Name: "test.lookup"}}}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "lookup", Content: "ok"}}})
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, _ *planner.PlanInput) (*planner.PlanResult, error) {
		if err := rt.PauseRun(ctx, &api.PauseRequest{RunID: "root", Reason: "oracle"}); err != nil {
			return nil, err
		}
		if err := rt.ResumeRun(ctx, &api.ResumeRequest{RunID: "root", Messages: []*model.Message{nil, contentMessage("resume")}}); err != nil {
			return nil, err
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "lookup"}}}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		assertIndexMessages(t, expected, in.Messages)
		return oracleFinal(), nil
	}})
	runIndexOracle(t, rt, bus, seed, func(hooks.Event) error {
		return nil
	})
}
