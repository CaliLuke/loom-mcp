# DeepWiki review fix plan

This plan turns the findings in [`docs/reviews/deepwiki-2026-07-15`](docs/reviews/deepwiki-2026-07-15) into verified product, documentation, and skill changes. Code and the bundled MCP specification remain authoritative; DeepWiki is comparison evidence, not a contract.

## Working rules

- Fix security and correctness contracts before explanatory documentation.
- For MCP behavior, add a generated client/server regression test first, observe it fail, then change the design or generator and regenerate affected fixtures.
- Never edit generated `gen/` files by hand.
- Update canonical docs and the repo-local skill in the same tranche as user-visible behavior.
- Complete each tranche with targeted tests, then run `make verify-mcp-local`, `make lint`, `make test`, and `make itest` before marking it done.

## Status

| Tranche | Scope | Review IDs | Status |
| --- | --- | --- | --- |
| 0 | Correct stale quickstarts, DSL/codegen/testing/MCP/provider/runtime docs; fix stream rate-limit completion and Anthropic tool-name bounds | D1–D8, A4–A5 | Complete and aggregate-verified 2026-07-16 |
| 1 | Prevent request-controlled MCP resource names from broadening server grants | A9, D11 | Complete 2026-07-16 |
| 2 | Enforce session principal binding on POST, GET, and DELETE across SDK and native JSON-RPC transports, including missing/expired bindings | A10, D11 | Complete and aggregate-verified 2026-07-16 |
| 3 | Make planner retry guarantees accurate and either enforce or retire policy expiry | A11–A12, D9 | Complete and aggregate-verified 2026-07-16 |
| 4 | Make the Temporal production guide explicit about workflow-history durability and wire durable runlog/session stores where promised | D10 | Complete and aggregate-verified 2026-07-16 |
| 5 | Prevent Anthropic/Bedrock tool-use ID collisions and preserve the absence of optional `TokenCounter` capability through middleware | A13–A14, D12 | Complete and aggregate-verified 2026-07-16 |
| 6 | Finish lower-priority registry, persistence, integration-fixture, glossary, observability, and idempotency consolidation | A6–A8, M1–M9 | Complete and aggregate-verified 2026-07-16 |
| 7 | Eliminate all remaining MCP rendered-source rewriting and enforce structured ownership as a repository contract | A8 | Complete and aggregate-verified 2026-07-16 |

## Tranche 7 — Eliminate MCP codegen migration debt

Contract: production MCP generation never inspects, parses, or mutates emitted
Go source. Every upstream extension owns a stable named section using evaluated
generator data and fails on missing or duplicate expected structure.

1. Replace the JSON-RPC server handler, endpoint initializer, and SSE stream
   sections with loom-mcp-owned Jennifer sections.
2. Delete obsolete optional-params, final-event, and mixed-transport
   compatibility logic already satisfied by pinned Loom v1.6.2.
3. Replace example CLI and adapter-stub template mutation with owned sections
   and exact file/section cardinality.
4. Add drift matrices and a production-source guard that rejects rendered
   section reads, regex matching, and source-string replacement.
5. Regenerate every checked-in consumer and run `make verify-mcp-local`,
   `make lint`, `make test`, `make itest`, and `git diff --check`.

## Tranche 1 — MCP resource authorization boundary

Contract: server adapter options define the maximum resource grant. Request-scoped allowed names may narrow that grant, but may never broaden it. Request and server denies remain additive and take precedence.

1. Add a real generated native JSON-RPC regression test that configures a server-only allow-list, sends `x-mcp-allow-names` for a different skill, and proves the skill remains denied.
2. Add adapter matrix coverage for server-only, request-only, intersection, disjoint, unknown-name, and deny-precedence cases.
3. Change the MCP adapter generator so server allow policies and request allow policies are evaluated as independent constraints.
4. Regenerate the assistant fixture with `make regen-assistant-fixture`.
5. Document that raw headers are untrusted input, authentication/authorization belongs to the application, and request-scoped allows only narrow server grants.
6. Record the same invariant in `.agents/skills/loom-mcp` and its codegen/MCP references.

Primary files:

- `codegen/mcp/adapter_jennifer_sections.go`
- `codegen/mcp/*_test.go`
- `integration_tests/fixtures/assistant/mcp_generated_server_metadata_test.go`
- `docs/mcp_sdk_server.md`
- `docs/runtime.md`
- `.agents/skills/loom-mcp/SKILL.md`
- `.agents/skills/loom-mcp/references/codegen-contracts.md`
- `.agents/skills/loom-mcp/references/user-guides/mcp-integration.md`

