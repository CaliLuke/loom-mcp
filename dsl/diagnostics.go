package dsl

import (
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
)

// incompatibleDSL reports the public DSL function whose context is invalid.
func incompatibleDSL(name string) {
	eval.ReportError("invalid use of %s", name)
}

// mcpRequiredDSL distinguishes a missing service MCP declaration from a
// general context mismatch.
func mcpRequiredDSL(name string, service *goaexpr.ServiceExpr) {
	eval.ReportError("%s requires service %q to declare MCP", name, service.Name)
}
