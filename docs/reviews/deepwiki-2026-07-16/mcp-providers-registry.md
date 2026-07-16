# DeepWiki Review: MCP, Providers, Registry, and Testing

Reviewed 2026-07-16. DeepWiki is indexed at `dd8379472f8341a694e65ce53935fe6cda12c2ad`; the local review target is `067b8ad`. DeepWiki remains a useful code-derived map of the indexed tree, but it does not include the session-security, provider-conformance, and documentation fixes already present locally. Current source is authoritative.

## Scope and classification

Pages reviewed: 5 through 9.2 — MCP protocol/adapters/transports/callers/OAuth/skills, model providers and middleware, registry, persistence/Pulse, and integration/testing.

- `DOC-GAP`: public or maintainer guidance is missing or misleading.
- `SKILL-GAP`: the routed loom-mcp skill guidance lacks a required contract.
- `ARCH-IMPROVEMENT`: implementation contract or ownership can be improved.
- `DEEPWIKI-INACCURACY`: DeepWiki conflicts with its indexed source or with the current product.
- `MATCH`: DeepWiki, source, docs, and skill materially agree.

Priorities: P0 is urgent, P1 is high-value, P2 is planned improvement, and P3 is polish.

## Page coverage checklist

The review was completed page-by-page. Findings are grouped below where the
same evidence applies to a parent page and its implementation-detail children.

| DeepWiki page | Status | Log location |
| --- | --- | --- |
| 5 MCP Protocol Layer | reviewed | 5 |
| 5.1 MCPAdapter & SDKServer | reviewed | 5.1 |
| 5.2 JSON-RPC Transport & SSE Streaming | reviewed | 5.2 |
| 5.3 MCP Caller & Client Adapters | reviewed | 5.3 |
| 5.4 MCP OAuth & Security | reviewed | 5.4 |
| 5.5 MCP Runtime Helpers & Skills | reviewed | 5.5 |
| 6 LLM Provider Adapters | reviewed | 6–6.4 |
| 6.1 Anthropic Client | reviewed | 6–6.4 |
| 6.2 AWS Bedrock Client | reviewed | 6–6.4 |
| 6.3 OpenAI, Gemini & Ollama Clients | reviewed | 6–6.4 |
| 6.4 Model Middleware & Conformance | reviewed | 6–6.4 |
| 7 Registry Service | reviewed | 7–7.3 |
| 7.1 Registry Service Internals | reviewed | 7–7.3 |
| 7.2 Runtime Registry Manager | reviewed | 7–7.3 |
| 7.3 Tool Registry Executor & Provider | reviewed | 7–7.3 |
| 8 Persistence & Infrastructure Features | reviewed | 8–8.2 |
| 8.1 MongoDB Persistence Layer | reviewed | 8–8.2 |
| 8.2 Pulse Streaming Infrastructure | reviewed | 8–8.2 |
| 9 Testing & Integration | reviewed | 9–9.2 |
| 9.1 Integration Test Framework | reviewed | 9–9.2 |
| 9.2 Codegen Golden Tests & Conformance | reviewed | 9–9.2 |
| 10 Glossary | reviewed | 10 |

## Executive findings

The recent local fixes close the previously identified MCP authorization and principal-hijack gaps. The remaining substantive issue is that the SDK and native JSON-RPC servers still have different session authorities: native JSON-RPC owns a `StreamableHTTPSessions` store with typed invalid/expired outcomes, while the SDK wrapper keeps a separate adapter-local map and turns an unknown or expired session into a generic `403` principal-binding error. This conflicts with the public transport contract and makes SDK lifecycle semantics harder to reason about.

Provider, registry, and test guidance is now notably stronger than DeepWiki. The main improvement is to make the provider capability/conformance table a reusable source of truth, rather than a manually maintained table in `docs/runtime.md`.

## Page-by-page findings

