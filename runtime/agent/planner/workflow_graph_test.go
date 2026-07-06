package planner

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/stretchr/testify/require"
)

func TestGraphWorkflowPlannerSchedulesParallelReadyNodes(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: rawjson.Message([]byte(`{"topic":"loom"}`))},
			{ID: "review", Kind: WorkflowNodeTool, Tool: "reviewer.review", Payload: rawjson.Message([]byte(`{"strict":true}`))},
		},
	})

	result, err := p.PlanStart(context.Background(), &PlanInput{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"draft", "review"}, toolCallIDs(result.ToolCalls))
}

func TestGraphWorkflowPlannerResumeDoesNotRerunCompletedParallelNode(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: rawjson.Message([]byte(`{"topic":"loom"}`))},
			{ID: "review", Kind: WorkflowNodeTool, Tool: "reviewer.review", Payload: rawjson.Message([]byte(`{"strict":true}`))},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", DependsOn: []string{"draft", "review"}, Payload: rawjson.Message([]byte(`{}`))},
		},
	})

	result, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft"}},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"review"}, toolCallIDs(result.ToolCalls))

	result, err = p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft"}, {ToolCallID: "review"}},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"publish"}, toolCallIDs(result.ToolCalls))
}

func TestGraphWorkflowPlannerLoopStopsAtMaxIterations(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{
				ID:   "retry",
				Kind: WorkflowNodeLoop,
				Loop: &WorkflowLoopConfig{
					Tool:          "worker.retry",
					Payload:       rawjson.Message([]byte(`{}`)),
					MaxIterations: 2,
				},
			},
		},
		FinalMessage: "done",
	})

	first, err := p.PlanStart(context.Background(), &PlanInput{})
	require.NoError(t, err)
	require.Equal(t, []string{"retry#1"}, toolCallIDs(first.ToolCalls))

	second, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"retry#2"}, toolCallIDs(second.ToolCalls))

	final, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1"}, {ToolCallID: "retry#2"}},
	})
	require.NoError(t, err)
	require.NotNil(t, final.FinalResponse)
}

func TestGraphWorkflowPlannerLoopDependentsWaitUntilLoopDone(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{
				ID:   "retry",
				Kind: WorkflowNodeLoop,
				Loop: &WorkflowLoopConfig{
					Tool:          "worker.retry",
					Payload:       rawjson.Message([]byte(`{}`)),
					MaxIterations: 2,
				},
			},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"retry"}},
		},
	})

	second, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"retry#2"}, toolCallIDs(second.ToolCalls))

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1"}, {ToolCallID: "retry#2"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"publish"}, toolCallIDs(next.ToolCalls))
}

func TestGraphWorkflowPlannerBranchAfterJoinSelectsTarget(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: rawjson.Message([]byte(`{}`))},
			{ID: "review", Kind: WorkflowNodeTool, Tool: "reviewer.review", Payload: rawjson.Message([]byte(`{}`))},
			{ID: "ready", Kind: WorkflowNodeJoin, DependsOn: []string{"draft", "review"}},
			{ID: "route", Kind: WorkflowNodeBranch, DependsOn: []string{"ready"}, Branch: &WorkflowBranchConfig{
				FromStep: "review",
				Cases:    []WorkflowBranchCase{{Path: "$.approved", Equals: "true", Target: "publish"}},
				Default:  "revise",
			}},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
			{ID: "revise", Kind: WorkflowNodeTool, Tool: "publisher.revise", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
		},
	})

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{
			{ToolCallID: "draft", Result: rawjson.Message([]byte(`{"ok":true}`))},
			{ToolCallID: "review", Result: rawjson.Message([]byte(`{"approved":true}`))},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"publish"}, toolCallIDs(next.ToolCalls))
}

func TestGraphWorkflowPlannerTypedInputFeedsLaterNodes(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "approval", Kind: WorkflowNodeTypedInput, Title: "Approval", Schema: rawjson.Message([]byte(`{"type":"object"}`))},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"approval"}},
		},
	})

	start, err := p.PlanStart(context.Background(), &PlanInput{})
	require.NoError(t, err)
	require.Empty(t, start.ToolCalls)
	require.NotNil(t, start.Await)
	require.Len(t, start.Await.Items, 1)
	require.Equal(t, AwaitItemKindTypedInput, start.Await.Items[0].Kind)
	require.Equal(t, "approval", start.Await.Items[0].TypedInput.ID)

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		TypedInputs: []TypedInputOutput{{ID: "approval", Payload: rawjson.Message([]byte(`{"approved":true}`))}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"publish"}, toolCallIDs(next.ToolCalls))
}

func TestGraphWorkflowPlannerBranchCanUseTypedInputPayload(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
			{ID: "route", Kind: WorkflowNodeBranch, DependsOn: []string{"approval"}, Branch: &WorkflowBranchConfig{
				FromStep: "approval",
				Cases:    []WorkflowBranchCase{{Path: "$.approved", Equals: "true", Target: "publish"}},
				Default:  "stop",
			}},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
			{ID: "stop", Kind: WorkflowNodeTool, Tool: "publisher.stop", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
		},
	})

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		TypedInputs: []TypedInputOutput{{ID: "approval", Payload: rawjson.Message([]byte(`{"approved":true}`))}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"publish"}, toolCallIDs(next.ToolCalls))
}

func toolCallIDs(calls []ToolRequest) []string {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		ids = append(ids, call.ToolCallID)
	}
	return ids
}
