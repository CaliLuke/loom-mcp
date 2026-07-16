package codegen

import (
	"fmt"
	"sort"

	"github.com/CaliLuke/loom/codegen"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/dave/jennifer/jen"
)

// mcpJSONRPCServerMountSection owns the MCP transport mount structurally. It
// replaces Loom's stable jsonrpc-server-mount section using evaluated service
// data, so session routes and policy middleware do not depend on rendered
// source text or upstream formatting.
func mcpJSONRPCServerMountSection(data *httpcodegen.ServiceData) codegen.Section {
	return codegen.NewJenniferSection(jsonrpcServerMountSectionName, func(stmt *jen.Statement) {
		comment := fmt.Sprintf("%s configures the mux to serve the JSON-RPC %s service methods.", data.MountServer, data.Service.Name)
		codegen.Doc(stmt, comment)
		stmt.Func().Id(data.MountServer).
			Params(
				jen.Id("mux").Add(codegen.TypeRef("loomhttp.Muxer")),
				jen.Id("h").Op("*").Id(data.ServerStruct),
			).
			BlockFunc(func(g *jen.Group) {
				g.Id("streamableHTTPSessions").Op(":=").Id("mcpruntime").Dot("NewStreamableHTTPSessions").Call()
				g.Id("requestCancellations").Op(":=").Id("mcpruntime").Dot("NewRequestCancellationRegistry").Call()
				g.Comment("MCP streamable HTTP: all request methods share transport policy and session state")

				handler := jen.Id("h").Dot("ServeHTTP")
				if data.CORS != nil {
					handler = jen.Id("h").Dot("Handler").Dot("ServeHTTP")
				}
				for _, route := range mcpJSONRPCMountRoutes(data) {
					for _, method := range route.methods {
						g.Id("mux").Dot("Handle").Call(
							jen.Lit(method),
							jen.Lit(route.path),
							jen.Id("withMCPPolicyHeaders").Call(
								jen.Id("streamableHTTPSessions"),
								jen.Id("requestCancellations"),
								handler,
							),
						)
					}
					if data.CORS != nil {
						emitMCPJSONRPCCORSPreflight(g, data.CORS, route.path, route.methods)
					}
				}
			})
		stmt.Line()
		codegen.Doc(stmt, comment)
		stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).
			Id(data.MountServer).
			Params(jen.Id("mux").Add(codegen.TypeRef("loomhttp.Muxer"))).
			Block(jen.Id(data.MountServer).Call(jen.Id("mux"), jen.Id("s")))
		stmt.Line()
	})
}

type mcpJSONRPCMountRoute struct {
	path    string
	methods []string
}

