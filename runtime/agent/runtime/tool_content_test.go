package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	runstoreinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingContentStore struct{ runstore.Store }

func TestToolContentPutFailureReturnsNoOutput(t *testing.T) {
	rt := New(WithRunStore(failingContentStore{runstoreinmem.New()}))
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"tool": newAnyJSONSpec("tool", "svc.ts")}
	rt.toolsets["svc.ts"] = ToolsetRegistration{Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: "tool", Result: map[string]any{"ok": true}}), nil
	}}
	input := ToolInput{RunID: "child", RootRunID: "root", ToolName: "tool", Payload: rawjson.Message(`{}`)}
	out, err := rt.ExecuteToolActivity(t.Context(), &input)
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	assert.Nil(t, out)
}

func TestWorkflowRequiresRootRunID(t *testing.T) {
	err := validateWorkflowInput(&RunInput{AgentID: "svc.agent", RunID: "root", WorkflowProtocolVersion: api.WorkflowProtocolVersion})
	assert.ErrorContains(t, err, "root run id")
}

func TestActivityContentReferencesSurviveEnvelopes(t *testing.T) {
	rt := New()
	spec := newAnyJSONSpec("tool", "svc.ts")
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"tool": spec}
	rt.toolsets["svc.ts"] = ToolsetRegistration{Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: "tool", Result: map[string]any{"ok": true}, ServerData: rawjson.Message(`{"private":true}`)}), nil
	}}
	out, err := rt.ExecuteToolActivity(t.Context(), &ToolInput{RootRunID: "root", RunID: "child", ToolName: "tool", Payload: rawjson.Message(`{"input":1}`)})
	require.NoError(t, err)
	call := planner.ToolRequest{Name: "tool", ToolCallID: "call"}
	result, err := (&toolBatchExec{r: rt}).decodeActivityToolResult(t.Context(), futureInfo{call: call}, out)
	require.NoError(t, err)
	assert.Equal(t, out.Refs, result.Refs)
	require.NotEmpty(t, result.Refs.Result)
	for _, hash := range []runstore.Hash{out.Refs.Result, out.Refs.ServerData, out.Refs.Payload} {
		require.NotEmpty(t, hash)
		_, err := rt.RunStore.Get(t.Context(), "root", hash)
		require.NoError(t, err)
	}
	events, err := rt.encodeToolEvents(t.Context(), []*planner.ToolResult{result})
	require.NoError(t, err)
	assert.Equal(t, out.Refs, events[0].Refs)
	planning, err := rt.encodeToolEventsForPlanning(t.Context(), []*planner.ToolResult{result})
	require.NoError(t, err)
	assert.Equal(t, out.Refs, planning[0].Refs)
	outputs, err := rt.buildPlannerToolOutputs(t.Context(), []planner.ToolRequest{call}, []*planner.ToolResult{result})
	require.NoError(t, err)
	encoded, err := encodePlannerToolOutputs(outputs)
	require.NoError(t, err)
	decoded, err := rt.decodeToolOutputs(encoded)
	require.NoError(t, err)
	assert.Equal(t, out.Refs, decoded[0].Refs)
	assert.Equal(t, outputs[0].Result, decoded[0].Result)
}

func TestActivityErrorWithoutResultStoresNoEligibleContent(t *testing.T) {
	rt := New()
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"tool": newAnyJSONSpec("tool", "svc.ts")}
	rt.toolsets["svc.ts"] = ToolsetRegistration{Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: "tool", Error: planner.NewToolError("failed")}), nil
	}}
	out, err := rt.ExecuteToolActivity(t.Context(), &ToolInput{RootRunID: "root", ToolName: "tool"})
	require.NoError(t, err)
	assert.Empty(t, out.Refs.Result)
	assert.Empty(t, out.Refs.ServerData)
	assert.Empty(t, out.Payload)
	payload, err := rt.RunStore.Get(t.Context(), "root", out.Refs.Payload)
	require.NoError(t, err)
	assert.Equal(t, "null", string(payload))
}

func TestToolPayloadRecording(t *testing.T) {
	// Calls outside activity execution cannot create workflow-side storage effects.
	require.NoError(t, RecordToolPayload(t.Context(), make(chan int)))
	recorder := &toolPayloadRecorder{}
	ctx := context.WithValue(t.Context(), toolPayloadRecorderKey{}, recorder)
	payload := map[string]string{"private": "original"}
	require.NoError(t, RecordToolPayload(ctx, payload))
	payload["private"] = "mutated"
	assert.JSONEq(t, `{"private":"original"}`, string(recorder.payload))
	assert.ErrorContains(t, RecordToolPayload(ctx, make(chan int)), "encode executed tool payload")
}

func TestLoadToolResultFailures(t *testing.T) {
	store := runstoreinmem.New()
	hash, err := store.Put(t.Context(), "root", []byte(`"wrong type"`))
	require.NoError(t, err)
	spec := newProjectedResultSpec()
	for _, tc := range []struct {
		name  string
		store runstore.Store
		spec  tools.ToolSpec
		hash  runstore.Hash
		want  string
	}{
		{"empty hash", store, spec, "", "empty hash"},
		{"missing content", store, spec, "sha256:missing", "runstore content not found"},
		{"missing store", nil, spec, hash, "run store is required"},
		{"missing codec", store, tools.ToolSpec{Name: "tool"}, hash, "no result codec"},
		{"codec rejection", store, spec, hash, "decode stored tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := LoadToolResult(t.Context(), tc.store, "root", tc.spec, tc.hash)
			assert.Nil(t, value)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func (s failingContentStore) Put(context.Context, string, []byte) (runstore.Hash, error) {
	return "", runstore.ErrContentTooLarge
}