### 5. MCP Protocol Layer

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | The two-way bridge remains accurately represented: `runtime/mcp/caller.go` consumes external servers and `codegen/mcp` generates designed-server adapters. `docs/dsl.md`, `docs/mcp_sdk_server.md`, and `references/user-guides/mcp-integration.md` now give stronger user guidance than the indexed DeepWiki page. |
| DEEPWIKI-INACCURACY | P1 | The indexed overview's OAuth discovery route is stale: current code and docs use RFC 9728 protected-resource metadata at `/.well-known/oauth-protected-resource`, not `.well-known/mcp-oauth`. Keep this correction in any DeepWiki feedback. |
| MATCH | P3 | Design-owned MCP metadata, local `skill://` resources, projected tools, compact discovery, OAuth declarations, and caller support are now explicit in the skill's main contract. |

### 5.1 MCPAdapter and SDKServer

| ID | Class | Priority | Evidence and recommended action |
| --- | --- | --- | --- |
| M1 | ARCH-IMPROVEMENT + DOC-GAP | P1 | SDK handler requests with any `Mcp-Session-Id` call generated `adapter.assertSessionPrincipal` before the upstream SDK handler (`codegen/mcp/sdk_server_file.go:338-349`). An unknown or locally TTL-pruned ID returns the generic `"session principal binding missing"` as HTTP 403 (`adapter_core_jennifer.go:454-479`). Native JSON-RPC instead validates `StreamableHTTPSessions` and maps invalid/expired IDs to 404 (`codegen/mcp/generate.go:515-529, 609-650`; `runtime/mcp/streamable_http.go:102-127`). This contradicts `docs/mcp_sdk_server.md:367-370`, which promises unknown/expired/terminated IDs are 404 across generated transports. **Product action:** make one session-lifecycle authority shared by SDK and native generation, or at minimum give the SDK adapter typed invalid/expired/ownership errors and map them consistently. Add SDK tests for attacker-chosen, expired, terminated, and capacity-evicted IDs. **Docs action:** do not promise identical semantics until that work ships. |
| M2 | DOC-GAP | P2 | `x-mcp-allow-names` / `x-mcp-deny-names` are automatically converted to authorization context only by the native JSON-RPC mount (`codegen/mcp/generate.go:536-544`). SDK construction supplies request headers via `WithRequestHeaders`, but it does not call `WithAllowedResourceNames` or `WithDeniedResourceNames` (`codegen/mcp/sdk_server_file.go:330-338`; `runtime/mcp/httpcontext.go:69-93`). `docs/mcp_sdk_server.md:191-203` can read as transport-neutral. **Docs action:** state that SDK applications must apply narrowing through `RequestContext` and the `mcpruntime.With*ResourceNames` helpers; only native JSON-RPC maps these raw headers automatically. |
| M3 | ARCH-IMPROVEMENT | P3 | The adapter-local SDK lifecycle limit is hard-coded to 24 hours / 4096 records, while `StreamableHTTPSessions` has separate similarly hard-coded defaults (`codegen/mcp/adapter_core_jennifer.go`, `runtime/mcp/streamable_http.go:31-36`). Expose a single typed, bounded session policy only if deployment needs justify it; otherwise explicitly document these fixed safety limits and the operational implication of eviction. |
| MATCH | P3 | Current docs correctly state that a nil SDK `CrossOriginProtection` field restores the safe default, and document `SessionPrincipal`, transport observation, resource policy narrowing, and tool-call interceptors. These were gaps in the indexed tree and are now closed. |

### 5.2 JSON-RPC Transport and SSE Streaming

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | Unified POST/GET/DELETE handling, version negotiation, origin protection, cancellation, and SSE session handling match `codegen/mcp/generate.go` and `docs/runtime.md`. The current docs distinguish CORS response policy from origin validation. |
| DEEPWIKI-INACCURACY | P1 | The indexed page's separate `/events/stream` HTTP endpoint and its conflation of SSE event IDs with `Mcp-Session-Id` do not match generated transport ownership. The fixture mounts GET on the RPC route and handles `events/stream` as a JSON-RPC method. Keep these as DeepWiki correction candidates. |

