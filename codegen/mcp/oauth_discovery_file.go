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
	if data.OAuth.ResourceIdentifier != "" {
		imports = append(imports,
			&codegen.ImportSpec{Path: "context"},
			&codegen.ImportSpec{Path: "fmt"},
			&codegen.ImportSpec{Path: "github.com/modelcontextprotocol/go-sdk/auth", Name: "mcpauth"},
		)
	}
	return &codegen.File{
		Path: path,
		Sections: []codegen.Section{
			codegen.Header("MCP OAuth protected-resource discovery", data.MCPPackage, imports),
			codegen.MustJenniferSection("mcp-oauth-discovery", func(stmt *jen.Statement) {
				emitOAuthConstants(stmt, data.OAuth)
				emitOAuthMetadataType(stmt)
				emitOAuthHandlers(stmt)
				if data.OAuth.ResourceIdentifier != "" {
					emitOAuthAudienceEnforcement(stmt)
				}
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
	// oauthTrustProxyHeaders mirrors the DSL TrustProxyHeaders() opt-in.
	// When false (the default), forwarded headers are ignored by the
	// strict canonicalizer and the challenge-origin fallback — the safe
	// posture for any server reachable directly by clients.
	stmt.Const().Id("oauthTrustProxyHeaders").Op("=").Lit(oauth.TrustProxyHeaders).Line()
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
	stmt.Comment("").Line()
	stmt.Comment("Returns 400 Bad Request when the request carries a malformed").Line()
	stmt.Comment("Forwarded or X-Forwarded-* header, or when the request context").Line()
	stmt.Comment("yields no canonical resource URL (and no ResourceIdentifier is").Line()
	stmt.Comment("declared in the DSL). Emitting a PRM document with an empty or").Line()
	stmt.Comment("attacker-influenced `resource` field would violate RFC 9728 and").Line()
	stmt.Comment("silently accept malformed input; failing loud is the safe default.").Line()
	stmt.Func().Id("HandleProtectedResourceMetadata").Params(
		jen.Id("w").Qual("net/http", "ResponseWriter"),
		jen.Id("r").Op("*").Qual("net/http", "Request"),
	).Block(
		jen.List(jen.Id("resource"), jen.Id("resourceErr")).Op(":=").Id("resolveOAuthResourceIdentifier").Call(jen.Id("r")),
		jen.If(jen.Id("resourceErr").Op("!=").Nil()).Block(
			jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Id("resourceErr").Dot("Error").Call(), jen.Qual("net/http", "StatusBadRequest")),
			jen.Return(),
		),
		jen.Id("doc").Op(":=").Id("protectedResourceMetadataDocument").Values(jen.Dict{
			jen.Id("Resource"):               jen.Id("resource"),
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
	stmt.Comment("").Line()
	stmt.Comment("Returns an error when forwarded headers are present but malformed").Line()
	stmt.Comment("or when no canonical resource URL can be derived. Callers MUST").Line()
	stmt.Comment("surface that error as 400 Bad Request rather than falling back to").Line()
	stmt.Comment("a request-host origin, which would silently accept attacker input.").Line()
	stmt.Func().Id("resolveOAuthResourceIdentifier").Params(jen.Id("r").Op("*").Qual("net/http", "Request")).Params(jen.String(), jen.Error()).Block(
		jen.If(jen.Id("oauthResourceIdentifier").Op("!=").Lit("")).Block(jen.Return(jen.Id("oauthResourceIdentifier"), jen.Nil())),
		jen.Return(jen.Id("mcpruntime").Dot("CanonicalizeResourceURL").Call(jen.Id("r"), jen.Id("oauthTrustProxyHeaders"))),
	).Line()

	stmt.Comment("OAuthMetadataPath returns the RFC 9728 well-known path for a server mounted at mountPath.").Line()
	stmt.Func().Id("OAuthMetadataPath").Params(jen.Id("mountPath").String()).String().Block(
		jen.Return(jen.Id("mcpruntime").Dot("ProtectedResourceMetadataPath").Call(jen.Id("mountPath"))),
	).Line()

	stmt.Comment("OAuthChallengeHeader formats the RFC 6750 Bearer challenge for requests against a server mounted at mountPath.").Line()
	stmt.Comment("").Line()
	stmt.Comment("Uses CanonicalizeChallengeOrigin, which tolerates malformed").Line()
	stmt.Comment("forwarded headers by falling back to the request host rather than").Line()
	stmt.Comment("erroring. A challenge is a formatting artifact inside a 401").Line()
	stmt.Comment("response, not an identity claim — a less-than-ideal challenge URL").Line()
	stmt.Comment("is safer than no challenge at all. The PRM handler, which does").Line()
	stmt.Comment("emit identity, uses the strict canonicalizer instead.").Line()
	stmt.Func().Id("OAuthChallengeHeader").Params(
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("mountPath").String(),
	).String().Block(
		jen.Id("base").Op(":=").Id("mcpruntime").Dot("CanonicalizeChallengeOrigin").Call(jen.Id("r"), jen.Id("oauthTrustProxyHeaders")),
		jen.Id("metaURL").Op(":=").Id("buildOAuthMetadataURL").Call(jen.Id("base"), jen.Id("mountPath")),
		jen.Return(jen.Id("mcpruntime").Dot("BuildBearerChallenge").Call(jen.Id("metaURL"), jen.Id("oauthChallengeScope"))),
	).Line()

	stmt.Comment("OAuthInvalidTokenChallengeHeader formats the RFC 6750 Bearer invalid_token challenge, used when a decoded token fails audience binding, is expired, or is revoked.").Line()
	stmt.Func().Id("OAuthInvalidTokenChallengeHeader").Params(
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("mountPath").String(),
		jen.Id("errorDescription").String(),
	).String().Block(
		jen.Id("base").Op(":=").Id("mcpruntime").Dot("CanonicalizeChallengeOrigin").Call(jen.Id("r"), jen.Id("oauthTrustProxyHeaders")),
		jen.Id("metaURL").Op(":=").Id("buildOAuthMetadataURL").Call(jen.Id("base"), jen.Id("mountPath")),
		jen.Return(jen.Id("mcpruntime").Dot("BuildInvalidTokenChallenge").Call(jen.Id("metaURL"), jen.Id("errorDescription"))),
	).Line()

	stmt.Comment("ExpectedResourceIdentifier returns the DSL-declared ResourceIdentifier or an empty string when audience binding is not pinned.").Line()
	stmt.Func().Id("ExpectedResourceIdentifier").Params().String().Block(
		jen.Return(jen.Id("oauthResourceIdentifier")),
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

// emitOAuthAudienceEnforcement generates the EnforceAudience verifier
// wrapper and its ErrAudienceMismatch sentinel. Emitted only when the
// DSL declares ResourceIdentifier — audience enforcement requires a
// concrete expected-audience value, which the pinned identifier
// provides. Servers that derive the resource URI from the request
// context cannot enforce audience binding in the framework; that
// responsibility stays with the consumer in that mode.
//
// The wrapper exists so consumers who declare ResourceIdentifier cannot
// silently forget to audience-bind their verifier. Wrap once at mount
// time and every tool call is guaranteed to reject tokens minted for a
// different resource, with a spec-compliant invalid_token challenge.
func emitOAuthAudienceEnforcement(stmt *jen.Statement) {
	stmt.Comment("ErrAudienceMismatch is returned by EnforceAudience when a decoded token's").Line()
	stmt.Comment("`aud` claim does not match the DSL-declared ResourceIdentifier. It").Line()
	stmt.Comment("unwraps to mcpauth.ErrInvalidToken so go-sdk/auth.RequireBearerToken returns").Line()
	stmt.Comment("401 and mcpruntime.WithOAuthChallenge augments the response with the").Line()
	stmt.Comment("standard Bearer challenge (`resource_metadata=\"...\"`, plus `scope=\"...\"`").Line()
	stmt.Comment("when scopes are declared).").Line()
	stmt.Comment("").Line()
	stmt.Comment("Note: WithOAuthChallenge is error-agnostic — it does not distinguish").Line()
	stmt.Comment("audience mismatch from a missing token, so it does NOT add").Line()
	stmt.Comment("`error=\"invalid_token\"` to the challenge on its own. Callers who need the").Line()
	stmt.Comment("RFC 6750 §3.1 invalid_token challenge parameter should either emit it").Line()
	stmt.Comment("manually from a verifier wrapper (see OAuthInvalidTokenChallengeHeader) or").Line()
	stmt.Comment("wait for the planned error-aware challenge middleware — tracked in").Line()
	stmt.Comment("MCP_AUTH_MODERNIZATION_PLAN.md.").Line()
	stmt.Var().Id("ErrAudienceMismatch").Op("=").Qual("fmt", "Errorf").Call(
		jen.Lit("token audience does not match protected resource %q: %w"),
		jen.Id("oauthResourceIdentifier"),
		jen.Id("mcpauth").Dot("ErrInvalidToken"),
	).Line()

	stmt.Comment("EnforceAudience wraps a bearer-token verifier so tokens whose `aud` claim").Line()
	stmt.Comment("does not match the DSL-declared ResourceIdentifier are rejected.").Line()
	stmt.Comment("").Line()
	stmt.Comment("The claim is read from TokenInfo.Extra[\"aud\"] and may be a string, a").Line()
	stmt.Comment("[]string, or a []any (matching how encoding/json decodes a JWT `aud`").Line()
	stmt.Comment("array). Missing or wrong-typed claims are treated as mismatches.").Line()
	stmt.Comment("").Line()
	stmt.Comment("Wrap the consumer's verifier exactly once at mount time:").Line()
	stmt.Comment("").Line()
	stmt.Comment("\tmcpauth.RequireBearerToken(EnforceAudience(verifier), nil)").Line()
	stmt.Func().Id("EnforceAudience").Params(
		jen.Id("base").Id("mcpauth").Dot("TokenVerifier"),
	).Id("mcpauth").Dot("TokenVerifier").Block(
		jen.Return(jen.Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("token").String(),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).Params(jen.Op("*").Id("mcpauth").Dot("TokenInfo"), jen.Error()).Block(
			jen.List(jen.Id("info"), jen.Id("err")).Op(":=").Id("base").Call(jen.Id("ctx"), jen.Id("token"), jen.Id("r")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Id("info"), jen.Id("err"))),
			jen.If(jen.Id("info").Op("==").Nil()).Block(
				// A verifier that returned (nil, nil) is itself a bug, but
				// the response must still be a spec-compliant 401 — wrap
				// mcpauth.ErrInvalidToken so RequireBearerToken treats this
				// as an auth failure rather than letting a nil TokenInfo
				// reach downstream handlers.
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("verifier returned nil TokenInfo: %w"),
					jen.Id("mcpauth").Dot("ErrInvalidToken"),
				)),
			),
			jen.If(jen.Op("!").Id("audienceMatchesResourceIdentifier").Call(jen.Id("info").Dot("Extra").Index(jen.Lit("aud")))).Block(
				jen.Return(jen.Nil(), jen.Id("ErrAudienceMismatch")),
			),
			jen.Return(jen.Id("info"), jen.Nil()),
		)),
	).Line()

	stmt.Comment("audienceMatchesResourceIdentifier tests a JWT `aud` claim (which may be a").Line()
	stmt.Comment("string or an array of strings per RFC 7519 §4.1.3) against the pinned").Line()
	stmt.Comment("resource identifier. Returns false on missing, empty, or wrong-typed").Line()
	stmt.Comment("claims so mismatches surface as invalid_token rather than being silently").Line()
	stmt.Comment("admitted.").Line()
	stmt.Func().Id("audienceMatchesResourceIdentifier").Params(jen.Id("claim").Any()).Bool().Block(
		jen.Switch(jen.Id("v").Op(":=").Id("claim").Assert(jen.Type())).Block(
			jen.Case(jen.String()).Block(
				jen.Return(jen.Id("v").Op("==").Id("oauthResourceIdentifier")),
			),
			jen.Case(jen.Index().String()).Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("s")).Op(":=").Range().Id("v")).Block(
					jen.If(jen.Id("s").Op("==").Id("oauthResourceIdentifier")).Block(jen.Return(jen.True())),
				),
				jen.Return(jen.False()),
			),
			jen.Case(jen.Index().Any()).Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("e")).Op(":=").Range().Id("v")).Block(
					jen.If(jen.List(jen.Id("s"), jen.Id("ok")).Op(":=").Id("e").Assert(jen.String()), jen.Id("ok").Op("&&").Id("s").Op("==").Id("oauthResourceIdentifier")).Block(jen.Return(jen.True())),
				),
				jen.Return(jen.False()),
			),
			jen.Default().Block(jen.Return(jen.False())),
		),
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
