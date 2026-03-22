package codegen

import (
	goacodegen "github.com/CaliLuke/loom/codegen"
)

// Register agent code generation plugins with Goa.
// This ensures the plugin hooks run during both generation and example scaffolding.
func init() {
	goacodegen.RegisterPluginFirst("agent", "gen", Prepare, Generate)
	goacodegen.RegisterPlugin("agent", "example", nil, GenerateExample)
}
