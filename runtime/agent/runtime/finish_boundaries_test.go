package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinishRunRejectsInvalidTerminalContent(t *testing.T) {
	tests := []struct {
		name  string
		input *api.FinishRunInput
		want  string
	}{
		{"nil", nil, "root run id is required"},
		{"missing root", &api.FinishRunInput{}, "root run id is required"},
		{"invalid event json", &api.FinishRunInput{RootRunID: "root", ToolEvents: []*api.ToolEvent{{Result: rawjson.Message("{")}}}, "encode run tool events"},
		{"invalid final message", &api.FinishRunInput{RootRunID: "root", Final: &model.Message{Role: model.ConversationRoleAssistant, Meta: map[string]any{"invalid": make(chan int)}}}, "encode final message"},
		{"missing final result spec", &api.FinishRunInput{RootRunID: "root", FinalToolResult: &api.ToolEvent{Name: "unknown", Refs: tools.ResultRefs{Result: "hash"}}}, "result spec not found"},
		{"missing server data", &api.FinishRunInput{RootRunID: "root", FinalToolResult: &api.ToolEvent{Name: "tool", Refs: tools.ResultRefs{ServerData: "absent"}}}, "load final tool server data"},
		{"invalid final tool json", &api.FinishRunInput{RootRunID: "root", FinalToolResult: &api.ToolEvent{Result: rawjson.Message("{")}}, "encode final tool result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := New()
			out, err := rt.FinishRunActivity(t.Context(), tt.input)
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, out)
		})
	}
	rt := New()
	rt.RunStore = nil
	out, err := rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root"})
	require.ErrorContains(t, err, "run store is required")
	assert.Nil(t, out)
}

func TestFinishRunResolvesServerDataWithoutMutatingEvent(t *testing.T) {
	rt := New()
	hash, err := rt.RunStore.Put(t.Context(), "root", []byte(`{"private":"retained"}`))
	require.NoError(t, err)
	event := &api.ToolEvent{Name: "tool", Refs: tools.ResultRefs{ServerData: hash}}
	output, err := rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "root", FinalToolResult: event})
	require.NoError(t, err)
	require.NotNil(t, output.FinalToolResult)
	assert.JSONEq(t, `{"private":"retained"}`, string(output.FinalToolResult.ServerData))
	assert.Empty(t, event.ServerData)
}

func TestRecordContentRejectsInvalidSeedAndCompatibilityScope(t *testing.T) {
	rt := New()
	for _, data := range []string{"{", `{"Role":"user","Parts":[{"type":"unknown"}]}`} {
		output, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", Items: [][]byte{[]byte(data)}, SeedMessages: true})
		require.ErrorContains(t, err, "seed message")
		assert.Nil(t, output)
	}
	output, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", Compatibility: &contextcompat.Request{Scope: "other"}})
	require.ErrorContains(t, err, "root scope mismatch")
	assert.Nil(t, output)
	_, err = rt.RunStore.Get(t.Context(), "other", runstore.Hash("absent"))
	assert.ErrorIs(t, err, runstore.ErrNotFound)
}