### 5.3 MCP Caller and Client Adapters

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | Generated callers and generic runtime callers normalize tool responses, canonical JSON, query coercion, and client callback features as described. `docs/runtime.md` now uses the correct `NewStdioCaller(ctx, ...)` form. |
| DOC-GAP | P3 | The public caller guide explains canonical JSON but not the exact `CoerceQuery` boundary (notably repeated-value preservation and numeric non-coercion). Add a small example only when documenting resource-template or hand-written callers; it is not a blocker. |

### 5.4 OAuth and Security

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | OAuth remains metadata/challenge/audience plumbing, not a bundled authorization server or per-tool scope enforcer. Current DSL/runtime and skill guidance now say that an `invalid_token` parameter requires error-aware application middleware. |
| DEEPWIKI-INACCURACY | P3 | The indexed page overstates challenge availability: canonicalization cannot construct a usable challenge from every malformed or hostless input. Describe the normal mounted-server path, not an unconditional helper guarantee. |

### 5.5 MCP Runtime Helpers and Skills

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | Skill resources, strict frontmatter validation, compact discovery, resource policy inheritance, request callbacks, and tool-call interception are materially better documented locally than in DeepWiki. |
| SKILL-GAP | P3 | Add one concise cross-link in `references/user-guides/mcp-integration.md` to the primary `docs/mcp_sdk_server.md` interceptor section. The routing guide currently mentions interceptor order but not the raw-argument confidentiality requirement. |

### 6–6.4 Model Provider Adapters and Middleware

| ID | Class | Priority | Evidence and recommended action |
| --- | --- | --- | --- |
| P1 | MATCH | P3 | `docs/runtime.md:2004-2017` now has a useful provider capability and conformance matrix. It accurately distinguishes Gemini/Vertex's exact counting from its explicit `ErrStreamingUnsupported` outcome, and records Bedrock streamed structured output. The main skill also requires conformance, receive-time rate-limit normalization, truthful optional capabilities, and collision-safe reversible tool names. |
| P2 | ARCH-IMPROVEMENT | P2 | The capability matrix is manually duplicated knowledge: implementations and `testutil.RunProviderConformance` are the enforcement authority, while `docs/runtime.md` is prose. Add a machine-readable provider-capability declaration or a doc-generation test that compares the rendered table with provider conformance fixtures. This would prevent the Bedrock/Gemini documentation regressions already found in the indexed DeepWiki audit. |
| P3 | DEEPWIKI-INACCURACY | P1 | DeepWiki's provider section is stale/incomplete against the current tree: it universalizes streaming despite Gemini's explicit unsupported result, omits structured-output/tool-choice conformance, and understates the full five-adapter conformance suite. Retain the correction list instead of treating the page as a current capability matrix. |
| P4 | MATCH | P3 | Adaptive limiter documentation now accurately calls its behavior AIMD capacity adaptation, does not promise retries or exponential backoff, and requires terminal stream observation. `references/user-guides/production/model-rate-limiting.md` is current and operationally useful. |

### 7–7.3 Registry Service and Runtime Tool Registry

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | The replicated-map catalog, health registration epochs, deterministic streams, concurrent multi-registry search, provider backpressure, and structured retry hints agree with code and with the corrected skill guide. |
| DOC-GAP | P2 | The current documentation still lacks one deployer-oriented registry operations page: Redis/Pulse ownership, cluster names, `ResultStreamTTL`, liveness transitions, failure/partial-result semantics, and shutdown. `docs/runtime.md` has pieces, while `references/user-guides/internal-tool-registry.md` is a maintainer guide. Add a public subsystem map rather than expanding the already-large runtime guide. |
| ARCH-IMPROVEMENT | P3 | Catalog `List`/`Search` scan and decode replicated-map entries (`registry/catalog.go`). Document a scale envelope or add a secondary index before representing the service as horizontally scalable without qualification. |

