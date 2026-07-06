# ADK Feature Inheritance Plan

Loom MCP should adopt ADK-style framework capabilities as native Loom features, not by depending on ADK. The chosen direction is to add Loom-owned DSL declarations, expression validation, generated registration code, runtime contracts, and docs for artifacts, memory tools, interceptors, workflow graphs, typed human input, skill metadata, and a local debug server.

## Status

- 2026-07-06 — Plan created for review.
- 2026-07-06 — Fresh-agent review found contract blockers; plan rewritten around current runtime, planner, memory, hook, skill, and workflow owners.

## Current Contracts To Change

- `runtime/agent/planner/planner.go` currently has no `ToolResult.Artifacts`, `ToolOutput.Artifacts`, graph workflow state, or typed-input await kind. Existing await kinds are `clarification`, `questions`, and `external_tools`.
- `runtime/agent/api/types.go` currently carries workflow-safe tool output data through `ToolOutput`, `ProvidedToolResult`, and await signals. New artifact and typed-input fields must stay workflow-boundary safe.
- `runtime/agent/hooks/events.go` and `runtime/agent/hooks/codec.go` currently publish tool results without artifact references and await events without typed input. Any new hook payload must round-trip through codecs.
- `runtime/agent/runtime/hook_activity.go` appends canonical run events before stream and hook-bus publication. Event interceptors that drop events must run before `appendHookRunEvent`, and dropped events must be absent from runlog, stream, and hook bus.
- `runtime/agent/memory/memory.go` currently exposes `Store.LoadRun`, `Store.AppendEvents`, and `Reader` methods only. Search, label query, and preload semantics are new runtime contracts.
- `runtime/agent/runtime/agent_context.go` owns `PlannerContext.ModelClient(id)` decoration. Model interceptor tests and implementation belong in runtime, not the planner package.
- `runtime/agent/planner/composition.go` currently advances sequential workflows by `len(input.ToolOutputs)`. Graph workflows need node-state keyed by step ID and cannot derive resume state from output count.
- `runtime/mcp/skills/skills.go` currently parses only a description-like frontmatter field and silently skips duplicate skill directory names. Skill metadata work must migrate that behavior deliberately.
- `runtime/agent/runtime/skill_toolset.go` owns model-facing skill tools created by `Toolset(FromSkills(...))`; `runtime/mcp/skills` owns `skill://` resource discovery and reading.

## Milestones

### Milestone 1: Artifact Store And Model-Facing Artifact Tools

Toc: Artifacts

Goal: Persist run artifacts as first-class runtime data and expose bounded artifact access through generated model-facing tools.

Acceptance Criteria

- New tests in `runtime/agent/artifact` prove `Store.Save`, `Store.List`, `Store.Load`, metadata filtering, missing artifact errors, max-byte reads, and isolation by `agent_id` plus `run_id`.
- New tests in `runtime/agent/runtime` prove `planner.ToolResult.Artifacts` becomes workflow-safe `planner.ToolOutput.Artifacts`, persists through a configured artifact store, and appears in hook and runlog payloads as references only.
- New DSL and codegen tests prove `Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))` generates `artifacts.list_artifacts` and `artifacts.load_artifact` registrations after `make regen-assistant-fixture`.

Checklist

