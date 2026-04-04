# Refactoring Plan

This document turns the current refactoring pressure points in `loom-mcp` into an execution plan that is safe to run in small batches.

The goal is to reduce change amplification, lower bug-fix risk, and speed up the default verification loop without changing external behavior.

## Actionability Rules

This plan is only actionable if each refactoring task meets all of these rules:

- One task owns one hotspot and one clear outcome.
- One task touches at most 1-2 production files unless the task is pure file-splitting with no behavior change.
- Each task has a written entry command and exit command before editing starts.
- Each task names the exact file(s) to shrink, split, or consolidate.
- Each task names the characterization tests to add or preserve.
- Regeneration and docs updates are triggered only when the touched boundary requires them.

If a proposed task does not meet those rules, split it again before starting.

## Working Rules

- Keep refactoring and behavior changes separate.
- Change one hotspot at a time.
- Add or tighten tests before structural edits.
- Do not hand-edit generated `gen/` code.
- Treat `design/*.go` as the source of truth.
- When a design contract changes, regenerate intentionally and verify generated churn.
- Prefer smaller helpers, narrower contracts, and explicit ownership over fallback-heavy code.

## Repo Constraints

- Use `make loom-local` for iterative local feedback when core Loom integration is involved.
- Use `make loom-remote` before CI-facing verification or commit preparation when fork parity matters.
- Run `make regen-assistant-fixture` when the assistant fixture design changes.
- User-facing DSL, runtime, or codegen changes may also require docs updates under `content/en/docs/2-loom-mcp/`.
- Do not compensate in `loom-mcp` for upstream `loom` or fork regressions. Capture the failing scenario and stop.

## Success Criteria

- Bug fixes in core runtime, registry, and integration harness code require less context switching.
- Contract-related changes touch fewer files on average.
- Mongo-backed feature stores share more infrastructure and drift less.
- Verification is fast enough that contributors run the right checks by default.
- Complexity trends down in touched files instead of rising.

## Hotspot Inventory

These are the current primary hotspots in the repo and the commands that should gate edits there.

| Hotspot | Current Pressure | Initial File Scope | Focused Verification |
| --- | --- | --- | --- |
| Integration harness | `integration_tests/framework/runner.go` is 1328 lines and mixes fixture prep, lifecycle, transport execution, and assertions | `integration_tests/framework/runner.go`, `integration_tests/framework/runner_test.go` | `go test ./integration_tests/framework -count=1` |
| Registry manager | `runtime/registry/manager.go` is 675 lines and mixes cache, discovery, federation, sync, and observability | `runtime/registry/manager.go`, `runtime/registry/manager_test.go` | `go test ./runtime/registry -count=1` |
| Runtime tool execution | `runtime/agent/runtime/tool_calls.go` is 884 lines and carries dispatch, timeout, result merge, and child-tracker behavior | `runtime/agent/runtime/tool_calls.go`, targeted runtime tests | `go test ./runtime/agent/runtime -count=1` |
| Runtime shared helpers | `runtime/agent/runtime/helpers.go` is 766 lines and mixes cloning, transcript shaping, caps logic, hook publishing, and policy helpers | `runtime/agent/runtime/helpers.go`, targeted runtime tests | `go test ./runtime/agent/runtime -count=1` |
| Runtime agent tools | `runtime/agent/runtime/agent_tools.go` is 718 lines and mixes validation, prompt rendering, nested-run setup, and output adaptation | `runtime/agent/runtime/agent_tools.go`, targeted runtime tests | `go test ./runtime/agent/runtime -count=1` |
| Runtime await queue | `runtime/agent/runtime/workflow_await_queue.go` is 636 lines and mixes queue publication, wait handling, and provided-result consumption | `runtime/agent/runtime/workflow_await_queue.go`, targeted runtime tests | `go test ./runtime/agent/runtime -count=1` |
| Mongo feature clients | Four low-level clients repeat constructor, timeout, ping, and index setup patterns | `features/{memory,prompt,runlog,session}/mongo/clients/mongo/client.go` | `go test ./features/memory/mongo/... -count=1` and same for `prompt`, `runlog`, `session` |
| Timing-sensitive tests | Sleeps exist in `integration_tests/framework`, `runtime/registry`, and a smaller set of runtime/feature tests | test files only at first | package-level `go test` commands above, repeated with `-count=3` for touched packages |

## Safety Harness

