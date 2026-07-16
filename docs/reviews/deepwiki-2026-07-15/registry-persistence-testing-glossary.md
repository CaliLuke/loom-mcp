# DeepWiki review: registry, persistence, testing, and glossary

Reviewed 2026-07-15 against local commit `dd8379472f8341a694e65ce53935fe6cda12c2ad`, the same revision indexed by DeepWiki. DeepWiki was treated as a code-derived secondary source; current code remains authoritative.

## Classification

- `DOC-GAP`: user or contributor documentation is missing, stale, or hard to find.
- `SKILL-GAP`: `.agents/skills/loom-mcp` is missing or misstates a working contract.
- `ARCH-IMPROVEMENT`: the code is working as designed, but the architecture has a concrete improvement opportunity.
- `DEEPWIKI-INACCURACY`: DeepWiki overstates, conflates, or incorrectly describes the code.
- `MATCH`: DeepWiki, code, and local documentation agree materially.

Priorities run from `P0` (critical) to `P3` (polish).

## Executive findings

| ID | Class | Priority | Confidence | Finding |
| --- | --- | --- | --- | --- |
| R1 | RESOLVED | P1 | High | **Complete 2026-07-16:** the skill identifies the Pulse replicated map as catalog authority, documents current `ResultStreamTTL`, and explicitly retires the removed registry `Store` model. |
| R2 | DOC-GAP | P1 | High | `integration_tests/README.md` advertises five unsupported environment variables, describes implemented progress support as pending, and no longer represents the actual SDK/JSON-RPC/CLI fixture matrix. |
| R3 | SKILL-GAP | P1 | High | The testing skill contains non-compiling current-API examples (`model.Client.Generate` instead of `Complete`, and old tool-executor result shapes) and omits the repository's YAML scenario, official-SDK, golden, compile-matrix, and local-Loom verification workflows. |
| R4 | ARCH-IMPROVEMENT | P1 | High | **Resolved 2026-07-16:** Mongo transcript memory writes immutable append buckets and merges legacy run documents on read. |
| R5 | ARCH-IMPROVEMENT | P1 | High | The high-level Pulse subscriber acknowledges immediately after handing an event to an in-process channel. Callers cannot acknowledge after durable application processing, so the API cannot provide end-to-end at-least-once processing despite Redis/Pulse durability. |
| R6 | DEEPWIKI-INACCURACY | P1 | High | DeepWiki labels `event_key` as providing “exact-once processing.” It is a stable idempotency identity; neither the Pulse subscriber nor the broader pipeline provides exactly-once delivery. |
| R7 | DEEPWIKI-INACCURACY | P2 | High | DeepWiki calls `features/memory/mongo` long-term memory. It implements per-run transcript/event snapshots only; durable entry-shaped long-term memory is the separate `memory.Service` contract. |
| R8 | DOC-GAP | P2 | High | Registry operations, persistence guarantees, Pulse delivery semantics, and the test architecture are documented in scattered runtime/skill/internal files, but there is no concise public subsystem map or supported-configuration reference. |
| R9 | RESOLVED | P2 | Medium-high | **Complete 2026-07-16:** manager and semantic-client search share one concurrent fan-out and partial-failure implementation. |
| R10 | RESOLVED | P2 | Medium-high | **Complete 2026-07-16:** Mongo prompt overrides persist an indexed versioned fingerprint and resolve with at most one session plus one global query, ordered by specificity/newness. |
| R11 | ARCH-IMPROVEMENT | P2 | High | **Resolved 2026-07-16:** the integration runner embeds and copies checked-in, gofmt-clean SDK server application fixtures instead of injecting hundreds of lines of source constants. |
| R12 | DOC-GAP | P3 | High | `docs/runtime.md` has a small glossary, but there is no discoverable project-wide glossary covering MCP, engines, registries, skills, memory surfaces, and generated ownership. DeepWiki's glossary is useful but conflates several terms. |
| R13 | DEEPWIKI-INACCURACY | P2 | High | `TEST_AUDIT.md` is a dated remediation ledger tied to earlier baselines and hosted runs, not the live source of truth for repository reliability. Current required Make targets and their results are authoritative. |
| R14 | DEEPWIKI-INACCURACY | P2 | High | The glossary calls the transcript ledger an append-only record for an entire run. `transcript.Ledger` is mutable workflow state for the current turn; `runlog.Store` is the canonical append-only run-event record. |