- [ ] Read this plan end-to-end, read the linked skill at `.agents/skills/loom-mcp/SKILL.md`, then recite the workflow you will follow — milestone order, exit criteria, named commands in execution order, test-first rule, peer-review gate, commit/push handoff, and any inherited repo constraints. Do not edit code before this recital is on the record.
- [ ] Add compile-failing contract tests in `runtime/agent/planner` for new `ArtifactRef` fields on `ToolResult` and workflow-safe `ToolOutput`.
- [ ] Add failing hook codec tests in `runtime/agent/hooks` for artifact references on `ToolResultReceivedEvent` payload encode and decode.
- [ ] Add failing API boundary tests in `runtime/agent/api` for artifact references on `ToolOutput` and tool-result workflow payloads.
- [ ] Add failing store conformance tests in new package `runtime/agent/artifact` for `Save`, `List`, `Load`, metadata filtering, missing IDs, max-byte reads, and isolation by `agent_id` plus `run_id`.
- [ ] Add failing runtime tests in `runtime/agent/runtime` proving artifact references from `planner.ToolResult.Artifacts` are persisted through `WithArtifactStore` and never inject full artifact bodies into planner messages.
- [ ] Add failing runtime tests in `runtime/agent/runtime` proving `artifacts.list_artifacts` accepts `{\"mime_type\":\"text/plain\",\"limit\":50}` and returns references, while `artifacts.load_artifact` accepts `{\"id\":\"...\",\"max_bytes\":65536}` and returns `{content, mime_type, truncated, size_bytes}`.
- [ ] Add failing DSL tests in `dsl` and expression tests in `expr/agent` for `FromArtifacts`, `MaxArtifactBytes`, `MaxArtifacts`, empty toolset names, duplicate tool IDs, and negative limits.
- [ ] Add failing golden tests in `codegen/agent/tests` proving generated agent registration wires the artifact toolset without hand-written `gen/` edits.
- [ ] Implement `runtime/agent/artifact` interfaces, in-memory store, `ArtifactRef`, `ArtifactContent`, and max-byte loading behavior.
- [ ] Add artifact references to `planner.ToolResult`, `planner.ToolOutput`, workflow-safe API DTOs, hook event payloads, memory projection, and transcript replay paths.
- [ ] Wire artifact persistence into `runtime/agent/runtime` at tool-result materialization and add `WithArtifactStore`.
- [ ] Implement runtime artifact tool registration helpers consumed by generated agent code.
- [ ] Add DSL, expr, and codegen support for `FromArtifacts` and artifact tool options.
- [ ] Update `docs/dsl.md`, `docs/runtime.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with artifact-store and artifact-tool contracts.
- [ ] Run `go test ./runtime/agent/planner ./runtime/agent/api ./runtime/agent/hooks ./runtime/agent/artifact ./runtime/agent/runtime ./dsl ./expr/agent ./codegen/agent/tests -run 'Artifact|Artifacts' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make lint && make test`.

### Milestone 2: Explicit Memory Tools And Optional Preload

Toc: Memory

Goal: Give agents explicit current-run and indexed-memory access tools plus bounded opt-in preload without changing default transcript availability.

Acceptance Criteria

- New tests in `runtime/agent/memory` prove optional `Searcher` query semantics for agent, run, session, labels, event types, limit, and stable chronological ordering.
- New tests in `runtime/agent/runtime` prove `memory.load_memory` uses `Searcher` when configured, falls back to current-run `Reader` for `scope=current_run`, and returns `memory_search_unavailable` when indexed search is requested without a searcher.
- New DSL and codegen tests prove `Toolset("memory", FromMemory(MemoryMaxResults(20)))` and `PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))` are design-owned, generated, and compatible with existing `WithMemoryStore`.

Checklist

- [ ] Add failing contract tests in `runtime/agent/memory` for `Searcher.Query(ctx, Query)` with `AgentID`, `RunID`, `SessionID`, `Labels`, `Types`, `Limit`, and chronological result ordering.
- [ ] Add failing in-memory tests in `runtime/agent/memory/inmem` proving appended events can be queried by the new `Searcher` contract.
- [ ] Add failing runtime tests in `runtime/agent/runtime` for `memory.load_memory` payload `{scope, event_types, labels, limit}` and result `{events, truncated, scope}`.
- [ ] Add failing runtime tests proving indexed search without `WithMemorySearcher` returns a structured tool error with retry hint reason `unsupported_operation`.
- [ ] Add failing runtime tests proving preload memory is injected into planner input only when generated run policy enables `PreloadMemory`.
- [ ] Add failing DSL tests in `dsl` and expression tests in `expr/agent` for `FromMemory`, `MemoryMaxResults`, `PreloadMemory`, `MemoryScopeCurrentRun`, duplicate generated tool IDs, and negative limits.
- [ ] Add failing golden tests in `codegen/agent/tests` proving generated registration wires memory tools and run-policy preload conversion.
- [ ] Implement `memory.Query`, `memory.QueryResult`, `memory.Searcher`, and in-memory query support without replacing the existing `Store` interface.
- [ ] Implement runtime memory tool helpers in `runtime/agent/runtime` with generated tool ID `memory.load_memory`.
- [ ] Implement preload policy in planner input construction with bounded snippets and stable provenance.
- [ ] Add DSL, expr, and codegen support for memory tools and preload policy.
- [ ] Update `docs/dsl.md`, `docs/runtime.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with explicit memory-tool, searcher, and preload contracts.
- [ ] Run `go test ./runtime/agent/memory ./runtime/agent/memory/inmem ./runtime/agent/runtime ./dsl ./expr/agent ./codegen/agent/tests -run 'MemoryTool|PreloadMemory|FromMemory|Searcher' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make lint && make test`.

