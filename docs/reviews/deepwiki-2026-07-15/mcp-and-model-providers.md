# DeepWiki Review: MCP and Model Providers

Review date: 2026-07-15
Repository and DeepWiki revision: `dd8379472f8341a694e65ce53935fe6cda12c2ad`

Scope: DeepWiki pages 5 through 6.4, compared page-by-page with current code, `docs/`, and `.agents/skills/loom-mcp`.

Classification:

- `DOC-GAP`: user documentation is missing, stale, contradictory, or misleading.
- `SKILL-GAP`: the repo-local skill or one of its routed references is missing or stale.
- `ARCH-IMPROVEMENT`: the implementation contract can be made safer, simpler, or more consistent.
- `DEEPWIKI-INACCURACY`: DeepWiki is incomplete or conflicts with the indexed revision.
- `MATCH`: DeepWiki, code, and local guidance materially agree.

Priorities use `P0` (urgent) through `P3` (low). Confidence is based on direct source inspection at the shared revision.

## Executive findings

The MCP documentation and skill contracts are generally more complete than DeepWiki for current streamable-HTTP conformance, SDK callbacks, compact discovery, projected tools, and skill resources. The main MCP problems are stale examples in the long-form skill guide, two security-relevant documentation contradictions, and missing user-facing coverage for MCP-specific interceptors.

The model-provider documentation is much thinner. `docs/runtime.md` gives useful construction examples, but there is no provider capability matrix or direct Anthropic setup, the Bedrock streaming statement is stale, and the skill gives detailed rules only for Ollama. DeepWiki is useful here, but its model citations and conformance description contain material inaccuracies.

The most urgent implementation issues found by the review were at the MCP authorization boundary. Both were fixed on 2026-07-16: resource request allows now narrow rather than union with server grants, and session-principal ownership is enforced fail-closed across SDK and native JSON-RPC POST, GET, and DELETE with coupled lifecycle state.

At the provider boundary, adaptive rate limiting must observe the whole stream lifecycle, Anthropic tool names need bounded collision-safe encoding, Anthropic and Bedrock tool-use ID substitution must reserve pass-through IDs, and rate-limit middleware must not falsely advertise the optional `model.TokenCounter` capability.

## 5. MCP Protocol Layer

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5-mcp-protocol-layer>

Major claims checked: design-first generation; `MCPAdapter`; SDK-backed and generated JSON-RPC server paths; callers; streamable HTTP/SSE; OAuth; skills; compact discovery.

Local coverage: `DESIGN.md` describes the dual server/caller architecture; `docs/dsl.md`, `docs/runtime.md`, and `docs/mcp_sdk_server.md` cover the public contract; `SKILL.md` and `references/codegen-contracts.md` contain the current maintenance invariants.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | The two-way MCP bridge is accurately described. `codegen/mcp/generate.go` generates the server/client surfaces, `integration_tests/fixtures/assistant/gen/mcp_assistant/adapter_server.go` implements the protocol adapter, and `runtime/mcp/caller.go` is the shared external-caller boundary. `SKILL.md` explicitly documents both directions. |
| MATCH | P3 | High | Tools, resources, prompts, SDK `http.Handler`, compact discovery, resource policy, and caller transports all have direct local coverage in `docs/mcp_sdk_server.md`, `docs/dsl.md`, `docs/runtime.md`, and `DESIGN.md`. Local docs exceed the overview by covering projected method-backed tools, client roots, progress, transport observers, CORS/origin separation, and in-process discovery. |
| DEEPWIKI-INACCURACY | P1 | High | The overview says OAuth discovery generates `.well-known/mcp-oauth`. The implementation and the dedicated OAuth page use RFC 9728 `/.well-known/oauth-protected-resource`; see `runtime/mcp/oauth.go:26-52` and generated `oauth_discovery.go`. The overview is internally inconsistent. |
| DEEPWIKI-INACCURACY | P2 | High | Calling the generated JSON-RPC path a separate “custom, optimized transport implementation” overstates the architecture. `DESIGN.md` and `codegen/mcp/generate.go` show composition on Loom's standard JSON-RPC/SSE generator plus loom-mcp-owned named sections and runtime registries, not an independent transport stack. |
| DOC-GAP | P1 | High | The routed long-form skill guide still teaches removed DSL names `MCPServer(...)` and `MCPTool(...)` (`references/user-guides/mcp-integration.md:104-115,355-366`). Current source and `docs/dsl.md` use `MCP(...)` and `Tool(...)`. Because the skill routes agents to this guide, these are executable documentation failures. |