### 8–8.2 Persistence and Pulse

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | Current guidance properly separates live workflow state, canonical `runlog.Store`, transcript `memory.Store`, long-term `memory.Service`, and Pulse delivery. It also distinguishes manual acknowledgement from exactly-once processing. |
| DEEPWIKI-INACCURACY | P1 | DeepWiki's "exactly-once" interpretation of `event_key` and its characterization of Mongo transcript storage as long-term memory remain wrong. An event key is a stable idempotency identity; durable entry-shaped long-term memory is a different interface. |
| DOC-GAP | P2 | Add a single persistence matrix for default/in-memory behavior, Mongo collection/index, consistency/idempotency guarantees, retention/size risk, and deliberately out-of-scope state. This is the fastest way for operators to avoid treating transcript storage as an audit log or long-term memory. |

### 9–9.2 Testing and Integration

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | `integration_tests/README.md` and `references/user-guides/testing.md` now accurately describe YAML scenarios, SDK/native tests, fixture ownership, generation, CLI coverage, and the verification ladder. This is substantially stronger than the indexed DeepWiki testing overview. |
| DOC-GAP | P3 | Add a small docs-snippet compile/check strategy for the high-risk public examples (MCP setup, provider constructors, middleware). The prior audit caught multiple non-compiling snippets only through manual review; a lightweight compile fixture or extracted examples would keep the user contract executable. |

### 10 Glossary

| Class | Priority | Evidence and recommended action |
| --- | --- | --- |
| MATCH | P3 | `docs/glossary.md` is now the concise canonical terminology map, and `references/repo-map.md` routes maintainers to it. It makes the essential distinctions absent from the indexed DeepWiki glossary: workflow state versus `runlog.Store`, mutable transcript ledger versus derived transcript memory, `memory.Service` long-term memory, MCP skill resources versus model-facing skill tools, and `Idempotent()` metadata versus replay suppression. The main skill reinforces the same contracts (`SKILL.md:82-85, 107, 180-184`). |
| DEEPWIKI-INACCURACY | P1 | The indexed glossary calls the transcript ledger an append-only whole-run record and treats event identity as exact-once processing. Current contracts are the opposite: the ledger is mutable workflow-local state, `runlog.Store` is append-only, and an event key supports idempotency rather than delivery or side-effect exactly-once semantics (`docs/glossary.md`; `references/runtime-contracts.md:216-243`). Keep those corrections in DeepWiki feedback. |
| DEEPWIKI-INACCURACY | P2 | Its `CapsState` entry implies that it enforces `TimeBudget` directly. `policy.CapsState` owns counter budgets; active-time limits are deterministic runtime deadlines and external waits pause that budget (`SKILL.md:76-80`). The term should not imply wall-clock or all-policy ownership. |
| DOC-GAP | P2 | The local glossary intentionally stays compact, but add entries or cross-links for `CapsState`/active `TimeBudget`, streamable-HTTP session ownership, and provider capability/conformance. These are the three terms most likely to cause incorrect implementation assumptions in current MCP/provider work. |

## Prioritized improvement queue

1. **P1 — M1:** unify or type-normalize SDK and native session lifecycle behavior, then correct the 404/403 public contract and add adversarial lifecycle tests.
2. **P2 — P2:** derive or verify provider capability documentation from the same source that drives conformance.
3. **P2 — M2:** clarify request resource-narrowing behavior in SDK mode and demonstrate `RequestContext` + `WithAllowedResourceNames`.
4. **P2 — registry/persistence docs:** create concise operations and persistence-reference pages rather than enlarging the runtime monograph.
5. **P3 — M3/testing:** decide whether session bounds should be configuration; introduce compile-checked examples for public setup APIs.

## Review notes for the final roll-up

The high-impact gaps found in the prior indexed audit are already addressed locally: resource grants only narrow, session principal checks cover native and SDK request paths, model rate limiting observes stream completion, provider optional capabilities stay truthful, provider docs have a matrix, and the test/skill guidance is current. The new report deliberately records only remaining or newly exposed discrepancies, so the final summary should not relist completed work as open debt.
