# DeepWiki documentation and architecture review

Reviewed 2026-07-15 and independently re-audited 2026-07-16 against repository commit `dd8379472f8341a694e65ce53935fe6cda12c2ad`, the same commit indexed by DeepWiki. Code is treated as authoritative when code, project documentation, the repo-local skill, and DeepWiki disagree.

> Resolution update (2026-07-16): `A9`–`A14` and `D9`–`D12` are fixed in the current worktree.
> Server resource allow policies now define the maximum grant and
> request-scoped names can only narrow it. Generated transport regression,
> adapter, and codegen tests cover the contract. Planner methods are documented
> as retryable activity attempts, and the inert `CapsState.ExpiresAt` contract
> is deprecated and ignored in favor of deterministic runtime deadlines.
> Provider replay codecs avoid pass-through/synthetic ID collisions, and
> rate-limit middleware preserves token-counting capability truthfully.
> Session-principal bindings now fail closed across SDK and native JSON-RPC
> POST, GET, and DELETE.

## Scope and method

All 40 DeepWiki pages were reviewed in four parallel workstreams:

- [Foundations, DSL, and code generation](foundations-dsl-codegen.md): pages 1–3.2 (10 pages)
- [Runtime](runtime.md): pages 4–4.7 (8 pages)
- [MCP and model providers](mcp-and-model-providers.md): pages 5–6.4 (11 pages)
- [Registry, persistence, testing, and glossary](registry-persistence-testing-glossary.md): pages 7–10 (11 pages)

Each area log records page-level claims, code/doc/skill evidence, classifications, priorities, and a coverage checklist. Findings use five classes: `MATCH`, `DOC-GAP`, `SKILL-GAP`, `ARCH-IMPROVEMENT`, and `DEEPWIKI-INACCURACY`.

The independent audit fetched every page directly, reconciled the 40-page site
inventory with 40 unique coverage entries, and rechecked high-impact findings
against the pinned revision. It added several issues that the first pass missed,
including one authorization bypass, incomplete session-principal enforcement,
planner retry semantics, an inert policy deadline, and two provider-boundary
collision/capability defects.

DeepWiki is valuable here: its architectural map is broadly accurate, its source links made several stale local guides easy to detect, and it surfaced code-derived behavior absent from our user documentation. It is not an independent source of truth, however. Several pages repeat stale GoDoc, collapse distinct runtime concepts, or infer guarantees the code does not provide.

## Executive conclusion

The main product documentation is substantially stronger than the routed long-form skill references, generated quickstart, and test harness README. The original highest-risk issues were implementation-level: a client-controlled resource allow could broaden grants, and session principal checks were incomplete. Both are fixed in the current worktree: request allows only narrow server grants, and SDK/native session ownership is checked fail-closed on POST, GET, and DELETE.

Conflicting guidance was the broadest documentation problem. The current
worktree aligns generated paths and symbols, supported integration variables,
active-work timing, and application-owned security boundaries across canonical
docs, generated quickstarts, and the repo-local skill.

The most important architectural theme was unclear authority and delivery
semantics across run logs, transcript memory, long-term memory, streams, and
Pulse. Canonical runtime docs and the glossary now distinguish their authority,
failure, replay, and acknowledgement contracts.

## Architecture risk assessment

- **Resolved S0 / P0:** client-controlled MCP resource allow names could broaden server grants; `A9` now prevents that broadening.
- **Resolved S1 / P1:** session-principal binding now fails closed across both
  generated transports, and append buckets remove the unbounded Mongo
  transcript-document path.
- **S2 / P1-P2:** Pulse now supports manual acknowledgement for durable
  consumers; MCP source rewriting has been eliminated, while state-authority
  ambiguity remains an upgrade and recovery risk. Provider tool-use ID
  collisions and false token-counting capability (`A13`–`A14`) are resolved.
- **No other confirmed S0 issues:** no additional exploitable security, systemic outage, or data-corruption path was established by this review.

