# Goa-AI Port Priorities

Fork baseline: `e0d8d18b13c4400737569ad39ea63f3ad710fcde`

This file summarizes upstream `goadesign/goa-ai` release work after the fork point
and classifies it for `loom-mcp` as:

- must-port
- should-port
- nice-to-have

The classification is based on current `loom-mcp` code and docs, not just release
titles. Some upstream ideas are already present here under different names or
through deeper refactors, so they are intentionally omitted.

Reference: <https://github.com/goadesign/goa-ai/releases>

## Must-Port

### 1. Pre-model tool policy enforcement

Upstream reference: `v0.47.10`

Why it matters:

- Today this fork filters tool calls after planning, but does not clearly apply the
  same policy to the tool definitions advertised to the model.
- That means the planner/model can still see tools it is not actually allowed to
  execute, which creates avoidable retries and weakens the runtime contract.

Why it is a gap here:

- Per-run overrides and runtime policy decisions are applied to candidate tool
  calls in `runtime/agent/runtime/workflow_policy.go`.
- The current code does not show an equivalent pre-prompt filtering pass over
  `model.Request.Tools`.

Relevant local files:

- `runtime/agent/runtime/workflow_policy.go`
- `runtime/agent/runtime/model_wrapper.go`
- `runtime/agent/runtime/activities.go`

Recommended scope:

- Derive one canonical allowed-tool predicate per turn.
- Apply it both:
  - when building model-visible tool definitions
  - when enforcing execution
- Preserve the current policy event/audit behavior.

## Should-Port

### 1. OpenAI Responses API migration

Upstream reference: `v0.48.0`

Why it matters:

- This fork still uses Chat Completions via `github.com/sashabaranov/go-openai`.
- Upstream moved to the official Responses API and treated that as part of a
  cleaner runtime transport boundary.

Why it is worth doing:

- Better alignment with OpenAI’s current API direction.
- Reduces future adapter drift.
- Likely makes transcript/tool behavior easier to keep consistent with other
  providers over time.

Relevant local files:

- `features/model/openai/client.go`
- `go.mod`

Recommended scope:

- Replace the OpenAI adapter with an official Responses-based client.
- Revalidate tool calling, transcript mapping, and streaming behavior explicitly.

### 2. Richer result hint template contract

Upstream reference: `v0.49.2`, `v0.49.3`

Why it matters:

- This fork already has result hints, but the current template root exposes
  `.Result` and `.Bounds`, not typed `.Args`.
- Timestamp helpers are also narrower than upstream’s later version.

Why it is worth doing:

- Better UX for result previews and hook/stream rendering.
- Makes hints more expressive without forcing application-specific reshaping.

Relevant local files:

- `runtime/agent/runtime/result_preview.go`
- `runtime/agent/runtime/hints/hints.go`
- `runtime/agent/runtime/runtime_hints_sink.go`

Recommended scope:

- Extend result hint rendering to include typed args under `.Args`.
- Broaden `humanTime` / `since` helper parsing to handle alias and pointer-wrapped
  timestamp shapes robustly.
- Add regression coverage for preview rendering through hooks and stream output.

### 3. Mongo driver v2 migration

Upstream reference: `v0.50.0`

Why it matters:

- This repo still depends on `go.mongodb.org/mongo-driver v1`.
- Upstream moved memory, prompt, runlog, and session stores to v2.

Why it is worth doing:

- Keeps the storage layer current.
- Reduces future divergence in store behavior and maintenance burden.

Relevant local files:

- `go.mod`
- `features/memory/mongo/...`
- `features/prompt/mongo/...`
- `features/runlog/mongo/...`
- `features/session/mongo/...`

Recommended scope:

- Migrate all Mongo-backed stores together.
- Re-run full verification because this is broad dependency and integration work.

## Nice-To-Have

### 1. Shared tracing contract cleanup

Upstream reference: `v0.48.0`

Why it is lower priority:

- The OpenAI adapter migration is the materially important part.
- The tracing cleanup is good maintenance work, but not the main product gap in
  this fork.

Potential local surfaces:

- `runtime/temporaltrace/...`
- model provider adapters
- runtime lifecycle/error classification paths

