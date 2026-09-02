# Goa-AI Port Priorities

Reviewed through `goadesign/goa-ai` `v0.78.6` on 2026-09-01.

Reference: <https://github.com/goadesign/goa-ai/releases>

## Goal

Port upstream contracts that materially improve correctness, durability, or
verification in `loom-mcp`. Port concepts and tests in dependency order; do not
copy obsolete Goa-AI MCP transports or undo Loom-specific runtime ownership.

Generated evaluation contracts are outside this port. They will use a separate
project and a separate design.

The older survey stopped at `v0.53.11`. Its completed items are reflected in the
current tree and are no longer listed as open work here.

## Starting Contract Inventory

The following local contracts are the starting point for the port:

- `runtime/agent/model.Client` is both the provider implementation interface and
  the application-facing interface. `Response` and `Chunk` are directly
  observable without a central request contract.
- `model.Chunk` is an open struct with a string discriminator. Provider adapters
  and consumers separately validate legal field combinations.
- `model.Streamer` exposes `Recv`, `Close`, and `Metadata`. A clean `io.EOF` is
  treated as completion, and callers separately decide how to combine receive
  and close failures.
- `planner.ConsumeStream` publishes text and thinking as they arrive and returns
  tool calls accumulated before the terminal stream outcome.
- `features/model/gateway.RemoteClient` trusts normalized remote responses and
  chunks after they cross the transport.
- Typed completions apply generated codecs in `runtime/agent/completion`, but
  low-level clients, gateways, and ordinary planner output do not share that
  generated validation boundary.
- Runtime recovery uses `MaxRecoveryTurns`. One turn is one replacement
  planner activity after rejected model or tool output, and only successful
  registered domain work resets the allowance.
- Unknown model tool batches are rejected before any sibling executes. The
  replacement activity receives only bounded framework-authored guidance and
  the exact current tool catalog, never the rejected payload.
- Human and external-input awaits keep the current workflow open and resume it
  through engine signals.
- Registry provider generations, call admission, and terminal settlement are
  fenced by exact registration tokens. One durable call record owns the
  admitted/rejected decision, deadlines, result retention, and replay.

Existing strengths to preserve include generated tool codecs, typed server
data, result materializers, bounded-result validation, provider conformance,
runtime sealing, official MCP SDK ownership, Loom engine abstraction, run-log
authority, stream profiles, and real child workflows for agent-as-tool.

## Execution Order

### Active Progress — Strict Boundary and Complete-Stream Foundation

Implemented on `codex/port-goa-ai-runtime-contracts`:

- Raw `model.Provider` and consumer-facing `model.Client` now have distinct
  stream return types, so provider adapters cannot satisfy `Client` by accident.
- `model.NewClient` owns bounded request/response copies, captures an immutable
  request contract, validates unary and streaming output, and preserves native
  token counting only when the provider supports it.
- Tool identity, tool arguments, tool choice, structured output, generated
  completion decoding, usage, response shape, output limits, and terminal stream
  protocol are checked centrally.
- Validated streams reject wrapped or premature EOF, withhold tool calls and
  typed completions until terminal acceptance, expose the accepted response,
  and provide consumer-EOF-gated response access plus authoritative idempotent
  finalization with cancellation and cleanup-error preservation.
- Anthropic, Bedrock, OpenAI, Ollama, and Gemini translate provider output-limit
  reasons into the shared contract. Remote gateway clients validate again on the
  consuming side against the pre-transport request contract.
- Runtime interceptors revalidate short-circuit and replacement output against
  the pre-provider contract. Tracing and adaptive limiting commit stream
  lifecycle state only during finalization, and planner usage consumes the
  canonical response without double counting provider metadata.
- The provider conformance matrix now requires unary and streaming output-limit
  cases, the model package has an explicit coverage floor, and stress runs
  include the changed model, completion, planner, gateway, middleware, and
  provider packages.
- `model.Chunk` is a sealed sum type. Its eight value variants carry only their
  legal payload, expose a stable kind, clone mutable data at the validation
  boundary, and reject nil, pointer, or unsupported variants without panicking.
- Raw streams own a canonical terminal response and expose it only after literal
  EOF. The validation boundary reconciles provider response content, tool calls,
  usage, and stop state with the observed chunks. Stream metadata is no longer
  a second completion authority, and gateway handlers return the terminal
  response with the streamed chunks.

Still open in Batch 2: publish provisional presentation deltas with an explicit
accepted or discarded outcome. Presentation text and thinking now stay
provisional until stream validation and provider cleanup both succeed.

### Active Progress — Recovery and Atomic Registry Decisions

Implemented on `codex/port-goa-ai-runtime-contracts`:

