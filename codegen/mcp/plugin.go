package codegen

import (
	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

// PrepareServices validates the full pure-MCP generation contract before
// filtering HTTP transport generation for MCP-enabled services. Callers may use
// Generate directly, but when PrepareServices is part of the pipeline it
// guarantees invalid MCP designs fail before any transport pruning happens.
func PrepareServices(_ string, roots []eval.Root) error {
	source := collectSourceSnapshot(roots)
	for _, root := range roots {
		r, ok := root.(*expr.RootExpr)
		if !ok {
			continue
		}

		for _, svc := range r.Services {
			if mcpexpr.Root != nil && mcpexpr.Root.HasMCP(svc) {
				if err := validatePureMCPService(svc, mcpexpr.Root.GetMCP(svc), source); err != nil {
					return err
				}
				synthesizePureMCPJSONRPCEndpoints(r, svc)
			}
		}

		// Filter out HTTP transport for MCP-enabled services to avoid
		// generating conflicting HTTP SSE code. Keep JSON-RPC so the
		// service interface remains JSON-RPC SSE-aware where applicable.
		if r.API != nil && r.API.HTTP != nil {
			filtered := make([]*expr.HTTPServiceExpr, 0, len(r.API.HTTP.Services))
			for _, hs := range r.API.HTTP.Services {
				if hs.ServiceExpr != nil && mcpexpr.Root != nil && mcpexpr.Root.HasMCP(hs.ServiceExpr) {
					// Skip HTTP generation for MCP-enabled service
					continue
				}
				filtered = append(filtered, hs)
			}
			r.API.HTTP.Services = filtered
		}
	}

	return nil
}

func synthesizePureMCPJSONRPCEndpoints(root *expr.RootExpr, svc *expr.ServiceExpr) {
	if root == nil || root.API == nil {
		return
	}
	jsonrpcSvc := root.API.JSONRPC.Service(svc.Name)
	if jsonrpcSvc == nil || jsonrpcSvc.JSONRPCRoute == nil {
		return
	}
	if jsonrpcSvc.Root == nil {
		jsonrpcSvc.Root = &root.API.JSONRPC.HTTPExpr
	}

	existing := make(map[string]struct{}, len(jsonrpcSvc.HTTPEndpoints))
	for _, endpoint := range jsonrpcSvc.HTTPEndpoints {
		if endpoint != nil && endpoint.MethodExpr != nil {
			existing[endpoint.MethodExpr.Name] = struct{}{}
		}
	}

	for _, method := range svc.Methods {
		if _, ok := existing[method.Name]; ok {
			continue
		}
		jsonrpcSvc.HTTPEndpoints = append(jsonrpcSvc.HTTPEndpoints, &expr.HTTPEndpointExpr{
			MethodExpr: method,
			Service:    jsonrpcSvc,
			Meta: expr.MetaExpr{
				"jsonrpc": []string{},
			},
			Body:    method.Payload,
			Params:  expr.NewEmptyMappedAttributeExpr(),
			Headers: expr.NewEmptyMappedAttributeExpr(),
			Cookies: expr.NewEmptyMappedAttributeExpr(),
			Routes: []*expr.RouteExpr{{
				Method:   jsonrpcSvc.JSONRPCRoute.Method,
				Path:     jsonrpcSvc.JSONRPCRoute.Path,
				Endpoint: nil, // set below once the endpoint exists
			}},
		})
		endpoint := jsonrpcSvc.HTTPEndpoints[len(jsonrpcSvc.HTTPEndpoints)-1]
		endpoint.Routes[0].Endpoint = endpoint
		if method.Stream == expr.ServerStreamKind {
			endpoint.SSE = &expr.HTTPSSEExpr{}
		}
	}
	previousRoot := expr.Root
	expr.Root = root
	defer func() {
		expr.Root = previousRoot
	}()

	jsonrpcSvc.Prepare()
	for _, endpoint := range jsonrpcSvc.HTTPEndpoints {
		endpoint.Prepare()
	}
}
