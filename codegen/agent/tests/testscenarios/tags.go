package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// TagsBasic returns a DSL design with a tool exposing tags.
func TagsBasic() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("helpers", func() {
					Tool("summarize", "Summarize a document", func() {
						Tags("nlp", "summarization")
					})
				})
			})
		})
	}
}
