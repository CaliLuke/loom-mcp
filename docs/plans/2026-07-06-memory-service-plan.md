# Long-Term Memory Service Plan

Date: 2026-07-06

This plan covers `ADK_PRODUCT_GAPS.md` point 1: a first-class long-term memory
service for Loom MCP. It uses the current Google ADK Go package in
`/tmp/adk-go-memory-plan-oUtGDs/adk-go`, the ADK memory docs, and Loom MCP's
current runtime contracts as references.

## Current Contract Inventory

Loom MCP already has transcript memory, but it does not yet have a durable
long-term memory product surface.

- `runtime/agent/memory.Store` persists run transcript history with
  `LoadRun(ctx, agentID, runID)` and `AppendEvents(ctx, agentID, runID, ...)`.
- `runtime/agent/memory.Searcher` runs indexed or cross-run event queries and
  returns `memory.QueryResult{Events: []memory.Event}`.
- `runtime/agent/memory/inmem.Store` implements both `Store` and `Searcher`
  over in-process maps.
- `features/memory/mongo.Store` implements only transcript storage.
- `runtime/agent/runtime.Runtime` has `Memory memory.Store` and
  `MemorySearcher memory.Searcher`, configured by `WithMemoryStore` and
  `WithMemorySearcher`.
- `runtime/agent/runtime/memory_toolset.go` exposes one model-facing
  `load_memory` tool that returns raw `memory.Event` values from
  `scope:"current_run"` or `scope:"indexed"`.
- `runtime/agent/runtime/memory_preload.go` injects bounded
  `[]memory.Event` into `planner.PlanInput.PreloadedMemory` and
  `planner.PlanResumeInput.PreloadedMemory`.
- `dsl/toolset.go`, `dsl/policy.go`, `expr/agent/toolset.go`,
  `expr/agent/policy.go`, and `codegen/agent` own the DSL and generated
  registration surface for `FromMemory`, `MemoryMaxResults`, and
  `PreloadMemory`.
- `integration_tests/fixtures/agent_features` is the generated acceptance
  fixture for agent memory toolsets and preload behavior.

## ADK Reference Shape

ADK makes a product distinction that Loom should keep:

- `Session` and `State` are short-term conversation/runtime state.
- `MemoryService` is long-term searchable knowledge across sessions.
- The ADK docs describe four conceptual memory operations: ingest a completed
  session, ingest event deltas, write direct memory entries, and search memory.
- The current ADK Go API in `/tmp/adk-go-memory-plan-oUtGDs/adk-go/memory`
  exposes `Service.AddSessionToMemory` and `Service.SearchMemory`.
- ADK memory search returns entry-shaped results with content, author,
  timestamp, and custom metadata, not raw event logs.
- ADK's higher-value backends separate service style from framework API:
  in-memory keyword search, Memory Bank extraction/consolidation, and RAG-style
  retrieval are all memory services.

## Design Goals

1. Keep `memory.Store` as transcript persistence. Do not overload it with
   extracted facts, embeddings, or direct memory writes.
2. Add a first-class `memory.Service` for long-term memory entries.
3. Keep storage fully pluggable. Postgres, Mongo, vector databases, managed RAG,
   or in-memory tests should all implement the same runtime-facing service.
4. Keep extraction policy out of generated code. Backends may store raw snippets,
   run model-backed consolidation, or write into an external RAG system.
5. Make agent use design-visible: tool access and preload should intentionally
   select transcript memory or long-term memory.
6. Make tenant routing explicit. Long-term memory must never silently become a
   global process-wide search surface.
7. Preserve current runtime behavior unless a design opts into long-term memory.

## Proposed Runtime Contract

Add long-term memory types to `runtime/agent/memory`, next to the existing
transcript `Store` and event `Searcher` contracts.

