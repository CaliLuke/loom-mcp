package design

import (
	. "goa.design/goa-ai/dsl"
	. "goa.design/goa/v3/dsl"
)

var _ = API("assistant", func() {
	Title("AI Assistant API")
	Description("Simple MCP example exposing tools, resources, prompts, and an agent consumer")
	Version("1.0")
	Server("orchestrator", func() {
		Host("dev", func() {
			URI("http://localhost:8080")
		})
		Services("assistant")
	})
})

var _ = Service("assistant", func() {
	Description("AI Assistant service with full MCP protocol support")

	MCP("assistant-mcp", "1.0.0", ProtocolVersion("2025-06-18"))

	// Keep the design minimal; integration tests exercise MCP protocol handlers.
	JSONRPC(func() {
		POST("/rpc")
	})

	Method("list_documents", func() {
		Description("List available documents")
		Result(Documents)
		Resource("documents", "doc://list", "application/json")
		JSONRPC(func() {})
	})

	// Additional resources used by scenarios
	Method("system_info", func() {
		Description("Return system info")
		Result(func() {
			// Simple object; adapter marshals to JSON
			Attribute("name", String, "System name")
			Attribute("version", String, "System version")
		})
		Resource("system_info", "system://info", "application/json")
		JSONRPC(func() {})
	})

	Method("conversation_history", func() {
		Description("Return conversation history with optional query params")
		Payload(func() {
			Attribute("limit", Int, "Max items")
			Attribute("flag", Boolean, "Sample boolean flag")
			Attribute("nums", ArrayOf(Float64), "Numbers array")
		})
		Result(func() {
			Attribute("items", ArrayOf(String), "History items")
		})
		Resource("conversation_history", "conversation://history", "application/json")
		JSONRPC(func() {})
	})

	Method("figma_design_system", func() {
		Description("Return a fake Figma design system summary for implementation validation")
		Result(DesignSystem)
		Resource("figma_design_system", "figma://design-system/mobile-checkout", "application/json")
		JSONRPC(func() {})
	})

	// Static prompt for tests
	StaticPrompt("code_review", "Simple code review prompt", "system", "Review the provided code and suggest improvements.")

	Method("generate_prompts", func() {
		Description("Generate context-aware prompts")
		Payload(func() {
			Attribute("context", String, "Current context")
			Attribute("task", String, "Task type")
			Required("context", "task")
		})
		Result(PromptTemplates)
		DynamicPrompt("contextual_prompts", "Generate prompts based on context")
		JSONRPC(func() {})
	})

	Method("build_figma_implementation_prompt", func() {
		Description("Build a Figma-style implementation handoff prompt from a generated DPI spec")
		Payload(func() {
			Attribute("screen_title", String, "Title of the screen being implemented")
			Attribute("framework", String, "Target UI framework", func() { Enum("react", "swiftui", "jetpack-compose") })
			Attribute("design_tokens_uri", String, "Resource URI for the design system")
			Attribute("dpi_json", String, "Serialized DPI spec JSON")
			Required("screen_title", "framework", "design_tokens_uri", "dpi_json")
		})
		Result(PromptTemplates)
		DynamicPrompt("figma_implementation_prompt", "Generate implementation instructions from a DPI spec")
		JSONRPC(func() {})
	})

	Method("send_notification", func() {
		Description("Send status notification to client")
		Payload(func() {
			Attribute("type", String, "Notification type")
			Attribute("message", String, "Notification message")
			Attribute("data", Any, "Additional data")
			Required("type", "message")
		})
		Notification("status_update", "Send status updates to client")
		JSONRPC(func() {})
	})

	// ---- Tools (for MCP tools/list and tools/call) ----

	Method("analyze_sentiment", func() {
		Description("Analyze sentiment of text")
		Payload(func() {
			Attribute("text", String, "Input text to analyze")
			Required("text")
		})
		Result(func() {
			Attribute("sentiment", String, "Detected sentiment")
		})
		Tool("analyze_sentiment", "Analyze sentiment of text")
		JSONRPC(func() {})
	})

	Method("extract_keywords", func() {
		Description("Extract keywords from text")
		Payload(func() {
			Attribute("text", String, "Input text")
			Required("text")
		})
		Result(func() { Attribute("keywords", ArrayOf(String), "Extracted keywords") })
		Tool("extract_keywords", "Extract keywords from text")
		JSONRPC(func() {})
	})

	Method("summarize_text", func() {
		Description("Summarize text")
		Payload(func() {
			Attribute("text", String, "Input text to summarize")
			Required("text")
		})
		Result(func() { Attribute("summary", String, "Summary") })
		Tool("summarize_text", "Summarize text")
		JSONRPC(func() {})
	})

	Method("search", func() {
		Description("Search knowledge base")
		Payload(func() {
			Attribute("query", String, "Search query")
			Attribute("limit", Int, "Maximum number of results")
			Required("query")
		})
		Result(func() { Attribute("results", ArrayOf(String), "Search results") })
		Tool("search", "Search knowledge base")
		JSONRPC(func() {})
	})

	Method("execute_code", func() {
		Description("Execute code")
		Payload(func() {
			Attribute("language", String, "Language to execute", func() { Enum("python", "javascript") })
			Attribute("code", String, "Code to execute")
			Required("language", "code")
		})
		Result(func() { Attribute("output", String, "Execution output") })
		Tool("execute_code", "Execute code")
		JSONRPC(func() {})
	})

	Method("process_batch", func() {
		Description("Process batch of items")
		Payload(func() {
			Attribute("items", ArrayOf(String), "Items to process")
			Attribute("format", String, "Output format", func() { Enum("json", "text", "blob", "uri") })
			Attribute("blob", String, "Base64 blob")
			Attribute("uri", String, "Resource URI")
			Attribute("mimeType", String, "MIME type")
			Required("items")
		})
		Result(func() { Attribute("ok", Boolean, "Operation status") })
		Tool("process_batch", "Process a batch of items")
		JSONRPC(func() {})
	})

	Method("multi_content", func() {
		Description("Return multiple content items")
		Payload(func() {
			Attribute("count", Int, "Number of content items to return")
			Required("count")
		})
		Result(func() { Attribute("result", String, "Combined text result") })
		Tool("multi_content", "Return multiple content items")
		JSONRPC(func() {})
	})

	Method("generate_dpi_spec", func() {
		Description("Generate a deterministic implementation-ready DPI spec from a fake Figma frame")
		Payload(func() {
			Attribute("screen_title", String, "Name of the frame or screen")
			Attribute("platform", String, "Target platform", func() { Enum("ios", "web") })
			Attribute("density", String, "Layout density", func() { Enum("compact", "comfortable") })
			Attribute("primary_cta", String, "Primary call to action")
			Attribute("sections", ArrayOf(String), "Ordered screen sections")
			Attribute("include_dev_notes", Boolean, "Whether to include implementation notes")
			Required("screen_title", "platform", "density", "primary_cta", "sections")
		})
		Result(DPISpec)
		Tool("generate_dpi_spec", "Generate a deterministic design implementation plan from fake Figma data")
		JSONRPC(func() {})
	})
})

