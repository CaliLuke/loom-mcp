---
name: loom-mcp
description: Build and maintain the loom-mcp repository and framework code in Go. Use this skill when the task involves the agent DSL, generated `gen/` code, runtime/planner behavior, agent-as-tool, MCP integration, codegen internals, or refactoring a repo with a `design` package.
---
# loom-mcp

Use this skill for `loom-mcp` work in this repo. Keep `AGENTS.md` short and keep framework-specific guidance here and in the files under `references/`.

## Non-Negotiables

- Treat `design/*.go` as the source of truth.
- Regenerate after every design change with `loom gen <module-import-path>/design`.
- Never hand-edit generated `gen/` files.
- Implement business logic in non-generated files.
- Use Go import paths for generation commands, not filesystem paths.
- Commit generated code; do not rely on CI to regenerate it.
- Keep this skill current with the product. Update `SKILL.md` and the reference files directly instead of writing sidecar delta docs.

## Default Workflow

1. Detect the `loom-mcp` surface: `go.mod`, `design/`, DSL imports, `codegen/`, `runtime/`, or generated `gen/`.
2. Decide whether the task is DSL/codegen/runtime/application code.
3. Edit the DSL first when the contract changed.
4. Regenerate with `loom gen <module>/design`.
5. Run `loom example <module>/design` only when scaffold output is intentionally required.
6. Implement or refactor non-generated logic.
7. Verify with formatting, lint, and relevant tests.

## Planning Quality Rules

- Multi-file or multi-layer plans must start with a current-contract inventory
  that names the existing structs, files, generated surfaces, and invariants the
  work will change.
- Plans must use concrete Loom DSL shapes, runtime type names, generated-code
  owners, test files, and proof commands. Avoid placeholder feature names once
  code inspection has revealed the owner.
- Contract tests may intentionally compile-fail before implementation, but the
  plan must mark them as contract tests and must not run package proof commands
  until the symbols those tests import have been added.
- Before accepting a plan, check for impossible or wrongly placed tests: planner
  behavior belongs in `runtime/agent/planner`, runtime context and model-client
  decoration belong in `runtime/agent/runtime`, MCP skill resources belong in
  `runtime/mcp/skills`, and model-facing skill tools belong in
  `runtime/agent/runtime`.
- Framework-scale DSL, codegen, runtime, or MCP feature work must include the
  full repo gates expected by AGENTS.md: `make lint`, `make test`, `make itest`,
  and `make verify-mcp-local`, with targeted package commands only as earlier
  red-green checks.
- When a model-facing agent feature crosses DSL, codegen, generated
  registration, and runtime behavior, extend
  `integration_tests/fixtures/agent_features` as the generated acceptance
  fixture in addition to focused package tests.

## Current Product Rules

- Runtime planners have two streaming modes only:
  - use `PlannerContext.ModelClient(id)` and drain the decorated stream yourself, or
  - use `planner.ConsumeStream` with a raw client.
- Runtime registration is immutable after `Runtime.Seal` or the first submitted
  run. `RegisterModel`, `RegisterAgent`, and `RegisterToolset` all return
  `ErrRegistrationClosed`; model-client hot-swapping requires a replacement
  runtime.
- Runtime registry plain and semantic search paths share concurrent fan-out and
  partial-failure semantics. The Pulse replicated map is the catalog authority;
  the removed registry memory/Mongo `Store` model must not be documented or
  restored.
- Mongo prompt override resolution uses canonical indexed label-scope
  fingerprints, at most one session plus one global lookup, and a 15-label
  bound. Client startup backfills missing fingerprints before index creation
  and fails closed on migration errors.
- Planner turns are workflow activities, not exactly-once method calls. Generated
  registrations retry failed plan/resume activities up to three attempts by
  default, so model calls and direct side effects can repeat after a late attempt
  failure. Planner implementations must be retry-safe and protect non-idempotent
  effects with stable idempotency keys.
- `policy.CapsState` owns counter budgets only. Its deprecated `ExpiresAt` field
  is ignored; active-time enforcement belongs to deterministic runtime deadlines
  configured through `TimeBudget`, with external waits pausing that budget.
- Agent-as-tool runs as a real child workflow. Parent and child are linked by `ChildRunLinked`, and parent tool results carry `RunLink`.
- Stream visibility is profile-driven. Child runs are linked, not flattened, by default.
- Runtime schemas come from generated `tool_specs.Specs` and codecs, not `docs.json`.
- Workflow state is the live execution authority, while `runlog.Store` is the
  canonical append-only introspection record and fails closed. Streams, the
  hook bus, and `memory.Store` projections have distinct delivery guarantees;
  never describe them as interchangeable durable transcripts.