```go
type Service interface {
    IngestRun(ctx context.Context, input IngestRunInput) (IngestResult, error)
    IngestEvents(ctx context.Context, input IngestEventsInput) (IngestResult, error)
    PutEntry(ctx context.Context, input PutEntryInput) (Entry, error)
    Search(ctx context.Context, query SearchQuery) (SearchResult, error)
}

type Scope struct {
    Namespace  string
    UserID     string
    Visibility Visibility
}

type Visibility string

const (
    VisibilityUser   Visibility = "user"
    VisibilityShared Visibility = "shared"
)

type SourceKind string

const (
    SourceRunEvent SourceKind = "run_event"
    SourceDirect   SourceKind = "direct"
    SourceExternal SourceKind = "external"
)

type SourceRef struct {
    Kind           SourceKind
    AgentID        string
    SessionID      string
    RunID          string
    EventOrdinal   int
    EventHash      string
    ToolCallID     string
    ExternalID     string
    IdempotencyKey string
}

type IngestRunInput struct {
    Scope    Scope
    AgentID  string
    SessionID string
    RunID    string
    Events   []Event
    Labels   map[string]string
    Metadata map[string]any
}

type IngestEventsInput struct {
    Scope        Scope
    AgentID      string
    SessionID    string
    RunID        string
    StartOrdinal int
    Events       []Event
    Labels       map[string]string
    Metadata     map[string]any
}

type PutEntryInput struct {
    Scope     Scope
    Content   string
    Author    string
    Timestamp time.Time
    Sources   []SourceRef
    Labels    map[string]string
    Metadata  map[string]any
}

type IngestResult struct {
    Entries   []Entry
    Skipped   int
    Truncated bool
}

type Entry struct {
    ID             string
    Scope          Scope
    Content        string
    Author         string
    Timestamp      time.Time
    Sources        []SourceRef
    Labels         map[string]string
    Metadata       map[string]any
}

type SearchQuery struct {
    Scope Scope
    Query string
    Labels map[string]string
    Limit int
}

type SearchHit struct {
    Entry       Entry
    Score       float64
    Snippet     string
    MatchedRefs []SourceRef
}

type SearchResult struct {
    Hits      []SearchHit
    Truncated bool
}

type ToolSource string

const (
    ToolSourceTranscript        ToolSource = "transcript"
    ToolSourceIndexedTranscript ToolSource = "indexed_transcript"
    ToolSourceLongTerm          ToolSource = "long_term"
)
```

Notes:

- `Namespace` is the backend-neutral tenant/app boundary. Applications can map
  this to product, workspace, account, or deployment without changing Loom.
- `Scope` is required on long-term service calls. Runtime helpers derive it
  through a configured resolver; direct service callers supply it explicitly.
- `UserID` is part of `Scope` on entries, writes, ingests, and searches so
  durable backends can enforce user isolation instead of treating it as a
  query-only hint.
- `VisibilityUser` is the default for model-facing long-term memory. It
  requires a non-empty `UserID`; otherwise runtime returns an unsupported
  operation retry hint instead of searching shared memory accidentally.
- `VisibilityShared` is explicit opt-in for team/project/global knowledge. It
  allows empty `UserID` but still requires a non-empty `Namespace`.
- `Content` is text-first. Binary or large structured content should remain
  artifacts; memory entries may carry artifact IDs in `Metadata`.
- `SearchHit.Score`, `Snippet`, and `MatchedRefs` are query-time metadata.
  `Entry` stays durable row state for direct writes, ingest, provenance, and
  storage tests.
- `IngestRun` and `IngestEvents` accept Loom `memory.Event` values plus run
  identity. Services decide whether to store raw snippets, extract facts, merge
  entries, or forward to an external knowledge system.
- `SourceRef` avoids relying on nonexistent `memory.Event` IDs. Runtime ingest
  computes stable event ordinals and event hashes from the run snapshot; direct
  writes can supply `ExternalID` or `IdempotencyKey`.
- `SourceKind` keeps direct writes, run-event extraction, and external/RAG
  imports distinct without coupling the service to one storage layout.
- `ToolSource` is separate from `SourceKind`: tool sources control generated
  model-facing tool exposure, while source kinds describe entry provenance.
- `Labels` on ingest, entries, and search queries are filter/index labels.
  They are separate from `Scope`, which only carries routing and visibility.

## Storage-Agnostic Backend Shape

The core runtime should depend only on `memory.Service`. Storage packages live
under `features/memory/*` and remain optional.