## 5.1 MCPAdapter & SDKServer

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5.1-mcpadapter-and-sdkserver>

Major claims checked: adapter structure; bounded initialization/principal state; OpenTelemetry; strict decoding and recovery hints; SDK options; request-header precedence; principal binding; resource policy; compact discovery.

Local coverage: `docs/mcp_sdk_server.md` is the primary user guide. `docs/runtime.md` covers transport conformance and broadcaster behavior. `SKILL.md` and `references/codegen-contracts.md` capture generation rules.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | Generated adapter state has a 4096-entry capacity policy and a 24-hour pruning threshold (`adapter_core_jennifer.go:384-407`); principal capture/assert/clear helpers are generated (`adapter_core_jennifer.go:442-495`); telemetry fields are generated on `MCPAdapter`; strict payload decoding and canonical recovery hints are in `codegen/mcp/adapter_tools_jennifer.go`. The threshold is not itself an actively enforced expiry, as noted below. |
| MATCH | P3 | High | `RequestContext` header overlay semantics, stale session-context warning, `TransportObserver`, skill registration, resource policies, projected tools, local registration, and SDK compact-mode restrictions are all documented in `docs/mcp_sdk_server.md`. This is substantially more actionable than DeepWiki. |
| DEEPWIKI-INACCURACY | P1 | High | The page says the bound principal is asserted on subsequent requests and prevents session hijacking. Generated SDK code asserts only GET requests (`sdk_server_file.go:320-327`), while POST and DELETE are not checked; the generated JSON-RPC mount never calls `assertSessionPrincipal`. The only integration proof is correspondingly named `TestGeneratedSDKServerGETEnforcesSessionPrincipal`. |
| DEEPWIKI-INACCURACY | P2 | High | “Cleanup ... after the TTL expires” overstates the adapter contract. `pruneSessionsLocked` runs only when an unknown initialized session is inserted, and `isInitialized` merely checks map membership (`adapter_core_jennifer.go:384-438`). An old entry remains accepted past 24 hours until some later insertion triggers pruning; the timestamp is refreshed by SDK method dispatch, not by every transport request. The 4096 limit also does not bound every principal entry because `captureSessionPrincipal` can populate `sessionPrincipals` independently after SDK initialization. |
| DOC-GAP | P1 | High | The primary guide's `X-Mcp-Allow-Names` callback stores the value under an application-local `allowNamesKey` (`docs/mcp_sdk_server.md:127-144`), but generated policy reads only `mcpruntime.AllowedResourceNamesFromContext`. The example silently fails to apply the advertised restriction. It must call `mcpruntime.WithAllowedResourceNames`, and the guide must state that grant-bearing headers are trusted-proxy inputs, not safe client authority. |
| DOC-GAP | P1 | High | `MCPAdapterOptions.SessionPrincipal` is not documented in the public MCP guide. After the implementation is made complete, document stable-principal extraction, auth middleware ordering, all transport/method coverage, empty-principal behavior, expiry/eviction, and DELETE cleanup. |
| DOC-GAP | P1 | High | `docs/mcp_sdk_server.md` says passing `&mcp.StreamableHTTPOptions{CrossOriginProtection: nil}` disables protection. Generated `sdkStreamableHTTPOptions` replaces every nil policy with `http.NewCrossOriginProtection()` (`sdk_server.go:190-197`), so the documented disable path does not work. Either document that SDK mode cannot disable it or add an explicit supported disable option. |
| DOC-GAP | P2 | High | MCP-specific `ToolCallInterceptors`, `ToolCallInterceptorInfo`, ordering, raw arguments, and authorization/logging use cases are absent from the primary public guide even though the generated option is public (`adapter_server.go:65-106,148-149`). Runtime agent interceptors are a different surface and do not substitute for this documentation. |
| SKILL-GAP | P1 | High | Neither the main skill nor `references/codegen-contracts.md` records the authorization invariant behind resource policies or the session-principal contract. After the implementation is corrected, state that client-controlled input may narrow but never grant resource access, grant context must come from an explicitly trusted resolver, and authenticated session bindings must be checked fail-closed on every transport request. |
| ARCH-IMPROVEMENT | P0 | High | The generated JSON-RPC mount copies the raw client-controlled `x-mcp-allow-names` header into authorization context (`codegen/mcp/generate.go:1378-1385`), and adapter policy unions those names with configured allows (`adapter_jennifer_sections.go:526-566`). A client can broaden `AllowedResourceNames` by naming another resource. Do not accept grant headers implicitly: require an explicit trusted policy resolver/auth middleware, or combine request grants by intersection. Client-controlled deny/narrowing input can remain additive. |
| ARCH-IMPROVEMENT | P1 | High | Session-principal state fails open. `assertSessionPrincipal` returns nil when no binding exists; assertion is limited to SDK GET; JSON-RPC requests are never asserted; TTL/capacity pruning deletes a principal while the upstream SDK session can remain live; and SDK initialization can add principal entries that are not represented in the pruned/capped initialized-session map (`adapter_core_jennifer.go:384-495`, `sdk_server_file.go:320-345`). Enforce `(session ID, principal)` on every inbound POST/GET/DELETE in both transports, reject missing bindings for authenticated sessions, and make adapter and transport session creation/expiry/eviction atomic. Add POST, DELETE, JSON-RPC, initialize-only growth, expiry, and capacity-eviction hijack tests. |
| ARCH-IMPROVEMENT | P2 | High | **Resolved 2026-07-16:** `MCPAdapterOptions.DropIfSlow` is generated as `*bool`; nil preserves safe dropping, explicit false enables backpressure, and the untyped silent fallback is gone. |
| ARCH-IMPROVEMENT | P3 | Medium | After session/principal lifecycle enforcement is unified, consider typed TTL and capacity options with enforced minimum/maximum bounds for deployments with unusually short-lived or high-cardinality sessions; retain the current numeric defaults. |

