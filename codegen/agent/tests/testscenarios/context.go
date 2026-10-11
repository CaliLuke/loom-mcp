package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ContextPolicies exercises enabled and disabled context generation.
func ContextPolicies() func() {
	return func() {
		API("alpha", func() {

		})
		Service("alpha", func() {
			Agent("compactor", "Compaction", func() {
				RunPolicy(func() {
					Context(func() {
						TriggerAt(.85)
						KeepRecentTokens(8000)
						Summarize(func() {
							Instructions("Keep every \"order ID\".\nPreserve evidence.")
							Prompt("{{.Previous}}\n{{.Evicted}}\n{{.Instructions}}\nBudget: {{.Budget}}")
						})
						Recall(false)
					})
				})
				Use("evidence", func() {
					Tool("keep", "Keep evidence", func() {
						Args(String)
						Return(String)
						KeepResultInContext()
					})
				})
			})
			Agent("disabled", "Disabled compaction", func() {
				RunPolicy(func() {
					Context(func() {
						Disable()
						Recall(false)
					})
				})
			})
			Agent("defaults", "Default compaction", func() {
				RunPolicy(func() {
					Context(nil)
				})
			})
		})
	}
}