First implementations:

- `runtime/agent/memory/inmem.Service`: test/local service. Stores entries in
  maps keyed by namespace and agent/user/session labels. `IngestRun` and
  `IngestEvents` extract textual user/assistant/planner snippets with basic
  keyword search.
- `features/memory/postgres`: durable service for projects already on Postgres.
  Start with plain SQL plus JSONB labels/metadata and `tsvector` keyword search.
  Add optional `pgvector` support behind configuration, not in the core
  interface.
- `features/memory/mongo`: optional durable service parallel to the existing
  transcript Mongo store. It can use text indexes for entry search.
- Future RAG/provider packages: implement `memory.Service` directly, or wrap an
  external RAG client with the same `Search`/ingest contract.

Backend rules:

- No storage driver types in `runtime/agent/memory` or generated code.
- No required vector dependency in core.
- Backends own migrations/index setup and expose constructors that return
  `memory.Service`.
- Durable backends should support idempotent ingest using `Scope` plus
  `SourceRef.IdempotencyKey`, or a deterministic key derived from
  `kind/agent_id/session_id/run_id/event_ordinal/event_hash`.
- Postgres backends should model provenance as a separate `entry_sources` table
  instead of trying to put `[]SourceRef` into a uniqueness constraint. The
  entries table owns `namespace`, `user_id`, text, labels, metadata, and search
  indexes; `entry_sources` owns source uniqueness and many-source consolidation.
- Direct entry writes must be available even when event extraction is not.

## Runtime Wiring

Extend `runtime.Options` and `Runtime`:

```go
type Options struct {
    MemoryStore         memory.Store
    MemorySearcher      memory.Searcher
    MemoryService       memory.Service
    MemoryScopeResolver memory.ScopeResolver
}

func WithMemoryService(s memory.Service) RuntimeOption
func WithMemoryScopeResolver(r memory.ScopeResolver) RuntimeOption
```

Keep the old fields:

- `MemoryStore`: transcript snapshots and current-run replay.
- `MemorySearcher`: indexed raw event search.
- `MemoryService`: long-term entry memory.

Add a resolver contract in `runtime/agent/memory`:

```go
type ScopeResolver interface {
    ResolveMemoryScope(ctx context.Context, input ScopeInput) (Scope, error)
}

type ScopeInput struct {
    AgentID    string
    SessionID  string
    RunID      string
    Visibility Visibility
    Labels     map[string]string
    Payload    map[string]any
}
```

Resolver rules:

- Long-term memory operations in runtime-owned tools and preload must call the
  resolver before touching `MemoryService`.
- The default resolver uses a non-global namespace derived from agent ID,
  reads optional `memory.namespace` and `memory.user_id` run labels, and strips
  those reserved keys from the labels copied into memory entries or queries.
  Production multi-tenant apps should configure
  `WithMemoryScopeResolver` to map account/project/user context explicitly.
- If a configured resolver returns an empty namespace, runtime returns a
  structured error instead of searching global memory.
- If the requested visibility is `user` and the resolved `UserID` is empty,
  runtime returns a structured unsupported-operation retry hint. Shared memory
  requires explicit `VisibilityShared` in the design or runtime call.

Update `MemoryToolsetConfig`:

```go
type MemoryToolsetConfig struct {
    Name          string
    Store         memory.Store
    Searcher      memory.Searcher
    Service       memory.Service
    ScopeResolver memory.ScopeResolver
    Sources       []memory.ToolSource
    Visibility    memory.Visibility
    MaxResults    int
}
```

Keep transcript and long-term tools separate:

- `load_memory` remains the transcript/event tool. It accepts existing
  `scope:"current_run"` and `scope:"indexed"` values and returns `events`.
- New `search_memory` is the long-term entry tool. It requires `MemoryService`,
  accepts a required `query`, optional `labels`, optional `limit`, and returns
  `hits`.
