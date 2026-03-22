package codegen

import (
	goacodegen "github.com/CaliLuke/loom/codegen"
)

// Register MCP code generation plugins with Goa.
// This ensures the plugin hooks run during both generation and example scaffolding.
func init() {
	goacodegen.RegisterPluginFirst("mcp", "gen", PrepareServices, Generate)
	goacodegen.RegisterPlugin("mcp", "example", PrepareExample, ModifyExampleFiles)
}
