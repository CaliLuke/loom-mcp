# Runtime Contracts

Use this file for current loom-mcp runtime behavior in this repo. Prefer it over stale external notes.

## Planner Streaming

- `PlannerContext.ModelClient(id)` returns a runtime-decorated client.
- A planner can drain the decorated stream itself with `Recv()` and `Finalize`,
  or pass it to `planner.ConsumeStream`. The stream ownership marker prevents
  duplicate planner events in the helper path.
- `planner.ConsumeStream` also recognizes runtime presentation events when a
  validated stream is not decorated, so both supported paths use the same
  durable acceptance boundary.
- `planner.ConsumeStreamWithObserver` calls a fallible observer for every
  accepted chunk variant. It keeps the same event ownership and presentation
  behavior as `ConsumeStream`.
- The observer error is the exact primary error supplied to `Finalize`.
  `StreamConsumptionError` exposes that primary error and the finalization
  result separately. Both remain available through `errors.Is` and `errors.As`.
- `ConsumeStreamWithObserver` returns the canonical `Response` only after
  literal EOF. The response remains available when finalization reports a
  cleanup failure after EOF.
- Drain to the literal `io.EOF`; wrapped EOF is a provider failure.
- Call `ValidatedStreamer.Finalize(primaryErr)` after draining or aborting.
  `Close` is cleanup-only and does not prove successful completion.
- `Response()` becomes available only after the consumer observes literal EOF;
  internal provider draining does not satisfy this boundary. The first
  `Finalize` call owns the authoritative joined result.
- Context cancellation observed before terminal acceptance rejects the stream.
  Tracing, rate limiting, and other lifecycle observers commit only from
  `Finalize`, after cleanup errors are known.
- Final tool calls and typed completions are withheld until terminal validation
  accepts the entire stream.
- Each runtime model stream starts one provisional presentation. Live text and
  thinking share its presentation ID. Stream finalization stages valid content;
  the planner activity emits exactly one accepted or discarded outcome.
- Acceptance follows planner success and one atomic canonical response write for
  all ready presentations in the activity. The committed assistant-turn payload
  carries their presentation IDs plus the authoritative response messages. If
  the planner or write fails, discard every presentation and fail the attempt;
  never retain an accepted client projection from an activity that can retry.
- Canonical presentation commits support the model output boundary rather than
  the smaller ordinary-hook payload limit. Bound the aggregate activity commit
  before staging a presentation so oversized batches never become accepted.
- When a final planner response has the same visible text as an accepted model
  presentation, the runtime owns streamed-result suppression. A missing planner
  `Streamed` flag cannot create a duplicate canonical assistant turn.
- Runtime model tool-argument fragments stay private until validation produces
  a complete tool call. They are not provisional presentation events.

## Planner Activity Retries

- The runtime schedules one logical `PlanStart` or `PlanResume` turn, but the
  engine may invoke its Go method multiple times when an activity attempt fails.
- Generated registrations allow up to three activity attempts by default.
- A late failure can repeat a completed model call or planner-owned side effect;
  planners must be retry-safe and use stable idempotency keys for non-idempotent
  external effects.
- Planner errors fail the current activity attempt. The run fails only after the
  configured activity retries are exhausted or the error is non-retryable.
- `PlanResult.RetryHint` is successful planner output used to recover from tool
  failures. It is separate from engine activity retries.

## Cancellation Classification

- The runtime classifies the complete error graph. Every leaf must be
  `context.Canceled` or a Temporal cancellation for a canceled result.
- The classifier has fixed limits for depth, visits, and children. Cycles,
  typed nil errors, panics, and oversized graphs are failures. Each decision
  uses one bounded inspection, including stable Temporal marker discovery.
- A mixed cancellation and failure graph fails the run. The returned error
  keeps the cancellation leaf and each independent failure leaf.
- The Temporal adapter makes a mixed graph non-retryable. A stable application
  error type restores cancellation evidence after Temporal failure conversion.
  This applies to activities, children, top-level waits, and restart queries.
- The Temporal adapter replaces a rejected graph with a static, non-retryable
  failure. The failure converter does not traverse the rejected graph.
