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

func TestGraphWorkflowPlannerRetriesFailedLoopAttempt(t *testing.T) {
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

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1", Error: NewToolError("try again")}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"retry#2"}, toolCallIDs(next.ToolCalls))

	final, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{
			{ToolCallID: "retry#1", Error: NewToolError("try again")},
			{ToolCallID: "retry#2", Result: rawjson.Message([]byte(`{"ok":true}`))},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"publish"}, toolCallIDs(final.ToolCalls))
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

func TestGraphWorkflowPlannerRejectsInvalidGraphConfig(t *testing.T) {
	cases := []struct {
		name    string
		nodes   []WorkflowNode
		message string
	}{
		{
			name: "empty node id",
			nodes: []WorkflowNode{
				{Kind: WorkflowNodeTool, Tool: "worker.run", Payload: rawjson.Message([]byte(`{}`))},
			},
			message: "workflow graph node id is required",
		},
		{
			name: "duplicate node id",
			nodes: []WorkflowNode{
				{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: rawjson.Message([]byte(`{}`))},
				{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.redraft", Payload: rawjson.Message([]byte(`{}`))},
			},
			message: `duplicate workflow graph node id "draft"`,
		},
		{
			name: "missing dependency",
			nodes: []WorkflowNode{
				{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"review"}},
			},
			message: `workflow node "publish" dependency "review" does not exist`,
		},
		{
			name: "missing branch source",
			nodes: []WorkflowNode{
				{ID: "route", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{FromStep: "approval", Default: "stop"}},
				{ID: "stop", Kind: WorkflowNodeTool, Tool: "publisher.stop", Payload: rawjson.Message([]byte(`{}`))},
			},
			message: `workflow branch "route" fromStep "approval" does not exist`,
		},
		{
			name: "missing branch default target",
			nodes: []WorkflowNode{
				{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
				{ID: "route", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{FromStep: "approval", Default: "stop"}},
			},
			message: `workflow branch "route" default target "stop" does not exist`,
		},
		{
			name: "missing branch case target",
			nodes: []WorkflowNode{
				{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
				{ID: "stop", Kind: WorkflowNodeTool, Tool: "publisher.stop", Payload: rawjson.Message([]byte(`{}`))},
				{ID: "route", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{
					FromStep: "approval",
					Cases:    []WorkflowBranchCase{{Path: "$.approved", Equals: "true", Target: "publish"}},
					Default:  "stop",
				}},
			},
			message: `workflow branch "route" case target "publish" does not exist`,
		},
		{
			name: "missing loop until step",
			nodes: []WorkflowNode{
				{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopConfig{
					Tool:          "worker.retry",
					Payload:       rawjson.Message([]byte(`{}`)),
					MaxIterations: 2,
					Until:         &WorkflowPredicateConfig{Step: "review", Path: "$.done", Equals: "true"},
				}},
			},
			message: `workflow loop "retry" until step "review" does not exist`,
		},
		{
			name: "unsupported jsonpath",
			nodes: []WorkflowNode{
				{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
				{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`))},
				{ID: "stop", Kind: WorkflowNodeTool, Tool: "publisher.stop", Payload: rawjson.Message([]byte(`{}`))},
				{ID: "route", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{
					FromStep: "approval",
					Cases:    []WorkflowBranchCase{{Path: "$.approval.status", Equals: "true", Target: "publish"}},
					Default:  "stop",
				}},
			},
			message: `workflow branch "route" case path "$.approval.status" is unsupported`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewGraphWorkflowPlanner(WorkflowGraphConfig{Nodes: tc.nodes})

			result, err := p.PlanStart(context.Background(), &PlanInput{})

			require.Nil(t, result)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestGraphWorkflowPlannerStopsOnFailedToolOutput(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: rawjson.Message([]byte(`{}`))},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"draft"}},
		},
	})

	result, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "draft", Error: NewToolError("boom")}},
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, `workflow node "draft" failed at "draft": boom`)
}

func TestGraphWorkflowPlannerReportsStuckRequiredNodes(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "alpha", Kind: WorkflowNodeTool, Tool: "worker.alpha", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"beta"}},
			{ID: "beta", Kind: WorkflowNodeTool, Tool: "worker.beta", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"alpha"}},
		},
	})

	result, err := p.PlanStart(context.Background(), &PlanInput{})

	require.Nil(t, result)
	require.ErrorContains(t, err, "workflow graph stuck; incomplete nodes: alpha, beta")
}

func TestGraphWorkflowPlannerBranchSkipsUnselectedTargetsForLaterDependencies(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
			{ID: "route", Kind: WorkflowNodeBranch, DependsOn: []string{"approval"}, Branch: &WorkflowBranchConfig{
				FromStep: "approval",
				Cases:    []WorkflowBranchCase{{Path: "$.approved", Equals: "true", Target: "publish"}},
				Default:  "revise",
			}},
			{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
			{ID: "revise", Kind: WorkflowNodeTool, Tool: "publisher.revise", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
			{ID: "done", Kind: WorkflowNodeJoin, DependsOn: []string{"publish", "revise"}},
			{ID: "notify", Kind: WorkflowNodeTool, Tool: "publisher.notify", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"done"}},
		},
	})

	next, err := p.PlanResume(context.Background(), &PlanResumeInput{
		TypedInputs: []TypedInputOutput{{ID: "approval", Payload: rawjson.Message([]byte(`{"approved":true}`))}},
		ToolOutputs: []*ToolOutput{
			{ToolCallID: "publish", Result: rawjson.Message([]byte(`{"ok":true}`))},
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"notify"}, toolCallIDs(next.ToolCalls))
}

func TestGraphWorkflowPlannerNestedBranchOnUnselectedPathDoesNotFire(t *testing.T) {
	// Shared graph: "route" picks "publish" or the nested branch "escalate".
	// "escalate" picks the loop "retry", the shared tool "notify", or "stop".
	// "alert" independently picks "notify" (shared with "escalate") or "quiet".
	nodes := []WorkflowNode{
		{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: rawjson.Message([]byte(`{"type":"object"}`))},
		{ID: "route", Kind: WorkflowNodeBranch, DependsOn: []string{"approval"}, Branch: &WorkflowBranchConfig{
			FromStep: "approval",
			Cases:    []WorkflowBranchCase{{Path: "$.approved", Equals: "true", Target: "publish"}},
			Default:  "escalate",
		}},
		{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"route"}},
		{ID: "escalate", Kind: WorkflowNodeBranch, DependsOn: []string{"route"}, Branch: &WorkflowBranchConfig{
			FromStep: "approval",
			Cases: []WorkflowBranchCase{
				{Path: "$.retry", Equals: "true", Target: "retry"},
				{Path: "$.alert", Equals: "true", Target: "notify"},
			},
			Default: "stop",
		}},
		{ID: "retry", Kind: WorkflowNodeLoop, DependsOn: []string{"escalate"}, Loop: &WorkflowLoopConfig{
			Tool:          "worker.retry",
			Payload:       rawjson.Message([]byte(`{}`)),
			MaxIterations: 2,
		}},
		{ID: "stop", Kind: WorkflowNodeTool, Tool: "publisher.stop", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"escalate"}},
		{ID: "alert", Kind: WorkflowNodeBranch, DependsOn: []string{"approval"}, Branch: &WorkflowBranchConfig{
			FromStep: "approval",
			Cases:    []WorkflowBranchCase{{Path: "$.alert", Equals: "true", Target: "notify"}},
			Default:  "quiet",
		}},
		{ID: "notify", Kind: WorkflowNodeTool, Tool: "publisher.notify", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"alert"}},
		{ID: "quiet", Kind: WorkflowNodeTool, Tool: "publisher.quiet", Payload: rawjson.Message([]byte(`{}`)), DependsOn: []string{"alert"}},
	}

	cases := []struct {
		name              string
		approval          string
		wantCalls         []string
		completionOutputs []*ToolOutput
	}{
		{
			name:     "nested branch on unselected path never fires",
			approval: `{"approved":true}`,
			// "escalate" is skipped, so its own default "stop" and case
			// targets "retry"/"notify" must never be scheduled.
			wantCalls: []string{"publish", "quiet"},
			completionOutputs: []*ToolOutput{
				{ToolCallID: "publish", Result: rawjson.Message([]byte(`{"ok":true}`))},
				{ToolCallID: "quiet", Result: rawjson.Message([]byte(`{"ok":true}`))},
			},
		},
		{
			name:     "nested branch on selected path still fires",
			approval: `{"retry":true}`,
			// "route" defaults to "escalate", which selects the "retry" loop.
			wantCalls: []string{"retry#1", "quiet"},
			completionOutputs: []*ToolOutput{
				{ToolCallID: "retry#1", Result: rawjson.Message([]byte(`{"ok":true}`))},
				{ToolCallID: "retry#2", Result: rawjson.Message([]byte(`{"ok":true}`))},
				{ToolCallID: "quiet", Result: rawjson.Message([]byte(`{"ok":true}`))},
			},
		},
		{
			name:     "diamond target of skipped and selected branch still runs",
			approval: `{"approved":true,"alert":true}`,
			// "escalate" is skipped but "alert" selects "notify"; the shared
			// target must run while "retry" and "stop" stay skipped.
			wantCalls: []string{"publish", "notify"},
			completionOutputs: []*ToolOutput{
				{ToolCallID: "publish", Result: rawjson.Message([]byte(`{"ok":true}`))},
				{ToolCallID: "notify", Result: rawjson.Message([]byte(`{"ok":true}`))},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewGraphWorkflowPlanner(WorkflowGraphConfig{Nodes: nodes, FinalMessage: "done"})
			typedInputs := []TypedInputOutput{{ID: "approval", Payload: rawjson.Message([]byte(tc.approval))}}

			next, err := p.PlanResume(context.Background(), &PlanResumeInput{TypedInputs: typedInputs})
			require.NoError(t, err)
			require.ElementsMatch(t, tc.wantCalls, toolCallIDs(next.ToolCalls))

			final, err := p.PlanResume(context.Background(), &PlanResumeInput{
				TypedInputs: typedInputs,
				ToolOutputs: tc.completionOutputs,
			})
			require.NoError(t, err)
			require.Empty(t, toolCallIDs(final.ToolCalls))
			require.NotNil(t, final.FinalResponse)
		})
	}
}

func TestGraphWorkflowPlannerLoopUntilUsesLatestLoopIterationOutput(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{
		Nodes: []WorkflowNode{
			{
				ID:   "retry",
				Kind: WorkflowNodeLoop,
				Loop: &WorkflowLoopConfig{
					Tool:          "worker.retry",
					Payload:       rawjson.Message([]byte(`{}`)),
					MaxIterations: 3,
					Until:         &WorkflowPredicateConfig{Step: "retry", Path: "$.done", Equals: "true"},
				},
			},
		},
		FinalMessage: "done",
	})

	final, err := p.PlanResume(context.Background(), &PlanResumeInput{
		ToolOutputs: []*ToolOutput{{ToolCallID: "retry#1", Result: rawjson.Message([]byte(`{"done":true}`))}},
	})

	require.NoError(t, err)
	require.NotNil(t, final.FinalResponse)
}

func toolCallIDs(calls []ToolRequest) []string {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		ids = append(ids, call.ToolCallID)
	}
	return ids
}
