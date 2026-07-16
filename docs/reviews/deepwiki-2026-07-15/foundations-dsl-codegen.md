# DeepWiki review: foundations, DSL, and code generation

Reviewed 2026-07-15 against repository and DeepWiki index commit `dd8379472f8341a694e65ce53935fe6cda12c2ad`.

Scope: DeepWiki pages 1 through 3.2 (ten pages), current `README.md`, `DESIGN.md`, `docs/`, generated fixtures, and `.agents/skills/loom-mcp`. DeepWiki was inspected page-by-page in the rendered site. Code is treated as authoritative when code, docs, skill, and DeepWiki disagree.

Classification legend:

- `DOC-GAP`: user-facing or generated project documentation is missing, stale, contradictory, or unsafe to copy.
- `SKILL-GAP`: `.agents/skills/loom-mcp` or its references are missing or stale.
- `ARCH-IMPROVEMENT`: the implementation contract or internal architecture should be strengthened.
- `DEEPWIKI-INACCURACY`: DeepWiki is incomplete or incorrect relative to this exact commit.
- `MATCH`: the claim and local implementation/documentation agree.

Priority: P0 blocker, P1 high, P2 medium, P3 low. Confidence is `high`, `medium`, or `low`.

## 1. Overview

URL: https://deepwiki.com/CaliLuke/loom-mcp/1-overview

Major claims checked: design-first `DSL -> codegen -> runtime` pipeline; agent, engine, planner, memory, stream, and MCP adapter roles; agent-as-tool child workflows; Temporal durability; generated MCP tools/resources/prompts.

Local coverage: `README.md:3-9`, `docs/overview.md:24-68`, `docs/runtime.md`, `DESIGN.md:13-31`, `.agents/skills/loom-mcp/SKILL.md:51-143`, and the runtime/codegen contract references cover the same mental model in greater detail.

Code evidence: `dsl/agent.go:56`, `dsl/mcp.go:40`, `codegen/agent/generate.go:56-97`, `codegen/mcp/generate.go:38-112`, `runtime/agent/runtime/runtime.go`, `runtime/agent/engine`, and `runtime/agent/planner` implement the layers DeepWiki names.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | The overall pipeline, swappable in-memory/Temporal engines, generated MCP adapter, and child-workflow composition are accurate and already well covered locally. |
| DEEPWIKI-INACCURACY | P2 | high | The component table says `memory.Store` “persists conversation transcripts and run logs.” Current contracts separate raw memory events, run logs, and durable long-term entries; `.agents/skills/loom-mcp/SKILL.md:97-103` explicitly warns not to conflate them. DeepWiki should describe these as separate stores/surfaces. |
| MATCH | P3 | high | “Design-driven MCP” accurately describes method-backed tools, resources, prompts, and generated JSON-RPC/SDK adapters. Local `docs/dsl.md` and `docs/mcp_sdk_server.md` are more complete than this overview. |

## 1.1. Getting Started & Quickstart

URL: https://deepwiki.com/CaliLuke/loom-mcp/1.1-getting-started-and-quickstart

Major claims checked: Go/Loom prerequisites; local/remote Loom modes; design as source of truth; `loom gen` versus `loom example`; in-memory quickstart; planner/model-client wiring; generated specs; common make targets.

Local coverage: `README.md:32-64`, `quickstart/README.md`, generated `quickstart/AGENTS_QUICKSTART.md`, `.agents/skills/loom-mcp/SKILL.md:9-29`, and the skill quickstart references.