## 5.2 JSON-RPC Transport & SSE Streaming

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5.2-json-rpc-transport-and-sse-streaming>

Major claims checked: unified POST/GET/DELETE mount; session lifecycle; protocol-version negotiation; origin protection; request cancellation; SSE priming/retry/notification/final frames; batch buffering; omitted params; JSON-RPC errors.

Local coverage: `docs/runtime.md` has a strong “Generated JSON-RPC transport conformance” section. `references/codegen-contracts.md` is the authoritative skill contract. `docs/mcp_sdk_server.md` documents batch isolation and the SDK/JSON-RPC distinction.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | The generated mount owns `StreamableHTTPSessions` and `RequestCancellationRegistry`, validates protocol versions, handles `notifications/cancelled`, binds GET listeners, and terminates sessions on DELETE (`gen/jsonrpc/.../server/server.go:411-590,753+`). Local docs cover all of these, including 32 MiB body inspection and CORS/origin separation. |
| MATCH | P3 | High | Batch isolation, omitted params/arguments, namespaced intermediate notifications, priming IDs, retry frames, final-response suppression, and error mapping match code (`codegen/mcp/generate.go`, generated `server/stream.go`, `server/encode_decode.go`). |
| DEEPWIKI-INACCURACY | P1 | High | DeepWiki calls the SSE endpoint “typically GET `/events/stream`”. The generated transport mounts GET on the design RPC path (`/rpc`) and synthesizes the `events/stream` JSON-RPC method inside the unified handler (`server.go:414-419,116-125`). There is no `/events/stream` HTTP route in the fixture. |
| DEEPWIKI-INACCURACY | P1 | High | `ToolsCallServerStream.Open` writes a unique SSE event `id` from `NewSessionID()`, but that value is not the MCP `Mcp-Session-Id` issued during `initialize`. DeepWiki calls the event ID “the SessionID”, conflating SSE replay identity with the streamable-HTTP session registry. |
| DEEPWIKI-INACCURACY | P2 | High | `StreamableHTTPSessions` tracks issued session IDs and listener cancellation functions; request contexts keyed by JSON-RPC request ID are owned by the separate `RequestCancellationRegistry`. DeepWiki merges these responsibilities. |
| DOC-GAP | P2 | Medium | `docs/runtime.md` is accurate but buries a large transport contract inside the general runtime guide. A dedicated generated-JSON-RPC transport page, cross-linked from `docs/mcp_sdk_server.md`, would make session/cancellation/security operations discoverable and clarify how it differs from SDK mode. |