Run this before every refactoring batch:

1. Write down the exact behavior that must not change.
2. Run the focused package command for the hotspot.
3. Add characterization coverage if the target behavior is not obvious from existing tests.
4. Keep the edit small enough that the same focused command can be rerun in under a minute.

Escalate to broader verification only when the boundary requires it:

- Loom integration touched: `make loom-local`, then the focused command, then `make verify-mcp-local`.
- Assistant fixture design touched: `make regen-assistant-fixture`, then `make verify-mcp-local`.
- DSL, codegen, or user-facing generated behavior touched: regenerate intentionally, then review generated churn and update docs under `content/en/docs/2-loom-mcp/` if applicable.
- CI-facing verification before merge: `make loom-remote`, `make lint`, `make test`, and `make itest` when integration behavior changed.

## Execution Strategy

Do not run this plan phase-by-phase across the whole repo. Run it as a queue of bounded batches. Each batch should be independently reviewable and revertible.

Use this batch template:

```md
Batch:
- Goal:
- Files:
- Behavior frozen by:
- First extraction or move:
- Focused verification:
- Broader verification, if any:
- Done when:
```

## Workstream 1: Integration Harness

This is the highest-payoff starting point because it combines file size, mixed responsibilities, and timing debt.

### Batch 1A: Freeze `Runner` lifecycle behavior

- Goal: characterize server build, start, stop, and ping behavior before moving code.
- Files: `integration_tests/framework/runner.go`, `integration_tests/framework/runner_test.go`
- Behavior frozen by:
  - server startup succeeds for supported transports
  - ping waits for readiness
  - stop is safe after partial startup
- First extraction or move:
  - isolate `startServer`, `stopServer`, and `ping` support logic into a lifecycle-focused section or file
- Focused verification:
  - `go test ./integration_tests/framework -count=1`
- Done when:
  - `Runner.Run` no longer owns raw lifecycle detail
  - lifecycle helper names read as intent, not mechanism

### Batch 1B: Split fixture/example preparation from execution

- Goal: separate example cloning, regeneration, patching, and cleanup from transport execution.
- Files: `integration_tests/framework/runner.go`
- First extraction or move:
  - move `findExampleRoot`, `cloneExampleRoot`, `applySDKServerFixturePatch`, `regenerateExample`, and cleanup helpers behind a fixture-prep boundary
- Focused verification:
  - `go test ./integration_tests/framework -count=1`
- Done when:
  - fixture prep can be read without scanning transport code
  - transport execution paths no longer know fixture patching details

### Batch 1C: Split transport execution paths

- Goal: isolate JSON-RPC, SSE, CLI, and streaming execution from scenario orchestration.
- Files: `integration_tests/framework/runner.go`
- First extraction or move:
  - separate `executeJSONRPC`, `executeSSE`, streaming, and non-streaming step execution helpers from step expansion/defaulting logic
- Focused verification:
  - `go test ./integration_tests/framework -count=1`
- Done when:
  - transport-specific code is grouped by transport
  - scenario/default handling reads separately from HTTP/protocol detail

### Batch 1D: Remove fixed startup sleeps

- Goal: replace the current `time.Sleep(200 * time.Millisecond)` waits with explicit readiness checks.
- Files: `integration_tests/framework/runner.go`, related tests if needed
- First extraction or move:
  - add a polling helper around `ping` or observable server readiness
- Focused verification:
  - `go test ./integration_tests/framework -count=3`
- Done when:
  - startup/teardown sequencing does not rely on arbitrary fixed delays

## Workstream 2: Registry Manager

### Batch 2A: Freeze search, discovery, and cache behavior

- Goal: characterize current manager behavior before moving responsibilities.
- Files: `runtime/registry/manager.go`, `runtime/registry/manager_test.go`, property tests only if needed
- Behavior frozen by:
  - cache hit and cache miss behavior
  - federation include/exclude behavior
  - sync lifecycle behavior
- Focused verification:
  - `go test ./runtime/registry -count=1`

### Batch 2B: Split discovery/cache paths from sync/federation

- Goal: reduce mixed responsibilities in `Manager`.
- Files: `runtime/registry/manager.go`, new package-private files if needed
- First extraction or move:
  - separate `DiscoverToolset` cache/discovery flow from `StartSync`, `syncRegistry`, `doSync`, and federation filtering helpers
- Focused verification:
  - `go test ./runtime/registry -count=1`