Code evidence: `go.mod:3` requires Go 1.26.1; `scripts/loom_core_mode.sh:27-86` implements local/remote/status; `codegen/agent/generate.go:68-94` emits owner-scoped specs and `AGENTS_QUICKSTART.md`; `codegen/agent/generate_examples.go` owns example scaffolding.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | DeepWiki correctly treats Temporal as optional for the quickstart and the in-memory engine as the default. It also correctly distinguishes generated `gen/` output from application-owned `loom example` scaffolding. |
| DOC-GAP | P1 | high | `quickstart/README.md:7-10` says Go 1.24+ and lists Temporal as a prerequisite, while `go.mod:3`, `README.md:34-38`, and the same quickstart at `:89-93` require Go 1.26.1 and make Temporal optional. Align the runnable quickstart with the module requirement and remove the prerequisite contradiction. |
| SKILL-GAP | P1 | high | `.agents/skills/loom-mcp/references/user-guides/quickstart.md:1-40` is a generic Goa HTTP tutorial, says Go 1.18, omits `loom-mcp/dsl`, and even refers to the `goa` command. Replace it with the actual agent quickstart or route only to the newer split quickstart files; `.agents/skills/loom-mcp/references/user-guides/quickstart/installation.md` also still says Go 1.24+. |
| DOC-GAP | P1 | high | Generated `AGENTS_QUICKSTART.md` is unsafe to copy. The template at `codegen/agent/templates/agents_quickstart.go.tpl:264-315` points to obsolete `gen/<svc>/agents/<agent>/specs/<toolset>` packages, uses `<svc>.<toolset>.<tool>` IDs, and names nonexistent `ToMethodPayload_<Tool>` / `ToToolReturn_<Tool>` helpers. Current output uses owner-scoped `gen/<service>/toolsets/<toolset>`, IDs such as `projected.projected_lookup_tool` (`integration_tests/fixtures/assistant/gen/assistant/toolsets/projected/specs.go:15-24`), and `Init<Tool>MethodPayload` / `Init<Tool>ToolResult` (`.../transforms.go:12-37`). |
| DEEPWIKI-INACCURACY | P2 | high | DeepWiki repeats the older “per-agent specs” mental model. The agent does retain an aggregate under `gen/<service>/agents/<agent>/specs`, but defining toolset types/codecs/specs live once in owner-scoped packages (`codegen/agent/generate.go:68-73`; `codegen/agent/generate_agent_files.go:20-35`). |

## 1.2. Repository Structure & Conventions

URL: https://deepwiki.com/CaliLuke/loom-mcp/1.2-repository-structure-and-conventions

Major claims checked: directory ownership; no-hand-edit rule; CI generated-diff enforcement; Go/race/coverage conventions; local versus remote Loom source; subsystem package map.

Local coverage: `README.md:22-30`, `AGENTS.md`, `Makefile:39-62`, `.github/workflows/ci.yml:117-126`, `TEST_AUDIT.md`, and `.agents/skills/loom-mcp/references/repo-map.md`.

Code evidence: directories `design/`, `dsl/`, `expr/`, `codegen/`, `runtime/`, `registry/`, `features/`, and `integration_tests/` exist; `Makefile:40` runs the race detector; `Makefile:9,43-55` enforces 62% coverage; CI regenerates all tracked fixtures before `git diff --exit-code`.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | Directory responsibilities, design-first ownership, race-enabled tests, 62% coverage gate, golden codegen tests, and generated-fixture diff enforcement are accurate. |
| DOC-GAP | P2 | high | DeepWiki usefully includes `features/`, but the primary repository map in `README.md:22-30` omits it even though it contains model, Mongo, policy, prompt, session, runlog, and Pulse adapters. Add it to “What lives here”; `.agents/skills/loom-mcp/references/repo-map.md` also omits it. |
| DEEPWIKI-INACCURACY | P3 | high | Its final architecture diagram shortens owners to nonexistent `runtime/runtime.go` and `runtime/planner.go`. Current owners are under `runtime/agent/runtime/` and `runtime/agent/planner/`; local repo-map routing is correct. |
| MATCH | P3 | high | `make loom-local`, `make loom-remote`, and `make loom-status` accurately reflect `scripts/loom_core_mode.sh:27-86`; remote mode pins `v1.6.2`. |

## 2. DSL & Design Layer

URL: https://deepwiki.com/CaliLuke/loom-mcp/2-dsl-and-design-layer

Major claims checked: Goa/Loom eval roots; agent and MCP expression trees; agent/toolset/MCP declarations; policies and workflow; progressive discovery; validation and generation flow.