// ---- Shared Types (subset sufficient for integration tests) ----

var Documents = Type("Documents", func() {
	Attribute("items", ArrayOf(String), "Document entries")
	Required("items")
})

var PromptTemplates = Type("PromptTemplates", func() {
	Attribute("templates", ArrayOf(String), "Templates")
	Required("templates")
})

var DesignTokenGroup = Type("DesignTokenGroup", func() {
	Attribute("colors", ArrayOf(String), "Color tokens")
	Attribute("spacing", ArrayOf(String), "Spacing tokens")
	Attribute("typography", ArrayOf(String), "Typography tokens")
	Required("colors", "spacing", "typography")
})

var DesignSystem = Type("DesignSystem", func() {
	Attribute("name", String, "Design system name")
	Attribute("version", String, "Design system version")
	Attribute("platform", String, "Platform the tokens target")
	Attribute("tokens", DesignTokenGroup, "Grouped token information")
	Required("name", "version", "platform", "tokens")
})

var DPIViewport = Type("DPIViewport", func() {
	Attribute("width", Int, "Viewport width")
	Attribute("height", Int, "Viewport height")
	Required("width", "height")
})

var DPISection = Type("DPISection", func() {
	Attribute("name", String, "Section name")
	Attribute("component", String, "Primary UI component")
	Attribute("notes", ArrayOf(String), "Implementation notes for this section")
	Required("name", "component", "notes")
})

var DPICallToAction = Type("DPICallToAction", func() {
	Attribute("label", String, "CTA label")
	Attribute("style", String, "CTA visual style")
	Required("label", "style")
})

var DPISpec = Type("DPISpec", func() {
	Attribute("screen_title", String, "Screen title")
	Attribute("platform", String, "Target platform")
	Attribute("density", String, "Layout density")
	Attribute("viewport", DPIViewport, "Viewport dimensions")
	Attribute("sections", ArrayOf(DPISection), "Ordered screen sections")
	Attribute("primary_cta", DPICallToAction, "Primary CTA")
	Attribute("design_tokens_uri", String, "Design system resource URI")
	Attribute("dev_notes", ArrayOf(String), "Development handoff notes")
	Required("screen_title", "platform", "density", "viewport", "sections", "primary_cta", "design_tokens_uri", "dev_notes")
})