func mcpJSONRPCMountRoutes(data *httpcodegen.ServiceData) []mcpJSONRPCMountRoute {
	methodsByPath := make(map[string]map[string]struct{})
	for _, endpoint := range data.Endpoints {
		for _, route := range endpoint.Routes {
			methods := methodsByPath[route.Path]
			if methods == nil {
				methods = make(map[string]struct{})
				methodsByPath[route.Path] = methods
			}
			methods[route.Verb] = struct{}{}
			methods["GET"] = struct{}{}
			methods["DELETE"] = struct{}{}
		}
	}
	paths := make([]string, 0, len(methodsByPath))
	for path := range methodsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	routes := make([]mcpJSONRPCMountRoute, 0, len(paths))
	for _, path := range paths {
		methods := make([]string, 0, len(methodsByPath[path]))
		for method := range methodsByPath[path] {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		routes = append(routes, mcpJSONRPCMountRoute{path: path, methods: methods})
	}
	return routes
}

func emitMCPJSONRPCCORSPreflight(g *jen.Group, cors *httpcodegen.CORSData, path string, methods []string) {
	var handle jen.Code
	if cors.Runtime {
		handle = jen.Id("h").Dot("corsPolicy").Dot("HandlePreflight").Call(
			jen.Id("w"),
			jen.Id("r"),
			mcpStringSlice(methods),
		)
	} else {
		handle = codegen.Expr("loomhttp.HandleCORSPreflight").Call(
			jen.Id("w"),
			jen.Id("r"),
			mcpJSONRPCCORSPolicy(cors),
			mcpStringSlice(methods),
		)
	}
	g.Id("mux").Dot("Handle").Call(
		jen.Lit("OPTIONS"),
		jen.Lit(path),
		jen.Func().Params(
			jen.Id("w").Qual("net/http", "ResponseWriter"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).Block(handle),
	)
}

func mcpJSONRPCCORSPolicy(cors *httpcodegen.CORSData) jen.Code {
	origins := make([]jen.Code, 0, len(cors.Origins))
	for _, origin := range cors.Origins {
		fields := jen.Dict{jen.Id("Pattern"): jen.Lit(origin.Pattern)}
		if origin.Regex {
			fields[jen.Id("Regex")] = jen.True()
		}
		if len(origin.Methods) > 0 {
			fields[jen.Id("Methods")] = mcpStringSlice(origin.Methods)
		}
		if len(origin.Headers) > 0 {
			fields[jen.Id("Headers")] = mcpStringSlice(origin.Headers)
		}
		if len(origin.Expose) > 0 {
			fields[jen.Id("Expose")] = mcpStringSlice(origin.Expose)
		}
		if origin.MaxAge > 0 {
			fields[jen.Id("MaxAge")] = jen.Lit(origin.MaxAge)
		}
		if origin.Credentials {
			fields[jen.Id("Credentials")] = jen.True()
		}
		origins = append(origins, jen.Values(fields))
	}
	return codegen.TypeRef("loomhttp.CORSPolicy").Values(jen.Dict{
		jen.Id("Origins"): jen.Index().Add(codegen.TypeRef("loomhttp.CORSOrigin")).Values(origins...),
	})
}

func mcpStringSlice(values []string) jen.Code {
	items := make([]jen.Code, len(values))
	for i, value := range values {
		items[i] = jen.Lit(value)
	}
	return jen.Index().String().Values(items...)
}

// mcpJSONRPCClientInitSection owns MCP client transport decoration through the
// stable jsonrpc-client-init section and typed service data. This avoids
// rewriting the upstream NewClient function body after rendering.
func mcpJSONRPCClientInitSection(data *httpcodegen.ServiceData) codegen.Section {
	return codegen.NewJenniferSection("jsonrpc-client-init", func(stmt *jen.Statement) {
		codegen.Doc(stmt, fmt.Sprintf("New%s instantiates HTTP clients for all the %s service servers.", data.ClientStruct, data.Service.Name))
		stmt.Func().Id("New"+data.ClientStruct).
			Params(
				jen.Id("scheme").String(),
				jen.Id("host").String(),
				jen.Id("doer").Add(codegen.TypeRef("loomhttp.Doer")),
				jen.Id("enc").Func().Params(jen.Op("*").Qual("net/http", "Request")).Add(codegen.TypeRef("loomhttp.Encoder")),
				jen.Id("dec").Func().Params(jen.Op("*").Qual("net/http", "Response")).Add(codegen.TypeRef("loomhttp.Decoder")),
				jen.Id("restoreBody").Bool(),
			).
			Op("*").Id(data.ClientStruct).
			BlockFunc(func(g *jen.Group) {
				g.Id("doer").Op("=").Op("&").Id("mcpClientDoer").Values(jen.Dict{jen.Id("next"): jen.Id("doer")})
				fields := jen.Dict{
					jen.Id("Doer"):                jen.Id("doer"),
					jen.Id("RestoreResponseBody"): jen.Id("restoreBody"),
					jen.Id("scheme"):              jen.Id("scheme"),
					jen.Id("host"):                jen.Id("host"),
					jen.Id("decoder"):             jen.Id("dec"),
					jen.Id("encoder"):             jen.Id("enc"),
				}
				for _, endpoint := range data.Endpoints {
					if endpoint.SSE != nil {
						fields[jen.Id(endpoint.Method.VarName+"Doer")] = jen.Id("doer")
					}
				}
				g.Return(jen.Op("&").Id(data.ClientStruct).Values(fields))
			})
		stmt.Line()
	})
}
