package runtime

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverflowNoProgressPrecedesBudgetShortfall(t *testing.T) {
	for _, bearing := range []bool{true, false} {
		t.Run("view_bearing="+strconv.FormatBool(bearing), func(t *testing.T) {
			failure := contextmeasure.ContextOverflow{Source: contextmeasure.OverflowNoProgress, Failed: contextmeasure.RequestFacts{Model: "small", ViewBearing: bearing, ContextWindow: 200}}
			step := overflow.Step{ProviderLeft: 1}
			decision := decideOverflow(compaction.State{}, nil, index.ViewRef{}, failure, compaction.Settings{}, &step, nil)
			assert.Equal(t, "failed:no_progress", decision.outcome)
			assert.Nil(t, decision.shortfall)
			assert.Nil(t, decision.recovery)
			assert.Equal(t, 1, step.ProviderLeft)
			assert.ErrorIs(t, decision.applicationError(PlanActivityInput{}, index.ViewRef{}, compaction.State{}, failure), ErrContextOverflowNoProgress)
		})
	}
}

func TestOverflowRetryAccountsForFirstRecallDefinition(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 2)
	for i, size := range []int{2500, 1800} {
		entry := &base.Entries[2+i*2]
		entry.Hash = compactionPut(t, rt.RunStore, strings.Repeat("x", size))
		entry.Bytes = size + len(entry.ToolUseIDs[0]) + 2
		entry.Result.ContentBytes = size + 2
		entry.Keep = i == 1
	}
	segment, err := index.NewSegment("", "", base.Entries)
	require.NoError(t, err)
	base.Entries = segment.Entries
	provider := &overflowProvider{window: 8000, realWindow: 8000, failFirst: true}
	require.NoError(t, rt.RegisterModel("fake", provider))
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := input.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: input.Messages})
		if err != nil {
			return nil, err
		}
		return oracleFinal(), nil
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	caps := policy.CapsState{}
	out, err := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Len(t, wf.plannerCalls, 2)
	require.Len(t, provider.sizes, 2)
	assert.Less(t, provider.sizes[1], provider.sizes[0])
	assert.True(t, base.recallAvailable)
	assert.Nil(t, base.compaction, "recall activates without threshold compaction")
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err, "durable facts must reproduce recovery")
	require.Len(t, record.Runs[0].Calls, 2)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
	assert.NotEmpty(t, record.Runs[0].Calls[0].Record.Key)
}