- Source selection is enforced at runtime:
  - `FromMemory()` with no source option preserves current behavior:
    transcript plus indexed transcript.
  - Specifying any of `MemoryTranscript()`, `MemoryIndexedTranscript()`, or
    `MemoryLongTerm()` replaces that default with the explicit source set.
  - `load_memory` rejects unconfigured transcript scopes with
    `unsupported_operation`.
  - If exactly one transcript source is configured, empty `scope` defaults to
    that source. If both transcript sources are configured, empty `scope`
    preserves current-run compatibility.
  - `search_memory` is registered only when `MemoryLongTerm()` is configured.
- Toolset visibility is design/runtime-owned. Model payloads do not control
  visibility or routing; shared memory requires explicit `MemoryVisibilityShared`
  in the design/runtime configuration.
- Missing `MemoryService` or unresolved memory scope returns a structured
  `unsupported_operation` retry hint.

Do not auto-ingest every run into long-term memory in the first milestone.
Instead, provide explicit application/runtime entry points:

```go
func (r *Runtime) AddRunToMemory(ctx context.Context, input memory.IngestRunInput) (memory.IngestResult, error)
func (r *Runtime) AddEventsToMemory(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error)
func (r *Runtime) PutMemoryEntry(ctx context.Context, input memory.PutEntryInput) (memory.Entry, error)
```

The runtime convenience method may load transcript events from `MemoryStore`,
but callers must provide `Scope` in the input or opt into runtime scope
resolution through a helper that takes the current run context. Avoid the
two-argument `agentID/runID` shape because it hides tenant routing.
If `AddRunToMemory` receives no `Events`, it loads the run snapshot from
`MemoryStore`; `AddEventsToMemory` treats an empty `Events` slice as a no-op.

Automatic ingest can be added later as a design-visible run policy, after the
manual contract is proven.

## DSL And Codegen Shape

Keep transcript preload unchanged and add long-term preload separately:

```go
PreloadLongTermMemory(MemoryMaxResults(5))
```

Use long-term tool access and long-term preload intentionally:

```go
var TranscriptMemory = Toolset("transcript_memory", FromMemory(MemoryTranscript(), MemoryMaxResults(20)))
var PersonalMemory = Toolset("personal_memory", FromMemory(MemoryLongTerm(), MemoryVisibilityUser(), MemoryMaxResults(20)))
var TeamMemory = Toolset("team_memory", FromMemory(MemoryLongTerm(), MemoryVisibilityShared(), MemoryMaxResults(20)))

Agent("assistant", "Memory-aware assistant", func() {
    Use(PersonalMemory)
    RunPolicy(func() {
        PreloadLongTermMemory(MemoryVisibilityUser(), MemoryMaxResults(5))
    })
})
```

Codegen updates:

- `expr/agent/toolset.go`: add provider flags for explicit memory sources and
  visibility: `MemoryTranscript()`, `MemoryIndexedTranscript()`,
  `MemoryLongTerm()`, `MemoryVisibilityUser()`, and
  `MemoryVisibilityShared()`.
- Preserve existing `FromMemory(MemoryMaxResults(...))` behavior as
  transcript plus indexed transcript for source compatibility. Long-term memory
  remains opt-in via `MemoryLongTerm()`.
- `dsl/toolset.go`: add the same options as `FromMemory` provider options.
- `MemoryVisibilityUser()` and `MemoryVisibilityShared()` return option values
  accepted by both `FromMemory(...)` and `PreloadLongTermMemory(...)`.
  `MemoryTranscript()`, `MemoryIndexedTranscript()`, and `MemoryLongTerm()` are
  provider-only options and must be rejected outside `FromMemory(...)`.
- `expr/agent/policy.go`: add `LongTermMemoryPreload *MemoryPreloadExpr` while
  leaving existing `PreloadMemory` as transcript preload.
- `dsl/policy.go`: add `PreloadLongTermMemory(...)`. Its default visibility is
  `VisibilityUser`.
- `codegen/agent`: pass `Service: rt.MemoryService` and
  `ScopeResolver: rt.MemoryScopeResolver` into generated
  `NewMemoryToolsetRegistration` calls when the toolset opts into long-term
  memory. Also pass generated source and visibility values so runtime
  registrations can omit disallowed tools and keep visibility out of model
  payload control.
