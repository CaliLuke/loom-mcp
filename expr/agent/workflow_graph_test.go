package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowExprValidateGraph(t *testing.T) {
	cases := []struct {
		name     string
		workflow *WorkflowExpr
		err      string
	}{
		{
			name: "valid",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "draft", Kind: WorkflowNodeParallelStep, Tool: "writer.draft", Payload: `{}`},
					{ID: "review", Kind: WorkflowNodeParallelStep, Tool: "reviewer.review", Payload: `{}`},
					{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: `{}`, DependsOn: []string{"draft", "review"}},
					{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`, MaxIterations: 2}},
					{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: `{"type":"object"}`},
				},
			},
		},
		{
			name: "mixed sequential and graph",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				Steps: []*WorkflowStepExpr{
					{Name: "draft", Tool: "writer.draft", Payload: `{}`},
				},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "review", Kind: WorkflowNodeTool, Tool: "reviewer.review", Payload: `{}`},
				},
			},
			err: "workflow cannot mix sequential Step declarations with graph constructs",
		},
		{
			name: "duplicate",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: `{}`},
					{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.review", Payload: `{}`},
				},
			},
			err: "duplicate workflow node id",
		},
		{
			name: "reserved loop separator in node id",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "draft#1", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: `{}`},
				},
			},
			err: `workflow node id "draft#1" contains reserved character "#"`,
		},
		{
			name: "unresolved dependency",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: `{}`, DependsOn: []string{"missing"}},
				},
			},
			err: "unresolved workflow dependency",
		},
		{
			name: "dependency cycle",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "barrier", Kind: WorkflowNodeJoin, DependsOn: []string{"later"}},
					{ID: "later", Kind: WorkflowNodeTool, Tool: "worker.later", Payload: `{}`, DependsOn: []string{"barrier"}},
				},
			},
			err: "workflow dependency cycle: barrier -> later -> barrier",
		},
		{
			name: "unresolved branch source",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "route", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "missing", Default: "publish"}},
					{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: `{}`},
				},
			},
			err: `unresolved branch source step "missing"`,
		},
		{
			name: "unresolved loop until step",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`, MaxIterations: 2, Until: &WorkflowPredicateExpr{Step: "missing", Path: "$.done", Equals: "true"}}},
				},
			},
			err: `unresolved loop until step "missing"`,
		},
		{
			name: "unbounded loop",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`}},
				},
			},
			err: "Loop requires MaxIterations",
		},
		{
			name: "invalid typed input schema",
			workflow: &WorkflowExpr{
				Agent: &AgentExpr{Name: "assistant"},
				GraphNodes: []*WorkflowNodeExpr{
					{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: `{"type":`},
				},
			},
			err: "schema must be valid JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.workflow.Validate()
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.err)
		})
	}
}
