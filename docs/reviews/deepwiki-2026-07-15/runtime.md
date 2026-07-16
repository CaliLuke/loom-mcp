# DeepWiki Runtime Review

Review date: 2026-07-15
Repository and DeepWiki revision: `dd8379472f8341a694e65ce53935fe6cda12c2ad`

This review compares DeepWiki pages 4 through 4.7 with the current code, `docs/`, and `.agents/skills/loom-mcp`. Priorities are P0 (blocking), P1 (high), P2 (medium), and P3 (low). Confidence is based on direct code evidence at the reviewed revision.

## 4. Runtime Architecture

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4-runtime-architecture>

### Major claims checked

- Three-layer application/runtime/engine mental model.
- `Runtime` as coordinator for agents, toolsets, models, stores, hooks, policy, and streaming.
- Sealed registration and the plan/execute/resume loop.
- Activity, inline, and agent-child dispatch.
- Hook-to-stream/runlog pipeline, memory separation, confirmation, and telemetry.

### Local documentation and skill coverage

- `docs/runtime.md:22-54` is the source of DeepWiki's mental model and covers the same entities.
- `docs/runtime.md:309-470`, `585-618`, `1080-1520`, and `1520-1640` cover the execution, policy, confirmation, memory, and runlog surfaces in more detail.
- `.agents/skills/loom-mcp/references/user-guides/runtime.md` gives a concise user narrative.
- `.agents/skills/loom-mcp/SKILL.md` and `references/runtime-contracts.md` accurately capture current planner streaming, agent-as-tool, confirmation, memory, and stream-profile contracts.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| RT-01 | MATCH | P3 | High | The coordinator and three-layer model match `runtime/agent/runtime/runtime.go:84-168`, `docs/runtime.md:22-54`, and the engine abstraction in `runtime/agent/engine/engine.go:105-336`. |
| RT-02 | MATCH | P3 | High | The three dispatch modes are explicit in `runtime/agent/runtime/runtime_registration.go:200-230` and used by `tool_calls_dispatch.go`; agent-as-tool is a real child workflow as the skill states. |
| RT-03 | DEEPWIKI-INACCURACY | P1 | High | “Every run is instrumented with OpenTelemetry” and the summary's tool-execution metrics/spans are too broad. Runtime logger/metrics/tracer default to no-ops (`runtime_bootstrap.go:267-285`); planner and model spans exist (`planner_activities.go:221,238`, `model_tracing.go:69-242`), but the runtime core never calls `r.metrics` and has no semantic tool-execution span. Generated MCP adapters and Temporal have separate OTel paths, so the claim should be scoped by subsystem and configuration. |
| RT-04 | ARCH-IMPROVEMENT | P2 | High | `WithMetrics` suggests built-in runtime metrics, but `r.metrics` is only exposed through `PlannerContext.Metrics()` (`agent_context.go:62`). Add stable run/planner/tool counters and latency histograms, or rename/document the option as an application/planner-provided recorder. This is an observability consistency improvement, not a runtime correctness defect. |
| RT-05 | DOC-GAP | P2 | High | The runtime guide documents telemetry interfaces and model GenAI spans, but does not give one map of which subsystem actually emits which spans/metrics or which paths are no-op by default. DeepWiki's overstatement is an understandable result of this gap. |
| RT-06 | SKILL-GAP | P2 | High | Runtime skill guidance has no telemetry contract. Add a short routing rule distinguishing runtime semantic spans, Temporal instrumentation, generated MCP adapter instrumentation, transport observation, stream metrics profiles, and the opt-in debug server. |

> Resolved 2026-07-16 (`M8`): `runtime.WithMetrics` now records stable
> engine-neutral run, planner-attempt, and tool-result metrics. Planner spans
> carry stable correlation/outcome attributes, and newly inserted canonical
> tool results emit `tool.execute` spans without duplicate signals on ordinary
> hook retries. Runtime docs and the repo-local skill publish the metric names,
> dimensions, retry semantics, no-op defaults, and separate telemetry domains.

## 4.1 Runtime Coordinator & Registration

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.1-runtime-coordinator-and-registration>

### Major claims checked

- Thread-safe registries and two-phase agent/toolset registration.
- `Seal` closes registration and activates staged workers.
- Agent validation, interceptor merging, memory-preload validation, and dispatch-mode resolution.
- Model-client factories and explicit `RegisterModel`.
- Catalog/introspection methods and process-local policy overrides.