- Mongo transcript memory writes immutable append buckets to the companion
  events collection and reads legacy single-run documents before those buckets.
  Bucket queries use `created_at`, `_id` order and combined snapshots are
  stable-sorted by event timestamp; equal timestamps retain legacy-before-new
  and deterministic bucket order.
  Do not restore `$push` growth on one run document, and retain both collections
  through the legacy-read compatibility window.
- Pulse `Subscriber.Subscribe` is an auto-ack UI convenience. Durable consumers
  must use `SubscribeManual`, commit idempotently using `EventKey`, and call
  `Delivery.Ack` only after the commit. Malformed manual deliveries expose the
  raw payload and decode error, remain pending by default, and may be acked only
  after an application-owned dead-letter commit.
- Temporal persists workflow history, not the runtime's store defaults.
  Production worker and client runtimes must explicitly share persistent
  `runlog.Store` and `session.Store` implementations when run introspection and
  session metadata must survive process replacement; configure memory and
  stream projections independently.
- `TimeBudget` measures active runtime work. Clarification, confirmation,
  typed-input, and external-tool waits pause the budget, so elapsed wall time
  may exceed the configured duration.
- `Idempotent()` is generated metadata for planners/orchestrators. It does not
  provide runtime replay suppression or exactly-once execution by itself.
- Model providers must satisfy the shared conformance matrix for complete and
  streaming calls, tools, structured output, typed thinking, token counting,
  cancellation, normalized errors, and canonical/provider tool-name round
  trips. Rate limits that surface during stream receive must still wrap
  `model.ErrRateLimited`.
- Gemini and Vertex implement exact `model.TokenCounter` but do not implement
  streaming; `Stream` returns `model.ErrStreamingUnsupported`. Treat streaming
  as a provider capability, not a universal consequence of `model.Client`.
- Provider-safe tool names must be deterministic, byte-bounded, collision
  checked, and reversible through the request-scoped canonical mapping. Do not
  truncate long canonical IDs without a stable hash suffix.
- Provider tool names and tool-use correlation IDs are separate codecs. For
  Anthropic and Bedrock request encoding, reserve all provider-safe tool-use IDs
  before assigning synthetic `tN` values to unsafe IDs, skip occupied values,
  and keep replayed tool-use/result pairs correlated through one request-local
  mapping.
- Middleware must preserve optional model capabilities truthfully. In
  particular, a wrapped client implements `model.TokenCounter` only when the
  underlying provider does; do not replace interface absence with a runtime
  unsupported error.
- Toolset tools are runtime-only by default. Project a method-backed toolset
  tool into MCP only with `Expose(AgentRuntime, MCPSurface)` plus
  `MCPPlacement(service, mcpServer)`, and keep the placement on the same service
  as the bound method. Method-level `Tool(...)` declarations remain MCP-only.
- Projected MCP tools use generated toolset specs for MCP `ToolInfo` schemas
  and generated `Dispatch<Tool>Method(...)` for execution. Do not duplicate
  `BindTo(...)` transforms in MCP adapters.
- `Inject(...)` fields are server-owned: generated public payload structs and
  codecs keep them for runtime injection, while generated
  `ToolSpec.Payload.Schema`, `ExampleJSON`, and `ExampleInput` hide them and
  remove them from model-facing `required`. Injection renderers must inspect
  the prepared public payload field and emit pointer or value assignments that
  match its generated Go type.
- Unified tool-surface projection v1 rejects projected tools that use
  `Confirmation(...)`, `Inject(...)`, `ServerData(...)`,
  `ResultReminder(...)`, or `BoundedResult(...)`; treat those as validation
  errors until the runtime/MCP contract is explicitly extended.
- Tool confirmation is runtime-owned and design-visible: declare
  `Confirmation(...)` in tool DSL for default approval requirements, or use
  `runtime.WithToolConfirmation(...)` for runtime overrides. The runtime emits
  `AwaitConfirmation`, resumes through `ProvideConfirmation`, records
  `ToolAuthorization`, executes only approved calls, and synthesizes
  schema-compliant denied results.
- MCP is a two-way bridge:
  - consume external MCP servers through `runtime/mcp` callers,
  - expose designed services as MCP servers through generated adapters and registrations.
- The official MCP Go SDK is the only MCP wire transport. It owns protocol
  negotiation, Streamable HTTP, standard SSE, cancellation, and sessions.
- MCP designs do not declare `ProtocolVersion`, `Notification`, `Subscription`,
  `SubscriptionMonitor`, or MCP-only `JSONRPC` blocks. Explicit non-MCP
  `JSONRPC` transports remain valid.
