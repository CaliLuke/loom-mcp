package temporal

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestProvidedToolResultContractSurvivesTemporalConversion(t *testing.T) {
	rt := runtime.New()
	spec := tools.ToolSpec{Name: "test.lookup", Toolset: "test"}
	require.NoError(t, rt.RegisterToolset(runtime.ToolsetRegistration{
		Name: "test", Specs: []tools.ToolSpec{spec},
		Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			panic("provided results must not execute the tool")
		},
	}))
	ref, err := rt.RunStore.Put(t.Context(), "root", []byte("null"))
	require.NoError(t, err)
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{
		RootRunID: "root",
		Provided: &api.ProvidedContent{
			Calls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call"}},
			Results: []*api.ProvidedToolResult{{
				Name: spec.Name, ToolCallID: "call", Refs: tools.ResultRefs{Result: ref},
				Error:     planner.NewToolError("missing topic"),
				RetryHint: &planner.RetryHint{Reason: planner.RetryReasonMissingFields},
			}},
		},
	})
	assert.Nil(t, out)
	var contract *engine.ApplicationError
	require.ErrorAs(t, err, &contract)
	assert.Same(t, contract, err, "the activity must return the typed root to the adapter")

	// This is the same outbound conversion used by RegisterRecordContentActivity.
	converter := temporal.GetDefaultFailureConverter()
	failure := converter.ErrorToFailure(classifyTemporalCancellation(err))
	application := failure.GetApplicationFailureInfo()
	require.NotNil(t, application)
	assert.Equal(t, runtime.ToolResultContractErrorType, application.GetType())
	assert.True(t, application.GetNonRetryable())
	var received *engine.ApplicationError
	require.ErrorAs(t, normalizeTemporalError(converter.FailureToError(failure)), &received)
	assert.Equal(t, runtime.ToolResultContractErrorType, received.Type)
	assert.True(t, received.NonRetryable)
}