- Done when:
  - one-shot operations and background sync code no longer share the same control flow block

### Batch 2C: Isolate observability from business logic

- Goal: keep event emission and metrics/tracing detail out of manager decision paths.
- Files: `runtime/registry/manager.go`, `runtime/registry/observability.go`
- First extraction or move:
  - move log/metric/tracing helpers behind narrower observability helpers already owned by `observability.go`
- Focused verification:
  - `go test ./runtime/registry -count=1`
- Done when:
  - `Manager` methods mostly express decisions and sequencing

### Batch 2D: Remove timing-based sync assertions

- Goal: replace sleeps in `manager_test.go` and cache tests with deterministic waiting.
- Files: `runtime/registry/manager_test.go`, `runtime/registry/cache_test.go`, `runtime/registry/cache_property_test.go`
- First extraction or move:
  - add polling helpers or controllable clock/wait hooks where practical
- Focused verification:
  - `go test ./runtime/registry -count=3`
- Done when:
  - registry tests do not depend on ad hoc sleep durations

## Workstream 3: Runtime Orchestration Hotspots

Do not refactor the entire runtime package at once. Process the largest orchestration files independently.

### Batch 3A: `tool_calls.go`

- Goal: split tool dispatch, timeout synthesis, and result collection into concern-based helpers.
- Files: `runtime/agent/runtime/tool_calls.go`
- First extraction or move:
  - separate dispatch helpers from result-collection helpers
  - separate timeout synthesis from merge logic
- Focused verification:
  - `go test ./runtime/agent/runtime -count=1`
- Done when:
  - `executeToolCalls` reads as orchestration rather than implementation detail

### Batch 3B: `agent_tools.go`

- Goal: split validation/rendering/setup/output adaptation paths.
- Files: `runtime/agent/runtime/agent_tools.go`
- First extraction or move:
  - separate validation and prompt-rendering helpers from nested-run request construction and output adaptation
- Focused verification:
  - `go test ./runtime/agent/runtime -count=1`
- Done when:
  - the file groups around one concern per section

### Batch 3C: `helpers.go`

- Goal: stop the shared helper file from accumulating unrelated responsibilities.
- Files: `runtime/agent/runtime/helpers.go`
- First extraction or move:
  - move hook-publication helpers away from cloning/transcript/caps helpers
  - move policy-oriented helpers away from general utility helpers
- Focused verification:
  - `go test ./runtime/agent/runtime -count=1`
- Done when:
  - helper groupings are obvious from filenames and comments

### Batch 3D: `workflow_await_queue.go`

- Goal: separate publication, wait handling, and provided-result consumption.
- Files: `runtime/agent/runtime/workflow_await_queue.go`
- First extraction or move:
  - split await queue publication from waiting/consumption flows
- Focused verification:
  - `go test ./runtime/agent/runtime -count=1`
- Done when:
  - await queue orchestration is readable without scanning payload adaptation detail

### Runtime Guardrails

- Do not include `runtime_options.go` in the first runtime cleanup batch unless a prior batch proves the need.
- Prefer new package-private files over adding more helper sections to existing hotspot files.
- If a runtime batch starts touching prompt behavior, model behavior, and workflow behavior together, the batch is too large and must be split.

## Workstream 4: Mongo Client Consolidation

The four low-level Mongo clients are similar enough to warrant consolidation, but only after their common shape is written down.

### Shared Behavior Already Visible

- Each client validates a Mongo client and database name in `New`.
- Each client applies default collection names and default timeout behavior.
- Each client creates collection wrappers and calls `ensureIndexes`.
- Each client exposes `Name`, `Ping`, and a `withTimeout` helper or equivalent behavior.

### Batch 4A: Write the parity inventory

- Goal: capture what is actually shared before extracting anything.
- Files: all four `features/*/mongo/clients/mongo/client.go`
- Output:
  - constructor parity table
  - `Ping` behavior differences
  - timeout/nil-context behavior differences
  - index setup differences
- Focused verification:
  - `go test ./features/memory/mongo/... -count=1`
  - `go test ./features/prompt/mongo/... -count=1`
  - `go test ./features/runlog/mongo/... -count=1`
  - `go test ./features/session/mongo/... -count=1`
- Done when:
  - the first extraction target is obvious and stable

### Batch 4B: Extract constructor/timeout infrastructure only