## Page-by-page review

### 7. Registry Service

URL: <https://deepwiki.com/CaliLuke/loom-mcp/7-registry-service>

Claims checked:

- standalone gRPC registry acting as catalog and gateway;
- clustered catalog and health state through Pulse replicated maps;
- distributed health tickers and registration-token epochs;
- deterministic request streams, cross-node result routing, and tracing;
- configuration defaults.

Coverage and evidence:

- `registry/registry.go:51-97` owns the replicated maps, pool node, health tracker, stream manager, and current `Config`.
- `registry/catalog.go:26-36,67-87` stores JSON `catalogEntry` values with a new UUID registration token per save.
- `registry/health_tracker.go:27-80` defines registration-scoped health and staleness.
- `registry/stream_manager.go:57-141` creates deterministic streams lazily and injects trace context for calls.
- `docs/runtime.md:660-730` documents provider, consumer, and discovery responsibilities, but not complete registry deployment/operations.

Discrepancies:

- `MATCH` `P3`, high confidence: DeepWiki's core architecture and current defaults materially match the code.
- `DOC-GAP` `P2`, high confidence: public docs do not give one current deployment/configuration page covering Redis resource ownership, cluster naming, logging, TTLs, liveness transitions, and shutdown.
- `SKILL-GAP` `P1`, high confidence: `.agents/skills/loom-mcp/references/user-guides/internal-tool-registry.md:128-133,199-255` describes a pluggable metadata `Store`, memory/Mongo registry stores, `ResultStreamMappingTTL`, and a five-minute default. None are in current `registry.Config`; the catalog is the replicated map and `ResultStreamTTL` defaults to 15 minutes (`registry/registry.go:66-97`, `registry/service.go:47-75`).

### 7.1. Registry Service Internals

URL: <https://deepwiki.com/CaliLuke/loom-mcp/7.1-registry-service-internals>

Claims checked:

- catalog record format and search behavior;
- ticker reconciliation after unregister/re-register;
- trace propagation on tool calls;
- gRPC transport validation and graceful shutdown.

Coverage and evidence:

- `registry/catalog.go` is the canonical catalog, not a pluggable store.
- `registry/health_tracker.go` tracks the registration token for every local ticker and rotates stale epochs.
- `registry/stream_manager.go:113-168` owns lazy handles and producer tracing.
- generated gRPC decoders validate transport payloads; application validation stays in the registry design/generation path.

Discrepancies:

- `MATCH` `P3`, high confidence: this page is a more accurate current internal overview than the skill guide.
- `DOC-GAP` `P2`, high confidence: the useful registration-epoch invariant is code-commented but not surfaced in user/contributor docs; it explains why same-name provider replacement is safe.
- `ARCH-IMPROVEMENT` `P3`, medium confidence: catalog `List`/`Search` scans every replicated-map key and decodes every candidate (`registry/catalog.go:112-177`). This is acceptable for small catalogs but needs an explicit scale envelope or secondary index before calling the registry horizontally scalable without qualification.

### 7.2. Runtime Registry Manager

URL: <https://deepwiki.com/CaliLuke/loom-mcp/7.2-runtime-registry-manager>

Claims checked:

- multi-source discovery and catalog merging;
- TTL schema caching and failure fallback;
- semantic-first search with keyword fallback;
- gRPC client adaptation and observability.

Coverage and evidence:

- `runtime/registry/manager.go:13-125` owns registries and shared interfaces.
- `runtime/registry/manager_discovery.go:15-66` checks the cache, fetches, and applies failure fallback.
- `runtime/registry/manager_search.go:13-110` performs parallel plain search.
- `runtime/registry/search.go:89-220` adds semantic capability detection, filtering, ranking, and fallback.
- `docs/runtime.md:721-735` gives a concise, accurate discovery summary.

Discrepancies:

- `MATCH` `P3`, high confidence: DeepWiki's cache and fallback description matches the current contract.
- `DEEPWIKI-INACCURACY` `P3`, high confidence: the page repeatedly cites `grpc_client_adapter.go` as the owner of `Manager`; `Manager` is declared in `runtime/registry/manager.go`.
- `RESOLVED` `P2`, medium-high confidence: plain and semantic search now call
  `Manager.collectRegistrySearchResults`, which starts every selected registry
  concurrently and owns shared origin/error/partial-failure handling.

### 7.3. Tool Registry Executor & Provider

URL: <https://deepwiki.com/CaliLuke/loom-mcp/7.3-tool-registry-executor-and-provider>

Claims checked:

- canonical `call`, `result`, `output_delta`, and `ping` envelopes;
- deterministic request/result streams;
- provider concurrency and shedding;
- structured validation issues and retry hints.

Coverage and evidence:

- `runtime/toolregistry/messages.go` and `streams.go` define the wire contract and stream IDs.
- `runtime/toolregistry/executor/executor.go:161-215,476-694` dispatches, consumes, acknowledges, and maps structured issues to retry hints.
- `runtime/toolregistry/provider/provider.go:33-104,339-441` owns concurrency, hold/ack grace, ping handling, execution, and result publication.
- `docs/runtime.md:660-721,2580-2605` covers provider/consumer wiring and structured validation.

Discrepancies:

- `MATCH` `P3`, high confidence: the page accurately exposes an otherwise hard-to-discover subsystem.
- `SKILL-GAP` `P2`, high confidence: the internal-registry guide omits the operational consequences and tuning of `MaxConcurrentToolCalls`, queue capacity, `SinkAckGracePeriod`, output deltas, and the consumer acknowledgement/error contract.
- `DOC-GAP` `P2`, high confidence: add a failure-mode table (unhealthy provider, queue shedding, provider crash, timeout, duplicate delivery, poison message, result-stream cleanup) rather than documenting only the happy-path flow.

### 8. Persistence & Infrastructure Features

URL: <https://deepwiki.com/CaliLuke/loom-mcp/8-persistence-and-infrastructure-features>

Claims checked:

- Mongo adapters for sessions, runlog, transcript memory, and prompt overrides;
- shared Mongo client infrastructure;
- hook-to-stream translation and Pulse delivery.

Coverage and evidence:

- `features/{session,runlog,memory,prompt}/mongo` are four separate adapters with their own collections and indexes.
- `features/mongo/clientinfra` centralizes validation, timeouts, BSON normalization, health checks, and index setup.
- `runtime/agent/stream/subscriber.go` translates selected hooks; `features/stream/pulse` owns Redis/Pulse transport.
- `docs/runtime.md` covers the interfaces and feature packages, while the skill splits the story across memory, prompt, production, and streaming guides.

Discrepancies:

- `MATCH` `P3`, high confidence: the subsystem inventory is broadly accurate.
- `DEEPWIKI-INACCURACY` `P2`, high confidence: “durable storage for all critical agent state” is too broad. Artifacts require a separate `artifact.Store`; long-term memory requires `memory.Service`; Temporal owns durable workflow state.
- `DOC-GAP` `P2`, high confidence: add one persistence matrix with domain, runtime interface, default in-memory behavior, Mongo collection/index, consistency/idempotency contract, retention/size risks, and what is deliberately stored elsewhere.

### 8.1. MongoDB Persistence Layer

URL: <https://deepwiki.com/CaliLuke/loom-mcp/8.1-mongodb-persistence-layer>

Claims checked:

- idempotent session creation and transaction-guarded run mutations;
- atomic parent-child links;
- idempotent runlog append and cursor pagination;
- prompt specificity resolution;
- transcript memory append shape.

Coverage and evidence:

- `features/session/mongo/clients/mongo/client.go:92-262` uses `$setOnInsert`, transactions, session guards, and `$addToSet` child links.
- `features/runlog/mongo/clients/mongo/client.go:79-211` uses unique logical keys and stable `_id` cursor pagination.
- `features/prompt/mongo/clients/mongo/client.go:102-180,313-330` resolves session/global scopes by specificity with a compound lookup index.
- `features/memory/mongo/clients/mongo/client.go` now writes immutable append
  buckets to a companion events collection and reads the legacy embedded array
  before ordered buckets.
