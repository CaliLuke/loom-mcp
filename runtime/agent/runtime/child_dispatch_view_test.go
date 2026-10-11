package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type childHeadWorkflow struct {
	*testWorkflowContext
	outputs []*api.ChildDispatchOutput
}

func TestChildDispatchAdoptsHeadAndKeepsWarmPlanner(t *testing.T) {
	for _, steps := range []int{2, 40} {
		for _, load := range []bool{false, true} {
			for _, reject := range []bool{false, true} {
				t.Run(fmt.Sprintf("steps=%d/load=%t/reject=%t", steps, load, reject), func(t *testing.T) {
					store := &measuredViewStore{Store: storeinmem.New()}
					rt := New(WithRunStore(store))
					base := buildGrowthIndex(t, store, steps)
					base.CallRefs = map[string]runstore.Hash{"held-call": "sha256:held"}
					previous, err := rt.loadTestView(t.Context(), "root", "run", index.ViewRef{IndexHead: base.IndexHead})
					require.NoError(t, err)
					cfg, call := registerChildHeadValidator(t, rt, func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
						if load {
							messages, err := in.View.LoadMessages(ctx)
							require.NoError(t, err)
							assert.Len(t, messages, 2*steps)
						}
						if reject {
							return NewAgentToolValidationError("declined", []*tools.FieldIssue{{Field: "query", Constraint: "invalid_format"}}, nil)
						}
						return nil
					})
					resumed := false
					registerContentAgent(t, rt, &stubPlanner{resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
						resumed = true
						assert.Same(t, previous.Outputs[0], in.ToolOutputs[0], "cache did not reset or rederive old outputs")
						assert.Len(t, in.ToolOutputs, steps)
						assert.Len(t, in.TypedInputs, steps+1)
						return oracleFinal(), nil
					}})
					wf := &childHeadWorkflow{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), finishRuntime: rt}}
					pending, oldHead := base.view().Pending, base.IndexHead
					failure, err := rt.validateChildDispatch(wf, "content.agent", cfg, &call, &base.RunContext, base)
					require.NoError(t, err)
					assert.Equal(t, reject, failure != nil)
					require.Len(t, wf.outputs, 1)
					if load {
						assert.NotEmpty(t, wf.outputs[0].IndexHead)
						assert.Equal(t, wf.outputs[0].IndexHead, base.IndexHead)
						assert.NotEqual(t, oldHead, base.IndexHead)
						assert.Empty(t, base.view().Pending)
					} else {
						assert.Empty(t, wf.outputs[0].IndexHead)
						assert.Equal(t, oldHead, base.IndexHead)
						assert.Equal(t, pending, base.view().Pending)
					}
					assert.Equal(t, map[string]runstore.Hash{"held-call": "sha256:held"}, base.CallRefs)
					hash := testPutBytes(t, rt, "root", []byte(`"new answer"`))
					require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.TypedInput, InputID: "new-answer", Hash: hash}))
					store.reads.Store(0)
					_, err = rt.PlanResumeActivity(t.Context(), &PlanActivityInput{AgentID: "content.agent", RunID: "run", RunContext: base.RunContext, View: base.view()})
					require.NoError(t, err)
					assert.True(t, resumed)
					reads := int64(6) // Last result/answer step plus new answer and one segment.
					if load {
						reads = 2 // Only the new answer and its segment.
					}
					assert.Equal(t, reads, store.reads.Load(), "next planner reads are independent of prior history length")
				})
			}
		}
	}
}

func TestChildDispatchRepeatedHeadAdoption(t *testing.T) {
	rt := New()
	base := buildGrowthIndex(t, rt.RunStore, 2)
	cfg, call := registerChildHeadValidator(t, rt, func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
		_, err := in.View.LoadMessages(ctx)
		require.NoError(t, err)
		return nil
	})
	shared := &api.ChildDispatchInput{Call: call, ParentRun: base.RunContext, View: base.view()}
	first, err := rt.ChildDispatchActivity(t.Context(), shared)
	require.NoError(t, err)
	second, err := rt.ChildDispatchActivity(t.Context(), shared)
	require.NoError(t, err)
	require.NotEmpty(t, first.IndexHead)
	assert.Equal(t, first.IndexHead, second.IndexHead, "shared pending entries store byte-identical segments")
	base.adoptHead(first.IndexHead)
	base.adoptHead(second.IndexHead)
	assert.Empty(t, base.view().Pending)
	wf := &childHeadWorkflow{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), finishRuntime: rt}}
	_, err = rt.validateChildDispatch(wf, "content.agent", cfg, &call, &base.RunContext, base)
	require.NoError(t, err)
	assert.Empty(t, wf.outputs[0].IndexHead, "loading already committed entries returns no new head")
	assert.Equal(t, first.IndexHead, base.IndexHead)
}