## 5.3 MCP Caller & Client Adapters

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5.3-mcp-caller-and-client-adapters>

Major claims checked: generated typed caller; SDK-backed HTTP/SSE/stdio callers; stream merging; normalized text/structured/error results; trace metadata; canonical JSON; query coercion; client feature adapters; repair prompts.

Local coverage: `DESIGN.md`, `docs/overview.md`, and `docs/runtime.md` accurately explain the two caller paths and normalization. The routed skill guide contains detailed examples but also stale signatures.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | Generated `Caller.CallTool` drains the generated tools/call stream, merges events, preserves the latest structured/error fields, and delegates to shared normalization (`gen/jsonrpc/.../client/caller.go:36-142`). SDK transports converge through `runtime/mcp.SessionCaller` and `NormalizeToolCallResponse` (`runtime/mcp/caller.go`). |
| MATCH | P3 | High | Canonical JSON snake-case fallback, string-map enforcement, overflow checks, trailing-data rejection, and query coercion match `runtime/mcp/json_codec.go` and `query_coercion.go`. `docs/runtime.md` documents the important boundary guarantees. |
| MATCH | P3 | High | Elicitation, text sampling, roots, and progress adapters live in `runtime/mcp/sdkclient`; generated SDK request contexts install them. `docs/mcp_sdk_server.md` is more complete than this DeepWiki page. |
| DOC-GAP | P1 | High | `docs/runtime.md:2170` calls `mcp.NewStdioCaller(mcp.StdioOptions{...})` without the required `context.Context`. The actual signature is `NewStdioCaller(ctx context.Context, opts StdioOptions)`. The example does not compile. |
| SKILL-GAP | P1 | High | **Resolved 2026-07-16:** the MCP skill guide and generated quickstart use `Toolset(FromMCP(...))` plus the generated `NewCaller(client, suite)` adapter; generator assertions reject the removed forms. |
| DOC-GAP | P2 | Medium | Public docs mention canonical JSON but do not show `CoerceQuery` or explain its exact coercions and non-coercions (`0`/`1` remain numeric, repeated values preserve order). This is useful for resource-template and hand-written caller authors. |

## 5.4 MCP OAuth & Security

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5.4-mcp-oauth-and-security>

Major claims checked: MCP 2025-11-25 auth alignment; RFC 9728 PRM; path-suffixed metadata; strict/lenient canonicalization; Bearer challenges; audience validation; modernization phases.

Local coverage: `docs/dsl.md` and the OAuth section in `docs/runtime.md` are detailed. Current follow-up work lives in `ROADMAP.md`. The skill routes protocol catch-up through the dedicated feature-development workflow.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | PRM fields, path suffixing, safe default proxy posture, strict 400 behavior, lenient challenge fallback, audience claim shapes, and consumer-owned verifier all match `runtime/mcp/oauth.go`, generated `oauth_discovery.go`, `docs/dsl.md`, and `ROADMAP.md`. |
| MATCH | P3 | High | DeepWiki correctly identifies incremental scope consent, client ID metadata, and OIDC discovery as planned, not shipped. Local planning also explicitly excludes bundled JWT/JWKS and authorization-server behavior. |
| DOC-GAP | P1 | High | **Resolved:** `docs/dsl.md` no longer claims that `ErrAudienceMismatch` plus `RequireBearerToken`/`WithOAuthChallenge` automatically emits an RFC 6750 `error="invalid_token"` challenge. Generated code comments and `ROADMAP.md` record the remaining error-aware middleware work. |
| DOC-GAP | P2 | High | User docs should state next to `OAuthScope(...)` that scopes are advertised only; tool-level enforcement and `403 insufficient_scope` are not implemented yet. The DSL table says “documents one scope”, but the limitation is easy to miss in the mounting example. |
| DEEPWIKI-INACCURACY | P3 | High | “A reachable challenge is always returned” is too absolute: `CanonicalizeChallengeOrigin(nil, ...)` or a request with no usable host returns an empty string. The normal HTTP-server path is fine, but the helper contract is not unconditional. |

## 5.5 MCP Runtime Helpers & Skills

URL: <https://deepwiki.com/CaliLuke/loom-mcp/5.5-mcp-runtime-helpers-and-skills>

Major claims checked: request/session context; broadcasters; skill discovery/frontmatter/manifests/security; tool-call interception; compact search.

