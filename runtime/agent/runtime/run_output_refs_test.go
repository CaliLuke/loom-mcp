package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type finishRecordingEngine struct {
	engine.Engine
	queue string
	calls int
	input *api.FinishRunInput
}

func (e *finishRecordingEngine) RegisterFinishRunActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error)) error {
	e.queue = opts.Queue
	return e.Engine.RegisterFinishRunActivity(ctx, name, opts, func(ctx context.Context, input *api.FinishRunInput) (*api.FinishRunOutput, error) {
		e.calls++
		e.input = input
		return fn(ctx, input)
	})
}

func TestRunOutputConstantSize(t *testing.T) {
	var sizes []int
	for _, n := range []int{1, 50} {
		for _, size := range []int{1024, 512 * 1024} {
			t.Run(fmt.Sprintf("calls=%d/bytes=%d", n, size), func(t *testing.T) {
				eng := &finishRecordingEngine{Engine: engineinmem.New()}
				rt := New(WithEngine(eng))
				spec := newAnyJSONSpec("test.result", "test")
				spec.Bookkeeping = true
				spec.TerminalRun = true
				result := strings.Repeat("x", size)
				serverData := rawjson.Message(`{"private":"server-data"}`)
				require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					return Executed(&planner.ToolResult{Name: call.Name, Result: result, ServerData: serverData}), nil
				}}))
				calls := make([]planner.ToolRequest, n)
				for i := range calls {
					calls[i] = planner.ToolRequest{Name: spec.Name, ToolCallID: fmt.Sprintf("call-%d", i), Payload: rawjson.Message(`{}`)}
				}
				require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{
					ID: "test.agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
						return &planner.PlanResult{ToolCalls: calls}, nil
					}},
					Workflow:         engine.WorkflowDefinition{Name: "test.workflow", TaskQueue: "test.queue", Handler: rt.ExecuteWorkflow},
					PlanActivityName: "test.plan", ResumeActivityName: "test.resume", ExecuteToolActivity: "test.execute",
				}))
				_, err := rt.CreateSession(t.Context(), "session")
				require.NoError(t, err)
				out, err := rt.MustClient("test.agent").Run(t.Context(), "session", nil, WithRunID("root"))
				require.NoError(t, err)
				data, err := json.Marshal(out)
				require.NoError(t, err)
				assert.LessOrEqual(t, len(data), 1024)
				sizes = append(sizes, len(data))
				t.Logf("serialized RunOutput: %d bytes", len(data))
				assert.Equal(t, n, out.ToolEventCount)
				assert.Equal(t, 1, eng.calls)
				assert.Equal(t, "test.queue", eng.queue)
				assert.Equal(t, "root", eng.input.RootRunID)
				events, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", out.ToolEventsRef)
				require.NoError(t, err)
				require.Len(t, events, n)
				expected, err := json.Marshal(result)
				require.NoError(t, err)
				for _, event := range events {
					assert.Equal(t, expected, []byte(event.Result))
					assert.Equal(t, len(expected), event.ResultBytes)
					assert.Equal(t, serverData, event.ServerData)
				}
				stored, err := rt.RunStore.Get(t.Context(), "root", out.ToolEventsRef)
				require.NoError(t, err)
				var compact []*api.ToolEvent
				require.NoError(t, json.Unmarshal(stored, &compact))
				require.Len(t, compact, n)
				for _, event := range compact {
					assert.Empty(t, event.Result)
					assert.Empty(t, event.ServerData)
					assert.NotEmpty(t, event.Refs.Result)
					assert.NotEmpty(t, event.Refs.ServerData)
				}
			})
		}
	}
	for _, size := range sizes {
		assert.InDelta(t, sizes[0], size, 2)
	}
}