Local coverage: `docs/dsl.md` is comprehensive; `DESIGN.md:19-31`; `.agents/skills/loom-mcp/SKILL.md:51-147`; codegen/runtime contract references. The skill’s long-form DSL reference is materially older than `docs/dsl.md`.

Code evidence: `expr/agent/root.go:15-98` and `expr/mcp/root.go:15-87` register eval roots; `dsl/agent.go`, `dsl/toolset.go`, `dsl/tool.go`, `dsl/policy.go`, `dsl/workflow.go`, and `dsl/mcp.go` populate them; validation runs in expression `Validate` methods before generation.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | The expression-tree/eval model, reuse of Loom types/validations, and DSL-to-generated-registration flow are accurate. |
| DOC-GAP | P3 | high | `DESIGN.md:19` still points contributors to `expr/mcp.go`; the current package is `expr/mcp/`. DeepWiki’s package-level description is more accurate here. |
| DEEPWIKI-INACCURACY | P2 | high | Its high-level provider summary names only methods, external MCP servers, and agents. The current surface also includes registry, skills, artifacts, and memory providers; `FromMemory`, `MemoryTranscript`, `MemoryIndexedTranscript`, and `MemoryLongTerm` are implemented at `dsl/toolset.go:225-268` and documented at `docs/dsl.md:776-822`. |
| DEEPWIKI-INACCURACY | P1 | high | The policy summary calls `TimeBudget` a wall-clock limit. Runtime deadlines are extended by clarification, confirmation, typed-input, and external-tool waits (`runtime/agent/runtime/workflow_loop.go:101-117`, `workflow_await_queue.go:205-208,285-291`, `workflow_clarification.go:146-154`), so the budget measures active planner/tool work rather than elapsed wall time. |
| SKILL-GAP | P1 | high | `.agents/skills/loom-mcp/references/user-guides/dsl-reference.md:11-99` lists removed/legacy names (`Compress`, `MCPServer`, `MCPTool`, `MCPToolset`), omits workflows, memory/artifact/skill providers, MCP prompts/icons/search/OAuth, and incorrectly says MCP prompt management lacks a dedicated DSL. Use `docs/dsl.md` as the source and regenerate this reference. |

## 2.1. Agent & Toolset DSL

URL: https://deepwiki.com/CaliLuke/loom-mcp/2.1-agent-and-toolset-dsl

Major claims checked: `Agent`, `Use`, `Export`, `AgentToolset`; local/MCP/registry/skills/artifacts providers; `Tool` contexts; `BindTo`, `Inject`, `Expose`, `MCPPlacement`; policy; root preparation, cycle/uniqueness/projection validation.

Local coverage: `docs/dsl.md:154-211, 588-942`, `docs/runtime.md:597-842`, `.agents/skills/loom-mcp/SKILL.md:61-80, 93-107`, and the toolsets/codegen contract references.

Code evidence: `dsl/agent.go:56-154`, `dsl/toolset.go:61-449`, `dsl/tool.go:82-202, 570-617`; `expr/agent/root.go:72-98, 184-233, 426-468`; `expr/agent/tool.go:384-468` implements default runtime exposure and narrow MCP projection validation.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | Agent/toolset composition, provider-backed toolsets, method/toolset `Tool` contexts, `BindTo`, injected-field hiding, and root-level uniqueness/cycle checks are correctly described. |
| DEEPWIKI-INACCURACY | P2 | high | The provider table omits `FromMemory(...)` and agent-exported toolset references. It also depicts `FromMCP` as a runtime handshake transport without clarifying that it references a generated service MCP suite and is wired through generated/runtime callers. |
| DEEPWIKI-INACCURACY | P2 | high | The projection section omits the v1 contract: MCP projection must also include `AgentRuntime`, must be method-backed, must use a same-service placement, and rejects `Confirmation`, `Inject`, `ServerData`, `ResultReminder`, and `BoundedResult` (`expr/agent/tool.go:399-468`; `docs/dsl.md:911-942`). |
| DEEPWIKI-INACCURACY | P1 | high | The policy table again calls `TimeBudget` a hard wall-clock timeout and advertises the removed `Compress()` helper. Current timing pauses during human/external waits, and compression is configured with `CompressAtTurns` and/or `CompressAtMaxInputTokens` plus retention caps (`dsl/history.go:145-211`). |
| SKILL-GAP | P1 | high | `.agents/skills/loom-mcp/references/user-guides/toolsets.md:64-88` gives a `BoundedResult` example that authors canonical `returned`, `total`, `truncated`, and `refinement_hint` fields in the tool `Return`; current validation rejects those fields because bounds are runtime-owned (`docs/dsl.md:234-271`). |
| SKILL-GAP | P1 | high | The same skill guide advertises `New<Agent><Toolset>ToolsetRegistration(exec)` at `:125-131`, but current method-backed wiring is generated `RegisterUsedToolsets(..., With<Toolset>Executor(...))` (`integration_tests/fixtures/assistant/gen/assistant/agents/assistant_runtime/registry.go:80-105`) plus owner dispatchers. |