- Goal: share only the repeated infrastructure, not feature semantics.
- Files:
  - new shared internal helper package only if Batch 4A proves stable duplication
  - the four client files above
- First extraction or move:
  - default timeout resolution
  - nil-context normalization
  - common `Ping` shape
  - index bootstrap scaffolding
- Focused verification:
  - the four package commands above
- Done when:
  - shared code is materially smaller than the duplicated code it replaces

### Batch 4C: Stop if feature semantics start leaking

- Do not centralize feature-specific query logic.
- Do not centralize document encoding/decoding behavior.
- Do not centralize store-level APIs just because constructors look similar.

## Workstream 5: Contract And Generation Boundary Cleanup

This workstream is not a standalone starting point. Trigger it only when a contract, DSL, codegen, or assistant fixture change is already in flight.

### Trigger Conditions

- `design/*.go` changes
- `dsl/`, `expr/`, or `codegen/` changes
- assistant fixture design changes under `integration_tests/fixtures/assistant/`
- user-facing generated behavior changes that require docs sync

### Required Batch Shape

Each contract-boundary batch must name all of these up front:

- handwritten source of truth
- generated outputs expected to change
- fixture regeneration requirement, if any
- docs that must be checked or updated

### Standard Verification

- If fixture design changed:
  - `make regen-assistant-fixture`
  - `make verify-mcp-local`
- If DSL/codegen changed:
  - run the relevant generation command intentionally
  - review generated churn
- If user-facing behavior changed:
  - update docs under `content/en/docs/2-loom-mcp/`

### Exit Criteria

- Contract interpretation is owned in fewer places.
- Regeneration steps are explicit and reproducible.
- Small schema fixes no longer require undocumented choreography.

## Workstream 6: Verification Loop And Test Reliability

This stream runs alongside the others, but only for tests touched by active refactoring.

### Priority Order

1. `integration_tests/framework/runner.go` startup waits
2. `runtime/registry/manager_test.go` sync waits
3. `runtime/registry/cache_test.go` and `cache_property_test.go` TTL waits
4. smaller isolated sleeps in runtime and feature tests

### Rules

- Replace fixed sleeps with polling or explicit readiness conditions where possible.
- For repeated sync/timing behavior, prefer a controllable helper over longer sleeps.
- When a test still needs time-based behavior, keep the delay local and justified in a comment.
- For a touched timing-sensitive package, rerun the focused command with `-count=3` before calling the batch done.

## Recommended Execution Order

1. Workstream 1, Batch 1A
2. Workstream 1, Batch 1B
3. Workstream 1, Batch 1C
4. Workstream 1, Batch 1D
5. Workstream 2, Batch 2A
6. Workstream 2, Batch 2B
7. Workstream 2, Batch 2C
8. Workstream 2, Batch 2D
9. Workstream 3, one runtime file at a time in the order `tool_calls.go`, `agent_tools.go`, `helpers.go`, `workflow_await_queue.go`
10. Workstream 4, Batch 4A then 4B only if parity is proven
11. Workstream 5 only when contract work is already active

## First Four PRs

If this plan is executed now, the first four PRs should be:

1. Characterize and split `integration_tests/framework/runner.go` lifecycle behavior.
2. Split fixture/example preparation from transport execution in `integration_tests/framework/runner.go`.
3. Remove fixed startup sleeps in `integration_tests/framework`.
4. Characterize and split `runtime/registry/manager.go` one-shot operations from background sync.

That sequence yields the fastest reduction in file size, timing debt, and change risk.

## PR Tracking Template

Use this in PR descriptions or commit notes:

```md
Workstream:
- [ ] 1 Integration Harness
- [ ] 2 Registry Manager
- [ ] 3 Runtime Orchestration
- [ ] 4 Mongo Consolidation
- [ ] 5 Contract Boundary Cleanup
- [ ] 6 Verification Reliability

Batch:
- Goal:
- Files:
- Behavior frozen by:
- First extraction or move:

Verification:
- [ ] Focused package command
- [ ] Repeated run for timing-sensitive packages, if applicable
- [ ] Broader verification required by touched boundary
```

## Stop Conditions

- If a refactoring batch starts forcing behavior changes, stop and split the work into a separate bug-fix or feature pass.
- If a shared abstraction adds more indirection than it removes, revert and choose a smaller local refactor.
- If upstream `loom` or fork behavior blocks cleanup, capture the exact failing scenario and stop instead of compensating locally.
