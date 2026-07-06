package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowExprValidateGraph(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		workflow := &WorkflowExpr{
			Agent: &AgentExpr{Name: "assistant"},
			GraphNodes: []*WorkflowNodeExpr{
				{ID: "draft", Kind: WorkflowNodeParallelStep, Tool: "writer.draft", Payload: `{}`},
				{ID: "review", Kind: WorkflowNodeParallelStep, Tool: "reviewer.review", Payload: `{}`},
				{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: `{}`, DependsOn: []string{"draft", "review"}},
				{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`, MaxIterations: 2}},
				{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: `{"type":"object"}`},
			},
		}
		require.NoError(t, workflow.Validate())
	})

	t.Run("duplicate", func(t *testing.T) {
		workflow := &WorkflowExpr{
			Agent: &AgentExpr{Name: "assistant"},
			GraphNodes: []*WorkflowNodeExpr{
				{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.draft", Payload: `{}`},
				{ID: "draft", Kind: WorkflowNodeTool, Tool: "writer.review", Payload: `{}`},
			},
		}
		require.ErrorContains(t, workflow.Validate(), "duplicate workflow node id")
	})

	t.Run("unresolved dependency", func(t *testing.T) {
		workflow := &WorkflowExpr{
			Agent: &AgentExpr{Name: "assistant"},
			GraphNodes: []*WorkflowNodeExpr{
				{ID: "publish", Kind: WorkflowNodeTool, Tool: "publisher.publish", Payload: `{}`, DependsOn: []string{"missing"}},
			},
		}
		require.ErrorContains(t, workflow.Validate(), "unresolved workflow dependency")
	})

	t.Run("unbounded loop", func(t *testing.T) {
		workflow := &WorkflowExpr{
			Agent: &AgentExpr{Name: "assistant"},
			GraphNodes: []*WorkflowNodeExpr{
				{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`}},
			},
		}
		require.ErrorContains(t, workflow.Validate(), "Loop requires MaxIterations")
	})

	t.Run("invalid typed input schema", func(t *testing.T) {
		workflow := &WorkflowExpr{
			Agent: &AgentExpr{Name: "assistant"},
			GraphNodes: []*WorkflowNodeExpr{
				{ID: "approval", Kind: WorkflowNodeTypedInput, Schema: `{"type":`},
			},
		}
		require.ErrorContains(t, workflow.Validate(), "schema must be valid JSON")
	})
}
