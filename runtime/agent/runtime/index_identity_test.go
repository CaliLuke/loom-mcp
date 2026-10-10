package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoredCallsEqualDispatchedAfterFiltering(t *testing.T) {
	eng := &indexCaptureEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng))
	a, b := newAnyJSONSpec("test.a", "test"), newAnyJSONSpec("test.b", "test")
	var dispatched planner.ToolRequest
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{a, b}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		dispatched = *call
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: true}), nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: a.Name}, {Name: b.Name, Payload: rawjson.Message(`{"value":1}`)}}}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"), WithTurnID("turn"), func(in *RunInput) {
		in.Policy = &PolicyOverrides{RestrictToTool: b.Name}
		in.Labels = map[string]string{"tenant": "test"}
	})
	require.NoError(t, err)
	assert.Equal(t, generateDeterministicToolCallID("root", "turn", 1, b.Name, 0), dispatched.ToolCallID)
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", hash)
	}
	entries, err := index.Walk(t.Context(), source, eng.heads[len(eng.heads)-1])
	require.NoError(t, err)
	require.NotEmpty(t, entries[0].Calls)
	data, err := source(t.Context(), entries[0].Calls[0])
	require.NoError(t, err)
	var stored planner.ToolRequest
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, dispatched, stored)
}

func TestPreChildValidatorLoadsParentIndexWithoutRetainingMessages(t *testing.T) {
	rt := New(WithEngine(engineinmem.New()))
	called := false
	reg := NewAgentToolsetRegistration(rt, AgentToolConfig{AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child", DefaultTaskQueue: "child"}, PreChildValidator: func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
		called = true
		messages, err := in.View.LoadMessages(ctx)
		require.NoError(t, err)
		assertIndexMessages(t, []*model.Message{contentMessage("parent evidence"), {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "child-call", Name: "child.lookup", Input: rawjson.Message(`{}`)}}}}, messages)
		return NewAgentToolValidationError("declined", []*tools.FieldIssue{{Field: "sources", Constraint: "invalid_format"}}, map[string]string{"sources": "declined"})
	}})
	reg.Name = "child"
	spec := newAnyJSONSpec("child.lookup", "child")
	spec.IsAgentTool = true
	reg.Specs = []tools.ToolSpec{spec}
	require.NoError(t, rt.RegisterToolset(reg))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "child-call", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("parent evidence")}, WithRunID("root"))
	require.NoError(t, err)
	assert.True(t, called)
}

func TestFinalizationCallsStoredAfterCanonicalPolicyMetadata(t *testing.T) {
	eng := &indexCaptureEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng))
	domain := newAnyJSONSpec("test.read", "test")
	terminal := bookkeepingSpec("test.finish", true)
	terminal.Toolset = "test"
	var dispatched planner.ToolRequest
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{domain, terminal}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		if call.Name == terminal.Name {
			dispatched = *call
		}
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}}), nil
	}}))
	next := func() *planner.PlanResult {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: domain.Name, Payload: rawjson.Message(`{}`)}}}
	}
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return next(), nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		if in.Finalize == nil {
			return next(), nil
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: terminal.Name, ToolCallID: "planner-id", Payload: rawjson.Message(`{}`), Labels: map[string]string{FinalizationReasonLabel: "forged"}}}}, nil
	}})
	reg := rt.agents["content.agent"]
	reg.Specs = []tools.ToolSpec{domain, terminal}
	rt.agents[reg.ID] = reg
	_, err := rt.MustClient(reg.ID).Run(t.Context(), "session", nil, WithRunID("root"), WithTurnID("turn"), WithRunMaxToolCalls(1))
	require.NoError(t, err)
	last := eng.outputs[len(eng.outputs)-1]
	require.Len(t, last.ToolCallRefs, 1)
	data := testStoredBytes(t, rt, "root", last.ToolCallRefs[0])
	var stored planner.ToolRequest
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, dispatched, stored)
	assert.NotEqual(t, "planner-id", stored.ToolCallID)
	assert.Equal(t, string(planner.TerminationReasonToolCap), stored.Labels[FinalizationReasonLabel])
}

func TestRecoveryCorrectionContentRejectedBeforeStorage(t *testing.T) {
	rt := New()
	for _, text := range []string{" ", string([]byte{0xff}), strings.Repeat("c", maxRecoveryCorrectionBytes+1)} {
		_, err := rt.storePlanActivityOutput(t.Context(), "root", &PlanActivityOutput{Recovery: &ModelRecovery{Correction: text}}, nil, nil)
		require.ErrorContains(t, err, "correction must be")
	}
}

func TestStructuralTextOverInlineLimitUsesRecordContent(t *testing.T) {
	eng := &indexCaptureEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng))
	spec := newAnyJSONSpec("test.error", "test")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		t.Error("request errors must not execute")
		return nil, nil
	}}))
	text := strings.Repeat("large-error ", 2000)
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "error", Error: planner.NewToolError(text)}}}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		assertIndexMessages(t, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "error", Name: string(spec.Name)}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "error", Content: text, IsError: true}}}}, in.Messages)
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
	require.NoError(t, err)
	in := eng.inputs[1]
	data, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Less(t, len(data), 4096+400*len(in.View.Pending)+100)
	require.Len(t, in.View.Pending, 2)
	structural := in.View.Pending[1].Structural
	require.NotNil(t, structural)
	require.Len(t, structural.Text, 1)
	assert.Empty(t, structural.Text[0].Text)
	assert.Equal(t, text, string(testStoredBytes(t, rt, "root", structural.Text[0].Hash)))
}