- `MaxRecoveryTurns` now governs workflow recovery across the DSL, generated
  registrations, runtime policy, workflow state, Temporal serialization, docs,
  and fixtures. Historical Temporal payloads migrate the former consecutive
  failure fields without losing the remaining allowance.
- Model-output, generated argument, unavailable-tool, output-limit, and typed
  final-answer rejection use bounded framework-authored correction evidence.
  Unknown-tool batches execute no siblings and retain neither the rejected
  payload nor the unavailable name. Final-answer recovery disables tools.
- Recovery records are bounded and safe for concurrent planner model calls. The
  runtime selects the exact rejected call returned by the planner, accounts
  caught unary rejections, and checks all durable usage addition for overflow.
  Tool-call replacement catalogs are non-nil, bounded, unique, and cannot widen
  the active policy. Model requests and recovery catalogs share a 256-tool limit.
- Recovery-cap exhaustion on initial and resumed planning enters the tool-free
  failure finalizer. A finalizer rejection terminates without recursion. Mixed
  success-and-rejection tool batches reset and then consume one fresh allowance
  on both normal and await-queue paths.
- Generated retry-and-reflect output uses fixed framework text and does not
  persist raw service errors. The internal unavailable-tool call survives
  per-run tool and tag filters while remaining absent from policy candidates.
- Planner stream helpers detect runtime-owned event publication, so consuming a
  decorated model stream cannot duplicate transcript parts, tool deltas, or
  billed usage. Presentation events commit only after successful finalization.
  Direct unavailable-tool calls use the exact effective request catalog for
  unary output, stream chunks, and the canonical streamed response.
- Generated recovery acceptance runs on both the in-memory engine and a real
  Temporal worker, including replay-compatible payload conversion.
- Registry admission is an exact CAS generation over schema, deployment
  revision, incarnation, lease, and tombstones. Provider `Serve` now owns
  registration, renewal, draining, settlement, and release.
- Calls are durably admitted or rejected before execution. Registration tokens
  fence calls, deltas, and terminal results; replay observes the original
  decision; lost claimed authority commits `outcome_unknown`.
- Result history and provider request publication are bounded atomically in
  Redis. Result retention uses one registry-owned absolute expiration, and
  discovery plus health repair read their own committed revisions.
- Registry discovery results and authoritative keys have deterministic name
  order. Live-Redis coverage proves two-replica tombstones prevent an A-to-B-to-A
  stale resurrection. Provider recovery tests also require the serving loop to
  report cancellation and release its exact incarnation lease.
- The Docker and stress harnesses now compile registry integration-tagged tests
  instead of silently omitting them. Docker coverage proves Mongo, Pulse, and
  registry paths, while the five-pass race suite exercises live Redis recovery
  and admission transitions.

### Batch 1 — Strict Model Contract Foundation (Must Port)

Upstream references: `v0.62.0`, `v0.63.0`, `v0.64.0`, `v0.77.0`, `v0.78.1`,
`v0.78.2`, `v0.78.3`, `v0.78.4`, `v0.78.5`, `v0.78.6`.

Implement:

- Split raw `model.Provider` from validated `model.Client`.
- Construct public clients with `model.NewClient` and preserve optional
  `model.TokenCounter` capability truthfully.
- Capture an immutable `RequestContract` before provider work.
- Validate request shape, size, collection count, tool identity, tool choice,
  structured-output schema, and inference mode centrally.
- Replace open chunk structs with closed chunk variants.
- Validate unary and streaming output against the exact current request before
  canonical output reaches planners or generated completion helpers.
- Add `Response.OutputLimited` and terminal stream output-limit state.
- Add `OutputValidationError`, closed rejection kinds, bounded fingerprints,
  validated usage, private causes, and safe restoration across trusted
  transports.
- Keep provider translation failures, provider errors, cancellation, and
  cleanup failures distinct from model-output validation.

Primary local owners:

- `runtime/agent/model`
- `features/model/{anthropic,bedrock,gemini,ollama,openai}`
- `features/model/gateway`
- `features/model/middleware`
- `runtime/agent/completion`
- `runtime/agent/planner`
- `runtime/agent/runtime/model_wrapper.go`
- `testutil/provider_conformance.go`

Proof:

- Focused model contract tests cover malformed requests, size/depth bounds,
  invalid response shapes, tool-choice violations, unknown tool names,
  generated payload rejection, structured-output validation, output limits,
  usage validation, and private error rendering.
- Each provider passes the expanded shared conformance matrix under `-race`.
- A remote gateway cannot bypass the consuming client's generated validators.

### Batch 2 — Complete Streams and Validated Presentation (Must Port)

Upstream references: `v0.69.0`, `v0.74.0`, `v0.78.0`, `v0.78.5`.

