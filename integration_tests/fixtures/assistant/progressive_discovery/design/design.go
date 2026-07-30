package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

var _ = API("progressive-discovery", func() {
	Title("Progressive Discovery Provider Fixture")
	Version("1.0.0")
	DisableAgentDocs()
})

var _ = Service("catalog", func() {
	Description("Minimal MCP progressive-discovery parity fixture")
	MCP("catalog-mcp", "1.0.0",
		ProtocolVersion("2025-11-25"),
		ToolSearch(
			ToolSearchMaxResults(3),
			ToolSearchExactMatch(ToolSearchExactMatchNarrow),
		),
	)
	JSONRPC(func() {
		POST("/rpc")
	})

	Method("lookup", func() {
		Payload(func() {
			Attribute("query", String, "Lookup query")
			Required("query")
		})
		Result(func() {
			Attribute("value", String, "Lookup result")
			Required("value")
		})
		Tool("lookup", "Lookup a direct catalog entry",
			ToolDiscoveryCategory("catalog"),
			ToolDiscoveryTags("lookup", "direct"),
			ToolDiscoveryKeywords("catalog", "entry"),
		)
		JSONRPC(func() {})
	})

	Method("projected_lookup", func() {
		Payload(func() {
			Attribute("query", String, "Lookup query")
			Required("query")
		})
		Result(func() {
			Attribute("value", String, "Lookup result")
			Required("value")
		})
	})

	Agent("owner", "Owns the projected catalog tool", func() {
		Use("projected", func() {
			Tool("projected_lookup", "Lookup a projected catalog entry", func() {
				Args(func() {
					Attribute("query", String, "Lookup query")
					Required("query")
				})
				Return(func() {
					Attribute("value", String, "Lookup result")
					Required("value")
				})
				BindTo("catalog", "projected_lookup")
				Expose(AgentRuntime, MCPSurface)
				MCPPlacement("catalog", "catalog-mcp")
			})
		})
	})
})