Local coverage: `docs/mcp_sdk_server.md`, `docs/runtime.md`, and `docs/dsl.md` cover skills and compact discovery. `runtime-contracts.md` covers exact-once session broadcast and hard-error skill discovery. `SKILL.md` covers SDK client callbacks omitted by this page.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | `SkillDirectory` discovery, `skill://<id>/SKILL.md`, `_manifest`, supporting-file containment, frontmatter fields, snapshot-vs-rescan behavior, duplicate/invalid metadata failures, and resource policies all match `runtime/mcp/skills`, generated adapters, and local docs. |
| MATCH | P3 | High | Global broadcast and exactly-one live subscriber per session match `runtime/mcp/broadcast.go` and `runtime-contracts.md`. Tool search and generated interceptor chains match adapter code. |
| DOC-GAP | P2 | High | `docs/runtime.md` gives a short broadcaster example but does not document generated `MCPAdapterOptions.Broadcaster`, `BroadcastBuffer`, or `DropIfSlow`, nor the backpressure/data-loss tradeoff. These are operationally important for server-initiated notifications. |
| DOC-GAP | P2 | High | The public MCP guide should place tool-call interceptors beside broadcaster configuration, including execution order and the warning that raw arguments may contain sensitive values and should not be logged by default. |
| MATCH | P3 | High | Local docs exceed DeepWiki by documenting `Elicit`, text `Sample`, `ListRoots`, and `ReportProgress` with availability errors and progress-token semantics. No new gap was found for these helpers. |

## 6. LLM Provider Adapters

URL: <https://deepwiki.com/CaliLuke/loom-mcp/6-llm-provider-adapters>

Major claims checked: provider-neutral `model.Client`; request/response/part model; Anthropic, Bedrock, OpenAI, Gemini/Vertex, and Ollama adapters; middleware; conformance.

Local coverage: `docs/runtime.md` covers construction, model routing, caching, structured output, and rate limiting. `docs/overview.md` lists part and provider types. The skill's `runtime-contracts.md` only gives detailed provider rules for Ollama.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | The provider-neutral interface and typed part model are accurate. All five adapters implement `model.Client`; Bedrock and Gemini additionally implement exact `model.TokenCounter`. Runtime factory helpers exist for Bedrock, OpenAI, Gemini/Vertex, and Ollama. |
| DEEPWIKI-INACCURACY | P2 | High | The cited `runtime/agent/model/model.go` ranges extend beyond line 2,300, but the file is 891 lines at the indexed revision. The claims mostly remain true, but the source references cannot be verified as linked and indicate stale citation offsets/content. |
| DOC-GAP | P1 | High | **Resolved:** `docs/runtime.md` now includes a user-facing capability matrix covering streaming, structured output, tool choice, cache checkpoints, thinking, and exact token counting. |
| SKILL-GAP | P1 | High | **Resolved:** runtime contracts require the shared provider conformance matrix and record streaming, token-counting, name-codec, and error-normalization invariants. |
| ARCH-IMPROVEMENT | P3 | Medium | Several provider files carry multiple concerns and are very large (`bedrock/client.go` 1181 lines, `gemini/client.go` 884, `anthropic/client.go` 838). Continue splitting request preparation, encoding, response translation, and capabilities into focused files; preserve the package-level API and conformance suite. |

## 6.1 Anthropic Client

URL: <https://deepwiki.com/CaliLuke/loom-mcp/6.1-anthropic-client>

Major claims checked: Messages API request preparation; tool-name and tool-use-ID mapping; thinking/redacted thinking; cache policy; streaming state machine; usage and rate-limit mapping.