## 2.2. MCP Service DSL

URL: https://deepwiki.com/CaliLuke/loom-mcp/2.2-mcp-service-dsl

Major claims checked: `MCP(name, version, opts...)`; protocol and implementation metadata; method-level and projected tools; resources, subscriptions, skills, static/dynamic prompts; compact discovery; OAuth; validation; generated adapter/server flow.

Local coverage: `docs/dsl.md:346-406, 1471-1650`, `docs/mcp_sdk_server.md`, `DESIGN.md:81-164`, `.agents/skills/loom-mcp/SKILL.md:64-94, 105-143`, and the MCP codegen contract reference.

Code evidence: `dsl/mcp.go:40-550, 591-681`; `expr/mcp/root.go:53-129`; `expr/mcp/mcp.go`; `codegen/mcp/generate.go`; generated `integration_tests/fixtures/assistant/gen/mcp_assistant/{adapter_server.go,sdk_server.go,local_provider.go}`.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | Server identity/version, method tools, projected tools, resource/prompt surfaces, compact `search_tools`/`call_tool`, prompt-name collisions, duplicate MCP validation, and OAuth authorization-server validation are accurate. |
| DEEPWIKI-INACCURACY | P2 | high | Its final flow says the generated server handles “JSON-RPC over SSE/Stdio.” Generated server surfaces are native JSON-RPC HTTP/SSE and official-SDK Streamable HTTP. Stdio exists only as a runtime caller for consuming external servers (`DESIGN.md:126-139`), not as a generated server transport. |
| DEEPWIKI-INACCURACY | P3 | high | `SkillDirectory` exposes more than only `SKILL.md`: each skill includes `SKILL.md`, `_manifest`, and manifest-referenced supporting files (`dsl/mcp.go:550-557`; skill/runtime docs). |
| DEEPWIKI-INACCURACY | P2 | high | The page is materially incomplete for current MCP design metadata: tool/resource/prompt icons and titles, discovery categories/tags/keywords/call-template args, runtime prompt projection, notifications/subscriptions, completion, `TrustProxyHeaders`, resource documentation, and client features are all implemented and locally documented. |
| DEEPWIKI-INACCURACY | P1 | high | Its toolset projection example says `Expose(MCPSurface)`. That design is rejected: v1 projection requires `Expose(AgentRuntime, MCPSurface)`, a method-backed tool, and same-service `MCPPlacement(...)` (`expr/agent/tool.go:399-468`). This is unsafe copy guidance, not merely omitted detail. |
| SKILL-GAP | P1 | high | The long-form DSL skill reference’s statement that prompts are runtime-only conflicts with `StaticPrompt`, `DynamicPrompt`, `PromptIcons`, and `RuntimePrompt` in `dsl/mcp.go:414-681` and `docs/dsl.md`. The main `SKILL.md` is correct; the routed reference is not. |

## 2.3. Execution Policy & Workflow DSL

URL: https://deepwiki.com/CaliLuke/loom-mcp/2.3-execution-policy-and-workflow-dsl

Major claims checked: caps and timing; history; confirmation; idempotency; server data and bounded results; sequential and graph workflows; parallel/join/input/loop/branch; runtime enforcement.

