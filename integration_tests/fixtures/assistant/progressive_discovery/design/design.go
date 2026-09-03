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
		ToolSearch(
			ToolSearchMaxResults(3),
			ToolSearchExactMatch(ToolSearchExactMatchNarrow),
		),
	)
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
	})

	Method("status", func() {
		Payload(func() {
			Attribute("scope", String, "Status scope")
		})
		Result(func() {
			Attribute("state", String, "Current catalog state")
			Required("state")
		})
		WatchableResource("status", "urn:status", "application/json")
	})
	Method("health", func() {
		Result(func() {
			Attribute("state", String, "Current health state")
			Required("state")
		})
		WatchableResource("health", "urn:health", "application/json")
	})

	Method("stream_chunks", func() {
		StreamingResult(func() {
			Attribute("chunk", String, "Streamed chunk")
			Required("chunk")
		})
		Tool("stream_chunks", "Return two chunks through a Loom streaming method")
	})

	Method("wait_for_cancel", func() {
		Result(func() {
			Attribute("completed", Boolean, "Whether the wait completed normally")
			Required("completed")
		})
		Tool("wait_for_cancel", "Wait until the MCP client cancels the request")
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