- `docs/runtime.md:1540-1645` correctly separates transcript search, long-term entry memory, and runlog.

Discrepancies:

- `DEEPWIKI-INACCURACY` `P1`, high confidence: `features/memory/mongo` is not long-term memory. It is the `memory.Store` transcript snapshot adapter; `memory.Service` is separate and currently not implemented by this package.
- `ARCH-IMPROVEMENT` `P1`, high confidence — **Resolved 2026-07-16:** new
  transcript writes are immutable companion-collection buckets. Reads merge the
  old run document first, so existing histories need no offline rewrite and the
  hot document no longer grows.
- `RESOLVED` `P2`, medium-high confidence: prompt documents now carry a
  canonical versioned SHA-256 `scope_fingerprint`; lookup uses exact matching
  subset fingerprints, a compound index, a 15-label bound, and at most two
  limit-one queries without a Go candidate scan.
- `DOC-GAP` `P2`, high confidence: document transaction/topology requirements. Session transaction guarantees require a Mongo deployment that supports transactions; users need explicit production prerequisites and failure semantics.

### 8.2. Pulse Streaming Infrastructure

URL: <https://deepwiki.com/CaliLuke/loom-mcp/8.2-pulse-streaming-infrastructure>

Claims checked:

- hook filtering/translation via stream profiles;
- Pulse envelope and consumer groups;
- poison-message handling and acknowledgement failures;
- cluster-aware model rate limiting.

Coverage and evidence:

- `runtime/agent/stream/subscriber.go:24-130` filters and translates hooks and propagates sink errors synchronously.
- `features/stream/pulse/sink.go:48-149` publishes envelopes with stable `event_key` when available.
- `features/stream/pulse/subscriber.go` exposes both compatible auto-ack
  event/error channels and explicit manual delivery handles.
- `docs/runtime.md:2656-2680` describes the current high-level subscriber error contract; the skill's streaming guide covers profiles and session stream naming.

Discrepancies:

- `DEEPWIKI-INACCURACY` `P1`, high confidence: `event_key` does not guarantee “exact-once processing.” It enables deduplication where a consumer persists the key; Pulse delivery and the channel API do not make that persistence atomic with acknowledgement.
- `ARCH-IMPROVEMENT` `P1`, high confidence — **Resolved 2026-07-16:**
  `SubscribeManual` leaves entries pending until `Delivery.Ack` succeeds and
  retains `Subscribe` as the auto-ack UI convenience API.
- `DOC-GAP` `P1`, high confidence — **Resolved 2026-07-16:** runtime and skill
  guidance now distinguishes UI and durable consumers, documents ack timing,
  poison-message behavior, at-least-once redelivery, and event-key idempotency.
- `MATCH` `P3`, high confidence: DeepWiki accurately describes profile filtering, poison-message acknowledgement, terminal ack errors, and distributed rate limiting.

### 9. Testing & Integration

URL: <https://deepwiki.com/CaliLuke/loom-mcp/9-testing-and-integration>

Claims checked:

- layered DSL/codegen/runtime validation;
- codegen goldens, compile matrices, integration scenarios, provider conformance, and CI gates;
- `TEST_AUDIT.md` as an evidence ledger.

Coverage and evidence:

- 53 `.golden` files currently exist under codegen testdata; `codegen/testhelpers/golden.go` and `codegen/agent/tests/README.md` define the pattern.
- `codegen/mcp/transport_conformance_contract_test.go` asserts generated transport contracts in-memory rather than via snapshots.
- `Makefile:39-62,122-126` owns unit/coverage/integration/local-MCP gates.
- `TEST_AUDIT.md` records the current remediation and verification campaign, but is an audit ledger rather than general user documentation.

Discrepancies:

- `MATCH` `P3`, high confidence: the overall layered testing description is accurate.
- `DEEPWIKI-INACCURACY` `P2`, high confidence: `TEST_AUDIT.md` is a dated remediation and evidence ledger with explicit baseline commits and historical hosted runs. It is useful review evidence, but it is not the live source of truth for repository reliability; the current required Make targets and their results own that status.
- `DEEPWIKI-INACCURACY` `P3`, high confidence: the page treats `transport_conformance_contract_test.go` as a golden-file example. It generates once and asserts rendered strings; it does not compare `.golden` files.
- `DOC-GAP` `P1`, high confidence: `integration_tests/README.md` is stale. `TEST_PARALLEL`, `TEST_FILTER`, `TEST_KEEP_GENERATED`, `TEST_DEBUG`, and `TEST_TIMEOUT` appear only in the README; only `TEST_SERVER_URL` is implemented. The README also calls progress pending even though SDK progress tests are present.
- `SKILL-GAP` `P1`, high confidence: the testing skill shows `model.Client.Generate`, but the current interface is `Complete`/`Stream` (`runtime/agent/model/model.go:580-590`). It should link to the actual repository gates and harness docs instead of presenting stale copy-paste examples.

### 9.1. Integration Test Framework

URL: <https://deepwiki.com/CaliLuke/loom-mcp/9.1-integration-test-framework>

Claims checked:

- managed or external server lifecycle;
- YAML scenario schema and JSON-RPC/SSE/CLI modes;
- generated agent acceptance fixture;
- just-in-time fixture cloning and patching.

Coverage and evidence:

- `integration_tests/framework/runner.go:25-107,146-200` defines the scenario model and lifecycle entry points.
- `runner_transport.go` implements HTTP JSON-RPC, SSE, generated-client retry checks, and CLI execution.
- `runner_fixture_prep.go` clones, applies checked-in application fixtures,
  generates, and builds the assistant fixture; replacement SDK server sources
  live under `framework/testdata/sdk_server_patch`.
- `integration_tests/fixtures/agent_features` separately proves DSL-to-runtime generated agent behavior.

Discrepancies:

- `MATCH` `P3`, high confidence: DeepWiki is substantially more current than `integration_tests/README.md` about the runner's real architecture.
- `DEEPWIKI-INACCURACY` `P2`, medium confidence: the page says the framework covers HTTP, JSON-RPC, SSE, and CLI “without manual test implementation for every new feature.” YAML covers the generic scenario surface, but many SDK/client-feature contracts remain dedicated Go tests and fixture service methods.
- `ARCH-IMPROVEMENT` `P2`, high confidence — **Resolved 2026-07-16:**
  `applySDKServerFixturePatch` embeds checked-in files from
  `framework/testdata/sdk_server_patch`; the replacement wiring is reviewable,
  reusable, and format-checked as ordinary Go source.
- `DOC-GAP` `P1`, high confidence: replace `integration_tests/README.md` with an accurate map of YAML scenarios, direct Go conformance tests, official SDK tests, agent feature acceptance, supported environment variables, and the `make itest` / `make verify-mcp-local` boundaries.

### 9.2. Codegen Golden Tests & Conformance

URL: <https://deepwiki.com/CaliLuke/loom-mcp/9.2-codegen-golden-tests-and-conformance>

Claims checked:

- focused DSL scenarios and formatted golden source comparison;
- compile matrices in addition to string/golden assertions;
- generated MCP transport contracts;
- shared model-provider conformance.

Coverage and evidence:

- `codegen/agent/tests/testdata/golden` contains focused expected outputs; helpers normalize generated headers and format Go.
- `codegen/agent/mcp_executor_compile_test.go` compiles materially different generated package graphs.
- `codegen/mcp/transport_conformance_contract_test.go` checks optional params, SSE messages/retry, origin checks, session headers, and SDK surfaces.
- provider packages use shared `testutil.ProviderConformanceSuite` coverage.

Discrepancies:

- `MATCH` `P3`, high confidence: the combined golden + compile + contract + provider-conformance model is accurate.
- `DOC-GAP` `P2`, high confidence: `codegen/agent/tests/README.md:44-64` still labels many now-implemented cases as “planned additions.” Refresh the matrix from the actual `golden_*_test.go` and `.golden` inventory.
- `DOC-GAP` `P2`, high confidence: contributor docs should explain which proof to choose: exact golden, invariant string assertion, compile matrix, regenerated integration fixture, or live protocol conformance.