Local coverage: `docs/dsl.md:213-345, 1300-1468`, `docs/runtime.md:439-466, 1210-1290`, `.agents/skills/loom-mcp/SKILL.md:80-106`, and `.agents/skills/loom-mcp/references/runtime-contracts.md:109-129`.

Code evidence: `dsl/policy.go`, `dsl/timing.go`, `dsl/history.go`, `dsl/confirmation.go`, `dsl/idempotency.go`, `dsl/workflow.go`; `expr/agent/workflow.go`; `runtime/agent/planner/workflow_graph.go`; `runtime/agent/runtime/workflow_await_queue.go`.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | Caps, confirmation, deterministic sequential/graph planners, stable node IDs, and bounded-loop validation match code and local docs. |
| DEEPWIKI-INACCURACY | P1 | high | The page calls `TimeBudget` a wall-clock limit. The runtime explicitly extends both budget and hard deadlines by time spent awaiting clarification, confirmation, typed input, and external tools (`runtime/agent/runtime/workflow_loop.go:101-117` and wait call sites), so elapsed wall time may exceed the configured budget. |
| DOC-GAP | P1 | high | This DeepWiki error originates in local sources: `docs/dsl.md:323,340,1408`, `dsl/timing.go:15-42`, `runtime/agent/api/types.go:135-136`, and `runtime/agent/runtime/types.go:124-125` describe a wall-clock budget even though the execution code pauses it. Align all public docs and GoDoc around the active-work contract and distinguish the engine hard deadline. |
| DEEPWIKI-INACCURACY | P1 | high | `ServerData` is not server-injected input. It is typed server-only output/sidecar data, optionally sourced from a bound method result (`dsl/tool.go:365-421`). `Inject` is the hidden input mechanism (`dsl/tool.go:598-617`). Conflating them leads users to model the wrong direction of data flow. |
| DEEPWIKI-INACCURACY | P1 | high | The page says `Idempotent()` lets the runtime return a cached identical result. It currently only appends the `loom-mcp.idempotency=transcript` tag (`dsl/idempotency.go:23-29`); no built-in execution path consumes the parsed metadata (`runtime/agent/tools/idempotency.go:34-60`) to reuse or suppress calls. |
| ARCH-IMPROVEMENT | P2 | medium | Decide whether `Idempotent()` remains planner/orchestrator metadata or gains built-in transcript-local, success-only duplicate reuse with canonical argument comparison. The architecture is valid either way, but the public contract must choose one and test it end-to-end. |
| DEEPWIKI-INACCURACY | P2 | high | `Compress()` does not exist, and `BoundedResult()` does not require cursor fields. Compression uses trigger-specific helpers; bounded results always project `returned`/`truncated`, while cursor and next-cursor fields are optional and declared only for paging (`docs/dsl.md:234-285`; `dsl/history.go:145-211`). |
| DEEPWIKI-INACCURACY | P2 | high | The graph-mode explanation names only `Parallel`, `Join`, and `Branch`. `RequestInput` and `Loop` also set `WorkflowExpr.GraphMode` directly (`dsl/workflow.go:141-188`), so all five graph helpers switch generation to `planner.NewGraphWorkflowPlanner(...)`. |
| SKILL-GAP | P1 | high | `.agents/skills/loom-mcp/references/user-guides/dsl-reference.md:35,48,53-55` says “cross-transcript” de-duplication, describes `TimeBudget` as wall-clock, and exposes nonexistent `Compress()`. Current contracts are transcript-scoped metadata, an active-work budget, and `CompressAtTurns` / `CompressAtMaxInputTokens` plus retention caps. |
| DOC-GAP | P2 | high | Source comments consumed by code-derived documentation are stale: `dsl/confirmation.go:10-16` describes an `AwaitExternalTools`/ask-question protocol, `dsl/doc.go:140` calls `Idempotent` an MCP retry marker, and `dsl/timing.go:15-42` says wall-clock. Runtime behavior uses `AwaitConfirmation` / `ProvideConfirmation` / `ToolAuthorization`, transcript metadata, and paused active-work deadlines. |

