package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"sync"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type recordViewPlanner struct {
	mu       sync.Mutex
	lastView []*model.Message
}

func testTemporalRunRecordWithoutHistory(t *testing.T, temporalClient client.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("real Temporal history deletion proof runs in test-payload-growth")
	}
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	plan := &recordViewPlanner{}
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: plan}))
	_, err := fx.rt.CreateSession(ctx, "sess-record-p1")
	require.NoError(t, err)
	out, err := coordinator.NewClient(fx.rt).Run(ctx, "sess-record-p1", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "P1 seed"}}}}, agentsruntime.WithRunID("run-record-p1"))
	require.NoError(t, err)
	plan.mu.Lock()
	live := append([]*model.Message(nil), plan.lastView...)
	plan.mu.Unlock()
	require.NotEmpty(t, live)
	// Observe the final assistant outside reconstruction, matching the seal's
	// trailing entry, then remove the actual worker and all workflow/history access.
	live = append(live, out.Final)
	expected := live

	// Abandon the workflow engine/history after observing the live view. Only the
	// store interfaces cross the reconstruction boundary; no history is fetched.
	require.NoError(t, eng.Close())
	record, err := runrecord.Read(ctx, fx.rt.RunStore, fx.rt.RunEventStore, out.RunID, out.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	assert.True(t, record.Runs[0].Sealed)
	assert.Equal(t, expected, record.Runs[0].Transcript)
	last := record.Runs[0].Entries[len(record.Runs[0].Entries)-1]
	require.NotEmpty(t, last.Hash)
	data, err := fx.rt.RunStore.Get(ctx, out.RunID, last.Hash)
	require.NoError(t, err)
	var sealedMessages []*model.Message
	require.NoError(t, json.Unmarshal(data, &sealedMessages))
	assert.Equal(t, []*model.Message{out.Final}, sealedMessages)
	exported, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(exported)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
}

func (p *recordViewPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: workflow.Draft, ToolCallID: "record-draft", Payload: rawjson.Message(`{"topic":"P1 durable record"}`)}}}, nil
}

func (p *recordViewPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	p.mu.Lock()
	p.lastView = input.Messages
	p.mu.Unlock()
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "P1 complete"}}}}}, nil
}
