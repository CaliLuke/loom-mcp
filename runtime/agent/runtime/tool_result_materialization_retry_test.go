package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteToolActivityMaterializerErrorStaysRetryable(t *testing.T) {
	failure := errors.New("temporary enrichment service failure")
	rt := New()
	spec := newAnyJSONSpec("test.lookup", "test")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
		Name: "test", Specs: []tools.ToolSpec{spec},
		Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return Executed(&planner.ToolResult{Name: call.Name, Result: map[string]any{"ok": true}}), nil
		},
		ResultMaterializer: func(context.Context, ToolCallMeta, *planner.ToolRequest, *planner.ToolResult) error {
			return failure
		},
	}))
	out, err := rt.ExecuteToolActivity(t.Context(), &ToolInput{
		RootRunID: "root", RunID: "root", ToolName: spec.Name, ToolCallID: "call", Payload: []byte(`{}`),
	})
	assert.Nil(t, out)
	require.ErrorIs(t, err, failure)
	require.EqualError(t, err, "materialize test.lookup tool result: temporary enrichment service failure")
	// The Temporal adapter leaves plain wrapped errors on its ordinary retryable
	// path. Execution materializers may perform I/O and must retain that policy.
	var application *engine.ApplicationError
	assert.NotErrorAs(t, err, &application, "execution materializer errors must not become non-retryable contract failures")
	assert.Same(t, failure, errors.Unwrap(err))
}

func TestRecordProvidedContentClassifiesMaterializerError(t *testing.T) {
	failure := errors.New("invalid provided enrichment")
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	spec := providedValidationSpec()
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
		Name: "test", Specs: []tools.ToolSpec{spec},
		Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
			panic("provided results must not execute the tool")
		},
		ResultMaterializer: func(context.Context, ToolCallMeta, *planner.ToolRequest, *planner.ToolResult) error {
			return failure
		},
	}))
	ref, err := rt.RunStore.Put(t.Context(), "root", []byte(`{"answer":"yes"}`))
	require.NoError(t, err)
	before := store.snapshot()
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{
		RootRunID: "root",
		Provided: &api.ProvidedContent{
			Calls:   []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call"}},
			Results: []*api.ProvidedToolResult{{Name: spec.Name, ToolCallID: "call", Refs: tools.ResultRefs{Result: ref}}},
		},
	})
	assert.Nil(t, out)
	require.ErrorIs(t, err, failure)
	assertToolResultContractError(t, err)
	assert.Equal(t, before, store.snapshot(), "invalid materialization must fail before content writes")
}