### Milestone 3: Run, Model, Tool, And Event Interceptors

Toc: Interceptors

Goal: Expand the current tool interceptor path into ordered run, model, tool, and event interception points with explicit mutation and short-circuit rules.

Acceptance Criteria

- New tests in `runtime/agent/runtime` prove interceptor ordering, mutation propagation, error short-circuiting, panic-free nil handling, and hook non-duplication across run, model, tool, and event paths.
- New tests in `runtime/agent/runtime` prove model interceptors wrap clients returned by `PlannerContext.ModelClient(id)` exactly once and do not wrap raw clients passed directly to `planner.ConsumeStream`.
- New DSL and codegen tests prove `RunPolicy(func() { Interceptors("audit", "safety") })` stores agent-scoped interceptor IDs in generated configuration while concrete interceptor implementations stay runtime-wired application code.

Checklist

- [ ] Add failing runtime tests in `runtime/agent/runtime` for `BeforeRun`, `AfterRun`, `BeforeTool`, `AfterTool`, `BeforeModel`, `AfterModel`, `BeforeEvent`, and `AfterEvent` ordering.
- [ ] Add failing runtime tests proving an interceptor can mutate a tool request, replace a tool result, replace a model request, replace a model response, and drop an event.
- [ ] Add failing runtime tests proving dropped events are absent from `RunEventStore`, stream subscribers, and hook bus subscribers because event interception runs before `appendHookRunEvent`.
- [ ] Add failing runtime tests proving interceptor errors short-circuit the active call path and preserve existing terminal run-failure semantics.
- [ ] Add failing runtime tests proving `simplePlannerContext.ModelClient(id)` applies model interceptors exactly once after cache and tool-policy decorators and before tracing.
- [ ] Add failing DSL and codegen tests in `dsl`, `expr/agent`, and `codegen/agent/tests` for `Interceptors` policy validation, duplicate IDs, and generated config fields.
- [ ] Split current `runtime/agent/runtime` interceptor interfaces into typed run, tool, model, and event interfaces while preserving existing tool interceptor behavior through the new tool path.
- [ ] Wire model interceptors into `agent_context.go` without changing raw-client `planner.ConsumeStream` behavior.
- [ ] Wire event interceptors in `hook_activity.go` before `appendHookRunEvent`, `publishHookStreamEvent`, and `publishHookBusEvent`.
- [ ] Add DSL, expr, and codegen support for interceptor policy declarations.
- [ ] Update `docs/runtime.md`, `docs/dsl.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with ordering, mutation, drop, and short-circuit contracts.
- [ ] Run `go test ./runtime/agent/runtime ./dsl ./expr/agent ./codegen/agent/tests -run 'Interceptor|ModelClient|HookActivity' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make lint && make test`.

### Milestone 4: Rich Workflow Composition

Toc: Workflows

Goal: Extend generated workflow planners from sequential steps to deterministic graph execution with parallel, branch, join, and bounded loop nodes.

Acceptance Criteria

- New planner tests in `runtime/agent/planner` prove graph state is keyed by node ID, not `len(ToolOutputs)`, and resume after partial parallel completion does not re-run completed nodes.
- New DSL and codegen tests prove `Parallel`, `Branch`, `Join`, and `Loop(MaxIterations(n), UntilJSONPath(step, path, equals))` represent a workflow graph while existing `Workflow` plus `Step` output remains backward compatible.
- Assistant fixture integration coverage proves generated workflow registration compiles and runs through `make verify-mcp-local`.

Checklist

- [ ] Add failing planner tests in `runtime/agent/planner` for `WorkflowGraphConfig` with node IDs, dependencies, node state, branch predicates, loop bounds, and deterministic scheduling order.
- [ ] Add failing planner tests proving parallel fan-out emits all ready tool calls, join waits for all dependencies, branch selects one edge using prior step JSON output, and loop stops at `MaxIterations`.
- [ ] Add failing planner tests proving resume after one completed parallel node schedules only remaining ready nodes.
- [ ] Add failing DSL tests in `dsl` for `Parallel`, `Branch`, `Case`, `Default`, `Join`, `Loop`, `MaxIterations`, and `UntilJSONPath` while preserving existing `Workflow` plus `Step` behavior.
- [ ] Add failing expression tests in `expr/agent` for duplicate node IDs, unresolved dependencies, unresolved join targets, branch paths without defaults, and unbounded loop rejection.
- [ ] Add failing golden tests in `codegen/agent/tests` proving generated workflow planner data includes graph nodes, edges, branch predicates, loop bounds, and backward-compatible sequential output.
- [ ] Implement workflow graph expression types in `expr/agent/workflow.go`.
- [ ] Implement DSL helpers in `dsl/workflow.go` and keep existing `Workflow` plus `Step` source-compatible.
- [ ] Add `WorkflowGraphConfig`, `WorkflowNode`, `WorkflowEdge`, `WorkflowState`, and graph planner execution to `runtime/agent/planner`.
- [ ] Extend generated workflow planner data and generated registration code in `codegen/agent`.
- [ ] Update `docs/dsl.md`, `docs/runtime.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with graph workflow contracts.
- [ ] Run `go test ./runtime/agent/planner ./dsl ./expr/agent ./codegen/agent/tests -run 'Workflow|Parallel|Branch|Loop|Join|Graph' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make verify-mcp-local`.
- [ ] Run `make lint && make test`.