- Generate only the MCP service types, adapter, local-provider registration,
  OAuth discovery, prompt provider, and `SDKServer`. Do not generate a native
  MCP client, server, SSE extension, session store, or broadcaster.
- Adapter tool and resource calls are unary. Collect a streaming Loom method
  into one MCP result. Use `runtime/mcp.ReportProgress` for intermediate
  progress.
- Generated MCP service fields that carry optional arbitrary JSON use Loom's
  `loom.Nullable[any]` presence contract. Adapter boundaries must preserve a
  contained `json.RawMessage` without an intermediate decode and marshal other
  concrete values only when strict dispatch or wire forwarding requires raw
  JSON.
- MCP metadata is design-owned. Implementation `WebsiteURL`/`ServerIcons` and
  list-surface `ToolIcons`/`ResourceIcons`/`PromptIcons`/`DynamicPromptIcons`
  should be declared in the DSL and allowed to flow through codegen into
  `initialize`, `tools/list`, `resources/list`, and `prompts/list`.
- MCP skill exposure is design-owned too: declare local agent skill roots with
  `SkillDirectory(...)`, then let generated SDK servers expose
  `skill://` entries through `resources/list` and `resources/read`.
- MCP adapter allow policies define the server's maximum resource grant.
  Request-scoped allowed names, including raw `x-mcp-allow-names`, may narrow
  that grant but must never broaden it; request and server denies are additive.
  Treat headers as untrusted input and keep authentication and grant derivation
  in application-owned middleware backed by verified credentials.
- MCP streamable-HTTP sessions bind the issued session ID to one verified
  principal. The generated SDK wrapper must check that pair on every POST, GET,
  and DELETE request. It must reject missing authenticated bindings and
  anonymous-session adoption. Authentication middleware must run outside the
  generated handler.
- `WatchableResource` enables standard SDK subscriptions. The generated
  `SDKServer.ResourceUpdated(ctx, uri)` method sends resource-update
  notifications. Reject unknown URIs and reject watchable resources with
  stateless Streamable HTTP.
- Model-facing skill exposure is design-owned through
  `Toolset(FromSkills(..., SkillPreload(...), SkillReload(...)))`; generated
  agent registration should wire these skills into `runtime/agent/runtime`
  skill tools rather than MCP resource handlers.
- Long-term memory is separate from transcript memory. `memory.Store` persists
  raw run events, `memory.Searcher` searches indexed raw events, and
  `memory.Service` stores/searches durable `memory.Entry` values. Expose
  long-term memory intentionally with `FromMemory(MemoryLongTerm(), ...)` and
  `PreloadLongTermMemory(...)`; do not overload transcript preload or
  `load_memory` with extracted facts.
- Local skills can declare structured `SKILL.md` frontmatter (`id`, `name`,
  `description`, `allowed_tools`, `preload`, `reload`). Duplicate IDs and
  unknown load modes are hard errors, not silent skips.
- Generated MCP adapters can opt into progressive discovery with
  `MCPAdapterOptions.ToolSearch`. Compact mode makes `tools/list` authoritative:
  it exposes `search_tools`, `call_tool`, and validated `AlwaysVisible` pins.
  Hidden real tools are invoked through `call_tool`; direct hidden `tools/call`
  is rejected by default, except for the explicit JSON-RPC compatibility option
  `AllowDirectHiddenCalls`. `search_tools` must support tokenized natural
  language queries, fuzzy name/title matching, strong name/title match
  narrowing (exact, normalized, prefix, and contains), and DSL tuning through
  `ToolSearch(...)`; it must return exact `call_tool` JSON
  examples. `call_tool` schema text must require top-level `name` and
  `arguments` and warn not to use `args`. SDK compact mode must reject
  direct-hidden compatibility at construction.
- Projected MCP tools must participate in compact discovery like method-level
  MCP tools: validated `AlwaysVisible` pins, hidden `search_tools` discovery,
  and `call_tool` invocation through `MCPAdapter.ToolsCall`.
- Generated MCP packages with tools expose an in-process progressive-discovery
  `ToolsetRegistration` built from the same `MCPAdapter`. Its compact specs,
  search, visibility, interceptors, and method/projected dispatch must match
  public MCP behavior without initialization, sessions, JSON-RPC, or HTTP.
