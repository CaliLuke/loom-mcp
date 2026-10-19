package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCallPolicyRecordedResolution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		defaultCap int
		override   *int
		want       int
		invalid    bool
	}{
		{name: "unset"}, {name: "default", defaultCap: 3, want: 3},
		{name: "override", defaultCap: 3, override: new(2), want: 2},
		{name: "zero rejected", override: new(0), invalid: true},
		{name: "negative rejected", override: new(-1), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := newCallInfoRuntime(t, func(context.Context, planner.PlannerContext) error { return nil })
			reg := rt.agents["svc.agent"]
			reg.Policy.MaxModelCalls = tc.defaultCap
			rt.agents[reg.ID] = reg
			message, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "seeded"}}})
			require.NoError(t, err)
			out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", ModelCallPolicy: &api.ModelCallPolicyInput{AgentID: reg.ID, MaxModelCalls: tc.override}, Items: [][]byte{message}, SeedMessages: true})
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out.MaxModelCalls)
			assert.Equal(t, tc.want, *out.MaxModelCalls)
			assert.NotEmpty(t, out.IndexHead, "policy resolution must not skip initial content recording")
			assert.Len(t, out.Entries, 1)
		})
	}
}

func TestModelCallPolicyOverrideWirePresence(t *testing.T) {
	for _, cap := range []*int{nil, new(0), new(-1), new(3)} {
		value := PolicyOverrides{MaxModelCalls: cap}
		data, err := json.Marshal(value)
		require.NoError(t, err)
		var restored PolicyOverrides
		require.NoError(t, json.Unmarshal(data, &restored))
		assert.Equal(t, cap, restored.MaxModelCalls)
	}
}

func TestModelCallPolicyChildUsesOwnDefault(t *testing.T) {
	rt := New(WithoutContextDefault("parent"), WithoutContextDefault("child"))
	rt.agents["parent"] = AgentRegistration{ID: "parent", Policy: RunPolicy{MaxModelCalls: 5}}
	rt.agents["child"] = AgentRegistration{ID: "child", Policy: RunPolicy{MaxModelCalls: 3}}
	parent := &RunInput{AgentID: "parent", RunID: "parent-run", RootRunID: "parent-run", Policy: &PolicyOverrides{MaxModelCalls: new(2)}}
	wf := &testWorkflowContext{ctx: t.Context(), runtime: rt}
	resolvedParent, err := seedWorkflowIndex(wf, parent)
	require.NoError(t, err)
	require.NotNil(t, resolvedParent.MaxModelCalls)
	assert.Equal(t, 2, *resolvedParent.MaxModelCalls)
	nested := buildNestedAgentRunContext(planner.ToolRequest{AgentID: parent.AgentID, RunID: parent.RunID, RootRunID: parent.RootRunID, Name: "child.run", ToolCallID: "delegate"})
	child := buildInlineAgentRunInput("child", nested, nil)
	assert.Nil(t, child.Policy, "child dispatch must not inherit the parent's explicit override")
	assert.Equal(t, parent.RunID, child.ParentRunID)
	resolvedChild, err := seedWorkflowIndex(wf, &child)
	require.NoError(t, err)
	require.NotNil(t, resolvedChild.MaxModelCalls)
	assert.Equal(t, 3, *resolvedChild.MaxModelCalls, "child worker registration owns its admission allowance")
}