- All completion paths replace invalid graphs before interceptors, hook
  classification, logging, or serialization can inspect them.
- `context.DeadlineExceeded` is a timeout failure. It is not a cancellation.
- A canceled tool activity or agent child stops the owning run. The runtime
  does not create an ordinary failed tool result for this condition.
- Telemetry suppresses context termination only when the operation context is
  also done. Mixed gRPC or Go error graphs remain visible as errors.

## Agent-As-Tool

- Agent-as-tool runs as a real child workflow, not an inline local shortcut.
- Temporal parent closure requests child cancellation; it must not terminate a
  child run as a generic failure.
- Parent and child runs are linked with a `ChildRunLinked` event.
- Parent tool results carry a `RunLink` to the child run.
- Runtime execution goes through `ExecuteAgentChildWithRoute`.
- `AgentToolConfig.Route` is required; there is no fallback to ad hoc local lookup.
- Consumer-side prompt rendering is optional and payload-only. Provider-side context belongs in the provider planner/runtime, not the consumer.
- Generated helper packages expose `NewRegistration(...)`; runtime internals build the underlying registration with `runtime.NewAgentToolsetRegistration(...)`.

## Streams

- Streams are session-owned.
- `stream.StreamProfile` controls visibility by audience.
- Child runs are linked, not flattened, by default.
- Use profile selection to shape chat, debug, or metrics views instead of changing core runtime behavior.

## Prompt Runtime

- `Runtime.PromptRegistry` stores baseline prompt specs.
- `runtime.WithPromptStore(...)` adds scoped overrides.
- Planners should render prompts through `RenderPrompt(...)` so provenance flows into model requests.
- Mongo prompt overrides persist a canonical SHA-256 label-scope fingerprint.
  Resolution uses at most one session query plus one global query against the
  fingerprint index, ordered by label specificity and creation time; it does
  not scan same-specificity candidates in Go. Mongo client startup backfills
  missing `scope_fingerprint` values before creating the compound index and
  fails construction on migration errors.
- The Mongo adapter accepts at most 15 labels per stored or requested scope so
  the exact matching-subset fingerprint set remains bounded.

## Registration Lifecycle

- `Runtime.Seal(ctx)` closes agent, toolset, and model registration before
  activating staged workers. The first submitted run closes the same registry.
- `RegisterModel` returns `ErrRegistrationClosed` after that boundary. Loom does
  not support post-seal model-client hot-swapping; replace the runtime instead.
- Registration remains closed even when the first `Seal` activation attempt
  fails. A later successful `Seal` retry is idempotent.

## Registry-Routed Provider Lifecycle

- `runtime/toolregistry/provider.Serve` owns one provider incarnation from
  registration through lease renewal, drain, local settlement, acknowledgement
  drain, and exact-token release. Applications supply typed registry callbacks;
  they do not reproduce lifecycle ordering.
- Admission revision plus canonical generated schema and wire-protocol version
  derive one deterministic registration token. The token fences calls, claims,
  output deltas, overload reports, terminal settlement, and consumer reads.
- The registry persists one admitted or rejected decision before provider
  execution for each stable run/tool-call identity. Exact replay returns that
  decision and result stream. After admission, lost routing certainty is
  terminal `outcome_unknown`, not a safe planner retry.
- Providers claim before handler execution. Pulse redelivery observes claimed
  or terminal state and cannot repeat handler work. Stale generations settle
  queued calls as `stale_registration`.
- Queue publication checks unread backlog and appends atomically. Trimming is
  garbage collection, never admission control. One absolute execution deadline
  precedes one registry-owned absolute result-retention deadline.

## Model Clients

- Runtime model clients implement `runtime/agent/model.Client`.
- Provider packages under `features/model/*` implement raw `model.Provider`.
  Runtime helpers construct the provider and wrap it with `model.NewClient`.
  Direct callers must do the same before registration.
- `Provider.Stream` returns raw `model.Streamer`; `Client.Stream` returns
  `model.ValidatedStreamer`. This difference prevents provider adapters from
  satisfying the consumer-facing client interface accidentally.
