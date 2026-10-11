package agentfeatures_test

import (
	"testing"

	workflowhelpers "example.com/agentfeatures/gen/features/agents/coordinator/workflow"
	delegatedhelpers "example.com/agentfeatures/gen/features/agents/specialist/agenttools/delegated"
	delegated "example.com/agentfeatures/gen/features/agents/specialist/exports/delegated"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedTypedToolReExports(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func(*testing.T)
	}{
		{"draft", typedPayloadCheck(workflowhelpers.DraftTool, workflow.SpecDraft)},
		{"finalize", typedPayloadCheck(workflowhelpers.FinalizeTool, workflow.SpecFinalize)},
		{"method echo", typedPayloadCheck(workflowhelpers.MethodEchoTool, workflow.SpecMethodEcho)},
		{"publish", typedPayloadCheck(workflowhelpers.PublishTool, workflow.SpecPublish)},
		{"retry", typedPayloadCheck(workflowhelpers.RetryTool, workflow.SpecRetry)},
		{"review", typedPayloadCheck(workflowhelpers.ReviewTool, workflow.SpecReview)},
		{"revise", typedPayloadCheck(workflowhelpers.ReviseTool, workflow.SpecRevise)},
		{"delegated summarize", typedPayloadCheck(delegatedhelpers.SummarizeTool, delegated.SpecSummarize)},
	} {
		t.Run(tc.name, tc.check)
	}
}

// typedPayloadCheck verifies helper descriptors preserve their owner codecs and
// identifier while retaining concrete payload and result types at compile time.
func typedPayloadCheck[P, R any](descriptor tools.TypedTool[P, R], spec tools.ToolSpec) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		var named tools.Named = descriptor
		assert.Equal(t, spec.Name, named.Ident())
		assert.Equal(t, spec.Name, descriptor.Name)
		require.NotNil(t, descriptor.Result.ToJSON)
		require.NotNil(t, descriptor.Result.FromJSON)
		input := spec.Payload.ExampleJSON
		if len(input) == 0 {
			input = []byte("{}")
		}
		payload, err := descriptor.Payload.FromJSON(input)
		require.NoError(t, err)
		encoded, err := descriptor.Payload.ToJSON(payload)
		require.NoError(t, err)
		roundTrip, err := descriptor.Payload.FromJSON(encoded)
		require.NoError(t, err)
		assert.Equal(t, payload, roundTrip)
	}
}