### Local documentation and skill coverage

- `docs/runtime.md:97-224` covers production registration, `Seal`, options, defaults, and the debug server.
- `docs/runtime.md:1757-1840` covers model factories/registration; `1948-1970` covers introspection.
- `docs/overview.md:557-700` covers runtime construction, agent/model registration, clients, run control, and schemas.
- The skill routes registration work correctly but does not state the `Seal` lifecycle or its retry semantics.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| REG-01 | MATCH | P3 | High | `Seal` closes registration even if activation fails, is retryable/idempotent after success, and delegates to `engine.RegistrationSealer` (`runtime_registration.go:24-53`). `docs/runtime.md:141-157` is unusually precise here. |
| REG-02 | MATCH | P3 | High | `RegisterAgent` validates planner/workflow/activity names, memory preload dependencies, toolsets, worker queues, and interceptor order (`runtime_registration.go:56-197`). |
| REG-03 | MATCH | P3 | High | Dispatch mode is resolved once at registration and catalog methods are thread-safe (`runtime_catalog.go:16-150`). Model factories and explicit registration match `runtime_models.go:81-207`. |
| REG-04 | RESOLVED | P2 | High | **Complete 2026-07-16:** `RegisterModel` now uses the same atomic closed-registration guard as the sealed runtime lifecycle and returns `ErrRegistrationClosed` after `Seal`. |
| REG-05 | RESOLVED | P2 | High | **Decision 2026-07-16:** post-seal model hot-swapping is not supported. Applications construct, register, and seal a replacement runtime so in-flight lookup semantics remain immutable. |
| REG-06 | RESOLVED | P2 | High | `docs/runtime.md` and `docs/overview.md` now require model registration before `Seal` and document replacement-runtime rotation. |
| REG-07 | RESOLVED | P2 | High | The skill runtime contract now includes registration closure, failed-activation closure, idempotent seal retry, and the model-registration rule. |

## 4.2 Workflow Engine & Execution Loop

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.2-workflow-engine-and-execution-loop>

### Major claims checked

- Durable deterministic plan/start/resume loop.
- `engine.Engine`, replay-safe `WorkflowContext`, futures, Temporal, and in-memory implementations.
- Parallel activity tools, inline tools, child workflows, and deterministic result ordering.
- Time budget, hard deadline, finalizer grace, and cap-triggered finalization.

### Local documentation and skill coverage

- `docs/runtime.md:309-470` and `1948-2072` cover the loop, planner contract, graph composition, engine interfaces, Temporal, and in-memory execution.
- `.agents/skills/loom-mcp/references/user-guides/production/temporal-setup.md` explains durability, retries, and deployment.
- `runtime-contracts.md` covers graph composition and child workflows but not the general replay-safety rules.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| ENG-01 | MATCH | P3 | High | `ExecuteWorkflow` and `runLoop` implement the described loop; workflow code explicitly uses replay-safe time (`workflow.go:49-220`, `workflow_loop.go`, `engine.go:38-48`). |
| ENG-02 | MATCH | P3 | High | Activities/children are launched concurrently and collected into original call order (`tool_calls.go:21-59,282-350`). The DeepWiki distinction between completion order and deterministic planner-visible order is correct. |
| ENG-03 | MATCH | P3 | High | The engine abstraction supplies async futures, timers, receivers, cancellation scopes, and child workflows; Temporal and in-memory implementations satisfy the same contract (`engine.go:105-336`). |
| ENG-04 | MATCH | P3 | High | Budget and hard deadlines use workflow time; finalizer grace reserves a meaningful completion window (`workflow.go:27-35,164-183`; `workflow_loop.go:45-125`). |
| ENG-05 | DOC-GAP | P1 | High | Human wait time is deliberately excluded from `TimeBudget`: clarification/confirmation/typed-input waits extend both deadlines (`workflow_loop.go:49-54,107-119`; `workflow_await_queue.go:206-208,286-291`). Neither `docs/runtime.md` nor the production guides state this important billing/SLA behavior. |
| ENG-06 | SKILL-GAP | P2 | High | Add the core workflow-author rules to `runtime-contracts.md`: no direct I/O/random/system time, use `WorkflowContext.Now/NewTimer`, async results are re-ordered deterministically, and external waits pause runtime budgets. |
| ENG-07 | MATCH | P3 | High | **Resolved 2026-07-16:** `references/user-guides/production/temporal-setup.md` now scopes `WithEngine(...)` to Temporal workflow-history recovery, labels default runlog/session stores process-local, shows shared Mongo adapters through `WithRunEventStore(...)` and `WithSessionStore(...)`, separates memory/stream projections, and provides restart verification. |