## Tranche 2 — MCP session identity lifecycle

Contract: once a session is bound to an authenticated principal, every request that references that session must present the same principal. Missing, expired, or evicted bindings fail closed. Creation, expiry, eviction, and DELETE cleanup use one shared lifecycle across SDK and native JSON-RPC transports.

1. Build transport/method test matrices for POST, GET, and DELETE using the generated SDK and JSON-RPC servers.
2. Centralize session binding and validation outside method-specific handlers.
3. Reject principal mismatches and absent bindings with protocol-appropriate errors; ensure DELETE clears state only after successful validation.
4. Document the application-owned authentication callback, trusted principal derivation, TTL/eviction behavior, and failure responses.

## Tranche 3 — Planner retries and policy expiry

1. Replace attempt-level “exactly once” claims with a retry-safe planner contract and make repeated model calls/side effects explicit.
2. Decide `CapsState.ExpiresAt` deliberately:
   - enforce it using deterministic workflow time with the same paused-wait semantics as `TimeBudget`, or
   - deprecate/remove it and delete the unenforced promise.
3. Add deterministic tests for the selected behavior and update runtime docs, GoDoc, and skill contracts.

## Tranche 4 — Temporal durability guidance

1. Update the production example so “durable” distinguishes Temporal workflow history from runlog and session persistence.
2. Wire durable runlog/session stores in any example that promises end-to-end recovery; otherwise label the stores process-local.
3. Add restart/recovery verification instructions.

## Tranche 5 — Provider boundary correctness

1. Make Anthropic and Bedrock request-scoped tool-use ID codecs reserve pass-through IDs and skip occupied synthetic `tN` values.
2. Add collision round-trip tests covering safe IDs mixed with unsafe IDs.
3. Split rate-limit wrappers so `model.TokenCounter` is implemented only when the wrapped provider implements it.
4. Update the provider capability matrix and skill contract: Gemini streaming remains unsupported; optional interfaces must remain truthful.

## Tranche 6 — Larger improvements

Treat these as separately reviewed migrations rather than incidental cleanup:

- ~~bucket Mongo transcript events instead of growing one document indefinitely;~~ Complete 2026-07-16: immutable append buckets plus legacy reads;
- ~~add manual-ack Pulse delivery for durable consumers;~~ Complete 2026-07-16: explicit `Delivery.Ack` plus compatible auto-ack subscription;
- ~~replace the highest-risk source-text MCP generator rewrites with structured upstream section ownership;~~ Complete 2026-07-16: mount and client initialization use evaluated `ServiceData` plus Jennifer sections; narrower compatibility rewrites remain explicit migration candidates;
- ~~unify registry search and prompt-scope lookup behavior;~~ complete (`M1`–`M2`);
- ~~replace untyped MCP options such as `DropIfSlow any` with validated types;~~ Complete 2026-07-16: generated `DropIfSlow *bool` preserves nil as the safe default and removes invalid-type fallback;
- ~~move dynamically injected integration programs into checked-in fixtures;~~ Complete 2026-07-16: the SDK server patch is embedded from formatted Go files under `integration_tests/framework/testdata/`;
- ~~add a canonical glossary after state and delivery terms are unambiguous;~~ complete (`M6`);
- ~~correct public generator, confirmation, timing, policy, and idempotency comments consumed by code-derived docs;~~ complete (`M7`);
- ~~add semantic planner/tool metrics and spans across engines;~~ complete (`M8`);
- ~~keep `Idempotent()` metadata-only unless a separate replay contract is designed;~~ complete (`M9`).

Model registration (`M3`) is sealed with agent/toolset registration. Post-seal
model-client hot-swapping is rejected; applications replace and reseal a
runtime when rotating clients.

A6/A7 completion evidence:

- observed compile-failing contract tests for the absent bucketed Mongo client
  and `SubscribeManual` API before implementation;
- Mongo writes new transcript batches only to the companion events collection
  and merges legacy single-document history with ordered append buckets;
- a post-audit red regression proved reversed ObjectIDs and cross-worker bucket
  insertion could violate `memory.Snapshot` timestamp order. Bucket queries now
  sort by `created_at`, `_id`, and the combined legacy/new event list is
  stable-sorted by timestamp with deterministic equal-timestamp ordering;