### 10. Glossary

URL: <https://deepwiki.com/CaliLuke/loom-mcp/10-glossary>

Claims checked:

- agent/toolset/planner/runtime terms;
- agent-as-tool, engine, registry, memory, skill, transcript, MCP, and Pulse mappings.

Coverage and evidence:

- `docs/runtime.md:2765-2780` has a small runtime glossary.
- terminology is otherwise distributed across `docs/overview.md`, `docs/dsl.md`, `docs/runtime.md`, and skill contract files.

Discrepancies:

- `DOC-GAP` `P3`, high confidence: expand the existing glossary or add a project glossary with stable links to authoritative contracts. Include agent/tool identifiers, toolset sources, MCP server vs caller, tool registry vs prompt registry, run/session/turn, engine, planner, hooks vs streams, transcript memory vs long-term memory, skills as MCP resources vs model-facing skill tools, and generated ownership.
- `DEEPWIKI-INACCURACY` `P2`, high confidence: “Skill” conflates two different projections. `SkillDirectory(...)` exposes `skill://` MCP resources; `Toolset(FromSkills(...))` exposes model-facing skill discovery/load tools. A skill is primarily an instruction package with metadata, not simply a collection of local tools/resources.
- `DEEPWIKI-INACCURACY` `P2`, high confidence: “Transcript” is defined as an append-only run ledger, but `runtime/agent/transcript.Ledger` is mutable workflow state for the current assistant turn and can be rebuilt from projected memory events. The canonical append-only run-event record is `runlog.Store`; treating the ledger as durable run history obscures the actual recovery and introspection boundaries.
- `DEEPWIKI-INACCURACY` `P3`, high confidence: “Pulse Sink” is mapped to `runtime/agent/stream/stream.go`, which defines the generic `stream.Sink` interface; the Pulse implementation lives in `features/stream/pulse/sink.go`.
- `DEEPWIKI-INACCURACY` `P3`, high confidence: the Bedrock glossary entry contains a malformed code pointer and should not be copied into project docs.

> Resolved 2026-07-16 (`M6`): `docs/glossary.md` is now the discoverable
> canonical glossary. It distinguishes workflow state, runlog, transcript
> ledger/memory, long-term memory, hook/stream/Pulse delivery, MCP server/caller,
> MCP skill resources, model-facing skill tools, registries, and generated
> ownership, with links back to authoritative guides.

## Prioritized recommendations

1. Fix the skill before reusing it for implementation: rewrite `internal-tool-registry.md` from current `registry.Config`/replicated-map behavior and repair the testing guide's current APIs and verification paths.
2. Replace `integration_tests/README.md` with an evidence-derived harness map and remove unsupported environment variables and stale pending-feature claims.
3. ~~Document Pulse delivery semantics precisely and design a manual-ack subscriber for durable projectors.~~ Complete 2026-07-16.
4. ~~Plan a bucketed/append-only Mongo transcript representation before supporting unbounded long-running runs as a production guarantee.~~ Complete 2026-07-16.
5. Add a consolidated persistence/registry operations guide that links the authoritative deeper runtime pages instead of duplicating large code examples.
6. **Complete 2026-07-16:** registry search fanout is shared and concurrent;
   prompt override resolution uses an indexed scope fingerprint.
7. Move integration fixture patch source into checked-in fixture/application code.
8. Expand the glossary only after separating the currently conflated memory and skill surfaces.

## Page coverage checklist

- [x] 7 Registry Service
- [x] 7.1 Registry Service Internals
- [x] 7.2 Runtime Registry Manager
- [x] 7.3 Tool Registry Executor & Provider
- [x] 8 Persistence & Infrastructure Features
- [x] 8.1 MongoDB Persistence Layer
- [x] 8.2 Pulse Streaming Infrastructure
- [x] 9 Testing & Integration
- [x] 9.1 Integration Test Framework
- [x] 9.2 Codegen Golden Tests & Conformance
- [x] 10 Glossary
