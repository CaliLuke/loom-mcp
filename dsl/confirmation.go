package dsl

import (
	"github.com/CaliLuke/loom/eval"

	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
)

// Confirmation declares that the current tool always requires explicit out-of-band
// operator confirmation before execution.
//
// Confirmation must appear inside a Tool DSL in a Toolset.
//
// The runtime emits AwaitConfirmation and pauses the tool call. Applications
// resume it through ProvideConfirmation; the runtime records ToolAuthorization
// and executes only approved calls. Denials return a schema-compliant result.
func Confirmation(dsl func()) {
	tool, ok := eval.Current().(*agentsexpr.ToolExpr)
	if !ok {
		incompatibleDSL("Confirmation")
		return
	}
	tool.Confirmation = &agentsexpr.ToolConfirmationExpr{}
	if dsl != nil {
		eval.Execute(dsl, tool.Confirmation)
	}
}

// PromptTemplate sets the operator-facing prompt template rendered
// during confirmation. The template is executed with the tool payload value.
func PromptTemplate(tmpl string) {
	c, ok := eval.Current().(*agentsexpr.ToolConfirmationExpr)
	if !ok {
		incompatibleDSL("PromptTemplate")
		return
	}
	c.PromptTemplate = tmpl
}

// DeniedResultTemplate sets the JSON template used to construct a
// schema-compliant tool result when the user denies confirmation. The template
// is executed with the tool payload value and must render valid JSON.
func DeniedResultTemplate(tmpl string) {
	c, ok := eval.Current().(*agentsexpr.ToolConfirmationExpr)
	if !ok {
		incompatibleDSL("DeniedResultTemplate")
		return
	}
	c.DeniedResultTemplate = tmpl
}
