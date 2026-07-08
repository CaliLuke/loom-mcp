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
