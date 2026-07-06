package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestWorkflowGraphDSL(t *testing.T) {
	runDSL(t, func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("assistant", "Assistant", func() {
				Workflow(func() {
					Parallel(func() {
						Step("draft", "writer.draft", `{"topic":"loom"}`)
						Step("review", "reviewer.review", `{"strict":true}`)
					})
					Join("publish_ready", "draft", "review")
					RequestInput("approval", "Approval", `{"type":"object","properties":{"approved":{"type":"boolean"}}}`)
					Loop("retry", "worker.retry", `{}`, MaxIterations(2), UntilJSONPath("retry", "$.done", "true"))
				})
			})
		})
	})

	workflow := agentsexpr.Root.Agents[0].Workflow
	require.Len(t, workflow.GraphNodes, 5)
	require.Equal(t, agentsexpr.WorkflowNodeParallelStep, workflow.GraphNodes[0].Kind)
	require.Equal(t, []string{"draft", "review"}, workflow.GraphNodes[2].DependsOn)
	require.Equal(t, agentsexpr.WorkflowNodeTypedInput, workflow.GraphNodes[3].Kind)
	require.JSONEq(t, `{"type":"object","properties":{"approved":{"type":"boolean"}}}`, workflow.GraphNodes[3].Schema)
	require.Equal(t, 2, workflow.GraphNodes[4].Loop.MaxIterations)
}