### Milestone 5: Typed Human Input In Workflows

Toc: Human Input

Goal: Add typed human-input workflow nodes that pause, validate submitted answers, and resume through a typed input channel separate from tool results.

Acceptance Criteria

- New tests in `runtime/agent/planner`, `runtime/agent/api`, and `runtime/agent/hooks` prove the new await kind `typed_input`, `TypedInputOutput`, `SignalProvideTypedInput`, and `AwaitTypedInput` hook payload round-trip through planner, API, and codec boundaries.
- New tests in `runtime/agent/runtime` prove typed await publication, valid answer resume, invalid answer rejection, timeout handling, and completed-run rejection through the new typed input signal.
- New DSL and codegen tests prove `RequestInput("approval", Schema(func(){...}))` declares typed input in workflow design and generated runtime registration metadata.

Checklist

- [ ] Add compile-failing planner tests in `runtime/agent/planner` for `AwaitItemKindTypedInput`, `AwaitTypedInput`, `TypedInputOutput`, and `PlanResumeInput.TypedInputs`.
- [ ] Add failing API tests in `runtime/agent/api` for workflow-safe typed input signal payloads and `SignalProvideTypedInput`.
- [ ] Add failing hook codec tests in `runtime/agent/hooks` for `AwaitTypedInput` encode and decode.
- [ ] Add failing runtime tests in `runtime/agent/runtime` for typed question schemas, invalid answer payloads, valid resume payloads, timeouts, and completed-run rejection.
- [ ] Add failing planner graph tests proving typed human input becomes a workflow node output available to later deterministic nodes without entering `ToolOutputs`.
- [ ] Add failing DSL tests in `dsl` and expression tests in `expr/agent` for `RequestInput` workflow nodes, schema validation, required fields, and duplicate input IDs.
- [ ] Add failing golden tests in `codegen/agent/tests` proving generated workflow metadata includes typed input schemas and generated resume adapters compile.
- [ ] Extend planner await types and resume input types for typed workflow input.
- [ ] Extend runtime await publication and wait logic in `runtime/agent/runtime/workflow_await_publication.go` and `runtime/agent/runtime/workflow_await_wait.go`.
- [ ] Add `Runtime.ProvideTypedInput` signal handling next to clarification, external tool results, and confirmation in `runtime/agent/runtime/runtime_runs.go`.
- [ ] Add DSL, expr, and codegen support for typed workflow input nodes.
- [ ] Update `docs/dsl.md`, `docs/runtime.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with typed human-input workflow contracts.
- [ ] Run `go test ./runtime/agent/planner ./runtime/agent/api ./runtime/agent/hooks ./runtime/agent/runtime ./dsl ./expr/agent ./codegen/agent/tests -run 'TypedInput|AwaitTypedInput|RequestInput' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make lint && make test`.

### Milestone 6: Skill Frontmatter And Load Modes

Toc: Skills

Goal: Make local skills self-describing with structured frontmatter metadata, tool permissions, preload modes, and reload controls across MCP resources and model-facing skill tools.

Acceptance Criteria

- New tests in `runtime/mcp/skills` prove structured frontmatter parsing, fallback behavior for missing frontmatter, invalid metadata rejection, duplicate skill ID rejection, allowed-tool metadata, preload mode parsing, and reload mode parsing.
- New tests in `runtime/agent/runtime` prove `Toolset(FromSkills(..., SkillPreload(...), SkillReload(...)))` uses parsed metadata in `list_skills`, tool descriptions, preload content, and reload behavior.
- Generated MCP fixture tests prove `SkillDirectory` resources expose metadata through `resources/list` and `resources/read` while preserving existing `skill://<name>/SKILL.md` URIs for skills without explicit IDs.