- A raw provider stream returns its provider-owned terminal `Response` only
  after literal EOF. The validated boundary reconciles it with observed chunks;
  there is no independent stream metadata channel.
- The validated client owns bounded request/response copies and checks the exact
  tool catalog, tool choice, structured output, generated completion decoder,
  token usage, output limits, and terminal stream protocol.
- `model.TokenUsage.InputTokens` excludes cached input. `CacheReadTokens` and
  `CacheWriteTokens` are disjoint counts, and non-zero `TotalTokens` equals the
  sum of input, output, cache-read, and cache-write tokens. Provider adapters
  normalize native usage before the validated client and runtime aggregate it.
- Runtime model interceptors capture that immutable request contract before
  provider work and revalidate any short-circuit or replacement response, so an
  interceptor cannot broaden the request while replacing output.
- GenAI message tracing captures the effective request after cache, tool policy,
  and interceptor replacement. It does not capture rejected output. Internal
  `runtime.tool_unavailable` payloads use only the effective tool catalog.
- Model recovery keeps a bounded set of rejected calls and selects the exact
  error returned by the planner. It accounts all billed unary rejections, even
  when the planner catches one or model calls finish out of order. Durable token
  totals use checked addition. Stage streamed text and thinking until terminal
  validation and provider cleanup both succeed. Rejected or cleanup-failed
  presentation content must not enter the canonical run log.
- Tool-call recovery must carry a non-nil, bounded, unique copy of the rejected
  request catalog. It cannot widen an active policy. Recovery-budget exhaustion
  enters the tool-free failure finalizer on both start and resume paths. A
  finalizer rejection ends the run without recursive recovery. The model and
  recovery boundaries share a limit of 256 tool definitions per request.
- Runtime model streams own planner event publication. `planner.ConsumeStream`
  detects this ownership through the stream marker and only builds its summary;
  it must not publish the same text, thinking, tool delta, or usage twice.
- Runtime presentation delivery is a best-effort client projection. Sink
  failures or ended sessions cannot fail, retry, or reclassify model work.
  Canonical stream and bus projections use detached payloads so one consumer
  cannot mutate another projection or the planner transcript.
- Provider changes must extend the shared conformance matrix for applicable
  complete/streaming, multimodal, tool-call, structured-output, typed-thinking,
  token-counting, cancellation, normalized-error, and name-codec behavior.
- Setup-time and receive-time provider throttling must preserve
  `model.ErrRateLimited` in the error chain. Adaptive rate limiting probes only
  after a stream reaches `io.EOF`; a successful stream setup is not completion.
- Gemini and Vertex support exact `model.TokenCounter` but their `Stream`
  method returns `model.ErrStreamingUnsupported`; streaming is an explicit
  provider capability despite being part of the shared client interface.
- OpenAI Responses projects tool and structured-output schemas into the
  provider strict-schema subset, rejects structured output combined with
  tools, and round-trips canonical tool names through its request-local codec.
- Canonical tool IDs may exceed provider name limits. Provider codecs must use
  deterministic byte-bounded names with a stable hash suffix, retain the
  request-scoped reverse mapping, and fail fast on collisions.
- Tool-use correlation IDs have a separate request-scoped codec. Anthropic and
  Bedrock must reserve every provider-safe replay ID before encoding, allocate
  unsafe IDs from unoccupied synthetic `tN` values, and use the same mapping for
  matching tool-use and tool-result blocks.
- Anthropic explicit cache checkpoints mark the nearest preceding cacheable
  block in the applicable system or conversation history. This search crosses
  an encoded message boundary for a leading or checkpoint-only marker.
  Encoding fails when that history has no cacheable block. It never silently
  drops the marker.
- Model middleware must not invent optional interfaces. Adaptive rate limiting
  implements `model.TokenCounter` only when the wrapped provider implements it;
  providers without exact counting remain non-`TokenCounter` after wrapping.
  `NewAdaptiveRateLimiter` keeps estimated-input-only admission.
  `NewOutputReservationAdaptiveRateLimiter` requires an exact provider count
  and positive `MaxTokens`, then reserves their overflow-safe sum under a
  version-isolated Pulse key.