> Resolved 2026-07-16 (`M7`, `M9`): public generator, confirmation,
> idempotency, timing, and policy comments now match generated ownership,
> `AwaitConfirmation` / `ProvideConfirmation` / `ToolAuthorization`, and the
> paused active-work budget. `Idempotent()` is explicitly metadata-only; the
> built-in runtime provides no argument comparison, cached reuse, suppression,
> or exactly-once delivery.

## 3. Code Generation

URL: https://deepwiki.com/CaliLuke/loom-mcp/3-code-generation

Major claims checked: agent and MCP generation entry points; DSL root transformation; generated agents/specs/registrations; MCP adapter/SDK/native transport; tool spec builder; generation orchestration.

Local coverage: `DESIGN.md:19-47, 166-183`, `docs/dsl.md:1-20, 1760-1790`, `docs/mcp_sdk_server.md`, `.agents/skills/loom-mcp/SKILL.md:9-29, 143-147`, and `.agents/skills/loom-mcp/references/codegen-contracts.md`.

Code evidence: `codegen/agent/generate.go:56-97`; `codegen/agent/generate_agent_files.go:13-59`; `codegen/agent/specs_builder_*`; `codegen/mcp/generate.go:38-112`; generated fixture output under `integration_tests/fixtures/assistant/gen`.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | The two generator entry points, evaluated-root data transformation, JSON Schema/codecs, typed MCP adapter, official SDK server, and native JSON-RPC generation are accurate. |
| DEEPWIKI-INACCURACY | P1 | high | The agent artifact list is old: current generation does not emit standalone `workflow.go` or `activities.go`, and per-toolset specs are owner-scoped rather than under each agent. Current agent files are `agent.go`, `config.go`, `registry.go`, aggregate specs/catalog, consumer/export helpers, and executor helpers (`codegen/agent/generate_agent_files.go:13-59`; generated fixture). |
| DEEPWIKI-INACCURACY | P2 | high | The tool-spec section calls the emitted contracts “OpenAPI schemas.” Agent specs use JSON Schema draft 2020-12 (see the `$schema` values in generated owner-scoped `specs.go`); OpenAPI is a separate transport/documentation artifact and should not be presented as the model tool-schema format. |
| ARCH-IMPROVEMENT | P1 | high | **Resolved 2026-07-16:** policy/session/cancellation mounts, batch dispatch, endpoint initialization, empty results, error mapping/data, SSE streams, client initialization/defaults, and example scaffolding now use loom-mcp-owned sections built from evaluated generator data. Missing or duplicate upstream files/sections fail generation, and production MCP codegen contains no rendered-source rewrite path. |
| DOC-GAP | P1 | high | `DESIGN.md:16-31` repeats the obsolete architecture that generation emits agent workflows/activities, places tool specs below each agent, and applies only “small” transformations. It should describe owner-scoped toolset packages, current agent registration/config files, and the now-substantial MCP rewrite layer; these stale claims directly fed DeepWiki. |
| SKILL-GAP | P2 | high | `.agents/skills/loom-mcp/references/user-guides/code-generation.md` and `codegen/generated-layout.md` are largely generic Goa guides; they omit owner-scoped toolsets, generated agent registrations, MCP packages, adapter/SDK/local registration, generated quickstart, and repo verification flow. The main codegen contract is good but routing can still land an agent in the generic references. |

## 3.1. Agent Code Generation

URL: https://deepwiki.com/CaliLuke/loom-mcp/3.1-agent-code-generation

Major claims checked: generator data; owner/tool spec lifecycle; public versus transport types; injected-field projection; method transforms; generated symbols/layout; unions; codegen compilation/golden tests.

Local coverage: `docs/overview.md:81-147`, `docs/dsl.md:1760-1790`, `docs/runtime.md:597-681`, `.agents/skills/loom-mcp/references/codegen-contracts.md:14-47, 137-176`, and the toolset skill guide.