Checklist

- [ ] Add failing parser tests in `runtime/mcp/skills` for valid frontmatter fields `id`, `name`, `description`, `allowed_tools`, `preload`, and `reload`.
- [ ] Add failing parser tests proving missing frontmatter remains compatible by deriving ID from directory name and description from the first heading or first non-empty text.
- [ ] Add failing parser tests proving invalid frontmatter, duplicate IDs across roots, unknown preload modes, unknown reload modes, and malformed `allowed_tools` return errors instead of silent skips.
- [ ] Add failing MCP resource tests in `integration_tests/fixtures/assistant` proving skill metadata appears in generated SDK-backed `resources/list` and `resources/read`.
- [ ] Add failing model-facing skill tool tests in `runtime/agent/runtime` proving `list_skills` includes metadata and `load_skill` respects preload and reload modes.
- [ ] Add failing DSL and codegen tests in `dsl`, `expr/agent`, and `codegen/agent/tests` for `FromSkills(..., SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall))`.
- [ ] Add `gopkg.in/yaml.v3` with a short code comment or docs note explaining it is required for structured skill metadata rather than ad hoc frontmatter parsing.
- [ ] Replace the current description-only parser in `runtime/mcp/skills/skills.go` with structured metadata parsing and duplicate-ID validation.
- [ ] Extend skill resource listing and reading in generated MCP adapters to include parsed metadata while preserving URI compatibility.
- [ ] Extend `runtime/agent/runtime/skill_toolset.go` to use metadata-derived descriptions, preload behavior, and reload behavior.
- [ ] Add DSL, expr, and codegen support for skill load-mode options.
- [ ] Update `docs/dsl.md`, `docs/mcp_sdk_server.md`, `docs/runtime.md`, and `.agents/skills/loom-mcp/SKILL.md` with skill metadata and load-mode contracts.
- [ ] Run `go test ./runtime/mcp/skills ./runtime/agent/runtime ./dsl ./expr/agent ./codegen/agent/tests -run 'Skill|Frontmatter|FromSkills|SkillPreload|SkillReload' -count=1`.
- [ ] Run `make regen-assistant-fixture`.
- [ ] Run `make verify-mcp-local`.
- [ ] Run `make lint && make test`.

