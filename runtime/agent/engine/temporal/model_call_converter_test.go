package temporal

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCallPolicySurvivesTemporalConverter(t *testing.T) {
	limit := &policy.ModelCallLimit{Limit: 3, Call: runstore.CallKey{RunID: "run", Seq: 4, Attempt: 2}}
	assertModelCallWireRoundTrip(t, &api.PlanActivityInput{AgentID: "agent", RunID: "run", MaxModelCalls: 3})
	assertModelCallWireRoundTrip(t, &api.PlanActivityOutput{ModelCallLimit: limit})
	assertModelCallWireRoundTrip(t, &api.RecordContentInput{RootRunID: "root", ModelCallPolicy: &api.ModelCallPolicyInput{AgentID: "agent", MaxModelCalls: new(3)}})
	assertModelCallWireRoundTrip(t, &api.RecordContentOutput{MaxModelCalls: new(3)})
	assertModelCallWireRoundTrip(t, &api.SummarizeContextInput{RunID: "run", MaxModelCalls: 3})
	assertModelCallWireRoundTrip(t, &api.SummarizeContextOutput{ModelCallLimit: limit})
}

func assertModelCallWireRoundTrip[T any](t *testing.T, input *T) {
	t.Helper()
	converter := NewAgentDataConverter(nil)
	payload, err := converter.ToPayload(input)
	require.NoError(t, err)
	var restored *T
	require.NoError(t, converter.FromPayload(payload, &restored))
	assert.Equal(t, input, restored)
}
