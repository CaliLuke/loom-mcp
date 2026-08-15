package codegen

import (
	"fmt"
	"sort"
	"strings"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/expr"
)

// validatePureMCPService enforces that every method in an MCP-enabled service
// is represented by a standard MCP tool, resource, prompt, or projected tool.
func validatePureMCPService(svc *expr.ServiceExpr, mcp *mcpexpr.MCPExpr, source *sourceSnapshot) error {
	if err := validatePureMCPResources(svc, mcp.Resources); err != nil {
		return err
	}

	mapped := make(map[string]struct{}, len(svc.Methods))
	for _, tool := range mcp.Tools {
		mapped[tool.Method.Name] = struct{}{}
	}
	for _, resource := range mcp.Resources {
		mapped[resource.Method.Name] = struct{}{}
	}
	if mcpexpr.Root != nil {
		for _, prompt := range mcpexpr.Root.DynamicPrompts[svc.Name] {
			mapped[prompt.Method.Name] = struct{}{}
		}
	}
	for method := range source.projectedMethodNames(svc.Name, mcp.Name) {
		mapped[method] = struct{}{}
	}

	unmapped := make([]string, 0, len(svc.Methods))
	for _, method := range svc.Methods {
		if _, ok := mapped[method.Name]; ok {
			continue
		}
		unmapped = append(unmapped, method.Name)
	}
	if len(unmapped) == 0 {
		return nil
	}

	sort.Strings(unmapped)
	return fmt.Errorf(
		`service %q has methods not mapped to MCP constructs: %s`,
		svc.Name,
		strings.Join(unmapped, ", "),
	)
}

// validatePureMCPResources rejects resource payloads the adapter cannot map to
// deterministic URI query parameters without generic runtime coercion.
func validatePureMCPResources(svc *expr.ServiceExpr, resources []*mcpexpr.ResourceExpr) error {
	for _, resource := range resources {
		if resource.Method.Payload == nil || resource.Method.Payload.Type == expr.Empty {
			continue
		}
		if _, err := buildResourceQueryFields(resource.Method.Payload); err != nil {
			return fmt.Errorf(
				`service %q resource method %q has incompatible resource query payload: %w`,
				svc.Name,
				resource.Method.Name,
				err,
			)
		}
	}
	return nil
}
