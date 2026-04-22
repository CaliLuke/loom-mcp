# MCP Auth Modernization Plan

Remaining OAuth 2.0 work for `loom-mcp`, aligned with the MCP 2025-11-25 authorization spec (`third_party/modelcontextprotocol/docs/specification/2025-11-25/basic/authorization.mdx`).

All batches follow the workflow in `.agents/skills/new-mcp-feature-development/SKILL.md`: start from the bundled spec, add a client-vs-framework validation test, proceed red-green, do not ship until `make lint`, `make test`, `make itest`, and `make verify-mcp-local` are green.

## Foundation (shipped)

The discovery, challenge, and audience surfaces are in place. New auth work builds on them, not around them.

- RFC 9728 Protected Resource Metadata at `/.well-known/oauth-protected-resource` and the path-suffixed alias.
- RFC 6750 Bearer challenge on 401 via `mcpruntime.WithOAuthChallenge` (case-insensitive `resource_metadata=` detection per RFC 7235; CR/LF stripped from all header-embedded values).
- RFC 8707 canonical resource URI, derived strictly for PRM (400 on malformed forwarded headers) and leniently for challenge emission (falls back to `r.Host` rather than failing the response).
- Default forwarded-header posture: **not trusted**. `TrustProxyHeaders()` DSL option opts in when the operator fully controls an upstream proxy.
- Generated `EnforceAudience(base)` wrapper when `ResourceIdentifier(...)` is declared. Wraps a consumer verifier so mismatched `aud` claims (string, `[]string`, or `[]any` shape) fail closed with `ErrAudienceMismatch`, which unwraps to `mcpauth.ErrInvalidToken`.

User-facing docs: `docs/dsl.md` (OAuth options) and `docs/runtime.md` (OAuth 2.0 Protected Resource section).

## Design defaults (override if needed)

1. **OAuth 2.0 only for now.** Spec-normative. Other schemes (API key, mTLS) deferred.
2. **Multi-server capable.** RFC 9728 `authorization_servers` is always an array.
3. **OAuth is opt-in per MCP service.** Services that do not declare OAuth behave exactly as today.
4. **Verifier stays consumer-provided.** loom-mcp does not bundle a JWT library, JWKS fetcher, or audience-binding policy.

## Follow-up — Error-aware challenge middleware

`mcpruntime.WithOAuthChallenge` currently augments every 401 with the same Bearer challenge (resource_metadata + scope). It does not inspect the verifier error chain, so an `ErrAudienceMismatch` (wrapping `mcpauth.ErrInvalidToken`) returns 401 with the generic challenge rather than the RFC 6750 §3.1 form with `error="invalid_token"`.

Fix shape:

- Option A: extend `WithOAuthChallenge` to accept an optional `ErrorClassifier` that inspects a context-threaded error kind and emits the right challenge form. Requires the SDK's `RequireBearerToken` to stash the classified error on the request context (v1.5.0 does not appear to do this yet).
- Option B: own the 401 emission ourselves — replace the `RequireBearerToken + WithOAuthChallenge` pair with a single generated middleware that wraps the verifier, knows the error kind, and emits the exact RFC 6750 challenge form. Heavier but removes the split-ownership problem.

Lean toward Option B once it becomes a real user ask. Until then, the current split emits a spec-adequate 401 (PRM pointer is present, scope is listed); the missing `error="invalid_token"` parameter is advisory, not load-bearing.

## Batch 3 — Incremental scope consent (SEP-835)

Extends the 401/403 pattern so a tool call that needs a stronger scope than the token carries returns a spec-shaped challenge, letting the client request additional scopes and retry.

- New `Tool(..., RequireScope("write"))` DSL on tool declarations.
- Generated adapter inspects `TokenInfo.Scopes` before dispatch; emits 403 + `WWW-Authenticate: Bearer error="insufficient_scope", scope="<missing>"` when a required scope is absent.
- Partial coverage already exists in go-sdk 1.5.0 (PR #834 returns scope in `WWW-Authenticate`). Our job is to wire the tool-level requirement into the generated dispatch path.
- Scenarios: authorized-but-insufficient-scope, missing-token (still falls through to the Batch 1 discovery path).

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

- **SDK drift**: go-sdk 1.5.0's auth package is recently stabilized. Batches 3+ will likely brush against its internals; we should keep our generated code going through `mcpauth` types rather than reimplementing.
- **Multi-server semantics**: RFC 9728 allows multiple `authorization_servers` but MCP 2025-11-25 is vague on how clients pick one. Our PRM emission handles the list; client-side selection is out of scope.

## Execution order

1. Batch 3 (scope consent).
2. Batch 4 (client registration advertisement).
3. Batch 5 (OIDC) — only if there's a concrete driver.

Each batch produces its own PR. Don't combine.