- Generated SDK-backed MCP servers expose prompt argument completion for
  enum-backed dynamic prompt arguments and place runtime client-feature
  adapters in request contexts so service code can call `runtime/mcp.Elicit`
  and request-scoped `runtime/mcp.ReportProgress` during MCP calls. Elicitation
  uses the official multi-round-trip `InputRequests`/`InputResponses` contract:
  generated handlers return input-required results and re-enter service code on
  retry, with bounded, versioned opaque request state carrying issued input
  contracts, the exact pending round, and earlier client-supplied responses
  across multi-step flows. Generated servers encrypt and authenticate every
  state value with AES-GCM, bind it to the original logical request, and require
  a stable 32-byte
  `SDKServerOptions.RequestStateKey` shared by endpoint replicas. Treat the
  protected responses as client assertions and never as authorization evidence
  or server-owned state.
  Each `runtime/mcp.Elicit` call requires one round trip; multi-step flows
  require a `2026-07-28` client because the official legacy middleware re-enters
  only once. Official `2026-07-28` streamable HTTP is stateless and sessionless;
  each retry is a separate POST. Reject wrong input-response types, invalid
  elicitation actions, responses outside the exact pending input-request set,
  changed elicitation contracts on re-entry, and bounded-state violations.
  Preserve invalid-client-input markers through tool dispatch so these become
  protocol errors, not tool-level `isError` results. Code before elicitation
  must be retry-safe.
  MCP `2026-07-28` sampling and roots are deprecated and are not exposed through
  Loom runtime client-feature adapters. Keep official-SDK conversion and
  request-state handling in `runtime/mcp/sdkclient`, not duplicated in generated
  files.
- Generated SDK servers expose Loom transport observability directly through
  `SDKServerOptions.TransportObserver`; keep external middleware wrapping as an
  application-wide alternative, not the only enablement path.
- Runtime semantic telemetry is engine-neutral: stable
  `loom_mcp.runtime.*` run/planner/tool metrics and `tool.execute` spans come
  from planner activities and newly inserted canonical events. Planner metrics
  count retryable attempts; run/tool events are event-key deduplicated. Keep
  correlation IDs on spans rather than metric dimensions.
- Preserve explicit source JSON-RPC CORS policies when building the synthetic
  MCP transport. Loom's generated CORS handler and MCP origin validation are
  independent layers: CORS controls browser response policy, while
  `MCPCrossOriginProtection` remains the outer DNS-rebinding/CSRF check.
- Non-generated HTTP server scaffolds must retain bounded read and idle
  timeouts. Long-lived MCP SSE servers keep `WriteTimeout: 0` so a generic HTTP
  deadline cannot terminate a healthy stream.
- Generated `MCPAdapterOptions.DropIfSlow` is a typed `*bool`: nil keeps the
  safe drop-on-overflow default and an explicit false pointer enables
  backpressure. Do not restore untyped compatibility or silent fallback.
- Codegen should use partial evaluation and `NameScope` helpers rather than string surgery or runtime branching over static structure.
- Every MCP transport adaptation is owned through stable upstream section
  identifiers and evaluated generator data. Missing or duplicate expected
  sections must fail generation. Never inspect, parse, or rewrite rendered Go
  source to extend an upstream generator; add or replace a named section and
  enforce its exact cardinality instead.
- DSL/codegen/runtime internals should trust evaluated design invariants and fail fast instead of adding speculative fallback paths.
- loom-mcp DSL context failures must name the public DSL function explicitly;
  declarations that require a service `MCP(...)` contract must report that
  missing contract separately from general nesting errors.

## Command Reminders

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.8.0-alpha.14
loom version
loom gen <module-import-path>/design
loom example <module-import-path>/design
```

- Correct: `loom gen example.com/myapi/design`
- Incorrect: `loom gen ./design`

## References

- `references/repo-map.md`: source routing for repo docs and packages
- `references/runtime-contracts.md`: current runtime, planner streaming, stream profile, and agent-as-tool rules
- `references/codegen-contracts.md`: current DSL/codegen/type-ref/MCP generation rules
- `references/user-guides/runtime.md`: broader runtime narrative and examples
- `references/user-guides/toolsets.md`: toolset behavior, retry hints, injected fields, executors
- `references/user-guides/composition.md`: agent composition and child-run UX
- `references/user-guides/mcp-integration.md`: current MCP routing and caller examples
- `references/user-guides/testing.md`: testing patterns
- `references/user-guides/production.md`: production operations and deployment patterns

## Selection Rules

- Start with `references/runtime-contracts.md` or `references/codegen-contracts.md`, depending on the task.
- Use `references/repo-map.md` to jump into the right repo docs or packages.
- Use the `references/user-guides/*.md` files only when the contract files and repo docs are not enough.
- When docs and code disagree, trust the current repo code and update the skill/reference files accordingly.
