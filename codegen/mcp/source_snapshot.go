package codegen

import (
	"sort"

	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

type sourceSnapshot struct {
	services         []*expr.ServiceExpr
	projectedMethods map[string]map[string]struct{}
}

// collectSourceSnapshot captures the original services and projected methods.
// The snapshot is immutable per invocation so generation stays deterministic
// and reentrant without taking ownership of application transports.
func collectSourceSnapshot(roots []eval.Root) *sourceSnapshot {
	serviceByName := make(map[string]*expr.ServiceExpr)
	projectedMethods := make(map[string]map[string]struct{})

	for _, root := range roots {
		switch r := root.(type) {
		case *expr.RootExpr:
			for _, svc := range r.Services {
				serviceByName[svc.Name] = svc
			}
		case *agentsexpr.RootExpr:
			collectProjectedMethods(projectedMethods, r)
			continue
		}
	}

	serviceNames := make([]string, 0, len(serviceByName))
	for name := range serviceByName {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	services := make([]*expr.ServiceExpr, 0, len(serviceNames))
	for _, name := range serviceNames {
		services = append(services, serviceByName[name])
	}

	return &sourceSnapshot{
		services:         services,
		projectedMethods: projectedMethods,
	}
}

func collectProjectedMethods(target map[string]map[string]struct{}, root *agentsexpr.RootExpr) {
	for _, toolset := range gatheredSourceToolsets(root) {
		for _, tool := range toolset.Tools {
			if tool == nil || tool.Method == nil || !tool.ExposesSurface(agentsexpr.ToolSurfaceMCP) || tool.MCPPlacement == nil {
				continue
			}
			key := projectedMethodsKey(tool.MCPPlacement.Service, tool.MCPPlacement.MCPServer)
			if target[key] == nil {
				target[key] = make(map[string]struct{})
			}
			target[key][tool.Method.Name] = struct{}{}
		}
	}
}

func gatheredSourceToolsets(root *agentsexpr.RootExpr) []*agentsexpr.ToolsetExpr {
	if root == nil {
		return nil
	}
	var out []*agentsexpr.ToolsetExpr
	out = append(out, root.Toolsets...)
	for _, agent := range root.Agents {
		if agent == nil {
			continue
		}
		if agent.Used != nil {
			out = append(out, agent.Used.Toolsets...)
		}
		if agent.Exported != nil {
			out = append(out, agent.Exported.Toolsets...)
		}
	}
	for _, serviceExport := range root.ServiceExports {
		if serviceExport != nil {
			out = append(out, serviceExport.Toolsets...)
		}
	}
	return out
}

func projectedMethodsKey(serviceName string, mcpServer string) string {
	return serviceName + "\x00" + mcpServer
}

func (s *sourceSnapshot) projectedMethodNames(serviceName string, mcpServer string) map[string]struct{} {
	if s == nil {
		return nil
	}
	return s.projectedMethods[projectedMethodsKey(serviceName, mcpServer)]
}
