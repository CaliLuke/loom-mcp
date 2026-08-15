package codegen

import (
	"fmt"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/expr"

	"github.com/CaliLuke/loom-mcp/v2/codegen/shared"
)

// mcpExprBuilder builds Goa expressions for the MCP protocol service.
// It embeds the shared ProtocolExprBuilderBase for common functionality.
type mcpExprBuilder struct {
	*shared.ProtocolExprBuilderBase
	originalService *expr.ServiceExpr
	mcp             *mcpexpr.MCPExpr
	source          *sourceSnapshot
	mcpService      *expr.ServiceExpr
	root            *expr.RootExpr
	// projectedToolCount counts agent toolset tools projected into this MCP
	// server via MCPPlacement. Those tools reach AdapterData.Tools through
	// buildToolAdapters, so tools/list + tools/call methods and their types
	// must be emitted even when the DSL declares no method-level MCP tools.
	projectedToolCount int
}

// newMCPExprBuilder creates a new MCP expression builder for the given
// original service and its associated MCP expression configuration.
// projectedToolCount reports how many agent toolset tools are projected into
// this MCP server via MCPPlacement so the tool surface is generated even when
// the design declares no method-level MCP tools.
func newMCPExprBuilder(svc *expr.ServiceExpr, mcp *mcpexpr.MCPExpr, source *sourceSnapshot, projectedToolCount int) *mcpExprBuilder {
	return &mcpExprBuilder{
		ProtocolExprBuilderBase: shared.NewProtocolExprBuilderBase(),
		originalService:         svc,
		mcp:                     mcp,
		source:                  source,
		projectedToolCount:      projectedToolCount,
	}
}

// BuildServiceExpr creates the Goa service expression that models the MCP
// adapter contract for the original service.
func (b *mcpExprBuilder) BuildServiceExpr() *expr.ServiceExpr {
	b.mcpService = &expr.ServiceExpr{
		Name:        "mcp_" + b.originalService.Name,
		Description: fmt.Sprintf("MCP SDK adapter service for %s", b.originalService.Name),
		Methods:     b.buildMethods(),
	}

	for _, m := range b.mcpService.Methods {
		m.Service = b.mcpService
	}

	return b.mcpService
}

// userTypeAttr returns an attribute that references the MCP user type with the
// given name. This ensures downstream codegen treats the payload/result as a
// user type instead of inlining the underlying object, which is important for
// generated client body init functions to return pointer types consistently.
func (b *mcpExprBuilder) userTypeAttr(name string, builder func() *expr.AttributeExpr) *expr.AttributeExpr {
	return b.UserTypeAttr(name, builder)
}

// BuildRootExpr creates a temporary Goa root expression containing only the
// internal adapter service used to drive type generation.
func (b *mcpExprBuilder) BuildRootExpr(mcpService *expr.ServiceExpr) *expr.RootExpr {
	// Build all MCP types
	b.buildMCPTypes()

	// Create the root
	b.root = &expr.RootExpr{
		Services: []*expr.ServiceExpr{mcpService},
		Types:    b.CollectUserTypes(),
		API: &expr.APIExpr{
			Name:    "MCP",
			Version: "1.0",
			HTTP:    &expr.HTTPExpr{},
			JSONRPC: &expr.JSONRPCExpr{},
			GRPC: &expr.GRPCExpr{
				Services: []*expr.GRPCServiceExpr{}, // Initialize empty to avoid nil
			},
			// Add server with service name (not "MCP") for consistent CLI generation
			Servers: []*expr.ServerExpr{
				{
					Name:     mcpService.Name,
					Services: []string{mcpService.Name},
				},
			},
		},
	}

	// Initialize the example generator for the API
	b.root.API.ExampleGenerator = &expr.ExampleGenerator{
		Randomizer: expr.NewFakerRandomizer("MCP"),
	}

	return b.root
}

// BuildServiceMapping creates the mapping between MCP methods and original
// service methods, used by templates to wire adapters and clients.
func (b *mcpExprBuilder) BuildServiceMapping() *ServiceMethodMapping {
	mapping := &ServiceMethodMapping{
		ToolMethods:          make(map[string]string),
		ResourceMethods:      make(map[string]string),
		DynamicPromptMethods: make(map[string]string),
	}

	// Map tools to methods
	for _, tool := range b.mcp.Tools {
		mapping.ToolMethods[tool.Name] = tool.Method.Name
	}

	// Map resources to methods
	for _, resource := range b.mcp.Resources {
		mapping.ResourceMethods[resource.Name] = resource.Method.Name
	}

	// Map dynamic prompts to methods
	if mcpexpr.Root != nil {
		dynamicPrompts := mcpexpr.Root.DynamicPrompts[b.originalService.Name]
		for _, dp := range dynamicPrompts {
			mapping.DynamicPromptMethods[dp.Name] = dp.Method.Name
		}
	}

	return mapping
}

// getOrCreateType retrieves or creates a named user type used by the MCP model.
// This delegates to the embedded base type.
func (b *mcpExprBuilder) getOrCreateType(name string, builder func() *expr.AttributeExpr) *expr.UserTypeExpr {
	return b.GetOrCreateType(name, builder)
}

// ServiceMethodMapping maps MCP operations to original service methods.
type ServiceMethodMapping struct {
	ToolMethods          map[string]string
	ResourceMethods      map[string]string
	DynamicPromptMethods map[string]string
}