## 4.3 Planner Interface & Workflow Graphs

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.3-planner-interface-and-workflow-graphs>

### Major claims checked

- Stateless/thread-safe planner interface and run-scoped `PlannerContext`.
- `PlanStart`, `PlanResume`, finalization, tool calls, awaits, retry hints, prompts, models, and reminders.
- Graph node kinds, dependency resolution, parallelism, branches, joins, bounded loops, and typed input.
- Streaming aggregation/events and heuristic token estimation.

### Local documentation and skill coverage

- `docs/runtime.md:354-585` covers the planner interface, inputs/results/context/events, graph workflows, and both supported streaming paths.
- `docs/dsl.md` covers authored graph helpers and validation.
- `runtime-contracts.md` is stronger than DeepWiki on graph resume invariants and decorated-vs-raw streaming.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| PLN-01 | MATCH | P3 | High | Planner methods and context services match `planner/planner.go:62-117`; `PlanResult` covers tool calls, final response, await barriers, retry hints, and annotations (`planner.go:678-704`). |
| PLN-02 | MATCH | P3 | High | Graph node kinds and scheduling behavior match `workflow_graph.go:75-84,108-220`; completion uses stable node/call IDs, loops require `MaxIterations`, and typed input is separate from tool outputs. |
| PLN-03 | MATCH | P3 | High | Stream consumption aggregates text, thinking, tool-argument deltas, usage chunks, and terminal metadata (`planner/stream.go:31-149`). Local docs/skill correctly add the crucial rule that `ConsumeStream` is only for raw clients. |
| PLN-04 | MATCH | P3 | High | `model.TokenEstimator` is a real explicit heuristic fallback, not only a test helper (`model/model.go:414-421,744-782`). |
| PLN-05 | DOC-GAP | P3 | High | The public docs do not mention `TokenEstimator` or when to use it as a fallback for clients without exact counting. Add it near history/token budgeting, including that counts are inexact. |
| PLN-06 | SKILL-GAP | P3 | High | The skill covers planner streaming and graph workflows well; optionally add the token-estimator fallback to the model-client section. |
| PLN-07 | DEEPWIKI-INACCURACY | P1 | High | DeepWiki repeats `planner.Planner`'s claim that `PlanStart` is invoked “exactly once.” The workflow schedules one logical start activity, but generated registrations configure planner activities with `MaxAttempts: 3`, so `PlanStart` and `PlanResume` may execute multiple attempts after infrastructure or post-model hook failures (`planner/planner.go:62-72`; `integration_tests/fixtures/assistant/gen/assistant/agents/assistant_runtime/registry.go:44-60`; `run_options.go:222-233`). |
| PLN-08 | DOC-GAP | P1 | High | The public planner contract says planner errors terminate the run and `PlanStart` runs exactly once, but Temporal activity retry means both statements need attempt-level qualification. Require planners to be retry-safe/idempotent and warn that direct side effects and model calls can repeat after an activity attempt fails late; add the same rule to `runtime-contracts.md` and planner authoring docs. |

> Resolved 2026-07-16 (`A12`, `D9`): planner GoDoc, canonical runtime
> documentation, and the repo-local skill now distinguish one logical planner
> turn from retryable activity attempts and require retry-safe model calls and
> side effects.

## 4.4 Hooks, Streaming & Event Pipeline

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.4-hooks-streaming-and-event-pipeline>

### Major claims checked

- Typed hook bus, common event identity fields, and codec crossing activity/process boundaries.
- Hook-to-stream translation, profiles, sinks, Pulse transport, and display-hint decoration.
- Await, authorization, child-run, workflow, tool, assistant, and usage streaming.
- Canonical persistence versus best-effort projections.

### Local documentation and skill coverage