func TestChildDispatchTwoCallsShareCommittedView(t *testing.T) {
	eng := &childDispatchRecordingEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng))
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "work", Specs: []tools.ToolSpec{newAnyJSONSpec("work.echo", "work")}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, Result: "evidence"}), nil
	}}))
	// Real child work avoids the existing agent_tool_no_children protection path.
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "child.agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "work.echo", ToolCallID: "work-call", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}}, Workflow: engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "child.queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool"}))
	calls := []planner.ToolRequest{{Name: "child.lookup", ToolCallID: "child-1", Payload: rawjson.Message(`{"query":"first"}`)}, {Name: "child.lookup", ToolCallID: "child-2", Payload: rawjson.Message(`{"query":"second"}`)}}
	expected := []*model.Message{contentMessage("parent evidence"), {Role: model.ConversationRoleAssistant, Parts: []model.Part{
		model.ToolUsePart{ID: "child-1", Name: "child.lookup", Input: rawjson.Message(`{"query":"first"}`)},
		model.ToolUsePart{ID: "child-2", Name: "child.lookup", Input: rawjson.Message(`{"query":"second"}`)},
	}}}
	validated := 0
	registerChildHeadValidator(t, rt, func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
		messages, err := in.View.LoadMessages(ctx)
		require.NoError(t, err)
		assertIndexMessages(t, expected, messages)
		validated++
		return nil
	})
	resumed := false
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: calls}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		resumed = true
		oracle := append([]*model.Message(nil), expected...)
		oracle = append(oracle, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{
			model.ToolResultPart{ToolUseID: "child-1", Content: "done"},
			model.ToolResultPart{ToolUseID: "child-2", Content: "done"},
		}})
		assertIndexMessages(t, oracle, in.Messages)
		require.Len(t, in.ToolOutputs, 2)
		assert.Nil(t, in.ToolOutputs[0].Error)
		assert.Nil(t, in.ToolOutputs[1].Error)
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("parent evidence")}, WithRunID("root"))
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, 2, validated)
	require.Len(t, eng.inputs, 2)
	require.Len(t, eng.outputs, 2)
	require.NotEmpty(t, eng.outputs[0].IndexHead)
	assert.Equal(t, eng.outputs[0].IndexHead, eng.inputs[1].View.IndexHead)
	assert.Empty(t, eng.inputs[1].View.Pending, "second dispatch uses the adopted view")
	assert.Empty(t, eng.outputs[1].IndexHead)
}

// registerChildHeadValidator registers an agent tool for activity and workflow proofs.
func registerChildHeadValidator(t *testing.T, rt *Runtime, validator func(context.Context, *AgentToolValidationInput) *AgentToolValidationError) (*AgentToolConfig, planner.ToolRequest) {
	t.Helper()
	cfg := AgentToolConfig{Name: "child", AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"}, PreChildValidator: validator}
	reg := NewAgentToolsetRegistration(rt, cfg)
	spec := newAnyJSONSpec("child.lookup", "child")
	spec.IsAgentTool = true
	reg.Specs = []tools.ToolSpec{spec}
	require.NoError(t, rt.RegisterToolset(reg))
	return &cfg, planner.ToolRequest{Name: spec.Name, ToolCallID: "child-call", Payload: rawjson.Message(`{}`)}
}

func (wf *childHeadWorkflow) ExecuteChildDispatchActivity(ctx context.Context, call engine.ChildDispatchActivityCall) (*api.ChildDispatchOutput, error) {
	out, err := wf.testWorkflowContext.ExecuteChildDispatchActivity(ctx, call)
	wf.outputs = append(wf.outputs, out)
	return out, err
}