- The Ollama adapter uses the local `/api/chat` endpoint for text, image,
  streaming, function-tool, native thinking, and structured-output requests. It
  maps `model.Request.Thinking` to Ollama's top-level `think` flag and surfaces
  `message.thinking` as typed `model.ThinkingPart` / `model.ThinkingChunk`, never
  as assistant text or structured-output content. Some Gemma 4 variants,
  including MLX builds, also require the model-level `<|think|>` control token at
  the start of the system prompt to activate thinking; keep that prompt concern
  separate from the adapter's response-side typed thinking contract. It should
  keep tool calls as `model.ToolCall` values so the runtime, not the provider
  adapter, owns tool execution and unknown-tool recovery.

## Tool Execution Contracts

- Runtime-owned tool specs and codecs are the schema source of truth.
- Use generated `tool_specs.Specs` and codecs for payload/result schema and encoding needs.
- Do not introspect `docs.json` at runtime.
- Tool results and retry hints should stay structured; avoid best-effort coercion when contracts fail.
- Generated validation errors use fixed framework text. Do not preserve raw Goa
  validation messages because they can contain submitted values. Bound generated
  retry hints after all field-contract enrichment and before durable storage.
- Retry-and-reflect guidance also uses fixed framework text. Do not persist the
  raw service error in its tool error or retry hint because the error can contain
  submitted values.
- Retry-and-reflect failure tracking is process-local and limited to 4096
  run/tool keys. Each failure refreshes its key. When full, the interceptor
  discards the least-recently-failed keys, which restart at attempt one. Do not
  depend on workflow-completion callbacks to clear activity-worker state.
- `runtime.tool_unavailable` is not a policy candidate. Rewrite every direct
  model call to it with the exact effective request catalog at the model
  boundary. Preserve that narrower catalog at the activity boundary when the
  policy allows more tools. Never retain or echo a model-supplied
  `available_tools` value. Preserve this internal recovery call across per-run
  tool and tag filters.
- Generated `Idempotent()` tags are metadata only. Until an explicit runtime
  replay contract exists, do not claim exactly-once execution or automatic
  duplicate suppression.
- Tool confirmation is runtime-owned. Design-time `Confirmation(...)` records
  `tools.ConfirmationSpec` on generated tool specs; runtime
  `WithToolConfirmation(...)` can require or override confirmation for specific
  tools. Confirmed calls emit `AwaitConfirmation`, resume through
  `ProvideConfirmation`, record a `ToolAuthorization` hook/stream event, execute
  only when approved, and return schema-compliant denied results when rejected.
- Tool-produced artifacts use `artifact.Content` on `planner.ToolResult.Artifacts`;
  runtime persistence converts them to workflow-safe `artifact.Ref` values on
  planner outputs, API tool events, hook payloads, and memory records.
- Model-facing artifact access is design-owned through `Toolset("artifacts",
  FromArtifacts(MaxArtifactBytes(...), MaxArtifacts(...)))`. Generated
  registration must use `runtime.NewArtifactToolsetRegistration(...)` and the
  application runtime must provide `runtime.WithArtifactStore(...)`.
- Model-facing memory access is design-owned through `Toolset("memory",
  FromMemory(MemoryMaxResults(...)))`. Generated registration must use
  `runtime.NewMemoryToolsetRegistration(...)`; `scope:"current_run"` falls back
  to `runtime.WithMemoryStore(...)`, while `scope:"indexed"` requires
  `runtime.WithMemorySearcher(...)` and otherwise returns an
  `unsupported_operation` retry hint.
- Long-term memory is a separate `memory.Service` contract with entry-shaped
  `PutEntry`, `IngestRun`, `IngestEvents`, and `Search` operations. Generated
  `FromMemory(MemoryLongTerm(), ...)` registrations pass
  `rt.MemoryService` and `rt.MemoryScopeResolver`, expose `search_memory`, and
  accept only query/filter payload fields from the model. Visibility and scope
  are design/runtime-owned and must not be controlled by tool payloads. Tool
  results use model-facing hits and must not expose raw scope, source
  references, or metadata from stored entries.