- `docs/runtime.md:1308-1434` covers the hook bus, determinism boundary, custom subscribers, stream sinks, profiles, and workflow payloads.
- `.agents/skills/loom-mcp/references/user-guides/production/streaming-ui.md` covers session streams, Pulse, profiles, and UI consumption.
- The skill's stream rules correctly state session ownership, profile visibility, and linked-not-flattened child runs.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| EVT-01 | MATCH | P3 | High | `hooks.Event` carries run/session/agent/timestamp/event-key/turn identity (`hooks/events.go:53-82`), and the codec preserves these fields across hook activity envelopes (`hooks/codec.go:67-178`). |
| EVT-02 | MATCH | P3 | High | `stream.Subscriber` is the typed bridge and profiles govern visibility (`stream/subscriber.go:24-178`; `stream/stream.go:697-765`). Pulse is a sink/consumer implementation, not part of core semantics. |
| EVT-03 | MATCH | P3 | High | The hinting sink decodes canonical payloads using registered codecs and only enriches `ToolStart` (`runtime_hints_sink.go:16-96`); the runtime guide accurately documents durable defaults and consumer overrides. |
| EVT-04 | DEEPWIKI-INACCURACY | P2 | High | Calling `EventKey` “exact-once delivery” is too strong. It is a stable idempotency identity used by the canonical store. Stream and default memory/session projections are best-effort, while explicitly critical hook-bus subscribers can fail the hook activity; none of those surfaces has a general exactly-once delivery contract. Say “idempotent canonical persistence across activity retries.” |
| EVT-05 | DOC-GAP | P1 | High | The most important reliability contract is absent from user docs: runlog append is fail-closed; active-session stream errors are logged and swallowed; ended/sessionless streams are skipped; default memory/session subscribers are best-effort; critical bus subscribers propagate failure (`hook_activity.go:23-37,61-70,114-177`; `runtime_subscribers.go:33-57`; `runtime_hook_helpers.go:13-30,114-124`). Also correct `Runtime.streamSubscriber`'s stale field comment, which says active-session stream emission can be fatal even though the implementation deliberately swallows it. |
| EVT-06 | DOC-GAP | P2 | High | The event matrices lag code: `tool_call_args_delta`, `tool_authorization`, `assistant_turn_committed`, `retry_hint`, and `hard_protection_triggered` are missing or scattered. Mark tool-argument deltas as noncanonical/optional (`hook_activity.go:54-59`). |
| EVT-07 | SKILL-GAP | P1 | High | Add the canonical-versus-derived delivery contract to `runtime-contracts.md`, including interceptor ordering, fail-closed runlog, best-effort default subscribers, noncanonical deltas, and session-aware stream suppression. |

## 4.5 Memory, Transcript & Session Management

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.5-memory-transcript-and-session-management>

### Major claims checked

- Provider-precise transcript ledger and Bedrock ordering validation.
- Raw transcript store, indexed transcript search, entry-shaped long-term memory, and preload/tools.
- Session/run lifecycle and parent-child linkage.
- Prompt override resolution and artifact persistence/tools.
- Relationship between memory events, runlog events, and transcript reconstruction.

### Local documentation and skill coverage