## Follow-up status

The first implementation pass after this review has completed the documentation
and skill corrections `D1`–`D8`, including regenerated quickstarts. The public
contract now explicitly describes `Idempotent()` as metadata-only (`A1`) and
separates workflow state, run logs, transcript memory, long-term memory, streams,
and hook delivery (`A2`).

Two bounded code fixes are also complete:

- `A4`: adaptive rate limiting observes the first terminal stream receive
  outcome, probes only on EOF, and backs off on receive-time rate limits.
- `A5`: Anthropic provider names over 64 bytes use deterministic truncation
  with a stable hash suffix while preserving canonical round trips and collision
  checks.
- `A13`: Anthropic and Bedrock reserve pass-through tool-use IDs before
  allocating synthetic `tN` values, preserving replay correlation without
  collisions.
- `A14`: adaptive rate-limit middleware implements `model.TokenCounter` only
  when the wrapped provider supports exact counting.

The larger architectural migrations and P2 consolidation items identified by
this review are complete in the current worktree. The root fix plan records the
final all-green aggregate repository verification.

The independent audit added issues not covered by that implementation pass:
MCP allow-list authority and session binding (`A9`, `A10`), inert policy expiry
(`A11`), planner retry safety (`A12`), provider tool-use ID collisions (`A13`),
and optional token-counter preservation (`A14`). `A9`–`A14` are now complete.

## Prioritized backlog

### P0 — security remediation (complete)

| ID | Area | Improvement | Reason |
| --- | --- | --- | --- |
| A9 | MCP authorization | **Complete 2026-07-16:** server and request allow policies are independent constraints; raw request names can narrow but cannot broaden the server grant. Docs and skill identify headers as untrusted input. | Generated JSON-RPC, adapter-matrix, and codegen contract tests cover the fixed boundary. |
| A10 | MCP sessions | **Complete 2026-07-16:** SDK and native JSON-RPC validate `(session ID, principal)` on POST, GET, and DELETE; anonymous adoption and missing bindings fail closed; native initialization validates supplied IDs before adapter dispatch; expiry, eviction, and cleanup keep identity coupled to the session. | Generated transport matrices plus runtime lifecycle tests cover mismatch, missing binding, rejected DELETE, unknown/foreign initialize IDs, valid duplicate initialization, expiry, and capacity behavior. |

### P1 — documentation and skill corrections (`D1`–`D12` complete)

