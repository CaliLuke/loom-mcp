# MCP Auth Modernization Plan

Add spec-compliant OAuth 2.0 discovery and challenge plumbing to `loom-mcp`, aligned with the MCP 2025-11-25 authorization spec (`third_party/modelcontextprotocol/docs/specification/2025-11-25/basic/authorization.mdx`).

All batches follow the workflow in `.agents/skills/new-mcp-feature-development/SKILL.md`: start from the bundled spec, add a client-vs-framework validation test, proceed red-green, do not ship until `make lint`, `make test`, `make itest`, and `make verify-mcp-local` are green.

## Current state (baseline)

Audited `codegen/mcp/`, `runtime/mcp/`, `dsl/`, `expr/`, and the assistant fixture:

| Surface                                                                        | State                                                                           |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `SessionPrincipal` hook + `mcpauth.TokenInfoFromContext` fallback              | Wired (`codegen/mcp/adapter_core_jennifer.go` emits it; fixture test covers it) |
| `WWW-Authenticate` challenge on 401                                            | **Missing**                                                                     |
| Protected Resource Metadata (RFC 9728 `/.well-known/oauth-protected-resource`) | **Missing**                                                                     |
| OAuth DSL surface on MCP blocks                                                | **Missing** (only registry-level `Security(...)`)                               |
| Audience binding (RFC 8707 resource indicators)                                | **Missing**                                                                     |
| Incremental scope consent (SEP-835 extension)                                  | **Missing** (partial go-sdk 1.5.0 coverage via PR #834)                         |
| OpenID Connect Discovery 1.0                                                   | **Missing**                                                                     |

Consumers today can install `mcpauth.RequireBearerToken(verifier, nil)` themselves, but the generated server never advertises discovery info and never returns a spec-shaped 401, so clients that don't already know the authorization server cannot bootstrap.

## Design defaults (override if needed)

These defaults shape Batch 1; call out any you want changed before implementation starts.

1. **OAuth 2.0 only for now.** Spec-normative. Other schemes (API key, mTLS) deferred.
2. **Multi-server capable from day one.** RFC 9728 `authorization_servers` is always an array, so the DSL accepts one or many servers with no extra design work.
3. **Fixture stops at "got 401 + valid PRM JSON".** No in-repo mock authorization server in Batch 1. Token verification is plugged in by the consumer via the existing `mcpauth.RequireBearerToken(verifier, ...)` surface; full end-to-end token issuance belongs to a later batch.
4. **OAuth is opt-in per MCP service.** A service that does not declare OAuth behaves exactly as today (no 401, no PRM, no `WWW-Authenticate`). No breaking changes for existing consumers.
5. **Verifier stays consumer-provided.** loom-mcp does not bundle a JWT library, JWKS fetcher, or audience-binding policy — those belong to the consumer's security team. We generate the scaffolding that calls into whatever `mcpauth.TokenVerifier` the consumer wires in.

## Batch 1 — RFC 9728 discovery + 401 `WWW-Authenticate`

Lands the discovery flow in one coherent increment. A spec-compliant client that speaks only the 2025-11-25 protocol can discover the authorization server from an unauthenticated request alone.

### DSL

New block on `MCP(...)`:

```go
OAuth(
    AuthorizationServer("https://auth.example.com"),
    OAuthScope("read", "Read access to tool results"),
    OAuthScope("write", "Mutating tool invocations"),
    // Optional:
    ResourceIdentifier("https://api.example.com/mcp"),
    BearerMethodsSupported("header"),
    ResourceDocumentationURL("https://docs.example.com/mcp-auth"),
)
```

- `OAuth(opts...)` returns a `MCP(...)` option so it slots in alongside `ProtocolVersion`, `WebsiteURL`, etc.
- `AuthorizationServer(url)` may be called multiple times to list several auth servers.
- `OAuthScope(name, description)` documents scopes the resource defines; emitted in both PRM JSON (`scopes_supported`) and in the 401 `WWW-Authenticate` header's `scope` parameter. Named `OAuthScope` rather than `Scope` to avoid colliding with `goa.design/goa/v3/dsl.Scope` when both DSLs are dot-imported in a design file.
- `ResourceIdentifier(url)` declares the canonical resource URI emitted as the `resource` field in PRM JSON. RFC 9728 requires this field. If omitted, the generated handler derives it at request time from the incoming URL (scheme + host + path of the server mount point, canonicalized per RFC 8707). Batch 2 adds audience-binding semantics on top of the same declaration; Batch 1 only advertises it.
- `BearerMethodsSupported(...)` defaults to `["header"]`.
- `ResourceDocumentationURL(...)` surfaces as `resource_documentation` in PRM JSON.

### Expr

New `OAuthExpr` on `MCPExpr`:

```go
type OAuthExpr struct {
    AuthorizationServers     []string
    Scopes                   []ScopeExpr
    ResourceIdentifier       string // optional; auto-derived at request time when empty
    BearerMethodsSupported   []string
    ResourceDocumentationURL string
}

type ScopeExpr struct {
    Name        string
    Description string
}
```

Validated at DSL eval time: at least one `AuthorizationServer`; no duplicate scope names; every `BearerMethodsSupported` entry in `{"header", "body", "query"}`; `ResourceIdentifier` (when set) parses as an absolute URL with no fragment.

### Codegen

1. `codegen/mcp/generate.go` emits a new `oauth_discovery.go` file per MCP service when `OAuthExpr != nil`. Contains:
   - Package constants for the statically-known fields (`authorization_servers`, `scopes_supported`, `bearer_methods_supported`, `resource_documentation`, and `resource` when declared).
   - `HandleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request)` — returns the JSON document with `Content-Type: application/json` and `Cache-Control: max-age=3600`. The `resource` field is always emitted: from the DSL-declared value when set, otherwise derived from the request URL at handler time (`scheme://host<mount-path>`, canonicalized per RFC 8707 — lowercase host, no default port, no fragment, no query, trailing slash stripped). When the request arrives through a trusted reverse proxy, the derivation honors `X-Forwarded-Proto` and `X-Forwarded-Host` (and `Forwarded` per RFC 7239 when present). Consumers deploying behind a proxy they don't fully trust should declare `ResourceIdentifier(...)` to pin the canonical URI — this is documented as the recommended production posture.
   - `WriteUnauthorized(w http.ResponseWriter, resourceMetadataURL, scope string)` — writes 401 + `WWW-Authenticate: Bearer resource_metadata="<url>"`. The `scope="<space-delimited>"` parameter is appended only when at least one scope is declared; services without declared scopes emit the challenge without the `scope` parameter rather than an empty `scope=""`.
2. `codegen/mcp/adapter_core_jennifer.go` learns an `oauthChallenge()` method on `MCPAdapter` returning the correctly-formatted `WWW-Authenticate` header value. No-op when OAuth isn't declared.
3. `codegen/mcp/sdk_server_file.go` and JSON-RPC server mount wiring both route the metadata handler when OAuth is declared. Mount paths are explicit:
   - **Server at origin root (e.g., `https://api.example.com`)**: metadata is mounted at `/.well-known/oauth-protected-resource`.
   - **Server at a subpath (e.g., `https://api.example.com/mcp`)**: metadata is mounted at `/.well-known/oauth-protected-resource<mount-path>` per RFC 9728 §3.1's path-suffixed form so the metadata URI co-locates with the resource. The root path is also mounted as an alias for clients that only probe the root.
   - The `resource_metadata` URL in `WWW-Authenticate` always points at the path-suffixed form so clients that receive a challenge do not need to guess.

### Runtime

- `runtime/mcp/oauth.go` (new): tiny helper package exposing `WriteUnauthorized` and the `WWW-Authenticate` header assembly so generated code and hand-written middleware can agree on format. Pure formatting, no token logic.

### Fixture + scenarios

- Add `OAuth(...)` block to `integration_tests/fixtures/assistant/design/design.go` with one authorization server and two scopes.
- Regenerate via `make regen-assistant-fixture`.
- New scenarios in `integration_tests/scenarios/` covering:
  - `oauth_discovery_well_known_returns_valid_prm` — fetches the path-suffixed metadata URL, asserts `resource`, `authorization_servers`, `scopes_supported`, `bearer_methods_supported`, `resource_documentation`.
  - `oauth_discovery_root_alias_returns_same_prm` — fetches `/.well-known/oauth-protected-resource` at the origin root, expects the same document back.
  - `oauth_unauthenticated_tool_call_returns_401_with_metadata_url` — calls a protected tool without a token, expects 401 + `WWW-Authenticate` header whose `resource_metadata` parameter matches the path-suffixed URL and whose `scope` parameter lists the declared scopes.
  - `oauth_discovery_absent_when_not_declared` — on a service without `OAuth(...)`, both metadata paths return 404.
  - `oauth_prm_resource_derived_from_request` — service declared without `ResourceIdentifier(...)`, PRM response's `resource` field equals the canonicalized request URL.

### Docs

- New page under `docs/` describing the `OAuth(...)` DSL and the discovery flow.
- Update `.agents/skills/loom-mcp/SKILL.md` to point at it.

### Acceptance

- `make lint`, `make test`, `make itest`, `make verify-mcp-local` all green.
- Assistant fixture exposes a working discovery endpoint and emits the 401 challenge when the bearer middleware rejects a request.
- A consumer can plug `mcpauth.RequireBearerToken(verifier, nil)` in front of the generated handler and get a spec-compliant challenge for free.

## Batch 2 — Audience binding (RFC 8707)

Batch 1 already emits `resource` in PRM and accepts the `ResourceIdentifier(...)` DSL. This batch adds the matching enforcement so tokens that don't carry the right audience are rejected.

- loom-mcp does **not** validate `aud` itself. Generated code passes the declared resource identifier into the consumer's `mcpauth.TokenVerifier` via the verifier's request-time parameters, and it is the consumer's verifier that enforces audience binding. This preserves the "verifier stays consumer-provided" boundary.
- A small codegen-emitted wrapper around the consumer-provided verifier refuses to dispatch when the verifier returns an audience-mismatch error, mapping that error to 403 + `WWW-Authenticate: Bearer error="invalid_token", error_description="<verifier message>"`.
- Scenario: token minted for a different audience must be rejected with 403 + `WWW-Authenticate: Bearer error="invalid_token"`.
- Scenario: absence of `ResourceIdentifier(...)` means no audience-binding enforcement — tokens pass to the verifier as before (backward compatible).

## Batch 3 — Incremental scope consent (SEP-835)

Extends the 401/403 pattern so a tool call that needs a stronger scope than the token carries returns a spec-shaped challenge, letting the client request additional scopes and retry.

- New `Tool(..., RequireScope("write"))` DSL on tool declarations.
- Generated adapter inspects `TokenInfo.Scopes` before dispatch; emits 403 + `WWW-Authenticate: Bearer error="insufficient_scope", scope="<missing>"` when a required scope is absent.
- Partial coverage already exists in go-sdk 1.5.0 (PR #834 returns scope in `WWW-Authenticate`). Our job is to wire the tool-level requirement into the generated dispatch path.
- Scenarios: authorized-but-insufficient-scope, missing-token (still falls through to Batch 1 path).

## Batch 4 — OAuth Client ID Metadata Documents (SEP-991)

Recommended client-registration mechanism in 2025-11-25. Server-side, this is minimally invasive: PRM gains `client_registration_types_supported` and can advertise a Client ID Metadata Document URL. Most of the lifting is on the client.

- DSL extension: `ClientRegistration("client_id_metadata_document")`.
- PRM document gains `client_registration_types_supported`.
- Documentation covering how consumers host the Client ID Metadata Document — loom-mcp does not generate or host it.
- Scenario: fetch PRM, assert advertised registration types.

## Batch 5 — OpenID Connect Discovery 1.0

Largest piece. The server advertises both an OAuth 2.0 Authorization Server Metadata endpoint and the OIDC Discovery endpoint when the upstream auth server supports it.

- DSL extension to declare OIDC issuer URL alongside the OAuth 2.0 authorization server.
- Option to proxy/mirror the upstream `.well-known/openid-configuration` so clients don't need to chase redirects, or just advertise the issuer and let clients resolve it themselves (leaning toward the latter).
- Scenarios covering both configurations.

## Deferred / explicitly out of scope

- Shipping a JWT/JWKS implementation. Consumers wire their own `mcpauth.TokenVerifier`.
- A bundled mock authorization server. If we want one for testing, it becomes a separate `testutil/mockoauth` package, not part of the core framework.
- Dynamic client registration flows beyond advertising support. That's upstream auth server territory.
- mTLS, API-key, and session-cookie auth. Different batches if and when requested.

## Risks and open questions

- **SDK drift**: go-sdk 1.5.0's auth package is recently stabilized. Batches 2+ will likely brush against its internals; we should keep our generated code going through `mcpauth` types rather than reimplementing.
- **Consumer lock-in**: the `OAuth(...)` DSL commits us to a particular shape. If the MCP spec's auth surface changes again, the DSL might need breaking changes — accept that, document it, and keep the DSL narrow.
- **Multi-server semantics**: RFC 9728 allows multiple `authorization_servers` but MCP 2025-11-25 is vague on how clients pick one. Our PRM emission handles the list; client-side selection is out of scope.
- **Caching**: PRM is cache-friendly (`max-age=3600` in Batch 1). Revisit if we need per-tenant differentiation.

## Execution order

1. Batch 1 (discovery + 401).
2. Batch 2 (audience binding) — audience-first so scope checks never run against a wrong-audience token. This closes a side-channel: without audience validation, a client could probe which scopes the server requires by presenting tokens minted for a different resource. Batches 2 and 3 are otherwise independent.
3. Batch 3 (scope consent).
4. Batch 4 (client registration advertisement).
5. Batch 5 (OIDC) — only if there's a concrete driver.

Each batch produces its own PR. Don't combine.