- Pulse manual deliveries remain pending until `Delivery.Ack` succeeds, allow
  ack retry, and leave decode failures pending, while `Subscribe` retains its
  existing auto-ack/poison-message compatibility behavior;
- canonical runtime docs and repo-local skill contracts describe migration,
  retention, at-least-once delivery, idempotency, and acknowledgement timing;
- targeted package tests passed; final aggregate gates passed as recorded below.

## Completion record

For each completed tranche, append:

- failing regression test observed before implementation;
- implementation and regenerated artifacts;
- canonical docs and skill files updated;
- targeted test commands and results;
- full verification commands and results;
- any follow-up intentionally deferred to another tranche.

### 2026-07-16 — Tranche 1 complete

- Red test observed: `TestGeneratedJSONRPCRequestAllowNamesCannotBroadenServerGrant`
  reached `resources/read` successfully when the server allowed only
  `documents` and the client supplied `x-mcp-allow-names: code-review`.
- Generator fix: server URI/name allows and request-scoped name allows are now
  independent predicates; the resource must satisfy both when configured.
  Static and request denies remain additive and take precedence.
- Coverage: generated native JSON-RPC client/server regression, adapter policy
  matrix, rendered codegen contract, and regenerated assistant fixture.
- Documentation: corrected the SDK callback helper, documented the maximum
  server grant and untrusted-header boundary, and corrected the skill's OAuth
  authorization claim. The repo-local skill and codegen contract now carry the
  same invariant.
- Verification passed: `make verify-mcp-local`, `make lint`, `make test`
  (68.9% coverage), and `make itest`.
- Follow-up: transport-wide fail-closed session-principal enforcement moved to
  Tranche 2 and is completed below (`A10`, `D11`).

### 2026-07-16 — Tranche 2 complete

- Red generated-transport tests showed that native JSON-RPC accepted a wrong
  principal on POST and DELETE, left wrong-principal GET open, and allowed an
  authenticated caller to adopt an anonymously issued session. A final audit
  also showed that native `initialize` skipped session validation, allowing
  foreign existing IDs through adapter dispatch and accepting attacker-chosen
  IDs into bounded adapter state.
- `runtime/mcp.StreamableHTTPSessions` now stores the principal with issuance
  and provides principal-aware validation, listener registration, and atomic
  termination. Anonymous sessions cannot later be adopted by an authenticated
  request.
- Generated native JSON-RPC resolves principals through the exported
  `MCPSessionPrincipal` hook (TokenInfo `UserID` by default) and validates POST,
  GET, and DELETE. Native initialization now validates supplied session IDs
  before adapter dispatch: unknown IDs fail with HTTP 404, foreign IDs fail
  with HTTP 403, and an owner-bound duplicate reaches the adapter's
  protocol-level `Already initialized` error. Generated SDK handlers validate the same methods through
  `MCPAdapterOptions.SessionPrincipal` or TokenInfo.
- SDK initialization records the issued session and principal before writing
  the response, closing the eager-client race in which
  `notifications/initialized` could arrive before the binding was visible.
- Session/principal expiry and capacity eviction are coupled; SDK adapter
  pruning runs on validation, missing bindings fail closed, and rejected DELETE
  requests do not clear the rightful owner's session.
- Targeted verification passed: runtime session tests, MCP codegen tests, and
  generated SDK/native transport authentication tests, including owner-bound,
  foreign, and attacker-chosen IDs on native initialization. Final aggregate
  repository gates passed as recorded below.

### 2026-07-16 — Tranche 3 complete

- Red test observed: `TestMergeCapsIgnoresDeprecatedExpiresAt` failed because
  `mergeCaps` propagated a policy decision's absolute expiry even though no
  runtime path enforced it.
- Architecture decision: deterministic `runDeadlines` remains the sole owner of
  active-work time and paused-wait accounting. `policy.CapsState.ExpiresAt` is
  deprecated and ignored for source compatibility rather than becoming a
  second, wall-clock deadline authority.
- Planner contract: `PlanStart` and `PlanResume` now describe one logical turn
  backed by retryable activity attempts. GoDoc, canonical runtime docs, and the
  repo-local skill warn that model calls and direct side effects can repeat and
  require retry-safe implementations with stable idempotency keys.
- Targeted verification passed: `go test ./runtime/agent/runtime
  ./runtime/agent/planner ./runtime/agent/policy -count=1`.
- Final aggregate repository gates passed as recorded below.

