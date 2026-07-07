# ADK Product Gaps

Date: 2026-07-06

This document captures the product gaps found by reviewing current `loom-mcp`
against the Google Agent Development Kit docs. It is intended as a root-level
backlog so we can address the gaps systematically instead of losing the review
context.

## Source Documents

- ADK memory: <https://adk.dev/sessions/memory/>
- ADK session: <https://adk.dev/sessions/session/>
- ADK state: <https://adk.dev/sessions/state/>
- ADK events: <https://adk.dev/events/>
- ADK artifacts: <https://adk.dev/artifacts/>
- ADK callbacks: <https://adk.dev/callbacks/>
- ADK plugins: <https://adk.dev/plugins/>
- ADK graph workflows: <https://adk.dev/graphs/>
- ADK A2A: <https://adk.dev/a2a/>
- ADK Gemini Live streaming: <https://adk.dev/streaming/>
- ADK evaluation: <https://adk.dev/evaluate/>
- ADK environment simulation: <https://adk.dev/evaluate/environment_simulation/>

## Current Strengths

These are areas where Loom already has comparable or stronger primitives.

| ADK area | Loom status | Evidence |
| --- | --- | --- |
| Graph workflows | Covered. Loom has deterministic graph nodes, joins, branches, loops, and typed input. | `runtime/agent/planner/workflow_graph.go` defines `WorkflowNode`, `WorkflowNodeBranch`, `WorkflowNodeLoop`, and `WorkflowNodeTypedInput`. |
| Callbacks/plugins | Mostly covered under Loom's interceptor model. Loom has run, tool, model, and event interceptors. | `runtime/agent/runtime/interceptors.go` defines `RunInterceptor`, `ToolInterceptor`, `ModelInterceptor`, and `EventInterceptor`. |
| Tool confirmation | Covered. Runtime-owned approval can pause and resume tool execution. | `runtime/agent/runtime/confirmation.go`, `runtime/agent/runtime/confirmation_workflow.go`, and DSL `Confirmation(...)`. |
| MCP | Strongly covered. Loom both consumes external MCP servers and exposes designed services through generated adapters. | `runtime/mcp`, `codegen/mcp`, `docs/mcp_sdk_server.md`, `docs/runtime.md`. |
| Run artifacts | Partially covered. Loom can persist and load artifacts by agent/run/tool call. | `runtime/agent/artifact/artifact.go` and `runtime/agent/runtime/artifact_toolset.go`. |
| Memory lookup | Partially covered. Loom can persist run history and expose bounded current-run or indexed event lookup. | `runtime/agent/memory/memory.go`, `runtime/agent/memory/searcher.go`, `runtime/agent/runtime/memory_toolset.go`. |

## Priority Gap Backlog

### P1. Long-Term Memory Service

Status: core contract, in-memory service, runtime tools/preload, DSL/codegen,
and generated acceptance fixture implemented. Durable Postgres/Mongo/RAG
backends remain follow-up work.

Detailed plan: `docs/plans/2026-07-06-memory-service-plan.md`

ADK product shape:

- ADK separates short-term session state from long-term knowledge.
- Its `MemoryService` supports four conceptual operations:
  - ingest a completed session into memory,
  - ingest events incrementally,
  - write pre-built memory entries directly,
  - search memory.
- ADK memory responses are `MemoryEntry`-oriented and can carry content,
  author, timestamp, and metadata.
- ADK documents Memory Bank and RAG-backed services as higher-value durable
  memory backends, not just transcript search.

Original Loom evidence:

- `memory.Store` only persists run history with `LoadRun` and `AppendEvents`.
- `memory.Searcher` only exposes `Query(ctx, Query) (QueryResult, error)`.
- `QueryResult` returns matching `memory.Event` values, not durable extracted
  memory entries.
- `runtime.NewMemoryToolsetRegistration` exposes one model-facing
  `load_memory` tool over current-run or indexed event search.

Why this matters:

- Loom has searchable transcript memory, but not a first-class long-term memory
  product surface.
- There is no framework-owned contract for memory extraction, consolidation,
  direct memory writes, memory provenance, or multiple memory service styles.
- This was the main gap missed in the earlier ADK comparison.

Implemented core work:

1. Added first-class `memory.Service` distinct from `memory.Store`.
2. Added `memory.Entry`, `SearchResult`, `Scope`, provenance, ingest, direct
   write, and search contracts around long-term facts or snippets.