### 2. Typed completions as a first-class surface

Upstream reference: `v0.47.8`, `v0.47.9`

Why it is lower priority:

- This is more of a product expansion than a catch-up fix.
- It introduces a larger surface area and should be adopted intentionally, not as
  a parity exercise.

Potential local surfaces:

- DSL
- codegen
- runtime/provider adapters
- quickstart/docs/integration fixtures

### 3. Stateless transcript boundary cleanup

Upstream reference: `v0.49.0`

Why it is lower priority:

- This fork already has substantial run-log and snapshot infrastructure, including
  replay-derived snapshots and a `ToolOutputs`-centric planner boundary.
- There may still be cleanup value in simplifying transcript ownership further,
  but this is not the highest-confidence gap relative to current local design.

Relevant local files:

- `runtime/agent/runtime/run_snapshot.go`
- `runtime/agent/runtime/runtime_runs.go`
- `runtime/agent/api/types.go`

### 4. Cancel-aware await edge-case hardening

Upstream reference: `v0.47.6`

Why it is lower priority:

- The current runtime already routes waits through timeout-aware receiver
  abstractions and cancellation-aware contexts.
- This is worth reviewing if real cancellation bugs show up, but it does not look
  like the clearest missing upstream work from a static pass.

Relevant local files:

- `runtime/agent/runtime/workflow_await_wait.go`
- `runtime/agent/engine/temporal/workflow_context.go`

## Post-v0.50.0 Releases (v0.51.0 – v0.53.3)

Covers upstream releases newer than the original survey above.

### Must-Port (post-v0.50.0)

#### Opus 4.7 Bedrock patch trio

Upstream references: `v0.53.1`, `v0.53.2`, `v0.53.3`

Why it matters:

- Claude Opus 4.7 on Bedrock rejects the legacy `thinking: {enabled,
budget_tokens}` shape, rejects a `temperature` field, and needs explicit
  summarized-reasoning display to stream visible thinking text.
- Without these patches, configuring any `claude-opus-4-7` inference profile
  against the Bedrock adapter fails with 400s or silently loses visible
  reasoning.

Relevant local files:

- `features/model/bedrock/...` (model matcher, inference config, streaming
  request builder)

Recommended scope:

- Extend `isAdaptiveThinkingModel` with an explicit Opus marker slice covering
  Opus 4.7 across in-region, `us.` / `eu.` / `jp.` geo, and `global.` scopes.
- Omit `temperature` from Bedrock inference config for Opus 4.7.
- Request summarized reasoning for adaptive Claude models, including on
  no-tool requests.
- Port all three together; they share the same file surface.

### Should-Port (post-v0.50.0)

#### Canonical tool policy metadata and bookkeeping-aware budgets

Upstream reference: `v0.53.0`

Why it matters:

- Generated registrations publish canonical `policy.ToolMetadata` (including an
  explicit `BudgetClass`) instead of forcing the runtime to re-derive static
  facts from `tools.ToolSpec` at evaluation time.
- Clarifies that `RunPolicy.MaxToolCalls` counts only budgeted, non-bookkeeping
  invocations, and that `TerminalRun` specs must also be `Bookkeeping`.

Why it is worth doing:

- This fork already has the must-port item for pre-model tool policy
  enforcement (`v0.47.10`). Adopting `v0.53.0` at the same time yields one
  clean policy pipeline (generated metadata → runtime predicate → budgeting)
  instead of two layered refactors.

Relevant local files:

- `runtime/agent/policy/...`
- `runtime/agent/runtime/workflow_policy.go`
- `codegen/...` (tool registration emitters, MCP registration helpers)
- `runtime/toolregistry/...`

Recommended scope:

- Emit canonical `ToolMetadata` (with `BudgetClass`) from generated
  registrations and aggregated toolsets.
- Store metadata once at registration time; remove on-demand synthesis.
- Enforce `TerminalRun => Bookkeeping` and exempt bookkeeping tools from the
  `MaxToolCalls` budget.

#### Pause-aware `ToolExecutionResult` envelope

Upstream reference: `v0.52.0`

Why it matters:

