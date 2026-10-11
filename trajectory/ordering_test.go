package trajectory_test

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAssignsEventsToProducingStep(t *testing.T) {
	f := newFixture()
	first := f.call(t, 1, 1, runstore.CallCompleted, &model.Response{}, "", "")
	thinking := hooks.NewThinkingBlockEvent("run", "agent", "session", "first", "", nil, 0, true)
	thinking.SetPlannerStep(1)
	f.event(t, thinking, 1)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 1, index.ViewRef{}, []runstore.CallKey{first}, api.WorkflowProtocolVersion, time.Unix(2, 0)), 2)
	call := f.put(t, planner.ToolRequest{Name: "tool.read", ToolCallID: "call", Payload: rawjson.Message(`{}`)})
	result := f.putBytes(t, []byte(`{"whole":true}`))
	output := f.put(t, planner.ToolOutput{Name: "tool.read", ToolCallID: "call", Refs: tools.ResultRefs{Result: result}})
	head := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.Assistant, Calls: []runstore.Hash{call}, Call: &first},
		{ID: 2, Kind: index.ToolResult, Hash: result, Output: output, ToolUseIDs: []string{"call"}, Batch: 2},
		{ID: 3, Kind: index.Clarification, Hash: f.putBytes(t, []byte("clarification"))},
		{ID: 4, Kind: index.TypedInput, Hash: f.putBytes(t, []byte(`{"resume":true}`))},
	})
	second := f.call(t, 2, 1, runstore.CallCompleted, &model.Response{}, "", head)
	f.event(t, hooks.NewAwaitClarificationEvent("run", "agent", "session", "id", "clarify", nil, "", nil), 3)
	thinking = hooks.NewThinkingBlockEvent("run", "agent", "session", "second", "", nil, 0, true)
	thinking.SetPlannerStep(2)
	f.event(t, thinking, 3)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 2, index.ViewRef{IndexHead: head}, []runstore.CallKey{second}, api.WorkflowProtocolVersion, time.Unix(4, 0)), 4)
	third := f.call(t, 3, 1, runstore.CallCompleted, &model.Response{}, "", head)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 3, index.ViewRef{IndexHead: head}, []runstore.CallKey{third}, api.WorkflowProtocolVersion, time.Unix(5, 0)), 5)
	f.event(t, hooks.NewRunSealedEvent("run", head, api.WorkflowProtocolVersion, time.Unix(6, 0)), 6)
	rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	var inputSteps, eventSteps []int
	for _, step := range got.Steps {
		if step.Answer != nil {
			inputSteps = append(inputSteps, step.Step)
		}
		if step.Event != nil {
			eventSteps = append(eventSteps, step.Step)
		}
	}
	assert.Equal(t, []int{2, 2}, inputSteps)
	assert.Equal(t, []int{1, 1, 2}, eventSteps)
	require.Equal(t, []trajectory.StepKind{trajectory.StepModel, trajectory.StepTool, trajectory.StepEvent, trajectory.StepAwait, trajectory.StepModel, trajectory.StepAnswer, trajectory.StepAnswer, trajectory.StepEvent, trajectory.StepModel}, stepKinds(got.Steps))
	assert.Equal(t, hooks.ThinkingBlock, got.Steps[2].Event.Type)
	assert.Equal(t, hooks.AwaitClarification, got.Steps[3].Event.Type)
	assert.Equal(t, 1, got.Steps[3].Step)
	steps := recordSteps(got)
	require.Equal(t, []trajectory.StepKind{trajectory.StepModel, trajectory.StepTool, trajectory.StepModel, trajectory.StepModel}, stepKinds(steps))
	assert.Equal(t, []int{1, 1, 2, 3}, []int{steps[0].Step, steps[1].Step, steps[2].Step, steps[3].Step})
	assert.JSONEq(t, `{"whole":true}`, string(steps[1].Tool.Result))
}
