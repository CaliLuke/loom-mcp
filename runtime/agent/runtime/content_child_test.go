package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentChildOutputUsesRootScope(t *testing.T) {
	for _, typed := range []bool{false, true} {
		// Writes: parent call, child input, child events, child Final, parent result.
		for _, failAt := range []int{0, 5, 6} {
			t.Run(fmt.Sprintf("typed=%v/fail=%d", typed, failAt), func(t *testing.T) {
				store := &contentPathStore{Store: storeinmem.New(), failAt: func() int {
					if typed && failAt > 0 {
						return failAt - 1
					}
					return failAt
				}()}
				rt := New(WithRunStore(store))
				var childOutput *api.RunOutput
				require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{
					ID: "child.agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
						root, err := rt.Engine.QueryRootRunID(ctx, in.RunContext.RunID)
						require.NoError(t, err)
						assert.Equal(t, "root", root)
						result := &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("child final")}}
						if typed {
							result.FinalResponse = nil
							result.FinalToolResult = &planner.FinalToolResult{Result: rawjson.Message(`{"answer":"typed child"}`)}
						}
						return result, nil
					}}, Workflow: engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "child.queue", Handler: func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
						out, err := rt.ExecuteWorkflow(w, in)
						childOutput = out
						return out, err
					}}, PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool",
				}))
				registration := NewAgentToolsetRegistration(rt, AgentToolConfig{Name: "delegate", AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"}})
				spec := newAnyJSONSpec("delegate.child", "delegate")
				spec.IsAgentTool = true
				spec.AgentID = "child.agent"
				registration.Specs = append(registration.Specs, spec)
				require.NoError(t, rt.RegisterToolset(registration))
				resumed := false
				registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
				}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
					resumed = true
					require.Len(t, in.ToolOutputs, 1)
					if failAt == 5 {
						assert.Nil(t, childOutput)
						assert.NotNil(t, in.ToolOutputs[0].Error)
						assert.Empty(t, in.ToolOutputs[0].Refs.Result)
					} else {
						require.NotNil(t, childOutput)
						if typed {
							assert.Empty(t, childOutput.FinalRef)
						} else {
							require.NotEmpty(t, childOutput.FinalRef)
							data, err := store.Get(t.Context(), "root", childOutput.FinalRef)
							require.NoError(t, err)
							want, err := json.Marshal(childOutput.Final)
							require.NoError(t, err)
							assert.JSONEq(t, string(want), string(data))
						}

						ref := in.ToolOutputs[0].Refs.Result
						require.NotEmpty(t, ref)
						data, err := store.Get(t.Context(), "root", ref)
						require.NoError(t, err)
						if typed {
							assert.JSONEq(t, `{"answer":"typed child"}`, string(data))
						} else {
							assert.JSONEq(t, `"child final"`, string(data))
						}
						for _, w := range store.snapshot() {
							assert.Equal(t, "root", w.scope)
						}
					}
					return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("parent final")}}, nil
				}})
				_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
				if failAt == 6 {
					require.ErrorIs(t, err, runstore.ErrContentTooLarge)
					assert.False(t, resumed)
				} else {
					require.NoError(t, err)
					assert.True(t, resumed)
				}
			})
		}
	}
}