### 2026-07-16 — Tranche 6 A8, M4, and M5 complete

- MCP codegen audit first moved server mount/session/cancellation and client
  construction onto stable Loom section IDs with evaluated `ServiceData`.
  Follow-up debt elimination completed the same ownership model for batch
  dispatch, endpoint initialization, empty results, error mapping/data, SSE
  streams, and example scaffolding. Missing or duplicate files/sections now
  fail generation, and no production MCP generator inspects or rewrites
  rendered Go source.
- Generated `MCPAdapterOptions.DropIfSlow` is now `*bool`. Nil preserves the
  safe drop-on-overflow default, an explicit false pointer enables publisher
  backpressure, and the old `any` type switch and silent invalid-value fallback
  are removed. This is a deliberate generated-API migration: regenerate callers
  and pass a bool pointer.
- `applySDKServerFixturePatch` now embeds and copies checked-in, gofmt-clean
  `http.go` and `main.go` fixtures instead of carrying hundreds of lines of Go
  source in the runner.
- Targeted proof passed: MCP codegen transport/DropIfSlow contracts and
  framework fixture tests. Final aggregate repository gates passed as recorded
  below.

### 2026-07-16 — Tranche 6 M1–M3 complete

- Red tests observed: `TestSearchClientFansOutAcrossRegistriesConcurrently`
  timed out after only one sequential registry call started;
  `TestRegisterModelFailsAfterSeal` accepted a replacement model; prompt
  fingerprint contract tests did not compile because no canonical identity or
  indexed query field existed.
- `Manager.Search` and `SearchClient.Search` now share one concurrent fan-out,
  origin tagging, failure logging, and partial-failure implementation while the
  richer client retains semantic fallback, filtering, ranking, and limits.
- The stale registry `Store` model is explicitly retired in the skill. The
  Pulse replicated map remains the catalog authority.
- Mongo prompt overrides persist a canonical versioned SHA-256
  `scope_fingerprint`. Resolution performs at most one session and one global
  indexed query, sorted by label count and creation time, with no application
  candidate scan. The adapter rejects scopes above 15 labels to bound exact
  subset expansion; startup automatically backfills missing fingerprints before
  index creation and fails closed on migration errors.
- `RegisterModel` now shares the atomic registration-close guard and returns
  `ErrRegistrationClosed` after `Seal` or first-run closure. Hot-swapping is not
  supported; applications replace the runtime.
- Focused verification passed: `go test ./runtime/registry
  ./runtime/agent/prompt ./features/prompt/mongo/... ./runtime/agent/runtime
  -count=1`. Final aggregate repository gates passed as recorded below.

### 2026-07-16 — Tranche 6 M6–M9 complete

- `docs/glossary.md` now distinguishes agent/runtime/engine state, runlog,
  transcript ledger and memory, long-term memory, hook/stream/Pulse delivery,
  MCP server/caller and skill resources, model-facing skill tools, registries,
  generated ownership, and metadata-only idempotency.
- Public comments that code-derived documentation republishes now describe the
  actual generated ownership layout, confirmation protocol, active-work timing,
  deprecated policy expiry, and `Idempotent()` metadata contract.
- Runtime observability now records stable `loom_mcp.runtime.*` run lifecycle,
  planner-attempt, and tool-result metrics. Planner spans carry semantic
  outcome/correlation attributes, and newly inserted canonical tool results
  emit `tool.execute` spans using the reported execution interval. Event-key
  insertion prevents ordinary hook retries from duplicating run/tool signals;
  planner signals intentionally remain attempt-level.
- A full runtime-package test exposed direct-construction compatibility when
  telemetry dependencies were nil; local no-op resolution fixed the panic and
  preserves the `runtime.New` no-op contract for low-level callers.
- Canonical runtime docs and the repo-local skill publish exact metric names,
  dimensions, retry/deduplication semantics, no-op defaults, and the separation
  from Temporal/MCP/transport/stream/debug telemetry surfaces.
- Targeted verification passed: `go test ./runtime/agent/runtime -count=1` and
  focused `go test -race` coverage for planner, canonical event, run lifecycle,
  and oversized-hook paths. Final aggregate repository gates passed as recorded
  below.

### 2026-07-16 — Tranche 4 complete

- Runtime contract audit confirmed that `runtime.New` retains process-local
  in-memory `runlog.Store` and `session.Store` defaults even when configured
  with a Temporal engine.
