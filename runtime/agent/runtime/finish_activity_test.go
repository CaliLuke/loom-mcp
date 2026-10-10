package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinishRunKeepsUnreferencedContent(t *testing.T) {
	rt := New()
	event := &api.ToolEvent{Name: "inline", Result: rawjson.Message(`{"inline":true}`), ServerData: rawjson.Message(`{"private":true}`)}
	out, err := rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root", ToolEvents: []*api.ToolEvent{event}, FinalToolResult: event})
	require.NoError(t, err)
	assert.Equal(t, event, out.FinalToolResult)
	data, err := rt.RunStore.Get(t.Context(), "root", out.ToolEventsRef)
	require.NoError(t, err)
	var stored []*api.ToolEvent
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, []*api.ToolEvent{event}, stored)
	loaded, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", out.ToolEventsRef)
	require.NoError(t, err)
	assert.Equal(t, stored, loaded)
}

func TestLoadRunToolEventsFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"invalid list", "{}", "decode run tool events"},
		{"nil event", "[null]", "nil event"},
		{"missing result", `[{"Name":"tool","Refs":{"Result":"sha256:missing"}}]`, "load tool tool result"},
		{"missing server data", `[{"Name":"tool","Refs":{"ServerData":"sha256:missing"}}]`, "load tool tool server data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			ref, err := rt.RunStore.Put(t.Context(), "root", []byte(tc.data))
			require.NoError(t, err)
			events, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", ref)
			assert.Nil(t, events)
			assert.ErrorContains(t, err, tc.want)
		})
	}
	rt := New()
	_, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", "")
	require.ErrorIs(t, err, runstore.ErrNotFound)
	_, err = LoadRunToolEvents(t.Context(), rt.RunStore, "root", "sha256:missing")
	require.ErrorIs(t, err, runstore.ErrNotFound)
	_, err = LoadRunToolEvents(t.Context(), nil, "root", "sha256:missing")
	assert.ErrorContains(t, err, "run store is required")
}

func TestFinishRunFailureAndSummary(t *testing.T) {
	rt := New()
	rt.toolSpecs["tool"] = newAnyJSONSpec("tool", "test")
	_, err := rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root", FinalToolResult: &api.ToolEvent{Name: "tool", Refs: tools.ResultRefs{Result: "sha256:missing"}}})
	require.ErrorIs(t, err, runstore.ErrNotFound)
	rt.RunStore = failingContentStore{rt.RunStore}
	_, err = rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root"})
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	rt = New()
	out, err := rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root", ToolEvents: []*api.ToolEvent{{Error: planner.NewToolError("first")}, {Error: planner.NewToolError("last")}}})
	require.NoError(t, err)
	result := ConvertRunOutputToToolResult("parent", &RunOutput{ToolEventCount: out.ToolEventCount, ToolSummary: out.ToolSummary})
	assert.Equal(t, 2, result.ChildrenCount)
	assert.ErrorContains(t, result.Error, "all 2 nested tools failed")
}

func TestFinishPathsStoreEventsOnce(t *testing.T) {
	for _, path := range []string{"assistant", "planner final", "completion", "terminal", "canceled scope"} {
		t.Run(path, func(t *testing.T) {
			rt := New()
			rt.toolSpecs["tool"] = newAnyJSONSpec("tool", "test")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wf := &testWorkflowContext{ctx: ctx, runtime: rt}
			input := &RunInput{AgentID: "agent", RootRunID: "root"}
			base := testWorkflowInput(t, rt, &planner.PlanInput{RunContext: run.Context{RootRunID: "root", RunID: "child", Tool: "tool"}})
			st := &runLoopState{ToolEvents: []*planner.ToolResult{{Name: "tool", Result: "done"}}, Result: &planner.PlanResult{}}
			var out *RunOutput
			var err error
			switch path {
			case "assistant":
				st.Result.FinalResponse = &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant}}
				out, err = rt.finishWithoutToolCalls(wf.Context(), input, base, st, "")
			case "planner final":
				st.Result.FinalToolResult = &planner.FinalToolResult{Result: rawjson.Message(`"done"`)}
				out, err = rt.finishWithoutToolCalls(wf.Context(), input, base, st, "")
			case "completion":
				out, err = rt.finishAfterCompletionTool(wf.Context(), input, base, st, "", "tool")
			case "terminal":
				out, err = rt.finishAfterTerminalToolCalls(wf.Context(), input, base, st, "")
			case "canceled scope":
				cancel()
				finished, finishErr := rt.finishRun(wf.Context(), input, []*api.ToolEvent{{Name: "tool"}}, nil, nil)
				require.NoError(t, finishErr)
				out = &RunOutput{ToolEventsRef: finished.ToolEventsRef, ToolEventCount: finished.ToolEventCount}
			}
			require.NoError(t, err)
			require.NotNil(t, out)
			assert.Equal(t, 1, wf.finishCalls)
			assert.Equal(t, 1, out.ToolEventCount)
			events, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", out.ToolEventsRef)
			require.NoError(t, err)
			assert.Len(t, events, 1)
		})
	}
}

func TestFinishRunRejectsOversizedInlineFinal(t *testing.T) {
	rt := New()
	result, err := json.Marshal(strings.Repeat("x", 1_000_001))
	require.NoError(t, err)
	_, err = rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root", FinalToolResult: &api.ToolEvent{Result: rawjson.Message(result)}})
	assert.ErrorContains(t, err, "final tool result too large")
}