3. Added runtime entry points for run ingest, event ingest, direct entry writes,
   and long-term search through `search_memory`.
4. Kept `memory.Store` as durable run transcript storage and `memory.Searcher`
   as raw event search.
5. Added explicit DSL/codegen source selection with `MemoryTranscript()`,
   `MemoryIndexedTranscript()`, `MemoryLongTerm()`, memory visibility helpers,
   and `PreloadLongTermMemory(...)`.
6. Added an in-memory implementation and generated agent feature fixture
   coverage.

Remaining next work:

1. Add a Postgres-backed `memory.Service` once the core contract settles.
2. Add Mongo or provider/RAG-backed services if product demand remains.
3. Decide whether automatic ingest should be a run policy or application
   callback/interceptor pattern.

Primary implementation owners:

- `runtime/agent/memory`
- `runtime/agent/runtime/memory_toolset.go`
- `dsl`, `expr/agent`, and `codegen/agent` if DSL-visible memory service
  selection is added
- `docs/runtime.md`, `docs/dsl.md`, and `.agents/skills/loom-mcp/SKILL.md`

### P1. Persistent Session State

ADK product shape:

- ADK `Session` includes identifiers, chronological `events`, mutable
  `state`, and `lastUpdateTime`.
- ADK `session.state` is documented as a key-value scratchpad for preferences,
  task progress, accumulated information, and decision flags.
- ADK state has scoped key conventions such as session/user/app/temp state.

Current Loom evidence:

- `session.Session` only stores `ID`, `Status`, `CreatedAt`, and `EndedAt`.
- `session.Store` manages session lifecycle and run metadata, not session state.
- `planner.PlannerContext.State()` currently returns `noopAgentState{}` from
  `runtime/agent/runtime/agent_context.go`.
- Session-owned run grouping exists, but mutable state does not persist through
  the runtime/session store.

Why this matters:

- Workflows, callbacks/interceptors, and tools need a shared state surface if
  Loom wants ADK-style session continuity.
- Long-term memory should not be overloaded to handle short-term workflow state.
- Durable state is also a prerequisite for clean resumability, partial progress,
  and user/app-scoped preferences.

Recommended next work:

1. Add a state contract, likely separate from `session.Store` at first to avoid
   widening every session backend in one change.
2. Model scopes explicitly: session, user, app, and temp/in-run.
3. Replace `noopAgentState` with a runtime-backed implementation when a state
   store is configured.
4. Record state mutations through hook/run events so replay/debug remains
   explainable.
5. Document state versus memory versus artifacts clearly.

Primary implementation owners:

- `runtime/agent/session`
- `runtime/agent/planner`
- `runtime/agent/runtime/agent_context.go`
- `runtime/agent/runtime` bootstrap/options
- `features/session/mongo` if durable support is added
- `docs/runtime.md`, `docs/dsl.md`, and repo-local skills

### P1. A2A Protocol Interop

ADK product shape:

- ADK has explicit Agent-to-Agent support for exposing and consuming agents.
- ADK Go supports A2A exposing flows and dynamically generated agent cards.
- ADK also documents consuming remote A2A agents as collaborators.

Current Loom evidence:

- Repo search outside `third_party` found no A2A package, agent card type,
  A2A launcher, or remote A2A agent adapter.
- Loom has strong MCP interop and agent-as-tool child workflows, but those do
  not provide A2A compatibility.

Why this matters:

- MCP covers tools/resources/prompts; A2A covers agent collaboration as a
  protocol surface.
- If external agent ecosystems standardize on A2A for remote agent delegation,
  Loom needs a bridge analogous to its MCP bridge.

Recommended next work:

1. Decide whether A2A is a near-term product goal or a tracked non-goal.
2. If yes, design the smallest bridge:
   - expose a Loom agent as an A2A agent card plus HTTP handler,
   - consume a remote A2A agent as a Loom model-facing tool or agent-as-tool,
   - preserve Loom run/session IDs in metadata.
3. Keep this separate from MCP codegen until the protocol ownership boundary is
   clear.

Primary implementation owners:

- New `runtime/a2a` or `runtime/agent/a2a` package
- Agent registration/runtime adapters
- Generated helpers only after the runtime contract is stable
- New docs alongside MCP integration docs

### P2. Evaluation And Environment Simulation

