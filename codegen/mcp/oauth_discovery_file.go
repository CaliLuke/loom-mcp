package codegen

import (
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

// oauthDiscoveryFile returns the per-service OAuth protected-resource
// discovery file, or nil when the service does not declare OAuth. The
// generated file bakes the DSL-declared metadata into package-level
// values and exposes the HTTP handler plus two helpers that server
// mounts and consumer middleware use to build discovery URLs and
// WWW-Authenticate challenges.
func oauthDiscoveryFile(data *AdapterData) *codegen.File {
	if data == nil || data.OAuth == nil {
		return nil
	}
	svcPkg := "mcp_" + codegen.SnakeCase(data.ServiceName)
	path := filepath.Join(codegen.Gendir, svcPkg, "oauth_discovery.go")
	imports := []*codegen.ImportSpec{
		{Path: "encoding/json"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
	}
	return &codegen.File{
		Path: path,
		Sections: []codegen.Section{
			codegen.Header("MCP OAuth protected-resource discovery", data.MCPPackage, imports),
			codegen.MustJenniferSection("mcp-oauth-discovery", func(stmt *jen.Statement) {
				emitOAuthConstants(stmt, data.OAuth)
				emitOAuthMetadataType(stmt)
				emitOAuthHandlers(stmt)
			}),
		},
	}
}

func emitOAuthConstants(stmt *jen.Statement, oauth *OAuthData) {
	stmt.Var().Id("oauthAuthorizationServers").Op("=").Index().String().ValuesFunc(func(g *jen.Group) {
		for _, s := range oauth.AuthorizationServers {
			g.Lit(s)
		}
	}).Line()

	if len(oauth.Scopes) > 0 {
		stmt.Var().Id("oauthScopesSupported").Op("=").Index().String().ValuesFunc(func(g *jen.Group) {
			for _, s := range oauth.Scopes {
				g.Lit(s.Name)
			}
		}).Line()
	} else {
		stmt.Var().Id("oauthScopesSupported").Op("=").Index().String().Values().Line()
	}

	stmt.Var().Id("oauthBearerMethodsSupported").Op("=").Index().String().ValuesFunc(func(g *jen.Group) {
		for _, m := range oauth.BearerMethodsSupported {
			g.Lit(m)
		}
	}).Line()

	stmt.Const().Id("oauthResourceIdentifier").Op("=").Lit(oauth.ResourceIdentifier).Line()
	stmt.Const().Id("oauthResourceDocumentation").Op("=").Lit(oauth.ResourceDocumentationURL).Line()
	stmt.Const().Id("oauthChallengeScope").Op("=").Lit(challengeScope(oauth.Scopes)).Line()
}

func emitOAuthMetadataType(stmt *jen.Statement) {
	stmt.Comment("protectedResourceMetadataDocument is the JSON document returned at the RFC 9728 well-known endpoint.").Line()
	stmt.Type().Id("protectedResourceMetadataDocument").Struct(
		jen.Id("Resource").String().Tag(map[string]string{"json": "resource"}),
		jen.Id("AuthorizationServers").Index().String().Tag(map[string]string{"json": "authorization_servers"}),
		jen.Id("ScopesSupported").Index().String().Tag(map[string]string{"json": "scopes_supported,omitempty"}),
		jen.Id("BearerMethodsSupported").Index().String().Tag(map[string]string{"json": "bearer_methods_supported,omitempty"}),
		jen.Id("ResourceDocumentation").String().Tag(map[string]string{"json": "resource_documentation,omitempty"}),
	).Line()
}

func emitOAuthHandlers(stmt *jen.Statement) {
	stmt.Comment("HandleProtectedResourceMetadata serves the RFC 9728 Protected Resource Metadata document.").Line()
	stmt.Func().Id("HandleProtectedResourceMetadata").Params(
		jen.Id("w").Qual("net/http", "ResponseWriter"),
		jen.Id("r").Op("*").Qual("net/http", "Request"),
	).Block(
		jen.Id("doc").Op(":=").Id("protectedResourceMetadataDocument").Values(jen.Dict{
			jen.Id("Resource"):               jen.Id("resolveOAuthResourceIdentifier").Call(jen.Id("r")),
			jen.Id("AuthorizationServers"):   jen.Id("oauthAuthorizationServers"),
			jen.Id("ScopesSupported"):        jen.Id("oauthScopesSupported"),
			jen.Id("BearerMethodsSupported"): jen.Id("oauthBearerMethodsSupported"),
			jen.Id("ResourceDocumentation"):  jen.Id("oauthResourceDocumentation"),
		}),
		jen.List(jen.Id("payload"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("doc")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Lit("failed to marshal protected resource metadata"), jen.Qual("net/http", "StatusInternalServerError")),
			jen.Return(),
		),
		jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Content-Type"), jen.Lit("application/json")),
		jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Cache-Control"), jen.Lit("max-age=3600")),
		jen.Id("w").Dot("WriteHeader").Call(jen.Qual("net/http", "StatusOK")),
		jen.List(jen.Id("_"), jen.Id("_")).Op("=").Id("w").Dot("Write").Call(jen.Id("payload")),
	).Line()

	stmt.Comment("resolveOAuthResourceIdentifier returns the DSL-declared ResourceIdentifier when set, otherwise derives it from the request per RFC 8707.").Line()
	stmt.Func().Id("resolveOAuthResourceIdentifier").Params(jen.Id("r").Op("*").Qual("net/http", "Request")).String().Block(
		jen.If(jen.Id("oauthResourceIdentifier").Op("!=").Lit("")).Block(jen.Return(jen.Id("oauthResourceIdentifier"))),
		jen.Return(jen.Id("mcpruntime").Dot("CanonicalizeResourceURL").Call(jen.Id("r"))),
	).Line()

	stmt.Comment("OAuthMetadataPath returns the RFC 9728 well-known path for a server mounted at mountPath.").Line()
	stmt.Func().Id("OAuthMetadataPath").Params(jen.Id("mountPath").String()).String().Block(
		jen.Return(jen.Id("mcpruntime").Dot("ProtectedResourceMetadataPath").Call(jen.Id("mountPath"))),
	).Line()

	stmt.Comment("OAuthChallengeHeader formats the RFC 6750 Bearer challenge for requests against a server mounted at mountPath.").Line()
	stmt.Func().Id("OAuthChallengeHeader").Params(
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("mountPath").String(),
	).String().Block(
		jen.Id("base").Op(":=").Id("mcpruntime").Dot("CanonicalizeResourceURL").Call(jen.Id("r")),
		jen.Id("metaURL").Op(":=").Id("buildOAuthMetadataURL").Call(jen.Id("base"), jen.Id("mountPath")),
		jen.Return(jen.Id("mcpruntime").Dot("BuildBearerChallenge").Call(jen.Id("metaURL"), jen.Id("oauthChallengeScope"))),
	).Line()

	stmt.Func().Id("buildOAuthMetadataURL").Params(jen.Id("requestURL"), jen.Id("mountPath").String()).String().Block(
		jen.If(jen.Id("requestURL").Op("==").Lit("")).Block(
			jen.Return(jen.Id("mcpruntime").Dot("ProtectedResourceMetadataPath").Call(jen.Id("mountPath"))),
		),
		jen.List(jen.Id("u"), jen.Id("err")).Op(":=").Qual("net/url", "Parse").Call(jen.Id("requestURL")),
		jen.If(jen.Id("err").Op("!=").Nil().Op("||").Id("u").Dot("Scheme").Op("==").Lit("").Op("||").Id("u").Dot("Host").Op("==").Lit("")).Block(
			jen.Return(jen.Id("mcpruntime").Dot("ProtectedResourceMetadataPath").Call(jen.Id("mountPath"))),
		),
		jen.Id("u").Dot("Path").Op("=").Id("mcpruntime").Dot("ProtectedResourceMetadataPath").Call(jen.Id("mountPath")),
		jen.Id("u").Dot("RawQuery").Op("=").Lit(""),
		jen.Id("u").Dot("Fragment").Op("=").Lit(""),
		jen.Return(jen.Id("u").Dot("String").Call()),
	).Line()
}

func challengeScope(scopes []OAuthScopeData) string {
	names := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s.Name == "" {
			continue
		}
		names = append(names, s.Name)
	}
	return strings.Join(names, " ")
}