- Introduces `runtime.ToolExecutionResult` so tool-owned pauses (`api.ToolPause`
  / `api.ToolPauseClarification`) survive the executor boundary without
  polluting cumulative `ToolOutputs` history.
- Also adds `runlog.SessionReader` and Mongo-backed `ListSession` for
  session-scoped forward pagination over durable records.

Why it is worth doing:

- Cleans up the await/pause path so current-batch pause signals are not
  smuggled through tool-result history.
- Breaking executor-contract change, but self-contained.

Relevant local files:

- `runtime/agent/runtime/activities.go`
- `runtime/toolregistry/...`
- `codegen/...` (service, MCP, and registry executor emitters)
- `features/runlog/...` (+ Mongo adapter)

Recommended scope:

- Switch `ToolCallExecutor` to return `*runtime.ToolExecutionResult`.
- Provide `runtime.Executed(result)` wrapper for executors that only return a
  durable result.
- Add session-scoped runlog pagination.
- Refresh codegen goldens.

#### Canonical `assistant_turn_committed` + truthful `Seal`

Upstream references: `v0.51.0`, `v0.51.1`

Why it matters:

- Makes the durable transcript the single source of truth for final assistant
  output; adds `AssistantTurnCommittedEvent` hook and `assistant_turn` stream
  event fired only after the durable append.
- `RunCompletedEvent` and `stream.WorkflowPayload` become lifecycle-only.
- Splits seeded run-start history from appended turns so seeded history stays
  replayable without being re-emitted as fresh committed assistant output.
- Makes `Runtime.Seal(...)` / Temporal `SealRegistration(...)` truthful
  activation boundaries: failed activations remain retryable instead of
  silently becoming no-op successes. Temporal worker lifecycle switches to
  explicit `worker.Start()` / `worker.Stop()` with queue-qualified fatal
  errors via `OnFatalError`.
- Treats `model.CitationsPart` text as assistant-visible in transcripts.

Why it is worth doing:

- These two releases share one contract; port them together.
- Breaking for any downstream consumer that reads final assistant text off
  completion events.

Relevant local files:

- `runtime/agent/runtime/...` (transcript persistence, commit/stream fanout)
- `runtime/agent/hooks/...`
- `runtime/agent/stream/...`
- `runtime/agent/engine/temporal/...`
- `runtime/agent/run/...` (snapshot derivation)

Recommended scope:

- Add `AssistantTurnCommittedEvent` + `assistant_turn` stream event, emitted
  only after durable run-log append.
- Append terminal assistant messages into the transcript on terminal no-tool
  paths.
- Derive snapshot `LastAssistantMessage` from transcript replay.
- Separate seeded vs. appended transcript records; limit committed-turn
  fanout to appended records.
- Make `Seal` retry-honest; switch worker lifecycle to explicit
  `worker.Start/Stop` with `OnFatalError`.

### Nice-To-Have (post-v0.50.0)

#### Authored DSL `Example(...)` in generated tool specs + dependency refresh

Upstream reference: `v0.52.1`

Why it is lower priority:

- Hygiene work; not a correctness gap.
- May force regeneration of downstream golden snapshots.

Recommended scope:

- Prefer the last explicit top-level DSL payload `Example(...)` when
  generating `ExampleJSON`; normalize through the canonical JSON rewrite
  (including unions) so authored examples stay transport-correct.
- Keep the synthesized transport-example fallback for specs without an
  authored example.
- Fold the dependency bumps (Goa v3.26.0, Temporal SDK v1.42.0, Temporal API
  v1.62.8, Anthropic SDK v1.35.0, OpenTelemetry and `x/*`) into a general
  dependency refresh.

## Already Covered Here

These upstream themes appear already implemented in this fork and are not port
targets:

- explicit planner stream ownership guidance
- `ToolOutputs` as the planner-facing execution-history boundary
- canonical `await_confirmation.payload` contract
- registry-owned catalog / transport ownership cleanup
- single terminal `RunCompletedEvent` lifecycle contract
- health-tracker restart hardening
- JSON `omitempty` fix for generated tool payloads
- exported Bedrock tool-name sanitizer
