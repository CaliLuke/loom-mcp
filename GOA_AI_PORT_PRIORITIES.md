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

