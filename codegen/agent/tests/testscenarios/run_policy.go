package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// RunPolicyBasic returns a DSL design with caps, time budget, and interrupts.
func RunPolicyBasic() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				RunPolicy(func() {
					DefaultCaps(MaxToolCalls(5), MaxConsecutiveFailedToolCalls(2))
					TimeBudget("30s")
					InterruptsAllowed(true)
				})
				Use("helpers", func() {
					Tool("noop", "Noop", func() {})
				})
			})
		})
	}
}