ADK product shape:

- ADK has an evaluation product surface for trajectory/tool-use evaluation,
  test files, eval sets, web UI/CLI execution, and custom metrics.
- ADK environment simulation can inject tool errors, fixed responses,
  conditional argument matches, latency, and probabilistic behavior through
  callback/plugin integration.

Current Loom evidence:

- Loom has extensive Go tests, fixtures, and generated acceptance tests.
- There is no productized eval runner, eval set file format, agent trajectory
  scoring API, or environment simulation package.
- Existing interceptors could support environment simulation, but no first-class
  simulation contract exists.

Why this matters:

- Tests validate framework behavior; evals validate agent behavior over time.
- Without a first-class eval/simulation layer, downstream products must invent
  ad hoc harnesses for tool fault injection, baseline comparison, and scoring.

Recommended next work:

1. Start with an interceptor-backed tool simulation package.
2. Add a minimal eval case format based on run input, expected tool trajectory,
   expected final output, and scoring hooks.
3. Provide a programmatic runner before adding CLI or UI surfaces.
4. Reuse existing fixtures rather than creating a separate fake framework.

Primary implementation owners:

- New `runtime/agent/eval` or `features/eval` package
- `runtime/agent/runtime/interceptors.go`
- Integration fixtures
- `docs/runtime.md` and `docs/testing` references

### P2. Live Bidirectional Multimodal Streaming

ADK product shape:

- ADK Gemini Live API Toolkit supports low-latency bidirectional voice/video
  interaction.
- It supports user interruptions, text/audio/video input, and text/audio output.
- ADK also documents streaming tools that can return intermediate results.

Current Loom evidence:

- Loom has runtime event streaming, pulse-style session streams, HTTP/gRPC
  streaming design support, and interrupt/pause/resume support.
- Current agent stream primitives are not a Live API session abstraction for
  bidirectional audio/video model interaction.
- Repo search shows no Live API, audio/video, or multimodal realtime runtime
  surface beyond transport/schema mentions.

Why this matters:

- Loom is fine for event streams and ordinary streaming model output.
- It is not yet a framework for voice/video agents with realtime interruption
  semantics.

Recommended next work:

1. Treat this as optional unless voice/video agents become a product target.
2. If pursued, design a separate live-session interface rather than stretching
   current event streams.
3. Reuse existing interrupt and stream concepts, but keep binary media transport
   explicit.

Primary implementation owners:

- `runtime/agent/stream`
- `runtime/agent/model`
- HTTP/gRPC transport docs only after runtime contract exists

### P2. Artifact Scoping And Versioning

ADK product shape:

- ADK artifacts are named, versioned binary data.
- Artifacts are scoped to a session or to a user across sessions.
- ADK has artifact service operations for saving, loading, listing filenames,
  and loading versions.

Current Loom evidence:

- `artifact.Store` persists by `AgentID` and `RunID`.
- `artifact.Ref` has generated `ID`, optional `Name`, `MimeType`, metadata, and
  `CreatedAt`.
- There is no filename-version contract or user/session-scoped artifact lookup.

Why this matters:

- Loom's current artifact model is good for run outputs and tool-produced
  payloads.
- It is weaker for durable user files, reusable generated assets, or
  cross-session artifact recall.

Recommended next work:

1. Keep current run artifacts unchanged.
2. Add named/versioned artifact APIs only if downstream use cases need
   cross-session file continuity.
3. If added, distinguish run artifacts from user/session artifacts in the DSL
   and docs.

Primary implementation owners:

- `runtime/agent/artifact`
- `runtime/agent/runtime/artifact_toolset.go`
- `docs/runtime.md`, `docs/dsl.md`

### P3. Agent Config, Web Runner, And Deployment UX

ADK product shape:

- ADK includes docs for Agent Config, command line execution, API server, web UI,
  deployment, and runtime config.

Current Loom evidence:

- Loom is design-first and Go-codegen-first.
- Loom has docs, quickstarts, a debug server, and generated services, but not an
  ADK-style generic web runner/API runner product.

Why this matters:

- This may be an intentional positioning difference.
- If Loom remains a framework/codegen system, this should be documented as a
  non-goal rather than treated as a missing core feature.

Recommended next work:

1. Document whether generic YAML/Agent Config is a non-goal.
2. If desired later, make it a thin input format that compiles into existing
   design/runtime contracts.