### Milestone 7: Local Agent Debug Server

Toc: Debug Server

Goal: Provide an opt-in local debug server that exposes runs, events, awaits, memory, artifacts, and workflow state without becoming part of generated service APIs.

Acceptance Criteria

- New tests in `runtime/agent/debug` prove loopback-only HTTP handlers return JSON envelopes for run snapshots, run events, await state, memory snippets, artifact metadata, and workflow graph state from in-memory runtime fixtures.
- New runtime tests prove debug server construction is explicit through `debug.NewServer`, disabled by default, and does not change planner, hook, stream, MCP, or generated API behavior.
- Documentation states the debug server is development-only, binds to `127.0.0.1` unless application code overrides it, and is excluded from generated MCP servers.

Checklist

- [ ] Add failing handler tests in new package `runtime/agent/debug` for `GET /runs/{id}`, `GET /runs/{id}/events`, `GET /runs/{id}/await`, `GET /runs/{id}/memory`, `GET /runs/{id}/artifacts`, and `GET /runs/{id}/workflow`.
- [ ] Add failing handler tests proving success responses use `{data:...}` and error responses use `{error:{code,message}}`.
- [ ] Add failing server tests in `runtime/agent/debug` for default bind address `127.0.0.1:0`, explicit bind address, graceful shutdown, and absence of generated service registration.
- [ ] Add failing runtime tests in `runtime/agent/runtime` proving debug server construction does not alter hook, stream, planner, tool, memory, artifact, or MCP behavior.
- [ ] Implement `runtime/agent/debug` with adapters over runtime snapshot, runlog, memory searcher, artifact store, and workflow-state reader interfaces.
- [ ] Add explicit `debug.NewServer(debug.Config{Runtime: rt, Addr: \"127.0.0.1:0\"})` wiring without adding a generated DSL surface.
- [ ] Update `docs/runtime.md`, `docs/overview.md`, and `.agents/skills/loom-mcp/references/runtime-contracts.md` with debug-server usage and non-production constraints.
- [ ] Run `go test ./runtime/agent/debug ./runtime/agent/runtime -run 'Debug|RunSnapshot|Artifacts|Memory|Workflow' -count=1`.
- [ ] Run `make lint && make test`.

### Milestone 8: Final Verification, Review, And Handoff

Toc: Handoff

Goal: Prove the combined feature set is documented, generated, tested, reviewed, committed, and pushed from a clean working tree.

Acceptance Criteria

- `make lint`, `make test`, `make itest`, and `make verify-mcp-local` pass from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- A fresh-agent review of the implemented feature set has no blocker findings, and every non-blocker finding is recorded as applied or intentionally deferred in this plan status.
- `git status --short --branch` shows a clean tree on the intended branch after commit and push.

Checklist

- [ ] Run `make loom-status` and record whether the repo is in local or remote Loom mode before final verification.
- [ ] Run `make loom-remote` before CI-facing verification.
- [ ] Run `go fmt ./...`.
- [ ] Run `make lint`.
- [ ] Run `make test`.
- [ ] Run `make itest`.
- [ ] Run `make verify-mcp-local`.
- [ ] Ask a fresh agent to inspect the implemented code and this plan, critique only, and return blocker findings against tests, docs, codegen, and runtime contracts.
- [ ] Apply every blocker review finding and rerun the smallest failing proof command named by the reviewer.
- [ ] Run `git status --short --branch`.
- [ ] Stage only the files changed for this feature set.
- [ ] Commit with a message that names the ADK-inspired Loom MCP features.
- [ ] Push the current branch to its upstream.
