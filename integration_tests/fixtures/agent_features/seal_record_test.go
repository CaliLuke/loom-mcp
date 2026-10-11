package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type externalTerminationPlanner struct {
	signalTimeoutPlanner
}

// PlanResume commits the first answer, then waits so external termination leaves two heads.
func (externalTerminationPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if input == nil || len(input.TypedInputs) != 1 {
		return nil, errors.New("external termination planner expected one typed input")
	}
	return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "after-resume", Title: "Wait for external termination", Schema: rawjson.Message(`{"type":"object"}`)}))}, nil
}

func testTemporalExternalTerminationLeavesReadableHead(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: externalTerminationPlanner{}}))
	_, err := fx.rt.CreateSession(ctx, "sess-terminated")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-terminated", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "termination seed"}}}}, agentsruntime.WithRunID("run-terminated"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.countForRun(hooks.AwaitTypedInput, "run-terminated") == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{RunID: "run-terminated", ID: "signal-race", Payload: rawjson.Message(`{"continue":true}`)}))
	require.Eventually(t, func() bool {
		return fx.recorder.countForRun(hooks.AwaitTypedInput, "run-terminated") == 2 &&
			fx.recorder.countForRun(hooks.PlannerStep, "run-terminated") >= 2
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, temporalClient.TerminateWorkflow(ctx, "run-terminated", "", "external termination"))
	_, err = handle.Wait(ctx)
	require.Error(t, err)
	page, err := fx.rt.RunEventStore.List(ctx, "run-terminated", "", 100)
	require.NoError(t, err)
	var latest *hooks.PlannerStepEvent
	var steps []hooks.PlannerStepEvent
	for _, event := range page.Events {
		assert.NotEqual(t, hooks.RunSealed, event.Type)
		if event.Type == hooks.PlannerStep {
			var step hooks.PlannerStepEvent
			require.NoError(t, json.Unmarshal(event.Payload, &step))
			latest = &step
			steps = append(steps, step)
		}
	}
	require.NotNil(t, latest)
	require.GreaterOrEqual(t, len(steps), 2)
	for _, step := range steps {
		require.NotEmpty(t, step.IndexHead)
	}
	assert.NotEqual(t, steps[0].IndexHead, steps[len(steps)-1].IndexHead)
	record, err := runrecord.Read(ctx, fx.rt.RunStore, fx.rt.RunEventStore, "run-terminated", "run-terminated")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	assert.False(t, record.Runs[0].Sealed)
	assert.Equal(t, latest.IndexHead, record.Runs[0].IndexHead)
	require.NotEmpty(t, latest.IndexHead)
	entries, err := index.Walk(ctx, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return fx.rt.RunStore.Get(ctx, "run-terminated", hash)
	}, latest.IndexHead)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2)
	data, err := fx.rt.RunStore.Get(ctx, "run-terminated", entries[0].Hash)
	require.NoError(t, err)
	var seed model.Message
	require.NoError(t, json.Unmarshal(data, &seed))
	assert.Equal(t, "termination seed", messageText(&seed))
}
