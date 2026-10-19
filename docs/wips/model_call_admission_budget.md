# Explicit model-call admission budgets (#361)

## Summary

Add an opt-in `MaxModelCalls` cap per agent run. Each outer runtime `Complete` or `Stream` invocation reserves one slot through the existing atomic `RunStore.NextSeq(rootRunID, runID)`. A reservation with `Seq > MaxModelCalls` fails before invoking model middleware or the registered client. Reservations are never refunded. The cap covers planning, recovery, finalization, and summarization, including their fresh retry invocations. Child runs own independent caps. Provider-internal HTTP hops remain one logical invocation.

The effective cap is resolved once on the owning worker through a recorded run-start operation on the existing content activity, preferably carried by initial seeding. The workflow holds the recorded value and propagates it unchanged to every planner and summary activity. Refusal is sticky within an activity and produces structured terminal evidence, even if planner code catches the error. Existing canonical call records remain records of calls that reached the recorder; a refusal does not invent a provider call.

Status: accepted contract. Implementation and independent verification follow this scope.

## Problem Statement

[Issue #361](https://github.com/CaliLuke/loom-mcp/issues/361) requires a model-call admission limit because one planner activity can invoke a model repeatedly or concurrently. Tool counts, recovery turns, workflow iterations, and active time cannot bound that work. An activity-local counter resets on retry; counting completed records races and loses unknown outcomes. A new cap must work with existing durable identity, accounting, and compaction boundaries.

## Goals

- Bound runtime-owned logical model invocation reservations before generation I/O, including concurrent invocations and activity retries.
- Expose explicit design and per-run configuration with validated values and no hidden default.
- Keep one authority for admission and preserve replay, canonical call accounting, and existing tool/time policy.
- Record a machine-readable refusal reason and terminate without further model work.

## Non-Goals

No cost/token pricing, provider HTTP-hop cap, inherited tree-wide cap, reserved finalizer allowance, new orchestration engine, or compaction replacement. Direct application calls through `Runtime.ModelClient` remain undecorated and outside this run policy. User-authored code that retains a raw provider client is outside the guarantee. No new terminal-tool plan for model-budget exhaustion. No changes to existing tool/recovery cap semantics, statistical evaluation, or the policies for generic model failures.

## Current State

- The outer [callInfoClientWrapper](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/model_call_info.go:11) allocates one sequence per `Complete`/`Stream`, before all middleware. It explicitly consumes a fresh sequence when subsequent work fails. [AgentContext.ModelClient](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/agent_context.go:83) places it outside tracing, policy, interception, events, and the recorder.
- [Store.NextSeq](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runstore/runstore.go:50) allocates non-repeating ordinals independently per `(scope, runID)`. The [in-memory implementation](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runstore/inmem/inmem.go:67) uses a mutex; [Mongo](/Users/luca/code/loom-mono/loom-mcp/features/runstore/mongo/clients/mongo/content.go:53) uses atomic `FindOneAndUpdate/$inc`. A scope deletion removes counters too. In-memory storage is explicitly not durable; durable engines must use the same durable run store across workers/restarts.
- [CallRecording](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/call_recording.go) belongs to one activity attempt. It writes Started before provider execution, then stores completion/failure facts. Workflow adoption consumes only the returned activity's keys; keys and facts must remain parallel. Identity includes activity attempt, but Seq itself does not reset on a new attempt.
- [summary execution](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/summary_fold.go) uses the same wrapper with `RunID` and root `Scope`. [summaryCall](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/summary_model.go:18) invokes it afresh for each transient retry (up to three). Finalization routes through ordinary PlanResume in [workflow_finalize.go](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/workflow_finalize.go:184).
- [routed children](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/runtime_runs.go:49) and [inline children](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/tool_calls_dispatch.go:28) have independent RunIDs inside the same root scope and do not copy parent Policy.
- [AgentRoute](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/client.go:50) has identity and routing only. Caller-only processes can launch agents whose registration/default policy exists solely on a worker. [ExecuteWorkflow](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/workflow.go:66) also supports advanced direct engine entry.
- [initialCaps](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/runtime_policy_helpers.go:26) reads registered defaults during workflow execution. Reusing that pattern alone would not freeze the new cap against worker policy changes on replay. [Context start](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/context_overrides.go:36) demonstrates input snapshots and recorded child resolution, but caller context maps do not supply remote worker model-cap defaults.
- [RecordContentActivity](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/record_content.go:28) already owns typed initial context/continuation/content operations. [seedWorkflowIndex](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/runtime/record_content.go:94) can currently skip the activity for an empty run. [Workflow protocol](/Users/luca/code/loom-mono/loom-mcp/runtime/agent/api/workflow_protocol.go:17) is 19 and requires a bump and draining old runs for workflow changes.

## Proposed Design

### Admission authority

Use the existing sequence ordinal as a conservative reservation ledger. No new store interface or budget counter is required. This cap bounds invocation attempts, not the number of successful responses or actual network requests.

```text
Complete/Stream:
    seq = sharedStore.NextSeq(rootRunID, runID) // once per invocation
    on allocation failure: return unsent storage error; never invoke inner client
    attach existing CallInfo with seq and engine activity attempt
    if cap > 0 and seq > cap:
        record sticky ModelCallLimit{limit: cap, call: (runID, seq, attempt)}
        return typed model_call_cap refusal
    invoke the existing inner client chain unchanged
```

Counter allocation is the linearization point. Only ordinals 1..N can reach the chain under cap N. Requests refused by middleware, validation, context guards, cancellation, or provider failure still consume their reserved ordinal. A crash after allocation or an allocation committed with a lost response may consume a slot without a call record or provider I/O. There is no refund or reuse: uncertainty must not permit extra work. A storage error is a storage failure, not evidence that the cap was reached.

The counter may continue past N as callers attempt refused invocations. These later sequence values are refusal identities, not extra granted slots. Do not label Seq as successful/provider-call count, and do not infer the count from ListCalls. The lifetime is the run's existing root-scoped store lifetime; clearing an active scope or switching stores is outside the supported durability contract.

### Frozen worker-owned policy

Resolve the registered default and optional per-run override in a typed run-start policy operation on the agent's existing RecordContent activity. Its input names the agent and carries the optional override; its output carries the effective nonnegative cap. Execute it once before any planning, summary, or finalizer invocation. Co-locate it with initial content/continuation recording where possible; an empty run still requires the recorded resolution. Its result is not inferred from the worker registry during workflow replay.

The policy input is part of the same initial submission after BeforeRun interception, subject to validation. The recorded result is immutable for the run. Later registration overrides affect new resolutions only. Activity retries consume the cap already present in their input; they do not read registration again. A failed initial resolution can retry before any model work; the successful result becomes the authority. Direct ExecuteWorkflow and remote-route clients use the same worker path. Children resolve their own registration on their worker and use their own recorded cap.

Keep this operation orthogonal to context settings: no context fingerprint change, no model-cap field hidden in compaction settings, and no provider/model metadata lookup needed to resolve an integer policy. The existing RecordContent multi-operation routing must execute both initial policy resolution and initial content/continuation work rather than returning early after one.

## API / Contract Changes

| Surface | Supported / default | Rejected or deferred | Source and equivalence |
|---|---|---|---|
| DSL `DefaultCaps(MaxModelCalls(n))` | Positive integer cap; absent means unlimited | Explicit zero/negative rejected at design evaluation | `dsl/policy.go`, `expr/agent/policy.go`; generated default equals authored value |
| `runtime.RunPolicy.MaxModelCalls int` | Zero means no default cap, positive means bounded | Negative registration / OverridePolicy value rejected before execution; OverridePolicy zero means no change | Registration owns default; no hidden default or environment fallback |
| `api.PolicyOverrides.MaxModelCalls *int` | Nil inherits registered default; positive replaces it for this run | Explicit zero/negative rejected; disabling a registered cap per run is deferred | Alias in runtime; explicit wire codec preserves nil versus presence |
| `WithRunMaxModelCalls(n)` | Snapshots a positive override by value | Invalid value rejected by run validation before scheduling model execution | Same behavior as constructing PolicyOverrides directly |
| Recorded run-start policy | Effective cap 0 or positive, once per run | Invalid worker default/override fails before model activity | Existing RecordContent API and worker registration; local/remote/direct/child entry agree |
| Planner and summary activity inputs | Immutable resolved MaxModelCalls; zero retains unlimited admission | Malformed negative values fail unsent | Same value in start, resume, recovery, summary folds, finalizer |
| Refusal evidence | Structured `ModelCallLimit` containing limit and rejected original call identity; stable reason `model_call_cap` | No prompt, output, HTTP details, or fabricated usage; no successful/provider call implied | Same value through activity output, canonical adoption event, stored record export, and terminal error details |
| Public error classification | Typed limit error locally; non-retryable engine application error type `loom_mcp.model_call_cap` at workflow boundary | No transient/provider-retry classification | Stable across engine serialization; messages are explanatory, not classifiers |

`ModelCallLimit` is proposed as a shared policy-owned value usable by API and hook payloads, with a typed local error/sentinel. Its call identity is the existing `runstore.CallKey`, not a new experiment/request identity. Serialized fields are additive and omitted when absent. No aliases or dynamic policy-engine mutation of this cap are introduced; dynamic tool/recovery decisions cannot replenish it. The effective cap may appear alongside CapsState for inspection, but no workflow RemainingModelCalls counter is authoritative.

Authored policy projects through inspected `codegen/agent/data.go`, `data_runtime.go`, `registry_render.go`, quickstart template, policy goldens and generated agent_features design. Generated files remain generator-owned. Documentation belongs in `docs/runtime.md` and policy/DSL documentation plus the existing repository skill contracts.

## Detailed Behavior

### Counting and ownership

| Operation | Charge rule |
|---|---|
| Ordinary Complete or Stream | One reservation at entry, independently of output tokens or result |
| Stream Recv / Finalize / Close | No new reservation; same admitted invocation |
| Failed provider call, preflight refusal, invalid output, abandoned stream | Original reservation remains consumed |
| Application retries by calling Complete/Stream again | Fresh reservation |
| Engine retries an activity and executes model code again | Fresh reservations for its new invocations; earlier reservations remain consumed |
| Workflow history replay / duplicate adoption / repeated completion record write | No model invocation and no new allocation |
| Summary chunks and summaryCall transient retries | Each outer Complete consumes one; rejected partial summary never becomes a checkpoint |
| Finalization model call | Same cap; no exemption or extra allowance |
| Adapter-internal HTTP retry/continuation/structured-output repair | One outer invocation, one reservation; potentially multiple HTTP hops |
| Child agent / sibling agent | Own RunID counter and own worker default/override; parent does not donate or share reservations |
| New root run / session continuation | New RunID budget, consistent with existing per-run sequence identity |
| Raw Runtime.ModelClient or application-owned provider | Outside run-managed decoration; explicitly not protected |

For example, cap 2 permits an activity's first invocation. If it crashes after receiving the result but before completing its activity, its retry can make one further invocation, and the next is refused. This does not charge the original invocation twice: two executions could have reached the provider and both need reservations. Exact execution-once provider calls across activity retry are not claimed.

### Sticky refusal and canonical evidence

CallRecording can host a mutex-protected sticky refusal marker because it already spans all clients in one activity attempt; it must not host the budget counter. When multiple calls are refused, retain the lowest refused Seq in that activity as deterministic representative evidence. Already admitted concurrent calls may finish under their reservations; refusal must never cancel/reassign their identities or manufacture outcomes. Existing finish/stream cleanup remains mandatory.

A refused call stops before middleware and therefore has no Started/Completion pair. Preserve that fact: do not insert it in CallKeys, Facts, ProducingCall, usage, model calibration, growth, or compaction admission calculations. A reservation that failed before the recorder is likewise not a canonical call. The typed refusal identity can name a reserved sequence without asserting a started provider call.

On planner return, the sticky marker overrides a caught refusal, an earlier usable response, tool plan, await, or final message. Output carries limit evidence and all actual started keys/facts/usage from that attempt, but no successful PlanResult, recovery request, or overflow request. Complete ordinary recording cleanup first; a storage/cleanup failure remains fatal and must not be concealed as a clean policy refusal. Earlier captured content remains durable evidence but is not promoted to a terminal success.

Workflow adoption records the returned actual call keys and refusal on the existing PlannerStep boundary, then terminates with the classified non-retryable error. Validation accepts this mutually exclusive output shape. Refusal is neither recovery nor context overflow: it cannot trigger correction, compaction retry, or an automatic finalizer. Existing record validation must retain one-fact-per-actual-key and must not try to load the refused identity as a missing call record. RunRecord read/export must retain the structured refusal from its canonical event rather than relying only on a free-text lifecycle message.

Summary output follows the same rule through its existing ContextCompactionSkipped adoption boundary with kind `model_call_cap`: actual keys/facts/usage are adopted; no new checkpoint is published; the last committed view remains authoritative. After recording this evidence, the workflow terminates instead of treating it as ordinary summary degradation and moving to a planner call. A refusal on the first summary invocation is valid with zero CallKeys.

### Other limits and precedence

Existing workflow checks remain in their current order. If time/tool/recovery policy stops scheduling before a model invocation is attempted, its existing outcome applies and no reservation is made. If a model invocation obtains a rejected ordinal, `model_call_cap` takes precedence over a swallowed model error, successful planner output, context overflow/recovery, and a subsequently observed active-time expiry. Start/resume error handlers must not turn that recorded refusal into a time-budget finalizer. Cancellation or recording/storage failures still retain their existing failure semantics; do not suppress them to claim clean policy completion.

A time/tool/recovery finalizer may use the model only if its own reservation is within the cap. If refused, it terminates with model_call_cap. No additional model or configured terminal-tool plan is scheduled in response to that refusal. A run that completes successfully on its Nth reservation succeeds; exhaustion alone is not failure until another invocation requests admission. Existing bookkeeping tool exemptions do not exempt model calls made by a planner.

### Replay, durability, and deployment

The policy-resolution result and terminal activity result are recorded engine values. Workflow code does not call NextSeq or scan the store. Temporal replay consumes recorded results without provider I/O, new sequence allocation, or live default resolution. Durable activity retries/workers share the run store; persisted reservations survive restart. Unknown allocation outcome fails closed without refund, and the next attempted invocation obtains a new ordinal.

The protocol bump covers changed initial activity commands, activity outputs, adoption evidence, and terminal behavior. Historical runs are drained before deployment under the repository's existing mismatch contract. New-version recorded histories replay identically; old-version inputs remain explicitly rejected rather than silently reinterpreted. There is no promise of durability across process restart when using the documented in-memory store.

## Alternatives Considered

1. Activity-local count or completed-call scan: rejected; resets on retry or races, and does not reserve unknown outcomes.
2. New optional atomic admission store capability: unnecessary for conservative invocation-reservation semantics. It would be needed for a different product contract such as refundable/provider-only slots, but that is deferred.
3. Ingress-only default snapshot: local registration can supply defaults, but caller-only AgentRoute and direct engine entry cannot. Widening generated routes duplicates worker policy and lets runtime overrides drift. Local ingress plus worker fallback adds two authorities. Uniform recorded worker resolution preserves remote behavior and retry/replay semantics; co-location with seed recording limits scheduling overhead.
4. Root-tree budget or summary/finalizer exemption: rejected for V1. Existing policy and sequence owner is the run; exceptions would require another identity/counter authority or allow uncapped model work.

## Risks And Tradeoffs

Conservative reservations may underuse the configured allowance after local failures or lost allocator replies. This is intentional and documented; cap is a safety bound, not a guarantee of N provider attempts. Calls outside the decorated planner/summary boundary remain unbounded. An activity that ignores every error and never returns still needs existing activity/time limits; sticky refusal prevents successful adoption but cannot stop arbitrary user code. The initial worker resolution adds an activity for empty runs that previously skipped seed recording. Mongo counter durability inherits the configured database durability and shared-store requirements.

## Proof Obligations

| Behavioral matrix | Required observable proof / existing practical surface |
|---|---|
| DSL and Go parity | Absent, positive, zero, negative; override replacement and nil inheritance; DSL/expression tests, generator policy goldens, generated agent_features fixture, API JSON codec and run option validation |
| Single-activity bound | Cap N with >N Complete and Stream attempts; provider spy sees at most N, refused attempt never reaches any inner middleware; model_call_info and planner activity tests |
| Concurrent bound | Barrier-started mixed Complete/Stream clients share run across distinct context clients/activity attempts; exact accepted ordinals, at most N inner entries, race-enabled tests; existing runstore conformance |
| Failed/unknown reservations | Local rejection and provider failure consume slots; fault store commits allocation then returns error and retry cannot regain it; no phantom records/usage |
| Activity retries and restart | Persisted counter survives fresh runtime/activity attempt; earlier accepted identities are not recharged, new invocations are charged; inmem fixture for fast semantics plus Mongo replacement-store integration for durability |
| Replay | Recorded current-protocol history containing policy resolution, retry, refusal and terminal adoption replays without provider/counter calls, with changed live registration default; old protocol rejects; existing temporal recorded-history replay harness |
| Summary/finalizer | Multi-fold/retried summary shares planner counter; first-call summary rejection with no keys; no partial checkpoint; finalizer may consume last slot but never gets an extra one; existing summary/finalization fixtures |
| Child ownership | Parent and two routed/inline children share root scope but use independent counters/defaults; no accidental parent override inheritance; runtime agent child fixtures |
| Sticky evidence | Planner catches refusal and returns success/tool/await: all rejected; parallel completions retained; actual facts match actual keys; read/export sees stable refusal and no missing fabricated call; call_recording/runrecord fixtures |
| Other budgets/unset | No-cap existing behavior; Nth success remains success; time before invocation retains existing outcome; recorded model refusal plus elapsed time never schedules a finalizer; tool and recovery regressions covered by current suite |
| Failure of proof publication | Store/record/adoption failure cannot yield success or a misleading clean cap error; existing fault-injection helpers |

Required frozen-tree gates remain repository lint, test, and ci-integration, including generated fixture parity and real recorded-history replay. Qualification is not added solely for an invocation cap: statistical math and compaction qualification rules remain unchanged. A change to those rules during implementation would require revisiting scope and its gate.

## Open Questions

No product choice remains unresolved in this proposal: conservative reservation counting, per-run ownership, summary/finalizer inclusion, worker-owned default resolution, and terminal refusal are selected explicitly. Independent review should challenge whether the evidence projection and initial policy operation are sufficient in the current engine/record schemas before implementation is authorized.