- `codegen/agent/tests/golden_memory_toolset_test.go`: assert generated
  registration includes service and resolver fields only for long-term memory
  toolsets.
- Add a long-term-only golden scenario that exposes `search_memory` without
  exposing transcript `load_memory`, and a transcript-only scenario that keeps
  current behavior.
- `integration_tests/fixtures/agent_features/design/design.go`: add a
  long-term memory use case once runtime tests are green.

Preload behavior:

- `MemoryScopeCurrentRun`: continue returning current-run events.
- `MemoryScopeIndexed`: continue returning indexed events.
- `PreloadLongTermMemory`: search `MemoryService` using the current user
  message as query input when available; otherwise return no long-term preload.
- Add `PreloadedMemoryEntries []memory.Entry` to `planner.PlanInput` and
  `planner.PlanResumeInput`. Keep existing `PreloadedMemory []memory.Event` as
  transcript-event preload so planners can distinguish raw history from durable
  extracted entries without inspecting result payloads.
- Add a canonical `memory.FormatEntriesForPrompt(...)` helper for generated or
  default planners that want ADK-style prompt text while preserving structured
  planner inputs.
- Runtime builds the long-term preload query from the latest user text message
  in the planner messages after `applyHistoryPolicy`. If no user text remains
  after history policy, skip long-term preload. Tool-only resumes must not query
  with tool output text.

## Milestone Plan

Each milestone is test-first:

1. Add the smallest compile-enabling type/function stubs needed for the tests.
2. Add focused failing tests for the behavior named in the milestone.
3. Implement the behavior until the targeted tests pass.
4. Run the milestone proof command.

Do not run broad package proof commands while tests intentionally import symbols
that have not been stubbed yet; mark those as contract tests in the commit or
working notes.

### Milestone 1: Contract And In-Memory Service

Files:

- `runtime/agent/memory/service.go`
- `runtime/agent/memory/inmem/service.go`
- `runtime/agent/memory/service_test.go`
- `runtime/agent/memory/inmem/service_test.go`

Work:

1. Add `memory.Service`, `Entry`, ingest inputs, direct write input, search
   query, scope resolver, source reference, and result types.
2. Implement in-memory long-term service with deterministic keyword search.
3. Add contract-style tests covering direct writes, run ingest, event ingest,
   namespace/user isolation, labels, limits, defensive copies, and idempotent
   source identity.

Proof:

```bash
go test ./runtime/agent/memory/...
```

### Milestone 2: Runtime Tool Surface

Files:

- `runtime/agent/runtime/runtime.go`
- `runtime/agent/runtime/runtime_bootstrap.go`
- `runtime/agent/runtime/memory_toolset.go`
- `runtime/agent/runtime/memory_preload.go`
- `runtime/agent/runtime/memory_toolset_test.go`
- `runtime/agent/planner/planner.go`
- `runtime/agent/planner/planner_test.go`
- `runtime/agent/runtime/runtime_plan_test.go`

Work:

1. Add `Runtime.MemoryService` and `WithMemoryService`.
2. Add `Runtime.MemoryScopeResolver` and `WithMemoryScopeResolver`.
3. Wire `MemoryToolsetConfig.Service` and `ScopeResolver`.
4. Add long-term `search_memory` while keeping transcript `load_memory`
   result shape unchanged.
5. Add explicit runtime methods for `AddRunToMemory`, `AddEventsToMemory`, and
   `PutMemoryEntry`.
6. Return structured retry hints when long-term memory is requested without a
   service or resolvable scope.
7. Add `PreloadedMemoryEntries []memory.Entry` to start/resume planner inputs
   and populate it only for `PreloadLongTermMemory`.
8. Add tests for user visibility failure, explicit shared visibility, latest
   user-message query extraction after history policy, and tool-result-only
   resume skipping long-term preload.
9. Add tests proving `search_memory` payloads cannot control visibility and
   that user/shared access is determined by design/runtime scope.
10. Compute history-filtered messages before long-term preload so preload query
    extraction uses the same message view passed to the planner.

Proof:

```bash
go test ./runtime/agent/planner ./runtime/agent/runtime -run 'Memory|RuntimeMemory|PreloadedMemory'
```