Local coverage: prompt cache behavior is documented in `docs/runtime.md`; thinking parts are described in `docs/overview.md`. There is no focused Anthropic user guide or runtime factory example. The skill has no Anthropic-specific contract.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | Validation, message/tool encoding, cache placement, thinking budgets, tool-use ID substitution/restoration, streaming buffers, terminal lifecycle, usage metadata, and HTTP 429 mapping match `features/model/anthropic/client.go`, `stream.go`, and `tool_use_id.go`. |
| DEEPWIKI-INACCURACY | P1 | High | DeepWiki says `github.search` becomes `github_search`. `sanitizeToolName` actually takes the segment after the final dot, so that example becomes `search`; it may also strip a redundant toolset suffix. Collisions are then rejected. |
| ARCH-IMPROVEMENT | P1 | High | Anthropic tool names longer than 64 bytes are detected as unsafe, but the slow sanitization path preserves every otherwise-allowed rune and never truncates. This can still send an invalid provider name. Add deterministic truncate-plus-hash behavior and collision tests, as already implemented for Bedrock/OpenAI (`features/model/anthropic/client.go:667-725`). |
| ARCH-IMPROVEMENT | P1 | High | **Resolved 2026-07-16:** `toolUseIDCodec` reserves all safe transcript IDs before encoding and skips occupied synthetic `tN` values. Mixed pass-through/substituted histories, canonical decode, pairing, and provider-minted pass-through IDs are covered. |
| ARCH-IMPROVEMENT | P2 | High | Anthropic strips namespaces while Bedrock/OpenAI preserve them with underscores. The behavior is fail-fast on collision but creates provider-dependent visible tool names and avoidable collision risk. Reuse a shared internal name-codec contract with provider policy knobs. |
| DOC-GAP | P2 | High | Add direct Anthropic construction (`anthropic.NewFromAPIKey` and `anthropic.New`) and a compact feature/limitation section: no structured output, streaming supported, cache checkpoints supported, thinking budget constraints, tool choice supported, and no exact `TokenCounter`. |
| SKILL-GAP | P2 | High | **Resolved 2026-07-16:** the main skill and runtime contracts distinguish tool-name and tool-use-ID codecs, require safe replay IDs to be reserved, and require request-local correlation mappings. |

## 6.2 AWS Bedrock Client

URL: <https://deepwiki.com/CaliLuke/loom-mcp/6.2-aws-bedrock-client>

Major claims checked: Converse/ConverseStream; model-class routing; tool-name mapping; prompt cache; thinking/adaptive reasoning; citations/usage; structured-output normalization; throttling; exact token counting.

Local coverage: `docs/runtime.md` has a good runtime constructor, cache, and structured-output overview, but not Bedrock's schema-normalization details. Skill guidance does not record Bedrock-specific invariants.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | Converse interfaces, class-based model resolution, request-local tool maps, cache points, thinking, citation/usage aggregation, structured output, and throttling normalization match `features/model/bedrock`. |
| MATCH | P3 | High | DeepWiki accurately captures schema keyword stripping, closed objects, supported formats, and explicit rejection of unrepresentable `additionalProperties`; see `structured_output_schema.go`. It omits the useful exact `CountTokens` capability, which uses the same request path while removing replayed thinking. |
| DOC-GAP | P1 | High | `docs/runtime.md:1863-1866` says Bedrock streaming fails fast with `ErrStructuredOutputUnsupported`. Current code and tests support streamed structured output (`client.go:221-249`, `stream_usage_test.go`, `client_rate_limit_test.go:181+`). Update the provider matrix/prose. |
| SKILL-GAP | P1 | High | `references/user-guides/production/model-rate-limiting.md` constructs `bedrock.NewClient(bedrock.Options{Region, Model})`, but the current API is `bedrock.New(*bedrockruntime.Client, Options{DefaultModel: ...}, ledger)`. This routed reference is not compilable and also imports the old Pulse path later in the example. |
| DOC-GAP | P2 | High | Document Bedrock's schema rewrite/rejection rules and exact token counting. Users otherwise discover provider incompatibilities only at runtime. |
| DEEPWIKI-INACCURACY | P3 | High | The page states the tool-name regex as `^[a-zA-Z0-9_]*$`; code deliberately permits hyphens and enforces a 64-byte maximum with hash truncation (`tool_name.go`). |
| ARCH-IMPROVEMENT | P1 | High | **Resolved 2026-07-16:** Bedrock uses one request-scoped collision-checked codec for tool-use and tool-result blocks, reserves all safe replay IDs before encoding, and skips occupied synthetic `tN` values. Mixed-ID pairing is covered. |

## 6.3 OpenAI, Gemini & Ollama Clients

URL: <https://deepwiki.com/CaliLuke/loom-mcp/6.3-openai-gemini-and-ollama-clients>

Major claims checked: OpenAI Responses API; Gemini API and Vertex; Ollama `/api/chat`; model routing; streaming; structured output; token usage/counting; thinking; tool ownership; runtime factories.