- Planner-input memory preload is opt-in through generated
  `RunPolicy.PreloadMemory`. It fills `planner.PlanInput.PreloadedMemory` and
  `planner.PlanResumeInput.PreloadedMemory` with bounded snippets without
  changing default transcript/history behavior.
- Long-term preload is opt-in through generated
  `RunPolicy.PreloadLongTermMemory`. It searches with the latest
  history-filtered user text and fills `PreloadedMemoryEntries`, leaving raw
  transcript event preload unchanged. Planners that want prompt text should use
  `memory.FormatEntriesForPrompt(...)` rather than inventing ad hoc formatting.

## Interceptors

- Interceptors are opt-in typed interfaces: run, tool, model, and event.
- Runtime-level interceptors execute before agent-scoped interceptors.
- Generated `RunPolicy(func(){ Interceptors("id") })` stores interceptor IDs;
  application code supplies implementations with `runtime.WithNamedInterceptors(...)`.
- `PlannerContext.ModelClient(id)` applies model interceptors after cache and
  tool-policy decorators and before tracing. Raw clients passed directly to
  `planner.ConsumeStream` are not wrapped by runtime model interceptors.
- `BeforeModel` may return a response to short-circuit non-streaming
  completions. Streaming calls do not synthesize a `model.Streamer` from that
  response; returning one from `BeforeModel` on `Stream` fails the call.
- Event interceptors run in `runtime.publish_hook` before `appendHookRunEvent`,
  stream publication, and hook-bus publication. Dropped events must be absent
  from all three surfaces.
- Interceptor errors short-circuit the active path.

## Workflow Composition

- Plain `Workflow` plus `Step` remains source-compatible and generated through
  `planner.NewSequentialWorkflowPlanner(...)`. Sequential step names must be
  unique because they become tool-call IDs.
- Graph helpers (`Parallel`, `Join`, `RequestInput`, `Loop`, `Branch`) generate
  `planner.NewGraphWorkflowPlanner(...)`.
- Graph workflow resume state is derived from stable node/tool-call IDs in
  `ToolOutputs`, not from `len(ToolOutputs)`.
- Parallel resume must schedule only unfinished ready nodes. Joins are virtual
  dependency barriers. Loops must be bounded by `MaxIterations`. Failed loop
  iterations permit retries. If every iteration fails, return the last
  iteration's `ToolError`.
- Graph node IDs cannot contain `#`. The planner reserves that separator for
  generated loop iteration IDs.
- Workflow dependencies must be acyclic, and dependency, branch source/target,
  and loop predicate step IDs must resolve to declared graph nodes. Enforce the
  cycle contract in both design validation and runtime planner construction.
- `RequestInput` emits `AwaitTypedInput`; answers resume via
  `Runtime.ProvideTypedInput` and enter `PlanResumeInput.TypedInputs`, not
  `ToolOutputs`.
- The branch default DSL helper is `BranchDefault` to avoid colliding with Loom's
  `Default` helper in dot-imported designs.

## Time Budgets

- `TimeBudget` and `runtime.WithRunTimeBudget(...)` bound active runtime work,
  not total elapsed wall time.
- Clarification, confirmation, typed-input, and external-tool waits pause the
  active budget and resume it when work continues.
- `policy.CapsState` tracks counter budgets only. Its legacy `ExpiresAt` field is
  deprecated and ignored; do not create a second wall-clock deadline through a
  policy decision.
- Engine-specific queue/schedule timeouts are separate from semantic run,
  planner, and tool budgets.

## Runtime Observability

- `runtime.WithMetrics` emits stable `loom_mcp.runtime.*` run, planner, and
  tool counters/timers; `runtime.WithTracer` emits planner/model spans and the
  canonical `tool.execute` semantic span.
- Planner measurements are activity-attempt-level and may repeat on retry.
  Run/tool measurements are emitted only for newly inserted canonical event
  keys, preventing ordinary hook retry double-counting.
- Keep run/session/turn/tool-call IDs on spans only. Metric dimensions are the
  bounded `agent`, `operation`, `tool`, and `status` values documented in
  `docs/runtime.md`.