Implement:

- Add `ValidatedStream.Response()` as the only canonical terminal response.
- Add serialized, idempotent `ValidatedStream.Finalize(primaryErr)`.
- Join receive, processing, observer, lifecycle, context, and provider-close
  failures without erasing independent leaves.
- Keep `Close` cleanup-only; it must not imply successful completion.
- Reject wrapped EOF, missing stop, duplicate stop, incomplete content, invalid
  usage, and post-terminal events.
- Stage streamed model presentation per invocation. Publish provisional deltas
  live, then explicitly accept or discard them when the selected response is
  committed.
- Hold finalized tool calls and typed completions until the entire stream is
  accepted.
- Make gateway stream handlers return the raw terminal response and preserve
  cleanup errors.

Proof:

- Stream matrix covers clean EOF, wrapped EOF, premature EOF, caller abort,
  cancellation after partial output, provider close failure, observer failure,
  conflicting terminal failures, output limit, and repeated finalization.
- Rejected output never becomes durable history or an executable tool call.
- Provisional UI output receives exactly one accepted or discarded outcome.

### Batch 3 — Bounded Workflow Recovery (Must Port)

Status: complete on `codex/port-goa-ai-runtime-contracts`.

Upstream references: `v0.72.0`, `v0.75.0`, `v0.76.4`, `v0.76.9`, `v0.78.1`,
`v0.78.2`, `v0.78.3`.

Implement:

- Replace consecutive tool-failure counting with `MaxRecoveryTurns` across
  DSL, expressions, generated registrations, runtime policy, workflow state,
  documentation, and fixtures.
- Define one recovery turn as one replacement planner activity after rejected
  model or tool output.
- Reset the recovery allowance only after successful budgeted domain work.
- Recover generated tool-argument failures with bounded, generated guidance
  that contains field contracts but no submitted values or raw schemas.
- Recover unavailable tool names against the exact tool list in the current
  request. Reject the complete model batch before any sibling executes.
- Recover output-limited and application-schema-invalid final answers with
  tools disabled for the replacement turn.
- Persist bounded fingerprint, correction, usage, and attempt evidence instead
  of rejected response bodies.
- Enforce the recovery catalog again when consuming planner output so it cannot
  drift between production and workflow admission.

Proof:

- Generated agent acceptance scenarios run on the in-memory and real Temporal
  engines.
- Historical/replay tests pin the workflow payload migration.
- Mixed validation plus provider/transport/cancellation failures remain
  terminal and are never downgraded to recovery.

### Batch 4 — Atomic Registry Admission and Call Decisions (Must Port)

Status: complete on `codex/port-goa-ai-runtime-contracts`.

Upstream references: `v0.66.0`, `v0.66.1`, `v0.69.0`, `v0.70.0`, `v0.71.0`.

Implement:

- Store one exact-CAS admission generation per toolset with schema fingerprint,
  deployment revision, provider incarnation, lease, and tombstoned tokens.
- Register, renew, and release the provider generation inside `Serve`.
- Fence calls, deltas, and results with the admitted registration token.
- Persist one admitted or rejected decision per run/tool-call identity before
  provider execution.
- Replaying an identical call must observe the same decision and result stream.
- Once admitted, routing or provider uncertainty becomes `outcome_unknown`; it
  must not be reclassified as safely replannable.
- Bound publication atomically against unread backlog and make trimming garbage
  collection only.
- Use one registry-owned absolute result-stream retention contract.
- Make discovery and post-registration health scheduling read their own writes.

Proof:

- Redis-backed tests cover competing replicas, rolling provider generations,
  stale tokens, A-to-B-to-A resurrection, state loss, backlog saturation,
  uncertain post-admission outcomes, and exact retry replay.

### Batch 5 — Durable Cross-Workflow Continuations (Should Port)

Upstream reference: `v0.76.11` and its follow-up continuation fixes.

Implement:

- Complete a workflow successfully when it requires clarification,
  confirmation, typed input, questions, or external tool results.
- Store an immutable, versioned suspension in application-owned persistent
  storage.
- Add `Continue` and `StartContinuation` client APIs that start a new workflow
  on the current worker release.
- Bind session, predecessor run, new run, turn, pending request, original model
  tool-call identity, runtime execution identity, and required tools exactly.
- Restore the transcript and outstanding bounded-result chains as the successor
  run's replay seed.
- Revalidate saved payloads, results, terminal policies, and tool registrations
  before scheduling successor work.
- Atomically claim a suspension once; duplicate responses must fail closed.

Proof:

- Real Temporal worker-replacement coverage proves that a continuation created
  by one worker release resumes on the next release without keeping the old
  workflow open.

