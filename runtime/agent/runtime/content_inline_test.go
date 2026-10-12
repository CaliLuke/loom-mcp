package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentInlineResultBeforeResume(t *testing.T) {
	for _, failAt := range []int{0, 4, 5} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			store := &contentPathStore{Store: storeinmem.New(), failAt: failAt}
			rt := New(WithRunStore(store))
			spec := newAnyJSONSpec("inline.result", "inline")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "inline", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}, ServerData: rawjson.Message(`{"private":true}`)}), nil
			}}))
			resumed := false
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "inline", Payload: rawjson.Message(`{}`)}}}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				resumed = true
				require.Len(t, in.ToolOutputs, 1)
				result := in.ToolOutputs[0]
				require.NotEmpty(t, result.Refs.Result)
				data, err := store.Get(t.Context(), "root", result.Refs.Result)
				require.NoError(t, err)
				assert.JSONEq(t, `{"ok":true}`, string(data))
				data, err = store.Get(t.Context(), "root", result.Refs.ServerData)
				require.NoError(t, err)
				assert.JSONEq(t, `{"private":true}`, string(data))
				assert.Len(t, store.snapshot(), 7)
				return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant}}}, nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
			if failAt > 0 {
				require.ErrorIs(t, err, runstore.ErrContentTooLarge)
				assert.False(t, resumed)
			} else {
				require.NoError(t, err)
				assert.True(t, resumed)
			}
		})
	}
}

func TestContentErrorOnlyInlineDoesNotStoreResult(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	spec := newAnyJSONSpec("inline.error", "inline")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "inline", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("failed")}), nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "error", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		require.Len(t, in.ToolOutputs, 1)
		assert.Empty(t, in.ToolOutputs[0].Refs.Result)
		assert.Len(t, store.snapshot(), 5, "pinned prompt, settings, call, output metadata, and segment")
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
	require.NoError(t, err)
}