Local coverage: `docs/runtime.md` has useful constructor examples for all four deployment paths and good Ollama/OpenAI notes. `runtime-contracts.md` has strong Ollama guidance but little OpenAI/Gemini guidance.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | OpenAI uses the official Responses API, supports unary/streaming, strict schema projection, tool-name round trips, structured-output streaming, and buffered tool deltas. Gemini supports API-key and Vertex construction plus exact `CountTokens`. Ollama supports local text/images/tools/thinking/structured output and unary/streaming. |
| MATCH | P3 | High | DeepWiki correctly limits its streaming implementation citations to OpenAI and Ollama; Gemini's `Stream` currently returns `model.ErrStreamingUnsupported` (`features/model/gemini/client.go:242-244`). |
| DEEPWIKI-INACCURACY | P1 | High | Despite citing only OpenAI and Ollama stream implementations, the overview says “each adapter” implements `model.Streamer`, and page 6.4 says the interface is implemented by each provider. Gemini explicitly returns `model.ErrStreamingUnsupported`; the narrative should describe streaming as an optional conformance shape, not a universal capability. |
| DOC-GAP | P1 | High | **Resolved:** the provider matrix explicitly records Gemini/Vertex streaming as unsupported with `ErrStreamingUnsupported`. |
| DOC-GAP | P2 | High | Document that OpenAI drops replayed provider thinking/cache checkpoint parts, surfaces reasoning-summary deltas as typed thinking, and forbids structured output with tools. The current runtime guide covers only the last constraint and name/schema mapping. |
| SKILL-GAP | P2 | High | **Resolved:** runtime contracts record Gemini's explicit unsupported-stream contract and exact token counter, plus the shared collision-safe provider name-codec contract. |
| ARCH-IMPROVEMENT | P2 | Medium | Consider an inspectable provider capability surface or generated documentation source so planners and users can distinguish “interface method exists” from “feature supported”. At minimum, make the conformance matrix the single maintained source for docs. |

## 6.4 Model Middleware & Conformance

URL: <https://deepwiki.com/CaliLuke/loom-mcp/6.4-model-middleware-and-conformance>

Major claims checked: adaptive token bucket/AIMD; cluster coordination; request-too-large behavior; provider conformance cases; streaming lifecycle; middleware capabilities.

Local coverage: `docs/runtime.md` and skill production guides cover rate limiting. Conformance itself is not documented for contributors or users, and the skill does not require it.

| Class | Priority | Confidence | Evidence and discrepancy |
| --- | --- | --- | --- |
| MATCH | P3 | High | The limiter uses estimated input tokens, fixed max-TPM burst, 50% multiplicative backoff, 5%-of-initial additive recovery, a 10% floor, optional Pulse replicated-map coordination, and fail-fast `ErrRequestTooLarge`; see `features/model/middleware/ratelimit.go`. |
| DEEPWIKI-INACCURACY | P1 | High | The required conformance matrix also contains `StructuredOutputAndToolChoice`; DeepWiki omits it. All five adapters—Anthropic, Bedrock, OpenAI, Gemini, and Ollama—invoke `RunProviderConformance`, not only the subset shown. |
| DEEPWIKI-INACCURACY | P1 | High | DeepWiki says conformance requires a `ChunkTypeStart`, text, stop sequence. The suite has no such `ChunkTypeStart` assertion, and the provider streams do not generally emit a start chunk. It requires setup-error, receive-error, and terminal cases (`testutil/provider_conformance.go`). |
| ARCH-IMPROVEMENT | P1 | High | `limitedClient.Stream` calls `observe(err)` only when opening the stream, and probes immediately when setup succeeds. It does not wrap `Recv`, so mid-stream `ErrRateLimited` never triggers backoff and an ultimately failed stream can increase TPM. Wrap the streamer and observe the terminal receive outcome; probe only after successful terminal completion. |
| ARCH-IMPROVEMENT | P1 | High | **Resolved 2026-07-16:** the base limited client does not implement `model.TokenCounter`; middleware returns a separate counting wrapper only when the underlying provider implements exact counting. Capability assertions remain truthful. |
| SKILL-GAP | P1 | High | **Resolved:** main skill and runtime contracts require provider conformance, terminal stream observation, and truthful optional capability preservation. |
| SKILL-GAP | P1 | High | The rate-limit guide uses nonexistent/stale APIs (`bedrock.NewClient`, `runtime.WithModelClient`, `limiter.CurrentTPM`) and old imports. Replace snippets with current `bedrock.New`, `rt.RegisterModel`, and available observability hooks, then compile-check them. |
| DOC-GAP | P2 | High | `docs/runtime.md` calls the limiter's response “exponential backoff”; the implementation is AIMD capacity reduction, not time-based exponential retry backoff. Use the precise term to avoid implying automatic request retries. |
| DOC-GAP | P2 | High | **Resolved 2026-07-16:** runtime and production guidance distinguish estimated input admission from delegated exact counting, state that output tokens are not reserved, and preserve non-`TokenCounter` providers after wrapping. |