- `docs/runtime.md:1520-1641` accurately separates `memory.Store`, `memory.Searcher`, `memory.Service`, preloads, and `runlog.Store`.
- `docs/runtime.md:224-276` covers prompt baselines/overrides; `831-914` covers artifacts.
- `runtime-contracts.md:71-94` has the best compact description of transcript versus long-term memory.
- `.agents/skills/loom-mcp/references/user-guides/memory.md` is comprehensive for ledger/session/runlog basics but predates the long-term `memory.Service` surface and overstates `memory.Store` authority.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| MEM-01 | MATCH | P3 | High | Ledger parts, ordering, Bedrock validation, JSON round-trip, and `BuildMessagesFromEvents` match `transcript/ledger.go:30-183,229-357`. |
| MEM-02 | MATCH | P3 | High | The current docs/skill correctly distinguish raw per-run events (`memory.Store`), indexed raw events (`memory.Searcher`), and long-term entries (`memory.Service`) (`memory.go:17-62`, `searcher.go:12-39`, `service.go:11-108`). |
| MEM-03 | DEEPWIKI-INACCURACY | P1 | High | DeepWiki groups `memory.Searcher` under long-term memory. It searches `memory.Event` values; only `memory.Service` stores/searches long-term `memory.Entry` values. The current runtime docs and contract skill are more accurate. |
| MEM-04 | DEEPWIKI-INACCURACY | P1 | High | DeepWiki says the `RunEventStore` can be replayed through `transcript.BuildMessagesFromEvents`. The function accepts `[]memory.Event`, while `RunEventStore` stores `*runlog.Event` hook envelopes (`ledger.go:183`; `hook_activity.go:93-105`). There is no direct runlog-to-ledger projection, and replaying a transcript would not reconstruct workflow scheduling/progress state or by itself resume a run deterministically. |
| MEM-05 | DEEPWIKI-INACCURACY | P2 | High | `memory.Store` is per agent/run, not a “live transcript of the current session.” A session may contain multiple runs; stitching is an application/session-store concern (`memory.Store.LoadRun`; `session.Store.ListRunsBySession`). |
| MEM-06 | MATCH | P3 | High | Session creation/end, immutable run ownership, and atomic/idempotent child linkage match `session/session.go:16-152`; artifact listing/loading and bounded bodies match `artifact_toolset.go`; prompt overrides match the registry/store implementation. |
| MEM-07 | DEEPWIKI-INACCURACY | P2 | High | Prompt precedence is not a fixed “session > label > global” enumeration. It is session first, then the most constrained matching label set, then newest override (`prompt/inmem_store.go:31-67`, `prompt/scope.go:22-35`). DeepWiki's WIP-based summary omits this specificity and tie-breaker. |
| MEM-08 | ARCH-IMPROVEMENT | P1 | High | Canonical-state terminology is internally tense: docs call the transcript the single source of truth persisted in `memory.Store`, but memory persistence is a best-effort bus subscriber; the fail-closed source is `runlog.Store`, which cannot directly rebuild the ledger. Define the authority explicitly. Options: make memory append critical/direct, make transcript a rebuildable runlog projection, or document Temporal ledger + runlog + derived memory as three different authorities. |
| MEM-09 | SKILL-GAP | P1 | High | Update `references/user-guides/memory.md`: add `memory.Service`, `search_memory`, long-term preload/scope/visibility, and correct the “single source of truth” claim to match the best-effort subscriber architecture. `runtime-contracts.md` already has the right long-term model. |
| MEM-10 | DOC-GAP | P2 | High | `docs/runtime.md` has the types but lacks one lifecycle diagram/table showing where live workflow ledger, memory events, runlog events, session metadata, artifacts, and long-term entries are written and which failures are fatal. This would prevent the same conflation seen in DeepWiki. |

## 4.6 Policy Enforcement & Human-in-the-Loop

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.6-policy-enforcement-and-human-in-the-loop>

### Major claims checked

- Caps, pre-plan advertised-tool filtering, post-plan enforcement, and runtime overrides.
- Design/runtime confirmation, queue partitioning, templates, signals, authorization events, and denied results.
- Clarification/tool pauses, retry hints, manual pause/resume, and hook reliability.

### Local documentation and skill coverage

- `docs/runtime.md:1080-1307` covers pause/resume, clarification, external/typed input, confirmation, authorization, and validation.
- `docs/runtime.md:1435-1519` covers policy decisions, caps, and per-run/runtime overrides.
- The skill accurately records confirmation ownership, `ProvideConfirmation`, authorization events, and schema-compliant denial results.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| POL-01 | MATCH | P3 | High | Pre-plan policy controls the advertised allowlist and post-plan policy clamps requested calls/caps (`workflow_policy.go:31-164`). Docs correctly note caps do not truncate the catalog by themselves. |
| POL-02 | MATCH | P3 | High | Confirmation detection, rendering, await queue, decision validation, authorization event, approval execution, and denial synthesis are runtime-owned (`confirmation_workflow.go`, `workflow_await_queue.go`, `docs/runtime.md:1210-1307`). |
| POL-03 | DEEPWIKI-INACCURACY | P1 | High | Describing `TimeBudget` as the maximum elapsed duration of a run is wrong for HITL. Time spent awaiting clarification, confirmation, typed input, or external tools extends the deadlines and does not consume the budget (`workflow_loop.go:45-119`). |
| POL-04 | DEEPWIKI-INACCURACY | P2 | High | The “Tool Retry Hint System” section conflates two paths. `RetryHint` is structured planner/tool-failure guidance consumed by policy; `ToolPause{Clarification}` is separately projected into an await item (`workflow_turn.go:95-132`). A clarification is not itself a retry hint. |
| POL-05 | MATCH | P3 | High | DeepWiki's hook reliability description is correct: runlog failure fails the hook activity; stream errors are best-effort; event interceptors run before persistence/stream/bus (`hook_activity.go:23-75`). |
| POL-06 | DOC-GAP | P1 | High | Add explicit wall-clock semantics for `TimeBudget`, `FinalizerGrace`, and paused awaits. Operators otherwise cannot derive an SLA or timeout from the current policy docs. |
| POL-07 | SKILL-GAP | P2 | High | Add a compact distinction between planner `RetryHint`, runtime `ToolPause`, await barriers, and confirmation decisions. The current skill is strong on confirmation but silent on pause-produced clarification. |
| POL-08 | DEEPWIKI-INACCURACY | P2 | High | DeepWiki says `CapsState` tracks and enforces `TimeBudget`. Runtime time enforcement actually lives in `runDeadlines`; `initialCaps` does not derive `CapsState.ExpiresAt` from `RunPolicy.TimeBudget`, and no runtime path checks `ExpiresAt` (`policy/policy.go:147-150`; `runtime_policy_helpers.go:67-80,124-141`). |
| POL-09 | ARCH-IMPROVEMENT | P1 | High | `policy.CapsState.ExpiresAt` is a currently inert public contract: its GoDoc promises runtime termination after expiry, policy decisions can set it, but the runtime only merges it, forwards it to later policy decisions, and republishes it. Either enforce it using deterministic workflow time and the same await-pause semantics as `runDeadlines`, or remove/deprecate it so custom policy engines cannot rely on a limit that is silently ignored. |

