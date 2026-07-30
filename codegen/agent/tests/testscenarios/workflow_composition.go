package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// WorkflowComposition returns a DSL design with a generated sequential workflow planner.
func WorkflowComposition() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Workflow(func() {
					Step("draft", "writer.draft", `{"topic":"loom"}`)
					Step("review", "reviewer.review", `{"strict":true}`)
					FinalMessage("workflow complete")
				})
			})
		})
	}
}
