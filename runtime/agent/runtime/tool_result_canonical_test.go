package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRecordCanonicalResultsOnFailedBatch(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("rewrite=%t", rewrite), func(t *testing.T) {
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, in *BeforeEventInput) (*BeforeEventDecision, error) {
				if event, ok := in.Event.(*hooks.ToolResultReceivedEvent); ok {
					if rewrite {
						copy := *event
						copy.Refs = tools.ResultRefs{Result: "corrupted", ServerData: "corrupted"}
						return &BeforeEventDecision{Event: &copy}, nil
					}
					return &BeforeEventDecision{Drop: true}, nil
				}
				return nil, nil
			}}))
			first := newAnyJSONSpec("batch.canonical_first", "batch")
			second := newAnyJSONSpec("batch.canonical_second", "batch")
			data := strings.Repeat("x", 2<<20)
			server := rawjson.Message(`{"private":true}`)
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "batch", Inline: true, Specs: []tools.ToolSpec{first, second}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				if call.Name == second.Name {
					return nil, errors.New("later tool aborts batch")
				}
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]string{"text": data}, ServerData: server}), nil
			}}))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: first.Name, ToolCallID: "first", Payload: rawjson.Message(`{}`)}, {Name: second.Name, ToolCallID: "second", Payload: rawjson.Message(`{}`)}}}, nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
			require.ErrorContains(t, err, "later tool aborts batch")
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
			require.NoError(t, err)
			assert.True(t, record.Runs[0].Sealed)
			require.Len(t, record.Runs[0].Results, 1)
			assert.Contains(t, string(record.Runs[0].Results[0].Content), data)
			require.Len(t, record.Runs[0].ServerOnly, 1)
			assert.Equal(t, []byte(server), record.Runs[0].ServerOnly[0].Content)
			exported, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(exported)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

func TestRunRecordInlineErrorSidecarIsCanonical(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
		return &BeforeEventDecision{Drop: true}, nil
	}}))
	spec := newAnyJSONSpec("batch.canonical_error", "batch")
	server := rawjson.Message(`{"private":"error detail"}`)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "batch", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("failed"), ServerData: server}), nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
	require.NoError(t, err)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
	require.NoError(t, err)
	require.Len(t, record.Runs[0].ServerOnly, 1)
	assert.Equal(t, []byte(server), record.Runs[0].ServerOnly[0].Content)
}