### Milestone 3: DSL And Generated Registration

Files:

- `expr/agent/policy.go`
- `expr/agent/memory_test.go`
- `expr/agent/provider.go`
- `dsl/policy.go`
- `dsl/toolset.go`
- `dsl/memory_test.go`
- `codegen/agent`
- `codegen/agent/data.go`
- `codegen/agent/data_runtime.go`
- `codegen/agent/registry_render.go`
- `codegen/agent/tests/golden_memory_toolset_test.go`
- `codegen/agent/tests/testscenarios/memory_toolset.go`

Work:

1. Add source and visibility options for memory toolsets.
2. Add and validate `PreloadLongTermMemory(...)`, including user/shared
   visibility.
3. Generate `Service: rt.MemoryService` and
   `ScopeResolver: rt.MemoryScopeResolver` for opted-in memory toolsets.
4. Add golden coverage for transcript-only, indexed-only, long-term-only, and
   mixed memory toolsets.

Proof:

```bash
go test ./dsl ./expr/agent ./codegen/agent/tests -run Memory
```

### Milestone 4: Generated Acceptance Fixture

Files:

- `integration_tests/fixtures/agent_features/design/design.go`
- regenerated `integration_tests/fixtures/agent_features/gen/...`
- `integration_tests/fixtures/agent_features/*_test.go`

Work:

1. Update the agent features fixture to exercise long-term memory search.
2. Regenerate fixture output with `make regen-agent-feature-fixture`.
3. Seed the in-memory service through `PutMemoryEntry` or `IngestEvents`.
4. Assert generated registration, tool execution, and planner preload expose
   long-term entries without changing current-run transcript behavior.

Do not assert debug-server long-term memory output in this milestone. The
current debug endpoint is transcript-backed. Add a later debug milestone if the
product needs `/memory/search` or `/runs/{runID}/memory/long_term`.

Proof:

```bash
make regen-agent-feature-fixture
make verify-agent-feature-fixture
```

### Milestone 5: Storage Package Follow-Up

Files:

- `features/memory/postgres`
- `features/memory/mongo`
- `docs/runtime.md`
- `docs/dsl.md`

Work:

1. Add a Postgres-backed `memory.Service` after the core contract is stable.
2. Keep the constructor storage-specific but return the generic
   `memory.Service`.
3. Use plain SQL/JSONB/full-text first; add optional vector search only through
   backend config.
4. Add a Mongo service if product demand remains after Postgres lands.
5. Document backend selection and migration/index responsibilities.

Proof:

```bash
go test ./features/memory/...
```

### Milestone 6: Docs, Skill, And Full Verification

Files:

- `ADK_PRODUCT_GAPS.md`
- `docs/runtime.md`
- `docs/dsl.md`
- `.agents/skills/loom-mcp/SKILL.md`
- `.agents/skills/loom-mcp/references/runtime-contracts.md`

Work:

1. Mark the gap as designed/implemented as milestones land.
2. Document memory store vs searcher vs service clearly.
3. Update repo-local skill guidance so future work keeps transcript memory and
   long-term entry memory separate.
4. Run the full framework gate.

Proof:

```bash
make lint
make test
make itest
make verify-mcp-local
```

## Open Decisions

- Whether automatic ingest should be modeled as `RunPolicy` or as an application
  callback/interceptor pattern.
- Whether Postgres should use `database/sql`, `pgx`, or an existing project
  adapter. This should be decided when implementing `features/memory/postgres`,
  not in the core contract.
- Whether `PlannerContext` should expose a direct long-term memory client. The
  first design keeps long-term memory available through preload and model-facing
  tools only, preserving the existing `PlannerContext.Memory()` transcript
  meaning.

## Recommendation

Implement the service natively in Loom MCP rather than depending on ADK. The
ADK shape is useful as product guidance, but Loom should keep its design-first
DSL/codegen/runtime ownership and expose storage-neutral contracts. The first
green slice should be in-memory service plus opt-in `search_memory` tool
support; Postgres should follow as a normal optional backend once the interface
is stable.