| ID | Area | Improvement | Primary targets |
| --- | --- | --- | --- |
| D1 | Generated guidance | Repair `AGENTS_QUICKSTART.md`: owner-scoped toolset paths, `<toolset>.<tool>` IDs, current `Init...` transforms, and current registration wiring. Compile-check runnable snippets. | `codegen/agent/templates/agents_quickstart.go.tpl`, codegen tests |
| D2 | Skill | Replace or retire stale generic Goa quickstart, DSL, codegen, toolset, registry, memory, testing, MCP, and rate-limit guides. Prefer thin routing to canonical product docs where duplication adds no value. | `.agents/skills/loom-mcp/references/user-guides/` |
| D3 | Onboarding | Align the quickstart with Go 1.26.1, make Temporal consistently optional, correct canonical tool IDs, and add `features/` to repository maps. | `quickstart/README.md`, `docs/overview.md`, `README.md`, skill repo map |
| D4 | Testing | Replace the integration README's unsupported `TEST_*` variables and stale pending-feature matrix with the actual harness and verification commands. Modernize the testing skill examples. | `integration_tests/README.md`, skill testing guide |
| D5 | MCP/security | Remove the false automatic OAuth `invalid_token` claim; correct the cross-origin-protection disable instructions; fix `NewStdioCaller(ctx, ...)`; update old MCP DSL/caller examples. | `docs/mcp_sdk_server.md`, `docs/runtime.md`, MCP skill guide |
| D6 | Providers | Add a capability/conformance matrix for Anthropic, Bedrock, OpenAI, Gemini, and Ollama. Correct Bedrock structured-output streaming and make the full conformance suite a skill contract. | `docs/runtime.md`, runtime contracts, provider docs |
| D7 | Runtime reliability | Publish the exact fail-closed versus best-effort behavior for runlog append, transcript subscribers, session streams, and event buses. Define which surface is canonical and how it is rebuilt. | `docs/runtime.md`, memory skill guide, runtime contracts |
| D8 | Runtime semantics | Document that human/external waits pause `TimeBudget`; explain Temporal's linked new-root activity spans; separate runtime, Temporal, generated MCP, debug-server, Pulse, and MCP stream telemetry. | `docs/runtime.md`, telemetry and runtime skill references |
| D9 | Planner/policy | **Complete 2026-07-16:** planner guidance now distinguishes logical turns from retryable activity attempts; the unenforced `CapsState.ExpiresAt` promise is retired. | `runtime/agent/planner`, `runtime/agent/policy`, runtime docs and skill contracts |
| D10 | Temporal | **Complete 2026-07-16:** the production guide scopes engine-only durability to Temporal workflow history, wires shared Mongo runlog/session stores for lifecycle persistence, separates memory/stream projections, and adds restart verification. | `references/user-guides/production/temporal-setup.md`, `docs/runtime.md`, runtime skill contracts |
| D11 | MCP security | **Complete:** grant-bearing header trust, application-owned OAuth/authz, resolver ordering, all-method principal checks, failure statuses, expiry/eviction, anonymous sessions, and DELETE cleanup are documented. | `docs/mcp_sdk_server.md`, `docs/runtime.md`, MCP skill guide |
| D12 | Providers | **Complete 2026-07-16:** Gemini streaming is explicitly unsupported; provider replay codecs reserve pass-through IDs; middleware preserves absence of optional capabilities. | provider matrix, runtime contracts, main skill |

### P1 — architecture and contract improvements

| ID | Area | Improvement | Reason |
| --- | --- | --- | --- |
| A6 | Mongo transcripts | **Complete 2026-07-16:** new appends create immutable documents in a companion events collection; reads merge legacy run documents with `created_at`/`_id`-ordered buckets and stable timestamp normalization. | Removes the unbounded `$push`/hot-document path while preserving existing histories and the chronological snapshot contract without an offline migration. |
| A7 | Pulse delivery | **Complete 2026-07-16:** `SubscribeManual` returns deliveries acknowledged explicitly after durable work; the existing `Subscribe` API remains auto-ack compatible. | Tests cover acknowledgement timing, retry/idempotence, and leaving manual decode failures pending. |
| A8 | MCP codegen | **Complete 2026-07-16:** mount/session/cancellation, batch dispatch, endpoint initialization, error mapping/data, SSE streams, client initialization/defaults, and example scaffolding are owned through stable sections and evaluated generator data. | No production MCP generator inspects or rewrites rendered Go source. Exact file/section cardinality checks fail on upstream drift, and a source-ownership contract test prevents regression. |
| A11 | Policy expiry | **Complete 2026-07-16:** `ExpiresAt` is deprecated and ignored; deterministic `runDeadlines` remains the single active-time authority. | Source compatibility is preserved without forwarding a wall-clock deadline that the runtime does not enforce. |
| A12 | Planner retries | **Complete 2026-07-16:** planner GoDoc, runtime docs, and skill contracts require retry safety and qualify errors and side effects at the activity-attempt level. | Generated planner activities default to three attempts, so model calls or side effects may repeat after a late failure. |
| A13 | Provider IDs | **Complete 2026-07-16:** Anthropic and Bedrock reserve pass-through IDs and skip occupied synthetic `tN` values using request-local codecs. | Mixed-history tests prove safe and substituted IDs remain distinct while tool-use/result pairs round trip. |
| A14 | Optional capabilities | **Complete 2026-07-16:** rate-limited clients expose `model.TokenCounter` only through a delegating wrapper around providers that support it. | Contract tests prove unsupported providers remain non-`TokenCounter`. |

