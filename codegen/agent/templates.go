package codegen

import (
	"embed"

	"github.com/CaliLuke/loom/codegen/template"
)

const (
	bootstrapInternalT   = "bootstrap_internal"
	cmdMainT             = "cmd_main"
	exampleExecutorStubT = "example_executor_stub"
	plannerInternalStubT = "planner_internal_stub"
	quickstartReadmeT    = "agents_quickstart"
)

//go:embed templates/*.go.tpl
var templateFS embed.FS

var agentsTemplates = &template.TemplateReader{FS: templateFS}