> Resolved 2026-07-16 (`A11`, `D9`): deterministic `runDeadlines` remains the
> single active-time authority. `CapsState.ExpiresAt` is deprecated, ignored by
> cap merging, and documented as source-compatibility-only.

## 4.7 Telemetry & Observability

URL: <https://deepwiki.com/CaliLuke/loom-mcp/4.7-telemetry-and-observability>

### Major claims checked

- Runtime GenAI spans and error recording.
- Generated MCP adapter spans, counters/histogram, options, request headers, and transport observer.
- Temporal trace propagation/instrumentation.
- Debug server, MCP `events/stream`, broadcaster, and session tracking.

### Local documentation and skill coverage

- `docs/runtime.md:2073-2127` documents runtime telemetry interfaces, model GenAI attributes, and privacy-sensitive message capture.
- `docs/mcp_sdk_server.md:329-356` documents generated transport observation and its low-cardinality/privacy contract.
- `docs/runtime.md:181-223` and `runtime-contracts.md:150-159` accurately describe the opt-in local debug server.
- Generated MCP adapter metrics and Temporal trace-domain semantics are not documented in current user docs/skill.

### Findings

| ID | Classification | Priority | Confidence | Assessment and evidence |
| --- | --- | --- | --- | --- |
| OBS-01 | MATCH | P3 | High | Model calls emit `model.complete`/`model.stream` spans with GenAI attributes, usage, finish reason, TTFT, error status, and default-off message capture (`model_tracing.go:69-366`; `docs/runtime.md:2104-2127`). |
| OBS-02 | MATCH | P3 | High | Generated MCP adapters create per-method spans and three metrics, allow injected `Tracer`/`Meter`, and generated SDK handlers expose a transport observer (`adapter_server.go:421-468`; `sdk_server.go:157-188`). |
| OBS-03 | DEEPWIKI-INACCURACY | P2 | High | The histogram name is `loom_mcp.mcp.duration_ms`, not `loom_mcp.mcp.duration` (`adapter_server.go:430`). Exact metric names matter for dashboards and alerts. |
| OBS-04 | DEEPWIKI-INACCURACY | P1 | High | Temporal does not keep one trace ID from `AgentClient.Run` through all activities, and the data converter does not preserve trace metadata. Loom intentionally starts each activity as a new root and attaches the origin request as an OTel link (`temporal/instrumentation.go:24-32`; `runtime/temporaltrace/temporaltrace.go:1-14,103-190`). |
| OBS-05 | DEEPWIKI-INACCURACY | P1 | High | “Local debug server (Pulse)” conflates three separate surfaces: `runtime/agent/debug` is an opt-in read-only HTTP inspector; Pulse is a Redis-backed runtime stream sink; generated MCP `events/stream` is a protocol broadcaster. None is an alias for another (`debug/server.go`, `features/stream/pulse`, generated `EventsStream` at `adapter_server.go:3088`). |
| OBS-06 | DEEPWIKI-INACCURACY | P2 | High | `initializedSessions` and `sessionPrincipals` enforce MCP session lifecycle/principal continuity; they are not debug filters or telemetry session tracking (`adapter_server.go:261-352,663-680`). |
| OBS-07 | DOC-GAP | P1 | High | Document Temporal's trace-domain contract: new-root activity spans, origin links, default-on engine tracing/metrics, `DisableTracing`/`DisableMetrics`, and the difference between the Temporal global OTel provider and `runtime.WithTracer`. This is subtle enough that DeepWiki inferred the opposite. |
| OBS-08 | DOC-GAP | P2 | High | Add generated MCP adapter metric names, units, status attributes, injected `Tracer`/`Meter`, and `TelemetryName` to `docs/mcp_sdk_server.md`; keep transport observation as a distinct lower-level channel. |
| OBS-09 | SKILL-GAP | P1 | High | Add the Temporal trace-domain rule and the separation among debug server, runtime stream/Pulse, MCP broadcaster, adapter OTel, and transport observer to the runtime/codegen contracts. |
| OBS-10 | ARCH-IMPROVEMENT | P2 | High | Complete runtime-level observability with semantic tool spans and stable core run/tool metrics. Today Temporal gives activity-level infrastructure telemetry and MCP adapters give protocol metrics, but in-memory/custom engines lack equivalent semantic tool/run measurements. |
| OBS-11 | ARCH-IMPROVEMENT | P2 | High | `InstrumentationOptions.TracerOptions` is retained but ignored, while surrounding option comments still say it customizes interceptor behavior (`temporal/engine.go:55-59,118-145`). Deprecate/remove it or revise top-level comments to prevent configuration that silently has no effect. |

