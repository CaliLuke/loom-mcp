package planner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphWorkflowRejectsIncompleteConfigurationBeforeScheduling(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		node       WorkflowNode
	}{
		{"tool missing name", "requires tool", WorkflowNode{ID: "work", Kind: WorkflowNodeTool}},
		{"unknown kind", "unknown workflow node kind", WorkflowNode{ID: "work", Kind: "future"}},
		{"typed input missing schema", "requires schema", WorkflowNode{ID: "work", Kind: WorkflowNodeTypedInput}},
		{"branch missing configuration", "requires branch config", WorkflowNode{ID: "work", Kind: WorkflowNodeBranch}},
		{"branch missing source", "requires fromStep", WorkflowNode{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{}}},
		{"branch missing default", "requires default target", WorkflowNode{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{FromStep: "source"}}},
		{"branch case missing target", "case target is required", WorkflowNode{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchConfig{FromStep: "source", Default: "target", Cases: []WorkflowBranchCase{{Path: "$.ok", Equals: "true"}}}}},
		{"loop missing configuration", "requires loop config", WorkflowNode{ID: "work", Kind: WorkflowNodeLoop}},
		{"loop missing tool", "requires tool", WorkflowNode{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopConfig{MaxIterations: 1}}},
		{"loop missing bound", "requires MaxIterations", WorkflowNode{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopConfig{Tool: "tool"}}},
		{"loop missing predicate source", "until step is required", WorkflowNode{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopConfig{Tool: "tool", MaxIterations: 1, Until: &WorkflowPredicateConfig{Path: "$.ok"}}}},
		{"loop nested predicate", "only top-level", WorkflowNode{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopConfig{Tool: "tool", MaxIterations: 1, Until: &WorkflowPredicateConfig{Step: "source", Path: "$.nested.ok"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner := NewGraphWorkflowPlanner(WorkflowGraphConfig{Nodes: []WorkflowNode{{ID: "source", Tool: "read"}, {ID: "target", Tool: "save"}, tc.node}})
			result, err := planner.PlanStart(t.Context(), &PlanInput{})
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result, "invalid graphs must not dispatch even valid ready nodes")
		})
	}
	planner := NewGraphWorkflowPlanner(WorkflowGraphConfig{})
	result, err := planner.PlanStart(t.Context(), &PlanInput{})
	require.ErrorContains(t, err, "at least one node")
	assert.Nil(t, result)
	result, err = planner.PlanResume(t.Context(), nil)
	require.ErrorContains(t, err, "resume input")
	assert.Nil(t, result)
}

func TestGraphWorkflowReportsUnresolvedBranchWithoutSchedulingItsTargets(t *testing.T) {
	planner := NewGraphWorkflowPlanner(WorkflowGraphConfig{Nodes: []WorkflowNode{
		{ID: "source", Kind: WorkflowNodeJoin},
		{ID: "branch", Kind: WorkflowNodeBranch, DependsOn: []string{"source"}, Branch: &WorkflowBranchConfig{FromStep: "source", Default: "target"}},
		{ID: "target", Kind: WorkflowNodeTypedInput, Schema: []byte(`{"type":"string"}`)},
	}})
	result, err := planner.PlanStart(t.Context(), &PlanInput{})
	require.ErrorContains(t, err, "stuck")
	assert.Nil(t, result)
}
