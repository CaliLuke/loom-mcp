// Package design is the source of truth for the long-running evaluation fixture.
package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
)

var _ = API("longrun", func() {
	Title("Long-running context evaluation")
	Version("1.0")
})

var action = Type("Action", func() {
	Attribute("step", String, "Materialised work step")
	Attribute("target", String, "Final action target or combined evidence")
	Required("step", "target")
})

var domains = []struct {
	name  string
	tools []string
}{
	{"ops_deployer", []string{"list_services", "read_service_log", "deploy"}},
	{"ledger_clerk", []string{"search_invoices", "read_invoice", "post_payment"}},
	{"dispatch_planner", []string{"list_shipments", "read_manifest", "book_carrier"}},
}

var toolsets = func() map[string]*agentsexpr.ToolsetExpr {
	out := map[string]*agentsexpr.ToolsetExpr{}
	for _, domain := range domains {
		out[domain.name] = Toolset(domain.name, func() {
			for _, name := range domain.tools {
				Tool(name, "Execute "+name+" for the current task", func() { Args(action); Return(String) })
			}
		})
	}
	return out
}()

var _ = Service("longrun", func() {
	for _, domain := range domains {
		for _, suffix := range []string{"", "_nosum", "_reject"} {
			Agent(domain.name+suffix, "Long-running "+domain.name+" task", func() {
				Use(toolsets[domain.name])
				RunPolicy(func() {
					DefaultCaps(MaxToolCalls(300), MaxRecoveryTurns(10))
					Context(func() {
						TriggerAt(0.5)
						KeepRecentTokens(1000)
						if suffix != "_nosum" {
							Summarize(func() {
								instructions := "Keep planted key = value lines verbatim in order. Later corrections override earlier values."
								if suffix == "_reject" {
									instructions += " [[force-summary-rejection]]"
								}
								Instructions(instructions)
							})
						}
					})
				})
			})
		}
	}
})