### P2 — consolidation and maintainability

| ID | Area | Improvement |
| --- | --- | --- |
| M1 | Registry | **Complete 2026-07-16:** manager and semantic client searches share concurrent fan-out and partial-failure behavior; the skill retires the removed store model. |
| M2 | Prompt overrides | **Complete 2026-07-16:** Mongo persists an indexed canonical scope fingerprint and resolves with at most one session plus one global lookup. |
| M3 | Runtime registration | **Complete 2026-07-16:** `RegisterModel` is rejected after `Seal`; post-seal hot-swapping requires a replacement runtime. |
| M4 | MCP API | **Complete 2026-07-16:** generated `DropIfSlow *bool` uses nil as the safe default and removes untyped invalid-value fallback. |
| M5 | Integration tests | **Complete 2026-07-16:** the runner embeds checked-in SDK server Go fixtures instead of injecting large source strings. |
| M6 | Glossary | **Complete 2026-07-16:** `docs/glossary.md` distinguishes runtime state/persistence/delivery surfaces, MCP resources from model-facing skills, and generated ownership. |
| M7 | Public source comments | **Complete 2026-07-16:** generator ownership, confirmation, idempotency, and active-time policy GoDoc now matches implemented protocols. |
| M8 | Observability | **Complete 2026-07-16:** stable engine-neutral run/planner/tool metrics and canonical `tool.execute` spans are documented and tested; event-key insertion deduplicates run/tool retry signals. |
| M9 | Idempotency | **Complete 2026-07-16:** DSL/runtime GoDoc explicitly keeps `Idempotent()` metadata-only with no built-in comparison, cache, suppression, or exactly-once guarantee. |

## Summary by area

### Foundations and onboarding

The high-level design-first story and runnable edge are aligned. The quickstart
uses Go 1.26.1, treats Temporal as optional, and the generated agent guide uses
owner-scoped paths, current transforms, current MCP provider DSL, and generated
caller adapters.

### DSL and workflows

`docs/dsl.md` is authoritative and much more complete than DeepWiki or the routed skill DSL guide. DeepWiki still gives unsafe copy guidance such as `Expose(MCPSurface)` without the required runtime exposure and placement, treats `TimeBudget` as wall-clock, calls agent JSON schemas OpenAPI schemas, and implies OAuth DSL installs authorization. `Idempotent()` is metadata-only today; any runtime replay behavior should be a separate explicit contract.

### Code generation

Agent generation and its generated guidance now describe owner-scoped layouts
and current registration contracts. MCP generation provides broad protocol
behavior; its highest-risk mount and client-initialization paths now use stable
section ownership, while the remaining named compatibility rewrites retain
uneven, sometimes non-exact drift checks pending incremental migration.

### Runtime, state, and human interaction

Planner/workflow behavior is generally well documented and DeepWiki's mental model is useful. Cross-surface semantics are now explicit: Temporal history owns workflow recovery; persistent runlog/session stores own introspection and lifecycle metadata; transcript memory and streams remain separate projections. Human waits pause `TimeBudget`; generated planner activities retry up to three times and the public contract requires retry safety. Deterministic `runDeadlines` is the sole active-time authority; the legacy `CapsState.ExpiresAt` field is deprecated and ignored.

### MCP protocol and security

The local MCP documentation is broad. Client-controlled resource allows only narrow server grants, the SDK callback uses the supported context helper, and docs/skill identify OAuth and raw-header trust boundaries correctly. Session-principal binding now covers SDK and native JSON-RPC POST, GET, and DELETE, rejects missing authenticated bindings, and couples expiry/eviction/cleanup to the session.

### Model providers and middleware