## Prioritized recommendations

### P0

1. ~~Close the MCP resource-policy authorization bypass: stop turning raw client `x-mcp-allow-names` headers into additive grants; require a trusted policy resolver or intersection semantics and add an adversarial test.~~ Complete 2026-07-16.

### P1

2. ~~Make session-principal binding complete and fail-closed across SDK and generated JSON-RPC POST/GET/DELETE, with atomic expiry/eviction and cross-principal hijack tests.~~ Complete 2026-07-16.
3. **Complete 2026-07-16:** fixed security/transport documentation contradictions:
   - remove the false automatic OAuth `invalid_token` claim;
   - correct the SDK cross-origin disable instructions or add an explicit implementation option.
   - replace the ineffective resource-allow callback with `mcpruntime.WithAllowedResourceNames` and document the trusted-header boundary;
   - documented `SessionPrincipal` after completing its transport contract.
4. **Complete 2026-07-16:** repaired or retired stale runnable examples:
   - `docs/runtime.md` `NewStdioCaller` needs `ctx`;
   - the MCP skill guide must use `MCP`, `Tool`, and `NewCaller(client, suite)`;
   - the rate-limit guide must use current Bedrock/runtime/Pulse APIs and remove nonexistent `CurrentTPM`.
5. Add a provider capability/conformance matrix covering all five adapters and make `RunProviderConformance` a skill-level requirement. Correct Bedrock structured-output streaming and explicitly mark Gemini streaming unsupported.
6. ~~Wrap rate-limited streams so AIMD observes receive-time completion/rate-limit errors and preserve absence of optional `TokenCounter` when the wrapped provider cannot count exactly.~~ Complete 2026-07-16.
7. ~~Fix Anthropic tool-name handling for names over 64 bytes with deterministic hash truncation and collision tests.~~ Complete 2026-07-16.
8. ~~Make Anthropic and Bedrock tool-use ID substitution reserve pass-through IDs so mixed replay histories cannot produce duplicate provider IDs.~~ Complete 2026-07-16.

### P2

9. Add focused public sections for:
   - MCP tool-call interceptors and broadcaster/backpressure configuration;
   - direct Anthropic construction and limitations;
   - Bedrock schema normalization and exact token counting;
   - OpenAI thinking replay/strict-schema behavior;
   - OAuth advertised scopes versus unimplemented scope enforcement.
10. Split the routed skill references into small current contracts, or make them thin links to canonical product docs. Add compile-checked example tests so API drift breaks CI.
11. Unify provider tool-name and tool-use-ID codecs behind shared collision-checked internal contracts while retaining provider-specific allowed-character policy.
12. Consider a dedicated generated-JSON-RPC transport document and an inspectable provider capability surface.

### P3

13. Continue decomposing oversized provider files and consider bounded adapter session options for unusual deployments.
14. Submit corrections to DeepWiki (or account for them during future reviews): OAuth path in page 5, principal-binding and TTL overstatements in 5.1, GET route/session-ID conflation in 5.2, Anthropic name example, universal Gemini streaming implication, stale model line links, and conformance matrix/stream lifecycle claims.

## Page coverage checklist

- [x] 5 — MCP Protocol Layer
- [x] 5.1 — MCPAdapter & SDKServer
- [x] 5.2 — JSON-RPC Transport & SSE Streaming
- [x] 5.3 — MCP Caller & Client Adapters
- [x] 5.4 — MCP OAuth & Security
- [x] 5.5 — MCP Runtime Helpers & Skills
- [x] 6 — LLM Provider Adapters
- [x] 6.1 — Anthropic Client
- [x] 6.2 — AWS Bedrock Client
- [x] 6.3 — OpenAI, Gemini & Ollama Clients
- [x] 6.4 — Model Middleware & Conformance
