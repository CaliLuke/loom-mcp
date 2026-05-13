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

## Post-v0.50.0 Releases (v0.51.0 – v0.53.11)

Covers upstream releases newer than the original survey above.

### Should-Port (post-v0.50.0)

#### Canonical tool policy metadata and bookkeeping-aware budgets

Upstream references: `v0.53.0`, `v0.53.4`, `v0.53.10`

Why it matters:

- Generated registrations publish canonical `policy.ToolMetadata` (including an
  explicit `BudgetClass`) instead of forcing the runtime to re-derive static
  facts from `tools.ToolSpec` at evaluation time.
- Clarifies that `RunPolicy.MaxToolCalls` counts only budgeted, non-bookkeeping
  invocations, and that `TerminalRun` specs must also be `Bookkeeping`.
- Keeps successful bookkeeping-only tool turns out of planner-visible transcript
  and tool-output state.
- Keeps retryable bookkeeping failures visible to the planner so it can repair
  terminal/progress calls in the same workflow.
- Removes the transient `PlannerVisible` DSL/runtime contract that upstream
  briefly introduced in `v0.53.5`; do not port that intermediate shape.

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
- Rebuild provider transcript/tool-use entries only from runtime-admitted,
  planner-facing calls.
- Keep retry hints for retryable bookkeeping failures structured and visible
  without replaying successful bookkeeping side effects into planner state.

#### Restricted-tool terminal finalization contract

Upstream references: `v0.53.6`, `v0.53.8`

Why it matters:

- Restricted-tool runs should stay on the restricted tool path until the hard
  deadline instead of falling back to a tool-free finalization turn too early.
- Retry hints from restricted tools need to survive across subsequent turns so
  missing/invalid arguments can be repaired deterministically.
- Provider `FinalToolResult` values must be accepted as terminal run-loop output.
- Failed tool attempts should be counted consistently, including retry-hinted
  argument failures.

Why it is worth doing:

- This fork has terminal tools, `FinalToolResult`, retry hints, missing-field
  awaits, and tool caps already, but the cross-product is subtle enough to pin
  with focused regression tests before relying on it.

Relevant local files:

- `runtime/agent/runtime/workflow_turn.go`
- `runtime/agent/runtime/workflow_finalize.go`
- `runtime/agent/runtime/workflow_finish.go`
- `runtime/agent/runtime/workflow_clarification.go`
- `runtime/agent/runtime/tool_calls.go`
- `runtime/agent/planner/planner.go`

Recommended scope:

- Add regression tests for restricted tool retry hints across resume/finalize.
- Add regression tests for provider `FinalToolResult` as terminal output.
- Ensure failed tool attempt accounting treats retry-hinted argument failures
  the same way as other failed attempts.
- Keep restricted-tool finalization on the tool path until deadline/grace policy
  explicitly requires terminal finalization.

#### Tool pause transcript ordering

Upstream reference: `v0.53.11`

Why it matters:

- When a budgeted tool returns a runtime-owned pause or user-input request, the
  planner-facing tool result must be recorded before the workflow awaits input.
- Resume transcripts must preserve the original assistant `tool_use` / user
  `tool_result` ordering so providers receive valid conversation history.

Why it is worth doing:

- This fork has a richer await/confirmation path than upstream, which makes this
  a good regression target even if the exact executor envelope differs.
- It pairs naturally with the pause-aware `ToolExecutionResult` work below.

Relevant local files:

- `runtime/agent/runtime/workflow_turn.go`
- `runtime/agent/runtime/workflow_await_queue.go`
- `runtime/agent/runtime/workflow_await_wait.go`
- `runtime/agent/runtime/workflow_helpers.go`
- `runtime/agent/transcript/...`

Recommended scope:

- Add regression coverage for tool-created pause/await followed by resume.
- Assert the planner-facing tool output is appended before awaiting user input.
- Assert resumed provider transcripts preserve assistant tool-use ordering.

#### Workflow step and generated tool-registration normalization

Upstream reference: `v0.53.7`

Why it matters:

- Runtime state transitions should stay canonical across planner/tool/await
  boundaries.
- Generated tool registration and lookup should be specialized from
  generator-owned metadata instead of re-derived at runtime.
- Fan-out workflows need explicit key-event handling, while non-fan-out recipes
  should stay on a single-anchor contract.

Why it is worth doing:

- This overlaps with the `ToolMetadata` / `BudgetClass` work and should be
  reviewed during that refactor rather than treated as a separate broad rewrite.

Relevant local files:

- `runtime/agent/runtime/...`
- `runtime/agent/run/...`
- `codegen/agent/...`
- `runtime/toolregistry/...`

Recommended scope:

- Audit workflow step transitions around planner, tool, await, and terminal
  paths for one canonical representation.
- Fold generated registration specialization into the `ToolMetadata` work.
- Add tests around fan-out/key-event behavior only if the corresponding local
  concept exists in current runtime code.

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

#### Truthful `Seal` / Temporal worker activation

Upstream reference: `v0.51.1`

Why it matters:

- Makes `Runtime.Seal(...)` / Temporal `SealRegistration(...)` truthful
  activation boundaries: failed activations remain retryable instead of
  silently becoming no-op successes. Temporal worker lifecycle switches to
  explicit `worker.Start()` / `worker.Stop()` with queue-qualified fatal
  errors via `OnFatalError`.

Relevant local files:

- `runtime/agent/runtime/...`
- `runtime/agent/engine/temporal/...`

Recommended scope:

- Make `Seal` retry-honest; switch worker lifecycle to explicit
  `worker.Start/Stop` with `OnFatalError`.

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
- pre-model tool policy enforcement (`v0.47.10`)
- richer result hint template contract with typed `.Args` (`v0.49.2`, `v0.49.3`)
- Mongo driver v2 migration across memory/prompt/runlog/session stores (`v0.50.0`)
- Opus 4.7 Bedrock patch trio (`v0.53.1`, `v0.53.2`, `v0.53.3`)
- dependency-refresh half of `v0.52.1` (PR #126); loom-mcp tracks newer versions
  of Anthropic SDK, Temporal SDK/API, Goa, and OpenTelemetry
- authored DSL `Example(...)` preserved in generated tool specs (`v0.52.1`
  PR #125)
- OpenAI Responses API migration (`v0.48.0`); loom-mcp's
  `features/model/openai/client.go` uses `github.com/openai/openai-go`'s
  `responses` package
- `AssistantTurnCommittedEvent` hook + `assistant_turn` stream event surface
  and `model.CitationsPart` treated as assistant-visible in `agentMessageText`
  (`v0.51.0` PR #121). Upstream derives the new event from a separately
  durable transcript-delta runlog record so consumers gain a "canonical,
  replay-safe" guarantee distinct from the best-effort streamed
  `AssistantMessage`. loom-mcp has no transcript-delta runlog record:
  `AssistantMessageEvent` itself is already durably appended via
  `hookActivity` → `RunEventStore.Append` before stream/bus fanout, so the
  upstream split does not solve a real divergence here. The port therefore
  emits `AssistantTurnCommittedEvent` alongside `AssistantMessageEvent` from
  the no-tool finish path (`workflow_finish.go`) and the planner-driven
  finalize path (`workflow_finalize.go`) as a forward-compatibility shim so
  downstream consumers can subscribe to the canonical upstream event name.
  Memory projection and run-snapshot derivation deliberately stay on
  `AssistantMessageEvent` to avoid double-processing.