Code evidence: `codegen/agent/data.go`, `specs_builder_materialize.go`, `specs_builder_type_info.go`, `tool_inject_render.go`, `generate_toolset_transforms.go`; generated `integration_tests/fixtures/assistant/gen/assistant/toolsets/projected`; compile matrix in `codegen/agent/mcp_executor_compile_test.go`.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | DeepWiki accurately identifies public/transport type separation, injected-field hiding, generated `Init<Tool>MethodPayload` / `Init<Tool>ToolResult` transforms, typed codecs/specs, union envelopes, and compile/golden verification. |
| DEEPWIKI-INACCURACY | P1 | high | Its generated-artifact table again lists nonexistent standalone `workflow.go` and locates tool `types.go`/`codecs.go` under per-agent specs. The fixture shows `agent.go`, `config.go`, `registry.go`, agent aggregate `specs/`, and owner package `gen/assistant/toolsets/projected/{types,codecs,specs,transforms,provider}.go`. |
| DOC-GAP | P1 | high | The generated quickstart contradicts the accurate DeepWiki transform names and current generated output. Fixing `codegen/agent/templates/agents_quickstart.go.tpl:264-315` should be paired with a test that parses or compiles every code block whose imports/symbols are meant to be runnable. |
| DOC-GAP | P2 | high | `docs/overview.md:117-144` says canonical IDs are `<service>.<toolset>.<tool>`, while the authoritative runtime contract and generated constants use `<toolset>.<tool>` (`runtime/agent/tools/ident.go`; generated `specs.go:15-18`). Correct the example and audit the generated quickstart template for the same prefix error. |
| ARCH-IMPROVEMENT | P2 | high | `codegen/agent/generate.go:15-52` has stale GoDoc enumerating `workflow.go`, `activities.go`, agent-local toolset specs, and a future `service_toolset.go`; DeepWiki propagated that comment into user-visible misinformation. Keep generator GoDoc derived from, or tested against, actual file emission. |

## 3.2. MCP Code Generation

URL: https://deepwiki.com/CaliLuke/loom-mcp/3.2-mcp-code-generation

Major claims checked: source snapshot and projected inventory; synthetic MCP expression tree; adapter data and file emission; strict decoding/recovery; sessions/resource policy; SDK context and skills; JSON-RPC normalization/batches; compact discovery; OAuth; local provider.

Local coverage: `docs/mcp_sdk_server.md` covers these contracts in detail; `docs/dsl.md:1471-1650`; `docs/runtime.md:2310-2390`; `DESIGN.md:81-164`; `.agents/skills/loom-mcp/SKILL.md:64-94, 105-143`; codegen contract reference.

Code evidence: `codegen/mcp/generate.go:38-220`; `adapter_core_jennifer.go`; `adapter_tools_jennifer.go`; `sdk_server_file.go`; `local_provider_file.go`; generated assistant MCP fixture.

| Class | Priority | Confidence | Finding |
| --- | --- | --- | --- |
| MATCH | P3 | high | Source snapshotting, projected-tool inventory, synthetic Loom service generation, adapter construction, input recovery, strict JSON decoding, bounded sessions/principals, resource allow/deny policy, SDK Streamable HTTP, optional/null argument normalization, batch isolation, compact discovery, and OAuth challenge generation are accurately identified. |
| DEEPWIKI-INACCURACY | P2 | high | “Local provider” is not a convenience constructor that instantiates service, adapter, and server. `New<Service><MCP>LocalToolsetRegistration(adapter)` requires an already-built adapter and returns an in-process progressive-discovery `runtime.ToolsetRegistration` without protocol initialization or transport (`codegen/mcp/local_provider_file.go:18-47`; generated `local_provider.go:28-52`). Local docs describe this correctly. |
| DEEPWIKI-INACCURACY | P2 | medium | The OAuth section cites RFC 9470/9728 together. The implementation specifically emits RFC 9728 Protected Resource Metadata and RFC 6750 `WWW-Authenticate` behavior (`dsl/mcp.go:250-321`; `docs/dsl.md:401-406`). Remove the unsupported RFC 9470 attribution unless a concrete generated contract requires it. |
| DEEPWIKI-INACCURACY | P1 | high | The OAuth paragraph says the adapter is configured to issue challenges when unauthorized requests are detected. The DSL generates metadata, audience, and challenge-formatting helpers, but it does not install authentication or authorization: applications must wrap the handler with a token verifier and `runtime/mcp.WithOAuthChallenge`, and advertised `OAuthScope(...)` values are not operation-level scope enforcement (`docs/dsl.md:471-488`; generated `oauth_discovery.go`). |
| MATCH | P3 | high | The page’s SDK context merging, skill exposure, compact search weights, input recovery, and resource-policy observations agree with generated `adapter_server.go`/`sdk_server.go` and `docs/mcp_sdk_server.md`. |
| ARCH-IMPROVEMENT | P1 | high | **Resolved 2026-07-16:** mount/session/cancellation, handlers, endpoint initialization, SSE streams, and client construction use structured section ownership with exact cardinality. The optional-params and final-event compatibility rewrites were removed because pinned Loom already owns those contracts. |