> Resolved 2026-07-16 (`M8`): semantic instrumentation is now produced at
> planner activity and newly inserted canonical event boundaries, so in-memory,
> Temporal, and custom engines expose the same run/planner/tool contract.
> `tool.execute` spans use the canonical result's reported interval; run and
> tool metrics are event-key deduplicated, while planner metrics intentionally
> count retryable activity attempts.

## Prioritized Recommendations

1. **P1 — Resolve canonical transcript/runlog/memory authority.** Define whether `memory.Store` must be complete or is a rebuildable/best-effort projection. Align code failure policy, `BuildMessagesFromEvents`, docs, and the memory skill.
2. **P1 — Correct planner retry semantics.** Replace the attempt-level “exactly once” promise with a retry-safe/idempotent planner contract, and document possible repeated model calls or direct side effects after late activity-attempt failure.
3. **P1 — Resolve the inert policy deadline.** Enforce or remove `CapsState.ExpiresAt`; if enforced, use workflow time and preserve the same external-wait pause semantics as `TimeBudget`.
4. **P1 — Document event-pipeline reliability.** Publish fail-closed runlog, best-effort stream/default projections, critical bus subscribers, noncanonical tool argument deltas, and session-end behavior.
5. **P1 — Make Temporal setup honestly durable.** Wire persistent runlog/session stores in the production skill guide or explicitly state that `WithEngine(temporal)` alone protects only workflow history.
6. **P1 — Document Temporal trace domains.** State that activity spans are new roots linked to the origin, not descendants in one long trace; separate Temporal, runtime semantic, generated MCP, and transport instrumentation.
7. **P1 — Document HITL budget semantics.** Make clear that external-input waits pause `TimeBudget`, while finalizer grace reserves post-budget completion time.
8. **P2 — Formalize model registry mutability.** Either seal model registration or explicitly support/name/document post-seal hot swaps.
9. **P2 — Fill and document observability gaps.** Add semantic run/tool metrics and spans, publish generated MCP metric names/options, and distinguish the debug server, Pulse streams, and MCP `events/stream`.
10. **P2 — Refresh skill references and event matrices.** Update the memory guide for `memory.Service`; add `Seal`, delivery reliability, determinism, planner retries, pause-vs-retry, and telemetry contracts; mark events as durable, derived, profile-filtered, critical, or best-effort.
11. **P3 — Add token-estimator guidance.** Document the inexact fallback and its appropriate use in history/token budgeting.

## Page Coverage Checklist

- [x] 4 Runtime Architecture
- [x] 4.1 Runtime Coordinator & Registration
- [x] 4.2 Workflow Engine & Execution Loop
- [x] 4.3 Planner Interface & Workflow Graphs
- [x] 4.4 Hooks, Streaming & Event Pipeline
- [x] 4.5 Memory, Transcript & Session Management
- [x] 4.6 Policy Enforcement & Human-in-the-Loop
- [x] 4.7 Telemetry & Observability
