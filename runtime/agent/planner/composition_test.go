package planner

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestSequentialWorkflowPlannerRunsStepsInOrder(t *testing.T) {
	p := NewSequentialWorkflowPlanner(SequentialWorkflowConfig{
		Steps: []WorkflowStep{
			{Name: "draft", Tool: "writer.draft", Payload: rawjson.Message([]byte(`{"topic":"loom"}`))},
			{Name: "review", Tool: "reviewer.review", Payload: rawjson.Message([]byte(`{"level":"strict"}`))},
		},
		FinalMessage: "workflow complete",
	})

	first, err := p.PlanStart(context.Background(), &PlanInput{})
	require.NoError(t, err)
	require.Len(t, first.ToolCalls, 1)
	require.Equal(t, tools.Ident("writer.draft"), first.ToolCalls[0].Name)
	require.Equal(t, "draft", first.ToolCalls[0].ToolCallID)
	require.JSONEq(t, `{"topic":"loom"}`, string(first.ToolCalls[0].Payload))

	second, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft"}},
	})
	require.NoError(t, err)
	require.Len(t, second.ToolCalls, 1)
	require.Equal(t, tools.Ident("reviewer.review"), second.ToolCalls[0].Name)
	require.Equal(t, "review", second.ToolCalls[0].ToolCallID)

	final, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft"}, {ToolCallID: "review"}},
	})
	require.NoError(t, err)
	require.NotNil(t, final.FinalResponse)
	require.Equal(t, model.ConversationRoleAssistant, final.FinalResponse.Message.Role)
	require.Equal(t, []model.Part{model.TextPart{Text: "workflow complete"}}, final.FinalResponse.Message.Parts)
}

func TestSequentialWorkflowPlannerStopsOnFailedToolOutput(t *testing.T) {
	p := NewSequentialWorkflowPlanner(SequentialWorkflowConfig{
		Steps: []WorkflowStep{
			{Name: "draft", Tool: "writer.draft", Payload: rawjson.Message([]byte(`{}`))},
			{Name: "review", Tool: "reviewer.review", Payload: rawjson.Message([]byte(`{}`))},
		},
	})

	result, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft", Error: NewToolError("boom")}},
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, `workflow step "draft" failed: boom`)
}