## Prioritized recommendations

1. ~~**P1 — Repair and verify generated `AGENTS_QUICKSTART.md`.**~~ Complete 2026-07-16: owner-scoped imports, `<toolset>.<tool>` IDs, `Init...` transforms, executor wiring, and `Toolset(FromMCP(...))` caller guidance are covered by generator assertions.
2. ~~**P1 — Refresh the loom-mcp skill’s routed user guides.**~~ Complete 2026-07-16: routed guides are current contracts or thin links to canonical product docs; stale quickstart subpages no longer publish generic generated APIs.
3. ~~**P1 — Correct the `TimeBudget` contract everywhere.**~~ Complete 2026-07-16: `TimeBudget` and `Budget` are documented as active-work budgets with paused external waits, distinct from the engine hard deadline and caller-owned wall-clock SLAs.
4. **P1 — Continue reducing MCP source-rewrite coupling.** Mount/session/cancellation and client construction now use structured section ownership. Migrate the remaining named handler/decoder/SSE compatibility rewrites incrementally, adding exact section/cardinality drift checks as each one moves.
5. ~~**P1 — Refresh `DESIGN.md` and generated-layout guidance.**~~ Complete 2026-07-16: current docs describe owner-scoped toolsets, agent registration files, and explicit MCP compatibility ownership.
6. ~~**P2 — Decide the `Idempotent()` product contract.**~~ Complete 2026-07-16: it remains planner/orchestrator metadata and does not install runtime replay suppression.
7. ~~**P2 — Align user-facing quickstarts and overview facts.**~~ Complete 2026-07-16: Go 1.26.1, optional Temporal, canonical IDs, `features/`, and generated ownership are aligned.
8. ~~**P2 — Correct source comments consumed by code-derived documentation.**~~ Complete 2026-07-16: generator, DSL, timing, MCP provider, and canonical tool-ID comments now match emitted/runtime contracts.
9. **P2 — Feed DeepWiki corrections upstream.** Correct ServerData direction, active-work budgets, optional bounded-result cursors, graph-mode triggers, generated server transports, local provider purpose, memory-store separation, JSON Schema terminology, owner-scoped generation layout, projection restrictions, OAuth RFC attribution, and the application-owned OAuth enforcement boundary.

## Page coverage checklist

- [x] https://deepwiki.com/CaliLuke/loom-mcp/1-overview
- [x] https://deepwiki.com/CaliLuke/loom-mcp/1.1-getting-started-and-quickstart
- [x] https://deepwiki.com/CaliLuke/loom-mcp/1.2-repository-structure-and-conventions
- [x] https://deepwiki.com/CaliLuke/loom-mcp/2-dsl-and-design-layer
- [x] https://deepwiki.com/CaliLuke/loom-mcp/2.1-agent-and-toolset-dsl
- [x] https://deepwiki.com/CaliLuke/loom-mcp/2.2-mcp-service-dsl
- [x] https://deepwiki.com/CaliLuke/loom-mcp/2.3-execution-policy-and-workflow-dsl
- [x] https://deepwiki.com/CaliLuke/loom-mcp/3-code-generation
- [x] https://deepwiki.com/CaliLuke/loom-mcp/3.1-agent-code-generation
- [x] https://deepwiki.com/CaliLuke/loom-mcp/3.2-mcp-code-generation
