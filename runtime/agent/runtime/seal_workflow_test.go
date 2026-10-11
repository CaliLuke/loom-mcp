package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sealRejectedStore struct {
	runlog.Store
}

func TestWorkflowTerminalPathsSeal(t *testing.T) {
	for _, path := range []string{"finish", "failure", "cancellation", "finalizer"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rt := New()
			key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
			plan := &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				switch path {
				case "failure":
					return nil, errors.New("planner failed")
				case "cancellation":
					cancel()
					return nil, context.Canceled
				case "finalizer":
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "test.lookup", ToolCallID: "call"}}}, nil
				default:
					return assistantCommitResult(), nil
				}
			}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return assistantCommitResult(), nil
			}}
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: plan, PlanActivityName: "plan", ResumeActivityName: "resume"}
			wf := &testWorkflowContext{ctx: ctx, runtime: rt, hookRuntime: rt, finishRuntime: rt}
			if path == "finish" {
				wf.plannerOutputs = []*api.PlanActivityOutput{{Result: assistantCommitResult(), ProducingCall: &key, CallKeys: []runstore.CallKey{key}}}
			}
			input := &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion, Messages: []*model.Message{contentMessage("seed")}}
			if path == "finalizer" {
				input.Policy = &PolicyOverrides{MaxRecoveryTurns: 1}
				wf.plannerOutputs = []*api.PlanActivityOutput{
					{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry", DisableTools: true, Attempt: 1}},
					{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry again", DisableTools: true, Attempt: 2}},
					{Result: assistantCommitResult(), ProducingCall: &key, CallKeys: []runstore.CallKey{key}},
				}
			}
			_, err := rt.ExecuteWorkflow(wf, input)
			if path == "failure" || path == "cancellation" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			sealed, completed := -1, -1
			var head runstore.Hash
			for i, event := range page.Events {
				if event.Type == hooks.RunSealed {
					assert.Equal(t, -1, sealed, "exactly one seal")
					sealed = i
					var payload hooks.RunSealedEvent
					require.NoError(t, json.Unmarshal(event.Payload, &payload))
					head = payload.IndexHead
				}
				if event.Type == hooks.RunCompleted {
					completed = i
				}
			}
			require.GreaterOrEqual(t, sealed, 0)
			assert.Greater(t, completed, sealed)
			view, err := rt.loadTestView(t.Context(), "root", "run", index.ViewRef{IndexHead: head})
			require.NoError(t, err)
			assert.Equal(t, "seed", agentMessageText(view.Messages[0]))
			if path == "finish" || path == "finalizer" {
				entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
					return rt.RunStore.Get(ctx, "root", hash)
				}, head)
				require.NoError(t, err)
				assert.Equal(t, &key, entries[len(entries)-1].Call)
				assert.Equal(t, agentMessageText(assistantCommitResult().FinalResponse.Message), agentMessageText(view.Messages[len(view.Messages)-1]))
			}
		})
	}
}

func TestWorkflowFailureSealsPendingToolTurn(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rt := New()
			spec := newAnyJSONSpec("test.lookup", "test")
			registerTestTool(t, rt, spec, func(call *planner.ToolRequest) *planner.ToolResult {
				return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "stored result"}
			})
			plan := &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, nil
			}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				if canceled {
					cancel()
					return nil, context.Canceled
				}
				return nil, errors.New("resume failed")
			}}
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: plan, PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "execute"}
			wf := &testWorkflowContext{ctx: ctx, runtime: rt, hookRuntime: rt, finishRuntime: rt}
			_, err := rt.ExecuteWorkflow(wf, &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion, Messages: []*model.Message{contentMessage("seed")}})
			require.Error(t, err)
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			var sealed hooks.RunSealedEvent
			for _, event := range page.Events {
				if event.Type == hooks.RunSealed {
					require.NoError(t, json.Unmarshal(event.Payload, &sealed))
				}
			}
			require.NotEmpty(t, sealed.IndexHead)
			view, err := rt.loadTestView(t.Context(), "root", "run", index.ViewRef{IndexHead: sealed.IndexHead})
			require.NoError(t, err)
			require.Len(t, view.Messages, 3)
			assert.Equal(t, model.ConversationRoleAssistant, view.Messages[1].Role)
			require.Len(t, view.Outputs, 1)
			data, err := rt.RunStore.Get(t.Context(), "root", view.Outputs[0].Refs.Result)
			require.NoError(t, err)
			assert.JSONEq(t, `"stored result"`, string(data))
		})
	}
}

func TestWorkflowSealFailureCannotBecomeSuccess(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{AfterRunFunc: func(context.Context, *AfterRunInput) (*AfterRunDecision, error) {
		return &AfterRunDecision{}, nil
	}}))
	rt.RunEventStore = &sealRejectedStore{Store: rt.RunEventStore}
	rt.agents["agent"] = AgentRegistration{ID: "agent", PlanActivityName: "plan"}
	wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{{Result: assistantCommitResult()}}}
	_, err := rt.ExecuteWorkflow(wf, &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion})
	require.ErrorContains(t, err, "seal append rejected")
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
	require.NoError(t, err)
	for _, event := range page.Events {
		assert.NotEqual(t, hooks.RunSealed, event.Type)
		if event.Type == hooks.RunCompleted {
			decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: event.Type, RunID: event.RunID, Payload: event.Payload})
			require.NoError(t, err)
			assert.Equal(t, runStatusFailed, decoded.(*hooks.RunCompletedEvent).Status)
		}
	}
}

func (s *sealRejectedStore) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	if event.Type == hooks.RunSealed {
		return runlog.AppendResult{}, errors.New("seal append rejected")
	}
	return s.Store.Append(ctx, event)
}
