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

func TestWorkflowBranchTargetsShareBranchDependency(t *testing.T) {
	runDSL(t, func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("assistant", "Assistant", func() {
				Workflow(func() {
					RequestInput("approval", "Approval", `{"type":"object","properties":{"approved":{"type":"boolean"}}}`)
					Branch("route", "approval", Case("$.approved", "true", "publish"), BranchDefault("revise"))
					Step("publish", "publisher.publish", `{}`)
					Step("revise", "publisher.revise", `{}`)
					Join("done", "publish", "revise")
				})
			})
		})
	})

	workflow := agentsexpr.Root.Agents[0].Workflow
	require.Len(t, workflow.GraphNodes, 5)
	require.Equal(t, []string{"route"}, workflow.GraphNodes[2].DependsOn)
	require.Equal(t, []string{"route"}, workflow.GraphNodes[3].DependsOn)
	require.Equal(t, []string{"publish", "revise"}, workflow.GraphNodes[4].DependsOn)
}

func TestWorkflowDSLRejectsMixedSequentialAndGraphConstructs(t *testing.T) {
	cases := []struct {
		name  string
		graph func()
	}{
		{
			name: "parallel",
			graph: func() {
				Parallel(func() {
					Step("review", "reviewer.review", `{}`)
				})
			},
		},
		{
			name: "request input",
			graph: func() {
				RequestInput("approval", "Approval", `{"type":"object"}`)
			},
		},
		{
			name: "loop",
			graph: func() {
				Loop("retry", "worker.retry", `{}`, MaxIterations(2))
			},
		},
		{
			name: "branch",
			graph: func() {
				Branch("route", "draft", BranchDefault("publish"))
				Step("publish", "publisher.publish", `{}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, func() {
				API("alpha", func() {})
				Service("alpha", func() {
					Agent("assistant", "Assistant", func() {
						Workflow(func() {
							Step("draft", "writer.draft", `{}`)
							tc.graph()
						})
					})
				})
			}, "workflow cannot mix sequential Step declarations with graph constructs")
		})
	}
}