3. Avoid building this before memory/session state unless a concrete consuming
   product needs it.

## Suggested Implementation Order

1. Long-term memory service.
2. Persistent session state.
3. A2A protocol bridge.
4. Eval/environment simulation.
5. Artifact scoping/versioning if product requirements demand it.
6. Live multimodal streaming only when voice/video agents become a target.
7. Agent config/web runner only if a real consuming workflow needs it.

## FastMCP Comparison And Borrowable Patterns

Reviewed source:

- Docs: <https://gofastmcp.com/servers/server>
- Code: `PrefectHQ/fastmcp`, inspected from a shallow clone at
  `/tmp/fastmcp-review` on 2026-07-06.

FastMCP is not an ADK-equivalent agent runtime. It is an MCP server/client/app
framework, so it does not close Loom's ADK gaps for long-term memory, A2A,
agent evals, or multimodal live sessions. The useful implementation ideas are
mostly about MCP-facing runtime ergonomics: state, dynamic catalogs, transforms,
background tasks, and interactive UI affordances.

### FastMCP Pattern: Persistent Session State

FastMCP evidence:

- `FastMCP.__init__` accepts `session_state_store: AsyncKeyValue | None` and
  defaults to an in-memory store.
- It wraps that store in a typed `PydanticAdapter` under a `fastmcp_state`
  collection.
- `Context.set_state/get_state/delete_state` prefixes keys with
  `session_id`, persists JSON-serializable values with a TTL, and supports
  `serializable=False` request-scoped values for non-serializable objects.
- Tests cover same-session persistence, cross-session isolation, nested request
  behavior, request-scoped shadowing, and delete semantics.

Fit for Loom:

- This is a better fit than directly copying ADK's state model.
- Loom should adopt the shape, not the exact implementation:
  - `runtime.WithStateStore(...)` or `runtime.WithSessionStateStore(...)`,
  - session-prefixed keys by default,
  - request/run-scoped non-persistent values separate from durable state,
  - TTL support as store capability or runtime option,
  - explicit tests for isolation and shadowing.
- Loom should still keep ADK's conceptual scope split in mind: session, user,
  app, and temp/in-run. FastMCP only solves MCP session state cleanly.

Impact on backlog:

- Raises confidence in `P1. Persistent Session State`.
- The first Loom state implementation should be small and store-backed, not a
  broad session-store rewrite.

### FastMCP Pattern: Providers And Transforms

FastMCP evidence:

- `Provider` is a runtime catalog abstraction that can dynamically source tools,
  resources, resource templates, and prompts.
- Providers are queried in registration order, and transforms modify components
  as they flow through provider/list/get operations.
- Built-in transforms include namespace, visibility, resources-as-tools,
  prompts-as-tools, version filtering, tool transformation, and tool search.
- Search transforms replace `list_tools()` output with pinned tools plus
  synthetic `search_tools` and `call_tool`.

Fit for Loom:

- Loom already has design-owned toolsets and generated registrations; we should
  not replace that with runtime-first providers.
- The better idea is a generated/runtime catalog pipeline for MCP exposure:
  - keep static design ownership,
  - allow design-declared transforms such as namespace, visibility, version, or
    tool search,
  - preserve generated type/schema contracts,
  - make transforms pure and testable where possible.
- This maps especially well to recent progressive tool discovery work.

Impact on backlog:

- Does not change the ADK product priorities.
- Adds a useful implementation constraint for catalog-related work: transforms
  should be explicit pipeline stages rather than scattered list/call branches.

### FastMCP Pattern: Session Visibility

FastMCP evidence:

- Context exposes session-specific visibility operations.
- Visibility rules are stored in session state and applied after global
  transforms.
- Changes send list-change notifications only to the affected session.

Fit for Loom:

- This is valuable for large catalogs and progressive disclosure.
- Loom could support per-run or per-session component visibility over generated
  toolsets without changing the underlying designed service.
- It should remain policy-visible and deterministic: visibility changes should
  be hook/run events, not hidden server mutation.

Impact on backlog:

- Add as a subtheme under session state and MCP catalog ergonomics.
- It is lower priority than long-term memory, but it is a strong companion to
  progressive tool discovery.

### FastMCP Pattern: Protocol-Native Background Tasks

FastMCP evidence:

- Components opt into task execution with `task=True` / `TaskConfig`.
- `TaskConfig` supports `forbidden`, `optional`, and `required` modes.
- Task-enabled functions must be async.
- The server registers MCP task handlers such as `tasks/get`, `tasks/result`,
  `tasks/list`, and `tasks/cancel`.
- Task status maps backend execution states to MCP states such as `working`,
  `input_required`, `completed`, `failed`, and `cancelled`.

Fit for Loom:

- Loom already has durable runs, pause/resume, awaits, and Temporal-capable
  execution, so we should not add a separate task scheduler.
- The valuable idea is protocol projection: expose long-running Loom runs as
  MCP task-compatible handles where the MCP spec supports it.
- A Loom task bridge should map:
  - run ID to task ID,
  - run status to task status,
  - awaits/typed input/confirmation to `input_required`,
  - stream or hook events to progress notifications,
  - run output/artifacts to task result.

Impact on backlog:

- Add a new MCP protocol parity item when the MCP task extension becomes a
  target.
- Do not let this distract from ADK memory/session state; it is an MCP exposure
  layer over existing Loom runtime concepts.

### FastMCP Pattern: Interactive Apps

FastMCP evidence:

- `FastMCPApp` is a `Provider` that groups model-visible UI entry tools,
  app-only backend tools, and renderer resources.
- Backend tools default to app-only visibility unless explicitly exposed to the
  model.
- `Approval`, `Choice`, `FormInput`, `FileUpload`, and `GenerativeUI` are
  implemented as app providers.
- The approval provider is model-facing but relies on the LLM to stop and wait
  for a later user message.

Fit for Loom:

- Loom's runtime-owned confirmation is stronger than FastMCP's approval app for
  safety because execution actually pauses before the tool runs.
- The app grouping idea is still useful:
  - represent a user-facing MCP app as a grouped tool/resource bundle,
  - keep model-visible entry points distinct from app-only backend tools,
  - carry UI metadata through generated MCP surfaces,
  - use existing confirmation/typed-input runtime awaits for hard gates.

Impact on backlog:

- Does not supersede current tool confirmation.
- Adds an idea for future artifact/UI work: app bundles should be grouped at the
  provider/toolset level rather than as loose independent tools.

### FastMCP Pattern: Context Injection

FastMCP evidence:

- Tool/resource/prompt handlers receive a request `Context` through dependency
  injection or `get_context()`.
- The context exposes logging, progress, resource/prompt access, LLM sampling,
  elicitation, session state, session visibility, request IDs, client IDs, and
  transport metadata.

Fit for Loom:

- Loom already injects runtime data into generated service calls in narrower
  ways.
- The useful pattern is a single request capability object for MCP handlers,
  but in Loom it should be generated and typed rather than inferred from Python
  annotations.
- This could simplify service access to elicitation, progress, auth principal,
  session state, and runtime metadata.

Impact on backlog:

- Relevant to MCP ergonomics and session state implementation.
- Keep separate from planner context; service-handler context and agent planner
  context have different contracts.

### FastMCP Non-Fits

- FastMCP does not provide ADK-style long-term memory extraction or Memory Bank
  semantics. It only informs the storage/state mechanics.
- FastMCP does not solve A2A.
- FastMCP does not provide an ADK-like agent evaluation product. Its tests and
  server inspection tooling are useful, but not equivalent to eval sets.
- FastMCP's generative UI is interesting but should not be copied into Loom
  until there is a concrete MCP Apps target and host support story.

### Revised Takeaways

1. Keep long-term memory as the top ADK gap.
2. Implement persistent state with a FastMCP-style small store-backed contract:
   session-prefixed keys, request-scoped values, TTL, and isolation tests.
3. Treat MCP background tasks as a projection of Loom runs, not a new scheduler.
4. Treat providers/transforms as a catalog pipeline idea that fits generated MCP
   exposure, especially search and visibility.
5. Keep runtime-owned confirmation as the safety model, but borrow app grouping
   for future UI/tool bundles.

## Review Notes

- This review used ADK docs as the feature taxonomy first, then checked current
  Loom code and docs for matching contracts.
- The comparison intentionally does not treat every ADK product feature as a
  required Loom feature. Some gaps are potential non-goals.
- The most actionable next feature is long-term memory because Loom already has
  transcript storage, memory search, and generated memory tools, so the new
  service can extend existing concepts instead of creating a parallel system.