func TestPlannerToolOutputReferences(t *testing.T) {
	result := rawjson.Message(`{"result":"eligible-marker"}`)
	server := rawjson.Message(`{"private":"server-marker"}`)
	called := 0
	rt := newTestRuntimeWithPlanner("test.agent", &stubPlanner{resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
		called++
		require.Len(t, input.ToolOutputs, 1)
		assert.JSONEq(t, string(result), string(testLoadToolResult(t, ctx, input)))
		assert.JSONEq(t, string(server), string(testLoadServerData(t, ctx, input)))
		return &planner.PlanResult{FinalToolResult: &planner.FinalToolResult{Result: rawjson.Message(`{}`)}}, nil
	}})
	view := testOutputView(t, rt, []*api.ToolCallOutput{{Name: "test.result", ToolCallID: "call", Result: result, ServerData: server}})
	base := &workflowPlanInput{RunContext: run.Context{RunID: "child", RootRunID: "root"}, Entries: view.Pending}
	attempt := 1
	input, err := rt.buildNextResumeRequest("test.agent", base, &attempt)
	require.NoError(t, err)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "eligible-marker")
	assert.NotContains(t, string(data), "server-marker")
	out, err := rt.PlanResumeActivity(t.Context(), &input)
	require.NoError(t, err)
	base.adopt(out)
	finalInput, _, err := rt.buildFinalizePlanRequest(AgentRegistration{}, base, &RunInput{AgentID: "test.agent"}, policy.CapsState{}, 2, planner.TerminationReasonToolCap)
	require.NoError(t, err)
	_, err = rt.PlanResumeActivity(t.Context(), &finalInput)
	require.NoError(t, err)
	assert.Equal(t, 2, called)
}

func TestPlannerMissingToolOutputReference(t *testing.T) {
	rt := newTestRuntimeWithPlanner("test.agent", &stubPlanner{resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		t.Error("planner must not run with missing content")
		return &planner.PlanResult{}, nil
	}})
	view := testOutputView(t, rt, []*api.ToolCallOutput{{Name: "test.result", Refs: tools.ResultRefs{Result: "sha256:missing"}}})
	_, err := rt.PlanResumeActivity(t.Context(), &PlanActivityInput{AgentID: "test.agent", RunContext: run.Context{RootRunID: "root"}, View: view})
	require.ErrorIs(t, err, runstore.ErrNotFound)
}

func TestInlineToolOutputsExposeLoadableReferences(t *testing.T) {
	rt := New()
	spec := newAnyJSONSpec("inline.result", "inline")
	result := rawjson.Message(`{"inline":true}`)
	serverData := rawjson.Message(`{"private":true}`)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
		Name: "inline", Inline: true, Specs: []tools.ToolSpec{spec},
		Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"inline": true}, ServerData: serverData}), nil
		},
	}))
	pl := &stubPlanner{
		start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "inline-call", Payload: rawjson.Message(`{}`)}}}, nil
		},
		resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
			require.Len(t, in.ToolOutputs, 1)
			assert.NotEmpty(t, in.ToolOutputs[0].Refs.Result)
			assert.JSONEq(t, string(result), string(testLoadToolResult(t, t.Context(), in)))
			assert.JSONEq(t, string(serverData), string(testLoadServerData(t, t.Context(), in)))
			return &planner.PlanResult{FinalToolResult: &planner.FinalToolResult{Result: rawjson.Message(`"done"`)}}, nil
		},
	}
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "inline.agent", Planner: pl,
		Workflow:         engine.WorkflowDefinition{Name: "inline.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "inline.plan", ResumeActivityName: "inline.resume", ExecuteToolActivity: "inline.execute",
	}))
	_, err := rt.CreateSession(t.Context(), "session")
	require.NoError(t, err)
	out, err := rt.MustClient("inline.agent").Run(t.Context(), "session", nil, WithRunID("root"))
	require.NoError(t, err)
	events, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", out.ToolEventsRef)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].Refs.Result)
	assert.Equal(t, result, events[0].Result)
	assert.Equal(t, serverData, events[0].ServerData)
}
