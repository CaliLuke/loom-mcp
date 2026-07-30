package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// WorkflowGraph returns a DSL design with a deterministic graph workflow.
func WorkflowGraph() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("coordinator", "Graph workflow", func() {
				Workflow(func() {
					Parallel(func() {
						Step("draft", "writer.draft", `{"topic":"loom"}`)
						Step("review", "reviewer.review", `{"strict":true}`)
					})
					Join("publish_ready", "draft", "review")
					RequestInput("approval", "Approval", `{"type":"object","properties":{"approved":{"type":"boolean"}}}`)
					Step("publish", "publisher.publish", `{}`)
					FinalMessage("graph complete")
				})
			})
		})
	}
}