All five providers participate in a shared conformance suite and the runtime guide carries a concise capability matrix. DeepWiki incorrectly universalizes streaming even though Gemini returns `ErrStreamingUnsupported`. Stream-completion rate limiting, Anthropic tool-name bounds, collision-safe Anthropic/Bedrock replay IDs, and truthful optional `TokenCounter` preservation are fixed in the current worktree.

### Registry and prompt infrastructure

The production registry is a Pulse replicated-map catalog, and the skill now
explicitly retires the old pluggable `Store` model. Manager and semantic-client
search share concurrent fan-out and partial-failure behavior. Mongo prompt
override resolution uses an indexed scope fingerprint and at most two bounded
lookups rather than repeated specificity queries and application scans.

### Persistence and streaming

Mongo transcript appends now use immutable companion-collection buckets with
legacy read compatibility. Pulse retains auto-ack UI fanout and adds manual
deliveries for consumers that acknowledge after durable, idempotent processing.

### Testing

The integration README and testing skill now map actual codegen goldens,
compile matrices, provider conformance, YAML/SDK scenarios, and fixture
verification commands. The runtime harness embeds checked-in, gofmt-clean
fixture programs rather than injecting large source strings.

### Terminology

DeepWiki frequently conflates transcript memory with long-term memory, stable
event identity with exactly-once delivery, Pulse streams with MCP event streams,
and MCP skill resources with model-facing skill tools. `docs/glossary.md` now
records those distinctions and links each term to its authoritative contract.

## DeepWiki corrections to keep separate from our backlog

These are reference-site inaccuracies, not product gaps:

- `event_key` is stable idempotency identity, not exactly-once delivery.
- `features/memory/mongo` stores per-run transcript events; it is not the long-term `memory.Service` implementation.
- `ServerData` is server-only output/sidecar data; `Inject` hides server-provided input.
- Generated MCP servers expose native HTTP/SSE and SDK Streamable HTTP; stdio is an external-server caller transport.
- Temporal activity spans are new roots linked to the origin, not one propagated parent/child trace.
- `TimeBudget` measures active work and pauses during human/external waits; it is not elapsed wall-clock time.
- Generated planner activities may retry; `PlanStart` is one logical turn, not an attempt-level exactly-once call. Local GoDoc, docs, and skill contracts now state this explicitly.
- `CapsState.ExpiresAt` does not enforce `TimeBudget`; it is now deprecated and ignored in favor of deterministic runtime deadlines.
- OAuth DSL emits metadata/challenge/audience helpers but does not install authentication or operation-level authorization.
- Gemini does not implement streaming.
- The OAuth metadata path, JSON-RPC/SSE route model, Anthropic name example, generated artifact layout, and provider conformance lifecycle contain additional page-specific errors documented in the area logs.

## Suggested delivery sequence

1. ~~Remove client-controlled MCP grant broadening and enforce session principals across both transports (`A9`, `A10`).~~ Complete 2026-07-16.
2. ~~Fix the inert policy deadline, planner retry contract, provider tool-use ID collisions, and optional token-counter exposure (`A11`–`A14`, `D9`, `D12`).~~ Complete 2026-07-16.
3. ~~Finish planner, Temporal, MCP security, and provider documentation/skill corrections (`D9`–`D12`).~~ Complete 2026-07-16.
4. ~~Migrate Mongo transcript storage, add manual-ack Pulse consumption, and eliminate rendered-source rewriting through structured MCP codegen ownership (`A6`–`A8`).~~ Complete 2026-07-16.
5. ~~Consolidate registry, prompt, integration-fixture, glossary, observability, and idempotency work (`M1`–`M9`).~~ Complete 2026-07-16.

The review logs are evidence-backed audit artifacts. The current dirty worktree
contains the completed `D1`–`D12`, `A4`–`A14`, and `M1`–`M9` changes described
above. Aggregate repository verification is recorded in the root fix plan.
