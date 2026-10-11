package runtime

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecallStampedSettings(t *testing.T) {
	rt := New(WithContextCompaction("agent", ContextCompaction{RecallDiscoveryCalls: 3, RecallReadCalls: 1}), WithContextRecovery("agent", ContextRecovery{DisableRecall: true}))
	input := RunInput{AgentID: "agent"}
	rt.stampContextCompaction(&input)

	assert.True(t, input.ContextRecovery.DisableRecall)
	assert.Equal(t, 3, input.ContextCompaction.RecallDiscoveryCalls)
	assert.Equal(t, 1, input.ContextCompaction.RecallReadCalls)
	assert.Panics(t, func() {
		New(WithContextCompaction("agent", ContextCompaction{RecallReadCalls: -1}))
	})
}

func TestRecallAdvertisingStickyAndDisabled(t *testing.T) {
	base := workflowPlanInput{compaction: &ContextCompaction{}}
	head := runstore.Hash("checkpoint")
	input := PlanActivityInput{View: index.ViewRef{Checkpoint: &head}, ToolPolicyActive: true}
	prepareRecall(&base, &input)
	assert.Contains(t, input.AllowedTools, tools.RecallContext)
	require.Len(t, input.ExtraTools, 1)
	input = PlanActivityInput{ToolPolicyActive: true}
	prepareRecall(&base, &input)
	assert.Contains(t, input.AllowedTools, tools.RecallContext)
	input = PlanActivityInput{Recovery: &api.ModelRecovery{DisableTools: true}}
	prepareRecall(&base, &input)
	assert.Empty(t, input.ExtraTools)
	base.recovery.DisableRecall = true
	input = PlanActivityInput{View: index.ViewRef{Checkpoint: &head}}
	prepareRecall(&base, &input)
	assert.Empty(t, input.ExtraTools)
}

func TestRecallChargeCallOrderAndDiscoveryIndependence(t *testing.T) {
	base := workflowPlanInput{recallAdvertised: true, recallAvailable: true, compactionState: compaction.State{Recall: compaction.NewRecall(compaction.Settings{RecallDiscoveryCalls: 2, RecallReadCalls: 1})}}
	calls := []planner.ToolRequest{
		{Name: tools.RecallContext, ToolCallID: "one", Payload: rawjson.Message(`{"list":{"handle":"rc-bad"}}`)},
		{Name: tools.RecallContext, ToolCallID: "two", Payload: rawjson.Message(`{"find":null}`)},
		{Name: tools.RecallContext, ToolCallID: "three", Payload: rawjson.Message(`{"list":{}}`)},
		{Name: tools.RecallContext, ToolCallID: "four", Payload: rawjson.Message(`{"read":{"id":"missing","part":"result","offset":0}}`)},
		{Name: tools.RecallContext, ToolCallID: "five", Payload: rawjson.Message(`{"read":null}`)},
	}
	chargeRecallCalls(&base, calls)
	assert.Equal(t, compaction.RecallAllowances{}, base.compactionState.Recall)
	assert.False(t, base.recallCharges["one"].exhausted)
	assert.False(t, base.recallCharges["two"].exhausted)
	assert.True(t, base.recallCharges["three"].exhausted)
	assert.Equal(t, 1, base.recallCharges["three"].otherRemaining)
	assert.False(t, base.recallCharges["four"].exhausted)
	assert.True(t, base.recallCharges["five"].exhausted)
	assert.Equal(t, 0, base.recallCharges["five"].otherRemaining)
	chargeRecallCalls(&base, calls)
	assert.Equal(t, compaction.RecallAllowances{}, base.compactionState.Recall, "charges are made once before dispatch grouping")
}

func TestRecallFixedEnvelopeBudget(t *testing.T) {
	base := workflowPlanInput{compaction: &ContextCompaction{}}
	input := PlanActivityInput{AgentID: "agent", RunID: "run", View: index.ViewRef{TailFrom: 2}, ToolPolicyActive: true, AllowedTools: []tools.Ident{"svc.call"}}
	prepareRecall(&base, &input)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(data), 4096, "the runtime-owned catalog must fit P2's fixed envelope")
}

func TestRecallGeneratedIDDoesNotExposeName(t *testing.T) {
	id := generateDeterministicToolCallID("run", "turn", 1, tools.RecallContext, 0)
	assert.NotContains(t, id, "recall_context")
	assert.Equal(t, id, generateDeterministicToolCallID("run", "turn", 1, tools.RecallContext, 0))
}