- These semantic signals are engine-neutral. Temporal infrastructure spans,
  generated MCP adapter telemetry, SDK transport observation, Pulse streams,
  and the debug server remain separate surfaces.

## Event Authority And Reliability

- Hook activity payloads use JSON v2 and
  `encoding/json.FormatDurationAsNano(true)`. Duration-bearing per-run policy
  fields cross the workflow/activity boundary as integer nanoseconds while
  JSON field tags and duration map keys keep their standard behavior.
- Workflow state/ledger is the live deterministic execution authority.
- `runlog.Store` is the canonical append-only introspection record. Run-log
  append or metadata-update failure fails the hook activity so the engine can
  retry or stop; it is not best-effort.
- Stream sinks and the hook bus are observer projections with their own
  delivery contracts. High-volume partial signals may be best-effort.
- `memory.Store` is a derived raw-event projection; `memory.Searcher` indexes
  those events; `memory.Service` stores separate entry-shaped long-term memory.
- The Mongo `memory.Store` adapter appends immutable batch documents in its
  companion events collection. Reads load the legacy single-run document and
  new buckets in `created_at`, `_id` order, then stable-sort the combined events
  by timestamp. Equal timestamps retain legacy-before-new and deterministic
  bucket order. New code must not append back into the legacy document's
  unbounded `events` array.
- Pulse auto-ack subscription is suitable for ephemeral UI fanout only. Durable
  consumers use manual `Delivery` values, make processing idempotent with the
  stable event key, and acknowledge after their durable commit. Decode failures
  are manual deliveries too: dead-letter the raw payload before acknowledging,
  or leave the entry pending.
- Temporal activities start new trace roots linked to the scheduling workflow
  span. Do not model durable workflow-to-activity scheduling as ordinary
  in-process parent/child span nesting.
- `runtime.WithEngine(temporalEngine)` persists live workflow state through
  Temporal history only. It does not change the process-local defaults for
  `runlog.Store` or `session.Store`; production worker and client runtimes must
  explicitly share persistent implementations when introspection and session
  metadata must survive restart. `memory.Store` and `stream.Sink` remain
  separate projections with their own persistence and delivery contracts.
- Temporal replay does not make activity side effects exactly once. Activities
  may retry after an external side effect when successful completion was not
  recorded, so planner/tool effects must be retry-safe.

## Skills

- `runtime/mcp/skills` is the shared discovery and read path for MCP
  `SkillDirectory(...)` resources and model-facing `Toolset(FromSkills(...))`
  tools.
- `SKILL.md` frontmatter supports `id`, `name`, `description`,
  `allowed_tools`, `preload`, and `reload`. Skills without frontmatter remain
  compatible by deriving the ID from the directory name and description from the
  first heading or text line.
- Duplicate skill IDs, invalid frontmatter, unknown preload modes, and unknown
  reload modes are hard discovery errors.
- Generated `FromSkills(..., SkillPreload(...), SkillReload(...))`
  registrations wire `runtime.NewSkillToolsetRegistration(...)`; model-facing
  `list_skills` and `load_skill*` results include parsed metadata.

## MCP Progress And Resource Updates

- `runtime/mcp.ReportProgress` sends standard SDK progress notifications when
  the request includes a progress token.
- `WatchableResource` enables standard SDK subscriptions. The generated
  `SDKServer.ResourceUpdated(ctx, uri)` method sends update notifications and
  rejects unknown URIs.
- Watchable resources require persistent Streamable HTTP sessions. Reject them
  with stateless HTTP.

## Debug Server

- `runtime/agent/debug` is opt-in application code, not a DSL or generated API
  surface.
- `debug.NewServer(debug.Config{Runtime: rt})` defaults to `127.0.0.1:0`.
- Debug responses use `{data:...}` and `{error:{code,message}}` JSON
  envelopes for run snapshots, events, await state, memory, artifacts, and
  workflow counts.
- The debug server must read runtime stores without changing planner, hook,
  stream, MCP, or generated API behavior.

## Where To Verify

- `docs/runtime.md`
- `runtime/agent/runtime/agent_tools.go`
- `runtime/agent/runtime/model_wrapper.go`
- `runtime/agent/stream/stream.go`