### Batch 6 — Cancellation Classification (Must Port)

Upstream reference: `v0.76.15`.

Implement:

- Classify an error graph as cancellation only when every relevant leaf is a
  cancellation.
- Bound traversal and detect cycles so hostile joined or wrapped errors cannot
  hang runtime completion.
- Apply the same classification in the runtime, in-memory engine, Temporal
  adapter, one-shot execution, tool collection, and telemetry suppression.
- Cancel the owning run when a tool activity or child run is canceled. Do not
  convert cancellation into an ordinary failed tool result.
- Keep `context.DeadlineExceeded` distinct unless the owning engine reports a
  true cancellation.

Proof:

- Cancellation joined only with cancellation remains canceled.
- Cancellation joined with cleanup, provider, hook, or transport failure is a
  failed run that preserves every independent error leaf.

### Batch 7 — Bookkeeping and Deterministic Terminal Plans (Must Port)

Upstream references: `v0.53.x`, `v0.76.12`, `v0.76.13`, `v0.77.2`.

Implement:

- Add application-declared bookkeeping tools through the DSL, expressions,
  generated registrations, runtime specifications, documentation, and
  acceptance fixtures. Terminal-run tools are always bookkeeping tools.
- Charge only budgeted domain calls. A successful bookkeeping-only batch does
  not consume the tool-call allowance or force an ordinary planner resume.
- Reject a failed terminal side effect instead of reporting an apparently
  successful run.
- Add completion-tool policy and deterministic terminal plans for time,
  tool-call, and recovery limits. Validate tool ownership, confirmation
  incompatibility, canonical fixed payloads, and retry-policy compatibility.
- Execute terminal plans through the ordinary tool path with deterministic
  identities.
- Reserve the runtime finalization label, remove caller or planner attempts to
  set it, and stamp the exact final reason after policy application.
- Include terminal policy fields in the continuation schema before that schema
  is finalized.

Proof:

- DSL, generated-code, policy, in-memory, and Temporal acceptance tests cover
  budget accounting, failed terminal calls, each terminal plan, deterministic
  replay, and exact runtime-owned finalization labels.

### Batch 8 — Output-Reservation Adaptive Limiting (Should Port)

Implement:

- Add a separate adaptive-limiter constructor that reserves exact input tokens
  plus the positive requested maximum output.
- Require an exact `model.TokenCounter`, a positive output limit, and
  overflow-safe arithmetic before provider work begins.
- Use a versioned Pulse key so estimated-input and output-reservation modes
  cannot accidentally share state during a rolling upgrade.
- Preserve the existing fixed maximum burst contract.

Proof:

- Contract, unit, and Redis-backed tests cover missing or inexact counters,
  invalid limits, overflow, isolation between accounting modes, and exact
  reservation under concurrency.

## Explicit Non-Goals

- Do not port Goa-AI's MCP transport or generator stack. The official MCP Go
  SDK remains the sole wire owner in `loom-mcp`.
- Do not add an evaluation DSL or evaluation runtime in this project.
- Do not add native Claude Bedrock Messages without an application requirement
  for provider-native tool examples and a test environment that can prove its
  distinct wire contract. Converse already owns the currently required Bedrock
  capabilities.
- Do not add a Claude-on-Vertex authentication wrapper. The existing Anthropic
  client accepts the SDK's Vertex Google-auth transport, including ADC and WIF;
  add a convenience package only when a deployment needs a supported credential
  integration contract.
- Do not make agent-as-tool inline. Child agents remain real child workflows.
- Do not claim Temporal activities or registry tool calls are exactly once.
- Do not retain compatibility aliases indefinitely. Use explicit workflow and
  wire migrations with fail-fast version checks.
- Do not expose rejected model text, tool names, arguments, or raw schemas in
  public errors, telemetry, or durable correction evidence.
- Stage streamed text and thinking until terminal validation succeeds. Do not
  persist presentation content from a rejected stream.
- Build direct `runtime.tool_unavailable` payloads from the exact effective
  model request. Do not widen them to the run policy catalog later.
- Do not weaken generated codecs with repair, coercion, truncation, or heuristic
  parsing.

## Verification Ladder

Each batch starts with focused red-green contract tests and ends with:

1. `make loom-local`
2. regenerate affected outputs intentionally
3. `make verify-mcp-local`
4. `make lint`
5. `make test`
6. `make itest`
7. `make test-docker` when registry, persistence, Pulse, or worker replacement
   changed
8. `make test-stress` when concurrency or lifecycle behavior changed
9. `make loom-remote`
10. `make verify-generated`

Any red required gate blocks completion. Confirmed upstream Loom regressions are
reported upstream rather than patched around in this repository.