- The production Temporal guide now distinguishes workflow-history recovery,
  canonical runlog persistence, session/run metadata, derived transcript
  memory, and live stream delivery. It labels the engine-only example as
  process-local and provides current `NewWorker`, Mongo runlog/session adapter,
  and runtime option wiring for a shared-store production deployment.
- Removed stale `temporal.New` and nonexistent DSL `ActivityOptions` examples;
  current examples use `temporal.NewWorker` and `RunPolicy.Timing`.
- Canonical runtime docs and repo-local skill contracts now state that Temporal
  history does not replace persistent runtime stores and that activity side
  effects remain retryable rather than exactly once.
- Added a restart/recovery verification checklist that proves Temporal status,
  runlog continuity, session metadata, and optional memory/stream projections
  independently.
- Targeted verification passed: `go test ./features/runlog/mongo/...
  ./features/session/mongo/... ./runtime/agent/engine/temporal
  ./runtime/agent/runtime -count=1`; documentation diff checks and code-block
  delimiter checks also passed.
- Final aggregate repository gates passed as recorded below.

### 2026-07-16 — Tranche 5 complete

- Red tests observed: mixed replay histories assigned both an unsafe canonical
  ID and a later pass-through `t1` to wire ID `t1` in Anthropic and Bedrock;
  rate-limit middleware still satisfied `model.TokenCounter` around a provider
  without exact counting.
- Provider fix: Anthropic and Bedrock reserve every safe replay ID before
  encoding, skip occupied synthetic `tN` values, and retain one request-local
  mapping for each tool-use/result pair. Anthropic coverage also verifies
  canonical decode and unchanged provider-minted IDs.
- Middleware fix: the base rate-limited client implements only `model.Client`;
  a separate wrapper exposes and delegates `model.TokenCounter` when the
  underlying provider supports it.
- Documentation: the runtime provider guide and repo-local skill define the
  separate tool-name/tool-use-ID codecs, collision-free replay behavior,
  Gemini's unsupported streaming contract, truthful optional capabilities,
  estimated input admission, and the lack of output-token reservation.
- Targeted verification passed: `go test ./features/model/anthropic
  ./features/model/bedrock ./features/model/middleware -count=1`.
- Final aggregate repository gates passed as recorded below.

### 2026-07-16 — Final aggregate verification complete

- Regenerated the quickstart, assistant, progressive-discovery, and
  agent-features outputs with their canonical `make regen-*` targets.
- The SDK session-issuance ordering regression passed 20 race-enabled
  repetitions before the aggregate run.
- `make verify-mcp-local` passed for the assistant, progressive-discovery,
  agent-features, and integration framework fixtures.
- `make lint` passed with zero issues.
- `make test` passed under race and shuffle with 69.1% statement coverage,
  above the required 62.0% threshold.
- `make itest` passed, including quickstart generation/execution, generated
  fixtures, CLI coverage, framework tests, and the real MCP protocol suite.
- `git diff --check` passed. A subsequent A8 debt-elimination pass removed the
  remaining handler/decoder/SSE and example source rewrites; current aggregate
  verification is recorded below.

### 2026-07-16 — Tranche 7 A8 debt elimination complete

- Replaced the complete `jsonrpc-server-handler`, every ordered
  `jsonrpc-server-handler-init`, and every ordered
  `jsonrpc-sse-server-stream` section with loom-mcp-owned Jennifer sections.
- Batch isolation, `{}` empty results, `resource_not_found` `-32002` mapping,
  client-safe error data, SSE final `message` events, and reconnect hints are
  emitted directly from evaluated `ServiceData`.
- Removed obsolete optional-params, final-event, mixed-transport, error-data,
  error-code, and SSE source postprocessors. Pinned Loom v1.6.2 already emits
  omitted-params normalization and the correct final-event behavior.
- Replaced example CLI/stub template mutation and rendered factory discovery
  with exact-path, exact-section owned generation. Missing and duplicate files,
  headers, start/end sections, handlers, initializers, and streams fail fast.
- Added a source-ownership contract test that scans production MCP generator
  Go files and rejects rendered-section reads, regex matching, and source
  replacement patterns.
- Regenerated quickstart, assistant, progressive-discovery, and agent-features
  outputs with the canonical `make regen-*` targets.
- `make verify-mcp-local`, `make lint`, `make test`, and `make itest` passed.
  The race/shuffle unit suite reported 69.2% statement coverage against the
  62.0% threshold. `git diff --check` also passed.
