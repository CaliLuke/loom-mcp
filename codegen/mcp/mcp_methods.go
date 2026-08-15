package codegen

import (
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/expr"
)

// buildMethods creates the minimal unary service contract used by MCPAdapter.
// The official SDK owns protocol-level initialize, ping, list, completion,
// cancellation, and subscription methods.
func (b *mcpExprBuilder) buildMethods() []*expr.MethodExpr {
	methods := make([]*expr.MethodExpr, 0, 3)
	// The adapter's interceptor and tool-search extension points use the unary
	// tools/call contract even when a design currently registers no tools. The
	// SDK still infers the public tools capability from actual registrations.
	methods = append(methods, b.buildToolsCallMethod())
	if b.hasResources() {
		methods = append(methods, b.buildResourcesReadMethod())
	}
	if b.hasPrompts() {
		methods = append(methods, b.buildPromptsGetMethod())
	}
	return methods
}

func (b *mcpExprBuilder) buildToolsCallMethod() *expr.MethodExpr {
	return &expr.MethodExpr{
		Name:        "tools/call",
		Description: "Call a tool",
		Payload:     b.userTypeAttr("ToolsCallPayload", b.buildToolsCallPayloadType),
		Result:      b.userTypeAttr("ToolsCallResult", b.buildToolsCallResultType),
	}
}

func (b *mcpExprBuilder) buildResourcesReadMethod() *expr.MethodExpr {
	return &expr.MethodExpr{
		Name:        "resources/read",
		Description: "Read a resource",
		Payload:     b.userTypeAttr("ResourcesReadPayload", b.buildResourcesReadPayloadType),
		Result:      b.userTypeAttr("ResourcesReadResult", b.buildResourcesReadResultType),
	}
}

func (b *mcpExprBuilder) buildPromptsGetMethod() *expr.MethodExpr {
	return &expr.MethodExpr{
		Name:        "prompts/get",
		Description: "Get a prompt by name",
		Payload:     b.userTypeAttr("PromptsGetPayload", b.buildPromptsGetPayloadType),
		Result:      b.userTypeAttr("PromptsGetResult", b.buildPromptsGetResultType),
	}
}

func (b *mcpExprBuilder) hasResources() bool {
	return len(b.mcp.Resources) > 0 || len(b.mcp.SkillDirectories) > 0
}

func (b *mcpExprBuilder) hasPrompts() bool {
	if len(b.mcp.Prompts) > 0 {
		return true
	}
	if mcpexpr.Root != nil {
		return len(mcpexpr.Root.DynamicPrompts[b.originalService.Name]) > 0
	}
	return false
}
