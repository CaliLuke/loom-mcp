package codegen

import (
	"sort"

	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

type sourceSnapshot struct {
	services         []*expr.ServiceExpr
	jsonrpcRoutes    map[string]sourceJSONRPCRoute
	projectedMethods map[string]map[string]struct{}
}

type sourceJSONRPCRoute struct {
	method string
	path   string
}

// collectSourceSnapshot captures the original services and JSON-RPC routes from
// the current Goa roots. The snapshot is immutable per invocation so generation
// stays deterministic and reentrant while preserving the source transport
// contract for validation.
func collectSourceSnapshot(roots []eval.Root) *sourceSnapshot {
	serviceByName := make(map[string]*expr.ServiceExpr)
	jsonrpcRoutes := make(map[string]sourceJSONRPCRoute)
	projectedMethods := make(map[string]map[string]struct{})

	for _, root := range roots {
		switch r := root.(type) {
		case *expr.RootExpr:
			for _, svc := range r.Services {
				serviceByName[svc.Name] = svc
			}
			if r.API == nil || r.API.JSONRPC == nil {
				continue
			}
			for _, service := range r.API.JSONRPC.Services {
				if service.ServiceExpr == nil || service.JSONRPCRoute == nil {
					continue
				}
				jsonrpcRoutes[service.ServiceExpr.Name] = sourceJSONRPCRoute{
					method: service.JSONRPCRoute.Method,
					path:   service.JSONRPCRoute.Path,
				}
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
		jsonrpcRoutes:    jsonrpcRoutes,
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

func (s *sourceSnapshot) jsonrpcRoute(serviceName string) (sourceJSONRPCRoute, bool) {
	route, ok := s.jsonrpcRoutes[serviceName]
	return route, ok
}

func (s *sourceSnapshot) jsonrpcPath(serviceName string) (string, bool) {
	route, ok := s.jsonrpcRoute(serviceName)
	if !ok || route.path == "" {
		return "", false
	}
	return route.path, true
}

func (s *sourceSnapshot) projectedMethodNames(serviceName string, mcpServer string) map[string]struct{} {
	if s == nil {
		return nil
	}
	return s.projectedMethods[projectedMethodsKey(serviceName, mcpServer)]
}
