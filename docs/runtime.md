# loom-mcp Runtime Reference

The `loom-mcp` runtime is the execution engine that turns your agent designs
into running systems. Its Go module path is
`github.com/CaliLuke/loom-mcp/v2`; this document uses `loom-mcp` for product
discussion and the versioned module path in code examples.

## When to Use This Guide

Read this guide when you need to:

- Bootstrap a runtime for your agents
- Understand the plan → execute → resume loop
- Configure policy enforcement, memory, and streaming
- Implement custom planners or tool executors
- Debug agent behavior or performance issues

For design-time DSL concepts, see [`docs/dsl.md`](dsl.md). For a high-level system
overview, see [`docs/overview.md`](overview.md).

---

## Mental Model

The runtime operates on three layers:

```text
┌─────────────────────────────────────────────────────────────────────┐
│                        Application Layer                            │
│  Services call generated clients to start runs and stream events    │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────┐
│                         Runtime Layer                               │
│  Orchestrates: Planners ↔ Tools ↔ Memory ↔ Hooks ↔ Policy           │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────┐
│                         Engine Layer                                │
│  Provides durable execution: Temporal, in-memory, or custom         │
└─────────────────────────────────────────────────────────────────────┘
```

**Key concepts:**

| Concept     | Purpose                                                                                             |
| ----------- | --------------------------------------------------------------------------------------------------- |
| **Runtime** | Central registry and coordinator. Holds agents, toolsets, models, hooks, and stores.                |
| **Engine**  | Workflow backend (Temporal or in-memory). Provides durable execution, activities, and signals.      |
| **Planner** | Decision-maker. Analyzes messages and returns tool calls or a final response.                       |
| **Toolset** | Collection of tools with shared execution logic. Generated from DSL or registered manually.         |
| **Hooks**   | Internal event bus. Publishes lifecycle events for memory, streaming, and telemetry.                |
| **Stream**  | External event delivery. Transforms hook events into client-facing updates (SSE, WebSocket, Pulse). |

---

## Quick Start

### Minimal Example

```go
package main

import (
    "context"
    "fmt"

    chat "example.com/assistant/gen/orchestrator/agents/chat"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func main() {
    // 1. Create runtime (in-memory engine by default)
    rt := runtime.New()

    // 2. Register agent with a planner
    if err := chat.RegisterChatAgent(context.Background(), rt, chat.ChatAgentConfig{
        Planner: &MyPlanner{},
    }); err != nil {
        panic(err)
    }

    // 3. Create typed client and run
    client := chat.NewClient(rt)
    out, err := client.Run(context.Background(), "session-1", []*model.Message{{
        Role:  model.ConversationRoleUser,
        Parts: []model.Part{model.TextPart{Text: "Hello!"}},
    }})
    if err != nil {
        panic(err)
    }

    fmt.Println("Response:", out.Final)
}
```

### Production Configuration

```go
func main() {
    // Temporal engine for durable workflow execution
    temporalEng, _ := temporal.NewWorker(temporal.Options{
        ClientOptions: &client.Options{HostPort: "temporal:7233"},
        WorkerOptions: temporal.WorkerOptions{TaskQueue: "orchestrator.chat"},
    })
    defer temporalEng.Close()

    // MongoDB stores for persistence. Construct these with the adapters under
    // features/{memory,runlog,runstore,session}/mongo.
    mongoClient := newMongoClient()
    memStore := memorymongo.New(mongoClient)
    runlogStore := newMongoRunlogStore(mongoClient)
    runStore := newMongoRunStore(mongoClient)
    sessionStore := newMongoSessionStore(mongoClient)

    // Pulse sink for real-time streaming
    pulseSink, _ := pulse.NewSink(pulse.Options{Client: newPulseClient()})

    // Construct runtime with all features
    rt := runtime.New(
        runtime.WithEngine(temporalEng),
        runtime.WithMemoryStore(memStore),
        runtime.WithRunEventStore(runlogStore),
        runtime.WithRunStore(runStore),
        runtime.WithSessionStore(sessionStore),
        runtime.WithStream(pulseSink),
        runtime.WithPolicy(basicpolicy.New()),
        runtime.WithLogger(telemetry.NewClueLogger()),
        runtime.WithMetrics(telemetry.NewClueMetrics()),
        runtime.WithTracer(telemetry.NewClueTracer()),
    )

    // Register toolsets first, then agents, then seal registration.
    if err := chat.RegisterChatAgent(ctx, rt, chat.ChatAgentConfig{
        Planner:      newChatPlanner(),
        HistoryModel: smallModelClient, // for history compression
    }); err != nil {
        panic(err)
    }
    if err := rt.Seal(ctx); err != nil {
        panic(err)
    }

    // Workers poll and execute; clients submit runs from anywhere
}
```

Temporal persists workflow history, including the transcript index, but it
does not persist the runtime's other stores. If `WithRunEventStore`, `WithRunStore`, and
`WithSessionStore` are omitted, `runtime.New` uses process-local in-memory
stores: workflow replay still works, while canonical run introspection, stored content, call records,
and session/run metadata can disappear at process restart. Use shared persistent
stores in every worker and client-only process that reads or writes those
surfaces. Configure `memory.Store` and `stream.Sink` separately when their
derived projection or delivery state must also survive worker replacement.

The Mongo run store uses explicit majority write concern and primary reads.
`runtime.WithRunStore` exposes the store on `Runtime.RunStore`. `RootRunID` on
`api.RunInput`, `run.Context`, and `api.ToolInput` identifies the root of the run
tree and the run store scope. The runtime sets it to the top-level `RunID` after
caller options; child agents inherit it. Workflow input requires a non-empty
root ID. Each Mongo content item has a 15 MiB limit. `DeleteScope` deletes all
content, call records, and sequence counters in that scope.

Before returning, tool activities store the full canonical result JSON, the
non-empty `ServerData` sidecar, and the executed payload under separate content
hashes in the root scope. The executed payload includes changes from payload
adapters, `BeforeTool` interceptors, method mapping, and injected values.
**Every worker that executes tools must share the run store.** A failed write,
including the size limit, fails the activity without returning any references.
An error-only outcome has no eligible result hash.

`ToolOutput.Refs`, `planner.ToolResult.Refs`, `api.ToolEvent.Refs`,
`planner.ToolOutput.Refs`, and `api.ToolCallOutput.Refs` carry `tools.ResultRefs`
(also available as `api.ToolResultRefs`). `Result` addresses the full eligible
result; `ServerData` and `Payload` address server-only content. Never put the
server-only hashes or bytes into model messages. The workflow retains references; hook payloads and model-facing previews remain unchanged. Inline tools,
adapted child results, and externally provided results also carry result references.

Typed consumers can call
`runtime.LoadToolResult(ctx, store, rootRunID, toolSpec, refs.Result)` to load the
eligible bytes and decode them with the generated result codec. Empty or missing
hashes fail. Tool activities return references; terminal completion decoding runs in the finish activity.

### Transcript index and planner views (protocol 12)

The workflow keeps immutable `index.MessageEntry` values and a committed
`IndexHead`, rather than accumulated messages, tool outputs, typed answers, or a
transcript ledger. `runtime/agent/transcript/index` owns deterministic JSON v2
encoding, chain verification, segment walking, and pure rendering; it depends on
model values and run-store addresses, not the runtime, planner, or engine.

`PlanActivityInput.View` is an `index.ViewRef`. It contains the committed head
and `Pending`, limited to entries added since the last planner activity. It has
no accumulated `Messages`, `ToolOutputs`, or `TypedInputs`. Seeded caller history
is stored by the run-start `RecordContent` activity as the first segments;
caller history never enters `Pending`. Segments split below the Mongo store's
15 MiB content cap.

Before invoking start, resume, or finalization planners, the loader stores pending
entries as `IndexSegment{Prev, First, Entries}`. Only successful storage produces
a new `IndexHead` in `PlanActivityOutput`, which the workflow adopts. Deterministic
bytes make crash/retry writes idempotent. A failed write cannot advance the
workflow head; missing content needed by the view fails before planner invocation.
Successful results above `transcript.MaxToolResultContentBytes` (64 KiB) render
as the same size-limit omission object using the entry's byte count, preview and
bounds, without fetching the result body. A planner can request those bytes
through `ContentLoader`; a missing omitted body fails when explicitly loaded.
The loader walks segments backwards and applies entries in their original order. Worker-local
caches retain decoded segments, rendered state, and reference-only output/answer
slices for at most 256 runs per runtime by default, with least-recently-used
eviction. Set `runtime.WithViewCacheRuns(n)` to change that bound; `n` must be
greater than zero or the option panics. Eviction removes only the cache reference,
so active loads can finish safely. The next load of an evicted run rebuilds from
its stored `IndexHead`, producing the same messages, tool outputs and typed inputs.
Warm steps append only new entries. An owned canonical message
snapshot still costs work proportional to the visible message history.

The K12 loader also supports checkpoint projections. A safe `TailFrom` cut
creates a receipt-only checkpoint covering the newly evicted prefix, with the
previous summary and citations preserved. The record is stored before its hash
can appear in activity output or a planner-step event. Receipts read only eligible
call arguments and assistant prose; hidden bookkeeping calls and server-only
content are excluded. The full receipt remains in the immutable record chain.

Checkpoint messages render under the current `Budget`, including after a model
switch reduces it. Pins use `SelectPins`, cached by record and budget. Checkpoint
messages are cached by record hash, budget and effective pins; tail messages by
committed head, tail start, clear/preview/force watermarks and preview budget. Cleared results retain the stored
result's size, tool-use ID and first error line, while `Keep` results retain their
existing projection. Hidden-call state crosses the cut. Checkpoint views pass both
Anthropic and Bedrock view grammars; views without checkpoints retain the existing
resume-time Bedrock validation. Successful activity outputs may return `ViewUpdate`;
output-validation recovery outputs never adopt it. Protocol 7 introduced these view updates, recorded
request facts and model rates, entry size metadata and stamped run settings.
The current protocol is 12 (resume budget enforcement and terminal-result
adoption across the budget). Drain older workflows before upgrading; v5–v11
fixtures retain mismatch rejection before effects. Structural templates remain version 5.

### Opt-in context compaction

Configure each agent on the runtime used by its clients:

```go
rt := runtime.New(runtime.WithContextCompaction("service.agent", runtime.ContextCompaction{
    TriggerAt: contextmeasure.Rate{Tokens: 85, Bytes: 100},
    KeepResultTools: []tools.Ident{"service.keep_result"},
}))
```

Registration rejects an agent with compaction and `HistoryPolicy`. Workflow
start also rejects stamped compaction settings against a worker registration
with `HistoryPolicy`, including separate client and worker deployments. The client
stamps owned settings into `RunInput`, overwriting caller values, and snapshots
child-agent settings for replayable child starts. Worker option changes cannot
alter an existing run. `KeepResultTools` marks result entries `Keep`; those
results retain their normal projection when clearing.

Every agent observes facts from adopted planner outputs and sends `Models`;
threshold transformations remain opt-in. `Facts` parallels
`CallKeys` and contains the recorder's durable `Completion.Facts`; `Models` sends
a sorted snapshot of the last adopted rate and usable budget for each model.
Only view-bearing calls update rates and budgets. The first planner step sends
the full history because no usable budget has been observed yet.

Before every planner step, including finalization, a pure integer/rational
decision compares the estimated view plus fixed overhead with `TriggerAt × B`.
The estimate includes system entries retained before the tail cut, including
reminders, finalization hints and stored system messages.
Tier 1 clears older non-Keep results, protecting the newest result suffix, only
when it saves at least `MinSavingTokens`. If still above the threshold, eviction
uses closed-unit cut points and `KeepRecentTokens`. Zero token settings resolve
against the smallest adopted usable budget: keep 10%, protect 15%, minimum saving
10%, capped at 20k, 40k and 20k respectively. Checkpoint budgets shrink with that
budget. Below 2,900 usable tokens, including a known window fully consumed by
its output reserve, threshold compaction stays off for the run.

If compaction remains above the threshold, the no-progress guard suppresses
another attempt until estimated growth reaches `MinSavingTokens`. Checkpoint
size uses the last adopted rendered measurement only for the same record, tail
and budget; otherwise it uses `checkpoint.MaxBytes`. Output-validation recovery outputs adopt
facts but never a `ViewUpdate` or a threshold transition. `planner_step` records
the calls' actual loader-built view, including its pins, and marks rejected
outputs with `recovery` and correction retries with `recovering`. Reconstruction
keeps the last successful view and decision state across rejected outputs,
advances its index head, observes their adopted call facts, and preserves that
view during correction retries.

`context_compacted` and `context_compaction_skipped` are canonical record events,
with keys `<type>:<run>:<step>`, bypassing `BeforeEvent`. Their `call_keys` is empty
in K12. `context_compacted` records threshold reason, tier, unit/call counts,
before/after estimates and checkpoint; skipped records report `no_progress` or
`below_min_saving`. `no_progress` is recorded once per suppression episode;
reaching the minimum-growth boundary permits another attempt and episode. Normal hooks `context_unmeasured`, `context_limits_unknown`
and `context_budget_too_small` each occur at most once per run, keyed by type and
run for every agent. These diagnostics describe adopted facts, including
rewriting planners.

The loader derives canonical pins with `SelectPins(record users, Budget)`,
recomputing only when the record or budget changes. `ViewUpdate` and `CallView`
carry that exact list; incoming pins do not override canonical selection.
Rendering these recorded ViewRef pins through RunRecord is byte-identical to
the loader, including on a cold worker and after a budget shrink. Seeded and
resumed assistant tool-use parts produce call receipts with bounded arguments;
result-only user messages are covered by those receipts and get no user index row.
Resume references require one measurement per message; clarification answers
require a present size, with zero measuring an empty answer.

### Context overflow recovery (protocol 9)

Recovery and the pre-send guard run for every agent. Threshold compaction remains
opt-in. `RunInput.ContextSettings` records threshold, recovery, guard and recall
settings for the run and child starts. Configure the existing recovery option:

```go
runtime.WithContextRecovery("service.agent", runtime.ContextRecovery{
    GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}, // zero selects 10%
    DisableRecall: false,
})
```

After recording a started call, the wrapper checks its whole request. The guard
requires known limits and an exact count or calibrated estimate, rejects above
`B × (1 + GuardMargin)`, and records `ErrContextBudget` without sending. Exact
count eligibility before calibration uses the upper clamp rate, 1/2. Unknown
limits or uncalibrated estimates leave the guard off. The guard gets one rejection
per logical planner step; it never spends the two provider recovery retries or
fails a step by itself. Failed guard recovery lets the provider decide.

A provider overflow, guard rejection or failed progress check marks the entire
activity attempt. Further calls return `ErrContextOverflowPending` before recording
or sending. The activity returns `ContextOverflow` even if its planner swallowed
the error or returned a result. It discards every presentation from that attempt,
including earlier successful calls, and returns all started CallKeys and facts.
Other non-retryable provider errors become non-retryable engine application
errors of type `provider_error.<kind>`; they execute one activity attempt.

The workflow records `context_overflow` in place of `planner_step`, adopting the
head, actual call view, settings, facts and every call key. Its sources are
`provider`, `guard` and `no_progress`; outcomes are `recovered`, `guard_off` or
`failed:<reason>`. A transformation adds `context_compacted{reason: overflow}`
with tier `clear`, `evict`, `preview` or `placeholder`. These record events bypass
`BeforeEvent`. RunRecord recomputes each recovery from durable facts and stamped
settings, and rejects presentations committed from an overflow attempt.

Pure `compaction.Recover` uses the failed request's recorded window minus reserve,
never current adapter limits. It clears results without threshold protection or
minimum savings, evicts closed units while preserving the previous summary, then
halves all newest-unit previews evenly down to 256 bytes before forcing
placeholders, including Keep results. `PreviewThrough` and `ForceClearThrough`
are monotone view watermarks. A trailing user unit stays with the latest legal
assistant cut so both provider grammars remain valid. Projection caches include
both watermarks and the preview budget; cold and warm loads produce identical
bytes. The stored entries and full results remain available to recall.

Recovery estimates the whole retried request: outside-view text and non-text,
retained entries, and `checkpoint.MaxBytes` under the recomputed budget. With
known limits and a calibrated rate the target is strictly below `B × TriggerAt`;
otherwise it halves the failed request's view text bytes. The workflow immediately
reschedules without a threshold transformation, append or summary between recovery
and retry. A HistoryPolicy runs on the loaded recovered view; a request unrelated
to that view cannot be recovered.

Every call to a model in that step must be smaller than the smallest whole-request
size that model's provider rejected. Progress uses the fixed default rate plus
non-text and reserve, so calibration and call ordinal cannot change the baseline.
A failed check names both call keys and the planner. Provider overflows permit
two recoveries; the third fails. `ContextOverflowError` preserves facts and call
keys and unwraps distinct `ErrContextOverflowNoProgress`,
`ErrContextBudgetExceeded` (minimal view), or `ErrContextOverflowBudget` sentinels.
Minimal-view errors name its first message and estimated fixed overhead.

If recorded B gives `MaxBytes < MinCheckpointBytes`, recovery is unavailable.
The workflow preserves the provider overflow in a typed error naming the byte
shortfall, emits `context_budget_too_small`, and consumes no recovery retry.
It never enlarges the B-derived budget or substitutes a projection. A guard in
that situation disables itself; only an observed provider overflow fails the step.

Protocol 9 covers overflow activity shapes, settings, watermarks, events and the
loop. The real recorded v9 history exercises an overflow followed by bounded
recovery, lookup and finalization; v5–v8 are mismatch-before-effects fixtures.
Drain older workflows before upgrading. Structural templates remain version 5.

The index types have these fields:

| Type | Fields and meaning |
| --- | --- |
| `MessageID` | One-based `uint64` entry position |
| `MessageEntry` | `ID`, `Kind`, `Hash`, `Calls`, `Structural`, `Role`, `ToolUseIDs`, `Bytes`, `NonText`, `Record`, `Call`, `Keep`, `Batch`, `Result`, `InputID`, `Output`, `Chain`; `Bytes` measures model text, `NonText` measures non-text tokens; `Origin` remains reserved |
| `StructuralEntry` | `Kind`, `ProtocolVersion`, `Text`, `Reason` |
| `TextParameter` | Either inline `Text` (at most 16 KiB) or a full-content `Hash` |
| `IndexSegment` | `Prev`, `First`, `Entries`; only the first segment has no predecessor |
| `ViewRef` | `IndexHead`, `Pending`, `Checkpoint`, `TailFrom`, `ClearedThrough`, `PreviewThrough`, `ForceClearThrough`, `Pinned`, `Budget` |
| `RenderBudget` | Current user, summary, receipt, framing and preview token budgets |
| `Origin` | Reserved continuation scope, run, position, ID and carried form |

Entry kinds are stored message, assistant transcript/call batch, tool result,
clarification, typed input, and structural operation. `Hash` addresses the
corresponding existing content encoding. `Calls` contains hashes of exactly the
dispatched `planner.ToolRequest` values, including IDs and normalized metadata.
Planner activities canonicalize and store ordinary, question, and external calls
before return. `Output` addresses reference-only planner-output metadata; `Record`
is server-only and is never rendered. `ResultProjection` contains a preview,
bounds, canonical `ContentBytes` and optional rendered error. `Batch` groups adjacent results into one user
message. `Chain` hashes the previous chain and the whole entry except `Chain`
using domain-separated deterministic JSON. `Walk` verifies content addresses,
contiguous IDs, predecessor links, and every chain hash; orphans are not committed.

Structural kinds cover unknown tools, policy/confirmation denials, synthesized
errors, cancellation, finalize results and hint, reminders, and hidden bookkeeping
calls. Their templates are versioned by protocol 5. Structural text over 16 KiB
is stored through `RecordContent`. A workflow result batch collects result bytes,
server data, output metadata, large structural text and reminders into at most
one `RecordContent` call, with ordered returned hashes. The already-stored call
supplies original payload content when there is no separately adapted payload.

`HideBookkeeping` is a later entry naming hidden tool-use IDs. It removes matching
parts from the model view, deletes emptied assistant messages, and suppresses
all-hidden result messages without changing earlier entries or segment hashes.
Rendering preserves caller history, assistant batch grouping, confirmation pairs,
transcript attachment once to the last assistant message of a turn, result order,
joined reminders, clarification/resume messages, and finalize synthetics/hints.
Each nonempty clarification answer records its actual question as an assistant
message immediately before the user answer. This applies to planner awaits,
tool pauses, and missing-fields clarification, including await-only turns. Tool
results remain before that question/answer pair. The question is stored through
`RecordContent`, with a measured, immutable `Message` entry; the answer remains a
`Clarification` entry. Empty answers add no question or visible answer. Compacted
and uncompacted views use the same entries and preserve provider role alternation
without changing historical entry rendering. Questions and external-tool answers
use assistant tool-use/user tool-result pairs recorded together when each answer
is consumed, in await queue order. Publishing all prompts first does not place
unanswered tool uses ahead of another item's clarification. Typed answers stay
outside model messages. RunRecord reconstructs these same exchanges from their content refs.

Typed entries never render as model messages. Canonical JSON message equality is
the observable contract on both Temporal and in-memory engines; codec-collapsed
Go representations are not distinct messages. Resume-only `ValidateBedrock`,
`HistoryPolicy`, and recovery corrections run after loading as before.

**Breaking planner contract.** `PlanResumeInput.ToolOutputs` retains every
non-bookkeeping output as hashes, bounded preview, bounds, error, retry hint and
metadata. `Result`, `ServerData`, and `Payload` are empty. `TypedInputs` retains
every typed answer as `ID` and `PayloadRef` with empty `Payload`; finalization
still supplies no typed inputs. Planners use `PlanResumeInput.Content`, a
root-scoped `planner.ContentLoader`:

```go
resultJSON, err := input.Content.LoadToolResult(ctx, output.Refs)
serverJSON, err := input.Content.LoadServerData(ctx, output.Refs)
payloadJSON, err := input.Content.LoadToolPayload(ctx, output)
answerJSON, err := input.Content.LoadTypedInput(ctx, answer.PayloadRef)
```

Each method returns canonical `rawjson.Message` bytes; decode with the generated
codec when typed values are needed. Request only present content: empty and
missing hashes are errors. Graph result predicates use this loader. Sequential
workflows use output identity/error metadata and need no result decode.
Successful bookkeeping outputs remain excluded. Model-facing messages and hooks
keep their existing content; server-only content stays out of model messages.

**Content producers.** `RecordContentActivity` (`<agentID>.record_content`) is
registered by `RegisterAgent` through typed engine methods. It stores ordered
items before returning any references. It also validates typed answers,
materializes provided results, and records fixed finalization calls. Planner
activities store transcript/call/correction content before returning refs.
Inline and adapted child results are recorded as one batch before index adoption.
`FinishRunActivity` stores optional final messages as `FinalRef`.

Before content-bearing signals, clients query `root_run_id` and store full
messages, answers or results in that scope. Signals carry references only:
`ResumeRequest.MessageRefs`, `ClarificationAnswer.AnswerRef`,
`ProvidedToolResult.Refs`, and `TypedInputAnswer.PayloadRef`. Workflows never use
inline signal content or read the run store. Client-only processes must share
the workers' store. Pause and confirmation decisions have no content storage step.

**Envelope guard.** `enforcePlanActivityInputBudget` measures the entire JSON
`PlanActivityInput`. Above 1,000,000 bytes it returns exported
`*runtime.PlanActivityInputBudgetError{Bytes, Limit, RunID}`, discoverable with
`errors.As`. Exactly 1,000,000 bytes is permitted. The message reports the
whole envelope, without references to removed history fields.

`make test-payload-growth`, invoked by the normal `make itest` gate, runs the
full non-short acceptance workload: 1,000 steps with 50 parallel
100 KiB results and 500 typed-await rounds with 10 KiB answers. `make test` also
runs the typed workload and the 1,000-step growth case under `-short -race`.
The 50,000-result workload exceeds the roughly 90-second short-gate budget;
it and the 5,000-step growth case run without race instrumentation through
`make itest`. Run the same test selection with `go test -race -timeout=3h` for
full race measurements.
`TestIndexPayloadAcceptance` checks every whole input against
`4096 + 40*models + 400*newEntries + 100*heldCalls` bytes. Its scripted planners
call no models. The large tool workload retains hook metadata and payload digests
in its test run log to avoid keeping another 5 GiB of hook bodies; content storage,
full hook publication and planner loading use the normal paths.
The fixed part is under 4 KiB with default configuration: the minimal input is
556 bytes, and the full tool/typed workloads reach 670/657 bytes respectively.
Caller-configured `RunContext.ToolArgs`, `Labels`, `AllowedTools` and the recovery
tool catalog remain inline and increase this fixed cost. K7 accepts these
configuration-dependent overruns; the transcript-growth allowance is unchanged.
`TestPlanActivityInputFixedFields` asserts the following measured byte counts as
regression ceilings (each configured field is measured independently):

| Configuration | Fixed bytes |
| --- | ---: |
| Default minimal input | 556 |
| `RunContext.ToolArgs`, 10 KiB | 10,792 |
| `Labels`, 10 KiB value | 10,809 |
| `AllowedTools`, 256 names | 8,235 |
| `Recovery.ToolCatalog`, 256 names | 8,533 |

`TestIndexLoaderGrowth` logs cold/warm store reads, bytes loaded, retained planner
heap (also measured with the loader cache), final-load allocations, workflow index memory/JSON size, and
whole input size at 1,000 and 5,000 steps. Its workload has one 100 KiB tool result
and one 10 KiB typed answer per step. Warm store work depends only on the new
entries; cold reconstruction, planner projections and workflow index memory
remain linear in run length. The in-memory engine has no durable Temporal history
to measure. Workflow event history also grows; `ContinueAsNew` remains follow-up
work. K8 seals trailing terminal entries and records planner adoption;
`FinishRunInput.ToolEvents` still contains one ref per call.

**Breaking change: run tool events.** `api.RunOutput` (also aliased as
`runtime.RunOutput`) replaces `ToolEvents []*api.ToolEvent` with
`ToolEventsRef runstore.Hash` and `ToolEventCount int`. The reference addresses
one stored event list in the root run scope. The list omits bytes only for parts
that have references. `ToolSummary api.ToolEventSummary` holds the fixed set of
child failure/telemetry aggregates used by agent-as-tool parents. `ChildrenCount`
comes from `ToolEventCount`; parents keep the same final result and error rules.
The output no longer carries a growing event list. `Final`, `FinalToolResult`,
`Notes`, and `Usage` retain their roles; a typed final result is bounded to
1,000,000 encoded bytes, including its envelope.

Load full events explicitly on the client, outside workflow code:

```go
events, err := runtime.LoadRunToolEvents(ctx, rt.RunStore, rootRunID, out.ToolEventsRef)
if err != nil {
    return err
}
// events includes each full Result and server-only ServerData sidecar.
```

Use the root run ID for child outputs too. An empty or missing hash is an error,
not an empty list. A completed run with no tool calls still stores an empty
list. The helper does not load executed payloads. Callers must authorize access
to the root scope because the returned events include server-only content.

**Finish activity.** Every runtime path that produces a `RunOutput` schedules
`Runtime.FinishRunActivity` once, including terminal-tool and policy-finalization
paths. Its `api.FinishRunInput` carries the omitted-bytes event list and the
selected final-result envelope and optional `Final` message. The activity stores
the message as `FinalRef` in the root scope, stores the list, resolves a
referenced completion result through `LoadToolResult` and the generated result
codec, restores its server data, enforces the final-result bound, and returns
`api.FinishRunOutput`. Unreferenced final results keep their inline bytes.
Terminal storage uses a detached workflow scope so cancellation does not prevent
retaining events on a path that returns output. Store or codec failures fail the
finish activity. Retries may execute its handler more than once.

`RegisterAgent` registers this activity automatically on the agent's workflow
queue under `runtime.FinishRunActivityName(agentID)` (`<agentID>.finish`).
Applications that register activities manually must also call
`eng.RegisterFinishRunActivity(ctx, runtime.FinishRunActivityName(agentID),
opts, rt.FinishRunActivity)`, with `opts.Queue` set to that queue and an activity
timeout. Custom engine adapters must implement `Engine.RegisterFinishRunActivity`
and `WorkflowContext.ExecuteFinishRunActivity`, using
`engine.FinishRunActivityCall`. Planner, tool, finish, and client processes must
share the root-scoped run store.

Generated method dispatchers and local MCP adapters call
`runtime.RecordToolPayload(ctx, payload)` immediately before invoking the
implementation. Custom executors that transform or inject payloads must call
this helper after those changes. Otherwise, the activity records the JSON passed
to the executor. For remote MCP tools, only the outgoing request is available;
server-side transformations across the MCP connection cannot be recorded here.
The recorder is inactive outside tool activities.

The run log and run store each use a client constructor and a store wrapper:

```go
// runlogclient: features/runlog/mongo/clients/mongo
// runlogmongo: features/runlog/mongo
// runstoreclient: features/runstore/mongo/clients/mongo
// runstoremongo: features/runstore/mongo
runlogClient, err := runlogclient.New(runlogclient.Options{
    Client: mongoClient, Database: "agents",
})
if err != nil {
    return err
}
runlogStore, err := runlogmongo.NewStore(runlogClient)
if err != nil {
    return err
}
runStoreClient, err := runstoreclient.New(runstoreclient.Options{
    Client: mongoClient, Database: "agents",
})
if err != nil {
    return err
}
runStore, err := runstoremongo.NewStore(runStoreClient)
if err != nil {
    return err
}
rt := runtime.New(
    runtime.WithRunEventStore(runlogStore),
    runtime.WithRunStore(runStore),
)
```

### Seal semantics

`rt.Seal(ctx)` is a real activation boundary, not a pure no-op. On worker-mode
engines it:

- Closes registration so any later `RegisterAgent`, `RegisterToolset`, or
  `RegisterModel` call fails with `ErrRegistrationClosed`. Register and replace
  model clients before sealing; post-seal hot-swapping is intentionally not a
  runtime contract. Closure happens even if activation later fails, so partial
  bring-up cannot smuggle handlers or model clients onto a sealed runtime.
- Activates every staged Temporal worker by calling `worker.Start()` with
  retries until activation succeeds or `ctx` ends.
- Returns the activation error verbatim (queue-qualified) when `ctx` ends
  before the worker accepts. Callers may retry `Seal` with a fresh context;
  successful retries are idempotent.
- Surfaces process-fatal worker failures through `worker.OnFatalError` rather
  than crashing the goroutine — chain your own callback if you need to escalate.

---

## Runtime Configuration

### Construction Options

Create a runtime using `runtime.New()` with functional options:

```go
rt := runtime.New(
    runtime.WithEngine(engine),          // Workflow backend (required for production)
    runtime.WithMemoryStore(store),      // Transcript persistence
    runtime.WithMemorySearcher(searcher),// Indexed/cross-run memory lookup
    runtime.WithPromptStore(promptStore),// Scoped prompt overrides
    runtime.WithStream(sink),            // Real-time event streaming
    runtime.WithPolicy(engine),          // Policy enforcement
    runtime.WithHooks(bus),              // Custom event bus (rare)
    runtime.WithLogger(logger),          // Structured logging
    runtime.WithMetrics(metrics),        // Counter/histogram recording
    runtime.WithTracer(tracer),          // Distributed tracing
    runtime.WithWorker(agentID, cfg),    // Per-agent queue placement
)
```

### Local Debug Server

`runtime/agent/debug` provides an explicit development-only HTTP server for
local run inspection:

```go
srv, err := debug.NewServer(debug.Config{
    Runtime: rt,
    Addr:    "127.0.0.1:0",
})
if err != nil {
    return err
}
if err := srv.Start(); err != nil {
    return err
}
defer srv.Shutdown(context.Background())
```

The server is disabled by default and is not generated into service, MCP, or
agent APIs. The default bind address is `127.0.0.1:0`. It returns JSON
envelopes for run snapshots, run events, await state, memory, artifacts, and
workflow event counts under `/runs/{id}`.

When options are omitted, the runtime uses sensible defaults:

| Option                | Default                                                |
| --------------------- | ------------------------------------------------------ |
| Engine                | In-memory (synchronous, non-durable)                   |
| MemoryStore           | None (transcripts not persisted)                       |
| MemorySearcher        | None (indexed memory search unavailable)               |
| PromptStore           | None (baseline prompt specs only, no scoped overrides) |
| Stream                | None (no external event delivery)                      |
| Policy                | None (all tools allowed, caps from agent registration) |
| Hooks                 | In-process bus                                         |
| Logger/Metrics/Tracer | No-op implementations                                  |

`runtime.WithWorker(...)` is intentionally narrow: it controls agent placement
(`Queue`) only. Semantic planner and tool attempt budgets come from the DSL
(`RunPolicy.Timing`) or per-run overrides (`runtime.WithTiming(...)`). If you
use the Temporal engine and need queue-wait or liveness tuning, configure those
mechanics on `temporal.Options.ActivityDefaults` when constructing the engine.

### Prompt Registry and Overrides

The runtime always initializes `Runtime.PromptRegistry`. Prompt management has two layers:

- **Baseline specs**: register immutable `prompt.PromptSpec` definitions in memory.
- **Scoped overrides**: optionally resolve arbitrary label and session scopes
  through `prompt.Store` (`runtime.WithPromptStore(...)`). Matching session
  scopes outrank non-session scopes; more matching labels outrank fewer; equal
  specificity uses the newest override. Each scoped label must be present with
  the same value; an empty value does not match an absent label.

The Mongo adapter persists a versioned SHA-256 `scope_fingerprint` and performs
at most one indexed session lookup plus one indexed global lookup, each limited
to the most specific/newest record. Mongo scopes accept at most 15 labels so
the exact matching-subset set remains bounded. On startup the Mongo client
backfills missing `scope_fingerprint` values from `scope_labels` before creating
the compound index; migration errors and legacy scopes above the bound fail
construction rather than silently hiding overrides. Normal resolution remains
index-only.

This registry is the internal runtime prompt system Loom uses while executing planners and agents.
Registering a prompt here makes it available to Loom runtime code. MCP clients see prompts declared
with `StaticPrompt(...)` and `DynamicPrompt(...)`, which expose `prompts/list` and `prompts/get` on
generated MCP servers. A single-message `StaticPrompt(...)` can opt into both surfaces with
`RuntimePrompt(...)`; generated MCP packages then expose `RegisterRuntimePrompts(reg)` to register
matching baseline `prompt.PromptSpec` values from the same design declaration.

Generated MCP servers also expose design-declared `SkillDirectory(...)` roots as
`skill://` resources. Use that when MCP clients should discover local agent
skills through `resources/list` and read `SKILL.md`, `_manifest`, or supporting
files through `resources/read`.

```go
import (
    mcpassistant "example.com/assistant/gen/mcp_assistant"
    promptmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo"
    clientmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo/clients/mongo"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

mongoClient, _ := clientmongo.New(clientmongo.Options{
    Client:     rawMongoClient, // *mongo.Client from go.mongodb.org/mongo-driver/v2/mongo
    Database:   "aura",
    Collection: "prompt_overrides",
})
promptStore, _ := promptmongo.NewStore(mongoClient)

rt := runtime.New(
    runtime.WithPromptStore(promptStore),
)

_ = rt.PromptRegistry.Register(prompt.PromptSpec{
    ID:       "aura.chat.system",
    AgentID:  "orchestrator.chat",
    Role:     prompt.PromptRoleSystem,
    Template: "You are {{ .AssistantName }}.",
})

// For design-declared shared MCP/runtime prompts:
_ = mcpassistant.RegisterRuntimePrompts(rt.PromptRegistry)
```

Render prompts from planners through `PlannerContext.RenderPrompt(...)`. The result includes rendered
text and a versioned `PromptRef` for provenance.

### Two Deployment Patterns

**Worker process** — Registers agents and executes workflows:

```go
rt := runtime.New(runtime.WithEngine(temporalWorker))

// Register agents with planners
if err := chat.RegisterChatAgent(ctx, rt, chat.ChatAgentConfig{
    Planner: myPlanner,
}); err != nil {
    panic(err)
}

// Workers poll task queues and execute runs
```

**Client-only process** — Submits runs without local execution:

```go
rt := runtime.New(runtime.WithEngine(temporalClient))

// No registration needed; use generated client with route info
client := chat.NewClient(rt)
out, err := client.Run(ctx, "session-1", msgs)
```

The generated `NewClient` function embeds the route (workflow name, task queue) so
client-only processes can submit runs to remote workers.

Worker and client-only runtimes do not share in-memory defaults. In a
multi-process deployment, give both runtimes the same persistent
`WithRunEventStore(...)` and `WithSessionStore(...)` implementations. Temporal
history is the workflow-recovery authority; it is not a replacement for the
runlog, session store, transcript projection, or client stream.

---

## The Plan → Execute → Resume Loop

Every agent run follows this lifecycle:

```text
Start ──► PlanStart ──► Tool Calls? ──► Execute Tools ──► PlanResume ──► ...
                │                                              │
                │                                              │
                └──► Final Response ◄──────────────────────────┘
```

1. **Start** — `client.Run()` or `client.Start()` creates a workflow
2. **PlanStart** — Planner receives messages and decides: answer or call tools?
3. **Execute** — Tools run as activities (parallel by default)
4. **PlanResume** — Planner receives tool results and decides next step
5. **Repeat** — Loop continues until planner returns a `FinalResponse`

### Workflow Contracts

- **Workflow protocol versions must match.** `api.WorkflowProtocolVersion` is
  currently `5` and versions the agent workflow's inputs, outputs, signals, and
  state. The runtime stamps it after caller run options on top-level and child
  inputs. Workflow entry and history replay reject a different or missing
  version before interceptors, activities, or commands, returning
  `*api.WorkflowProtocolMismatchError` (matching
  `api.ErrWorkflowProtocolMismatch`). Temporal reports the original message as
  the non-retryable application failure `loom_mcp.workflow_protocol_mismatch`,
  regardless of workflow retry policy. Every workflow-code change must bump the
  version and add a replay test. **Drain running workflows before deploying a
  new version**; there is no migration of running executions.

- **SessionID is required.** `Start` fails fast if `SessionID` is empty.
- **Run IDs have one owner.** Sessionful starts reserve their run ID before asking
  the engine to start. Another start cannot overwrite an existing run's metadata.
  A failed admission retains that ID; submit a new run ID for a new attempt.
- **Start errors preserve acceptance uncertainty.** An engine error matching
  `engine.ErrWorkflowStartRejected` settles only the owning pending reservation
  as failed. Unknown transport outcomes, unclassified deadlines, and
  existing-execution conflicts remain pending for reconciliation. Custom engines should wrap this
  sentinel only when they can prove rejection before acceptance.
- **Custom session stores must implement atomic admission.** Implement
  `ReserveRun` and conditional `RejectRun` alongside existing metadata operations.
- **Child admission is replay-safe.** A child reservation and its parent link
  commit atomically before submission. An immediate, definite start rejection
  fails that pending child while retaining its link. A failure after acceptance
  follows ordinary run completion. Temporal asynchronous child-start failures
  are not classified as immediate rejections.
- **Agents must register before runs start.** Registration closes after the first
  run to maintain worker determinism.
- **Tool results flow through codecs.** The runtime decodes results centrally and
  provides typed values to planners and hooks.

### Tool payload codecs and defaults (Feature)

Tool payloads are decoded using a two-step model shared by the generated transport/codegen layer:

1. **Decode JSON into a helper “decode‑body” type** with pointer fields, so the codec can
   distinguish **missing** from **zero** and return precise validation issues.
2. **Transform helper → final payload** using `codegen.GoTransform`.

For tool payloads, the generated payload struct uses **default‑aware field shapes**:
optional primitives with defaults become **values** (non‑pointers). During step (2), the transform
generator injects defaults when helper fields are nil.

This is a hard codegen contract: any generated transforms that read tool payload fields must use
matching AttributeContext default semantics, or the generated code may contain invalid nil checks or
assignments and fail to compile.

See [`docs/tool_payload_defaults.md`](tool_payload_defaults.md) for the full contract.

---

## Planner Contract

Planners implement the decision logic for agents. The runtime invokes planners through
activities and feeds results back into the workflow loop.

### Interface

```go
type Planner interface {
    PlanStart(ctx context.Context, input *PlanInput) (*PlanResult, error)
    PlanResume(ctx context.Context, input *PlanResumeInput) (*PlanResult, error)
}
```

**PlanStart** receives the initial messages; **PlanResume** receives messages plus
recent tool results. Both return a `PlanResult` containing tool calls, a final
response, or an await request.

`PlanStart` and `PlanResume` are workflow activities, so “one planner turn” does
not imply one Go method invocation. Generated registrations use an infrastructure
retry policy with up to three attempts by default. If an attempt fails after a
model call, event hook, or other side effect, the engine may invoke the same
planner method again with the same logical input. Planner implementations must be
retry-safe: avoid direct non-idempotent side effects, or protect them with stable
idempotency keys. A returned error fails the current activity attempt; the run
fails only after the configured retries are exhausted or the error is
non-retryable. `PlanResult.RetryHint` is successful planner output for recovering
from tool failures and does not control activity retries.

### PlanInput and PlanResumeInput

```go
type PlanInput struct {
    Messages   []*model.Message      // Conversation history
    RunContext run.Context           // Run-level identifiers and labels
    Agent      PlannerContext        // Runtime services (memory, models, reminders)
    Events     PlannerEvents         // Streaming event emitter
    Reminders  []reminder.Reminder   // Active system reminders
}

type PlanResumeInput struct {
    Messages    []*model.Message
    RunContext  run.Context
    Agent       PlannerContext
    Events      PlannerEvents
    ToolOutputs []*ToolOutput         // Reference + preview, no inline result bytes
    TypedInputs []TypedInputOutput    // ID + PayloadRef, no inline answer
    Content     ContentLoader         // Load full content in the root scope
    Finalize    *Termination          // Non-nil when runtime forces finalization
    Reminders   []reminder.Reminder
}
```

### PlanResult

```go
type PlanResult struct {
    ToolCalls     []ToolRequest    // Tools to execute (empty for final response)
    FinalResponse *FinalResponse   // Terminal assistant message
    Streamed      bool             // True if text was already streamed via Events
    Await         *Await           // Pause for clarification or external tools
    RetryHint     *RetryHint       // Guidance after tool failures
    Notes         []PlannerAnnotation
}
```

### PlannerContext

`PlannerContext` provides read-only access to runtime services:

```go
type PlannerContext interface {
    ID() agent.Ident                      // Agent identifier
    RunID() string                        // Current run identifier
    Memory() memory.Reader                // Read prior turn history
    Logger() telemetry.Logger             // Structured logging
    Metrics() telemetry.Metrics           // Counters and histograms
    Tracer() telemetry.Tracer             // Distributed tracing
    State() AgentState                    // Ephemeral per-run key-value store
    ModelClient(id string) (model.Client, bool)  // LLM client lookup
    RenderPrompt(ctx context.Context, id string, data any) (*prompt.PromptContent, error)
    AddReminder(r reminder.Reminder)      // Register backstage guidance
    RemoveReminder(id string)             // Clear a reminder
}
```

### PlannerEvents

`PlannerEvents` emits streaming updates that the runtime captures and publishes:

```go
type PlannerEvents interface {
    AssistantChunk(ctx context.Context, text string)
    PlannerThinkingBlock(ctx context.Context, block model.ThinkingPart)
    PlannerThought(ctx context.Context, note string, labels map[string]string)
    UsageDelta(ctx context.Context, usage model.TokenUsage)
}
```

### Deterministic Workflow Composition

For fixed, model-free coordination, generated workflows use planner implementations
instead of bypassing the runtime loop.

`planner.NewSequentialWorkflowPlanner(...)` remains the source-compatible path for
plain `Workflow` plus `Step` declarations. It emits authored tool steps one at a
time.

When `PlanResumeInput.Finalize` is set, both built-in workflow planners return
their configured `FinalMessage` without scheduling more tools or input requests.
This applies to time, tool, and failure caps, even after a failed tool call.
Use a final message that remains accurate when the workflow stops before all steps complete.

`planner.NewGraphWorkflowPlanner(...)` powers graph workflows with parallel nodes,
join barriers, typed human-input nodes, branch targets, and bounded loops. It
derives tool completion from stable node/tool-call IDs in accumulated
`ToolOutputs`, and typed-input completion from `PlanResumeInput.TypedInputs`, so
resuming after partial parallel completion schedules only the remaining ready nodes.
Graph node IDs cannot contain `#` because loop iterations use `<node>#<iteration>` IDs.
A failed loop iteration permits the next attempt. If all iterations fail, the planner
returns the last iteration's `ToolError`.
Branch selection deactivates the entire unselected path: a nested branch on a
not-taken path never resolves, and its own targets are skipped transitively,
while a node that is also the target of a selected branch still runs.

Typed human-input nodes emit `AwaitTypedInput` and resume through
`Runtime.ProvideTypedInput`; typed answers are kept separate from tool execution
history.

Cross-layer changes that span the agent DSL, generated registration, and runtime
behavior should extend `integration_tests/fixtures/agent_features`. That fixture
is the acceptance proof for model-facing artifacts, memory, skills, interceptors,
retry-and-reflect, workflow graphs, typed input, and debug endpoint visibility;
package-level tests remain the owner for narrower DSL, codegen, planner, runtime
toolset, and debug-server contracts.

### Model-call identity

Every `Complete` and `Stream` invocation through
`PlannerContext.ModelClient(id)` receives `model.CallInfo` in its context before
model middleware, interceptors, or the registered client run. Read it with
`model.CallInfoFrom(ctx)`. It contains `AgentID`, `RunID`, `RootRunID`, `SessionID`,
`TurnID`, `ModelID`, `Seq`, and `Attempt`. `ModelID` is the registered ID requested
by the planner, not necessarily `Request.Model`.

The runtime allocates `Seq` with
`Runtime.RunStore.NextSeq(ctx, RootRunID, RunID)` for each invocation, starting at
1 per run in the root run's scope. Parallel calls and activity retries receive
new, distinct values. Sequence order is allocation order, not completion order;
failed or short-circuited calls can consume values. Allocation failure returns an
error before the registered client is called. Workers must share the run store
to preserve uniqueness across retries on different workers.

`Attempt` is the engine activity attempt, not `run.Context.Attempt`. Temporal
supplies its current attempt; the in-memory engine executes once with attempt 1.
Custom engines attach their one-based attempt with `engine.WithActivityAttempt`.
`engine.ActivityAttemptFromContext` returns zero outside such an activity.

`Runtime.ModelClient(id)` returns the registered client directly and does not add
`CallInfo` or allocate a sequence. This also applies to clients obtained outside
the planner path. `model.WithCallInfo` can attach metadata explicitly. Call
identity alone does not record model requests or responses.

### Model-call records

Planner model calls are recorded in `Runtime.RunStore`, inside model
interceptors, after request decoration. The runtime writes `started` before
sending, stores any returned response, then writes `completed` or `failed`.
Missing `CallInfo` or an engine attempt below 1 rejects the call before sending.
`Runtime.ModelClient` calls and interceptor short-circuits bypass recording.

`runtime/agent/callrecord` owns the deterministic JSON v2 bodies. Requests use
ordered, zero-based half-open spans of the committed view captured before
history policy, plus hashes for other messages. Matching includes `Meta` and
cache checkpoints. Reconstruction loads the recorded `ViewRef` and verifies
all exported request fields against the recorded request. Messages whose
ordinary JSON round trip changes numeric precision use hashes; hashed messages
and tool schemas retain exact numeric tokens. The private completion validator
is excluded.

Stream usage is the sum of usage chunks. `Finalize` or `Close` writes completion
synchronously; activity exit marks streams still open as `stream_abandoned`.
Storage failures propagate, and a failed terminal write can leave `started`
evidence. There are no background writes.

`runtime.CallRecording` exposes started keys and the first `RequestGrowth`
warning when message bytes outside the view exceed those inside it. Planner
activity output carries `CallKeys`, `ProducingCall`, and `RequestGrowth`. The
workflow emits `context_request_growth` once per run and commits every validated
output, including recovery, through `planner_step:<run>:<step>`. All started
keys in that output are adopted; `ProducingCall` only correlates assistant
content with the last completed call that emitted planner events.

`TurnID` remains the conversational turn. `Step` starts at 1 and increments for
every scheduled planner activity, including recovery and finalization. Assistant
index entries, including the final response, carry the producing call key.
Presentation commits carry `CallKeys` parallel to `PresentationIDs`; a nil key
identifies a standalone helper presentation without a recorded invocation.
Planner record events and `child_run_linked` bypass event interceptors, append to the canonical run
log, then publish best-effort projections. Initial successful adoption shares
one hook activity with the execution-phase event, with `planner_step` first.
Pure workflow loop-state preparation completes before that batch publishes.
Recovery and resume adoption use a separate activity before returning to the
loop; their later publications can be skipped by validation or finalization.

### Terminal transcript sealing

`ExecuteWorkflow` owns one live planning state through terminal cleanup. On
success, failure, cancellation and finalization it schedules `<agent>.seal` on
a detached workflow context before `run_completed`. `SealRecord` validates and
stores the remaining index segments, including the final assistant entry, then
appends typed `run_sealed{IndexHead, ProtocolVersion}` directly to the canonical run log. It skips
interceptors and projections. A seal failure fails the run.

The workflow fixes `EventKey = run_sealed:<run>` and the timestamp before
scheduling; retries store identical content and append one event, including
a lost acknowledgement after append. The logical identity is deterministic;
run-log `ID` remains the store-owned pagination cursor. A run terminated outside
workflow code has no seal: its readable head is the latest `planner_step` head.
The workflow protocol is 6; structural templates remain 5.

**Breaking engine adapter change:** custom `engine.Engine` implementations must
add `RegisterSealRecordActivity`, and their `engine.WorkflowContext` must add
`ExecuteSealRecordActivity`. The typed input is `api.SealRecordInput`; the output
returns the committed `IndexHead`.

### RunRecord and export

`runrecord.Read(ctx, runStore, runLog, rootRunID, runID)` reconstructs a run and
its `child_run_linked` descendants using only the stores. Each run uses its seal
head, or its latest planner-step head with `Sealed=false`, and one incremental
index renderer. Structural entries use their own template protocol (currently
5). `planner_step` and `run_sealed` carry `ProtocolVersion`, copied from the
workflow input checked by `validateWorkflowInput`. The reader uses those stamps
and rejects disagreement. Without either record event, the protocol is unknown
(zero), even if calls record a positive `Step`.

Every listed model call includes its exact rebuilt request, activity and step.
Canonical record-event keys decide adoption for all calls of an attempt,
including auxiliary calls; a terminal unadopted call is abandoned and an open
call is incomplete. Presentation commits are reported separately as adopted,
presented-not-adopted or presented-unrecorded. The record also contains full
eligible results, typed and clarification answers, and a separate `ServerOnly`
field. `index.CheckpointRecord` includes summary call keys and all checkpoint
metadata; the reader resolves recorded checkpoint references. K12 produces receipt-only checkpoints and RunRecord rebuilds each request from
its exact tail, cleared-result watermark, checkpoint budget and recorded pins.
`Run.CompactionSettings` and `KeepResultTools` come from canonical planner-step
or terminal seal records, so event interception cannot erase the configuration.

`record.Export().Marshal()` produces deterministic version-1 JSON with rendered
transcripts, rebuilt requests, events and addressed content, including server-only
bytes. `runrecord.FromExport(data)` restores the identical record without a store
or historical renderer. Opaque log payloads retain their exact bytes; numeric
request metadata above 2^53 retains its exact value.

The 1,000-step recorder-write growth and real Temporal reconstruction proofs skip
`-short` and run through `make test-payload-growth` (also invoked by `make itest`).
The same proof includes real threshold compaction with stored receipt-only
checkpoints. The recorder owns all call writes; index and checkpoint setup writes
remain excluded from this recorder-growth measurement. Counting every recorder `Put` and every `PutCall` body write, including repeated
writes, the K12 run measured these four modes over 1,000 steps:

| Mode | Bytes/step slope | Maximum residual (bytes) |
| --- | ---: | ---: |
| `off` | 2,212.230 | 990.001 |
| `keep_recent` | 2,242.624 | 1,239.443 |
| `compress` | 2,669.240 | 3,937.941 |
| `real_compaction` | 2,740.337 | 542.128 |

The entry-size metadata change moved all modes from the K8 baselines. Index and
checkpoint setup writes are excluded; all recorder-owned addressed content and
call bodies are included. The real-compaction mode starts with 1,000 seed turns,
then measures 1,000 new steps with two evictions. Its first request sends the full
seed. The non-race proof passed once (60.881s for all four comparison modes).

The complete request is only a canonical verification digest: it is never
stored as addressed content or copied into export `Content`. Rebuild and import
verify the reconstructed request against that digest.

### Trajectories from RunRecord

The top-level `trajectory` package derives evaluation steps from a self-contained
`RunRecord`. `Build(record, runID, options)` reads no stores and produces the same
trajectory from `runrecord.Read` and `runrecord.FromExport`. The requested run and
all linked children must be sealed; an unsealed run returns `ErrNotTerminal`.
The compiled example below uses `r`, a harness Result from the assistant fixture,
and `h`, its harness; later reference blocks reuse `tr`.

<!-- snippet: runtime_trajectory -->
```go
tr, err := trajectory.Build(r.Record, r.RunID, trajectory.Options{
    Output:           r.Output,
    EventsUnverified: h.Runtime().HasEventInterceptors(),
})
require.NoError(t, err)
data, err := tr.Marshal()
require.NoError(t, err)
```

`Options.Output`, when supplied, selects its `Final` for the requested run,
including an explicit nil final. Otherwise the final answer comes from the last
assistant entry naming an adopted model call. Child final answers always come
from their records. `Status` comes from the observed `run_completed` payload;
`StatusVerified` reports its trust, and an absent completion event leaves status
empty.

`Trajectory.Steps` contains model calls, tool calls, await prompts, answers and
selected observer events. Step N's planner head is the view its model read.
An assistant entry belongs to the step of its producing model call, identified
by the entry's `Call` key. Its results and structural entries follow it in that
step, even when step N+1's head first includes them. User, resume and clarification
entries belong to the first step whose head includes them. Each step's model
calls, sorted by `(Seq, Attempt)`, precede its produced entries. Events published
from the planner activity's event sink or planner-context prompt observer carry
an optional `planner_step` in their hook and run-log JSON payloads. The builder
places each stamped observation in that step, including planner notes published
before the step's adoption. Every unstamped observation belongs to the latest
step whose `planner_step` record precedes it, or step 0 before the first boundary.
Workflow and tool events remain unstamped: await prompts and phase changes stay
with the step whose output the workflow is handling, as does `prompt_rendered`
from agent-as-tool execution. Observations follow their step's entries and
precede the next step's model calls. Older unstamped records use this same
preceding-boundary rule; event types do not determine placement. Trailing sealed
assistant entries also use their producing call's step. Abandoned and incomplete
model calls remain visible with their recorded class and step. Each model step preserves request hashes,
prompt references, chosen tool calls, usage,
latency, failure kind and context-measurement facts. `callrecord.DecodeResponse`
restores exact numeric metadata and tool inputs and preserves chosen tool payload
bytes.

Tool arguments come from the assistant entry's stored call, before server
injection. Results, bounds, structured errors and duration come from canonical
stored output metadata; full result bytes come from its result reference, even
when hooks or transcript previews omit the body. Results with no matching call,
duplicate results, name mismatches and repeated tool-call IDs are errors. Child
trajectories appear in `ToolStep.Children` directly under their owning call, in
child-link order. A missing linked run is an error. `AnswerStep.Content` is
`json.RawMessage`: clarification text becomes a JSON string, with invalid UTF-8
replaced by U+FFFD. Typed-input payloads retain their exact stored JSON bytes and
numeric tokens. Serialized answers are readable JSON values, never base64.
Deterministic marshaling sorts
object keys and removes formatting whitespace without rounding numbers.
Question and external-tool answers are full JSON tool results, and confirmation
decisions appear as observed authorization steps.

Record-derived model, tool and stored-answer steps have `Verified=true`.
Thinking, notes, retry hints, policy decisions, await prompts, phase changes,
prompt-rendering events, confirmation decisions and completion status are observed
fields. Set `EventsUnverified` whenever an application has a `BeforeEvent`
interceptor: those observed steps become unverified, while record-derived steps
remain verified. An interceptor that drops observer events cannot remove recorded
calls, results or answers.

Totals are local to each run. They include all model attempts, counts by adoption
class, adopted model calls, tokens per model, tool calls, failed tool calls and
repeated calls with the same name and key-sorted JSON arguments. Wall time spans
the first and last **record-event** timestamps (`planner_step`, `run_sealed`,
`child_run_linked` and compaction record events). Observer events never feed
totals, so dropping them leaves totals unchanged. Each child has its own totals.

`trajectory.Session` is a caller-ordered list of trajectories. Both trajectory
and session `Marshal` methods produce deterministic JSON with nanosecond
durations and sorted object keys.

#### Rendering trajectories

`Trajectory.Render` and `Session.Render` return text or Markdown without reading
stores or changing the evidence. A session retains its caller-supplied run order.
The zero `Format` renders text; use `FormatMarkdown` for nested lists and fenced
JSON. Children appear below their owning tool, with two additional spaces in
text. Steps remain in slice order, grouped by planner step number. Each run ends
with its own totals, whose map keys are sorted.

<!-- snippet: runtime_render -->
```go
text := tr.Render(trajectory.RenderOptions{
    Format: trajectory.FormatText,
    Limits: trajectory.DefaultLimits(),
})
markdown := trajectory.Session{tr}.Render(trajectory.RenderOptions{
    Format: trajectory.FormatMarkdown,
    Limits: trajectory.DefaultLimits(),
})
```

`Limits` has independent `MaxValueBytes`, `MaxSteps` and `MaxDepth` fields.
`DefaultLimits()` returns 4096 bytes per value, 200 steps per run and child depth
4. Each zero field means unlimited; defaults apply only when explicitly supplied.
The root has depth zero. Value limits count the compact JSON or quoted text bytes
(including quotes), or the readable text bytes for prompts and final text parts.
Cuts move back to a UTF-8 boundary. Every omission is visible:

- `…[truncated: 2048 of 5242880 bytes]` reports retained and original value bytes.
- `[37 more steps omitted]` counts omitted slice elements in that run.
- `[child run child-5 omitted: depth limit 4]` identifies an omitted child.

There is no total rendering budget. Render limits never truncate JSON documents.
JSON values are compact and key-sorted, preserving exact numeric tokens; non-JSON
bytes render as quoted text. Markdown fences exceed the longest backtick run
inside their value. Multiline or backtick-containing readable text also uses
fenced blocks to preserve Markdown list structure. Observations include their
UTC RFC3339Nano timestamps and generic JSON payloads. Thinking text and await prompts also appear as readable
text. Unverified status carries `(unverified)`; every unverified step carries
`(observed, unverified)`. Verified values have no such marker. Final text parts
are readable text; other final parts render as `[<kind> part]`. Rendered durations
use `time.Duration.String()`, hashes render as `sha256:<first 12 hex digits>`, and
lines have no trailing whitespace. Rendering has no clock or locale dependency.
Render methods intentionally fail fast: nil trajectories, nil elements in
`Session` or `Children`, and unsupported final message `Part` types panic.

#### Versioned trajectory JSON

`EncodeDocument(tr)` and `EncodeSessionDocument(session)` use `callrecord.Encode`
to wrap the existing `Marshal` representation in `Document`. The envelope has
`schema: "loom.trajectory.v1"` and exactly one of `trajectory` or `session`.
A nil trajectory or nil session is rejected; a non-nil empty session encodes as
`[]`. There is no document decoder. `Schema()` returns an owned copy of the
embedded JSON Schema 2020-12 at
[`trajectory/schema/trajectory.v1.schema.json`](../trajectory/schema/trajectory.v1.schema.json).
`SchemaVersion` is the envelope version, independent of run-record and workflow
protocol versions. An added optional field stays v1; removal, rename, type changes
or changes in meaning require v2.

<!-- snippet: runtime_document -->
```go
data, err = trajectory.EncodeDocument(tr)
require.NoError(t, err)
```

The field reference below uses the exact JSON spelling. Trajectory-owned names
are snake case; embedded runtime types retain their existing uppercase names.
All durations in documents are integer **nanoseconds**, including `latency`,
`duration`, `wall_time` and `count_latency`. Hashes are full `sha256:<64 hex>`
content hashes, or empty when absent; only human-readable rendering shortens them.
Raw JSON fields retain number tokens above 2^53, decimals such as `1.2300`, and
exponents such as `1e+09`. Consumers must avoid decoding these through float64
when preserving the evidence.

| Object | Fields and meaning |
| --- | --- |
| `Document` | `schema`: version identifier; `trajectory`: one run; `session`: array in caller order. Exactly one member is present. |
| `Trajectory` | `run_id`, `session_id`, `agent_id`: identities; optional `trace_id`: caller tracing correlation; `steps`: causal slice; `final`: model message or null; `status`: observed terminal status; `status_verified`: observer trust; `totals`: run-local recorded counts. |
| `Step` | `kind`: `model`, `tool`, `await`, `answer` or `event`; `step`: planner schedule number; optional `entry_id`: index entry identity; `verified`: evidence trust; exactly one populated `model`, `tool`, `event` or `answer` member. |
| `ModelStep` | `key`: `RunID`, `Seq`, `Attempt`; `class`: `adopted`, `abandoned` or `incomplete`; `activity`: `planner` or `summarize`; `step`: recorded planner number; `model`, `model_id`: provider and registration identity; `usage`: token counts; `latency`: duration; optional `error`: `kind`, `message`; optional `facts`: request measurements; `chosen`: tool calls; `system_hash`, `tools_hash`, `request_digest`: full request provenance; `prompts`: prompt refs. |
| `TokenUsage` | `Model`, `ModelClass`: attribution; `InputTokens`, `OutputTokens`, `TotalTokens`, `CacheReadTokens`, `CacheWriteTokens`: disjoint normalized token counts. |
| `RequestFacts` | `call`: call key; `model`: resolved identity; `context_window`, `output_reserve`, `input_tokens`, `text_bytes`, `fixed_bytes`, `non_text`: measurements; `view_bearing`: transcript-view inclusion; `view_text_bytes`, `view_non_text`: the matched view's text bytes and non-text parts (optional); `count_latency`: counting duration; `outcome`: measurement outcome. |
| Chosen tool call / prompt ref | Tool call: `Name`, `ID`, `Payload` (raw JSON). Prompt ref: `ID`, `Version`. |
| `ToolStep` | `name`, `tool_call_id`, optional `parent_tool_call_id`: call identity; `arguments`: original JSON before injection; `result`: complete JSON or null if absent; optional `bounds`, `error`: stored metadata; `duration`: execution duration; `children`: linked trajectories in canonical link order. |
| Bounds / structured tool error | Bounds: `Returned`, nullable `Total`, `Truncated`, nullable `NextCursor`, `RefinementHint`. Error: `Message`, optional `kind`, nullable recursive `Cause`. |
| `ObservedEvent` | `type`: event kind; `event_key`: durable identity; `timestamp`: RFC3339 timestamp; `payload`: complete generic JSON. Trust lives on its containing step. |
| `AnswerStep` | `kind`: `clarification` or `typed_input`; optional `input_id`: await correlation; `content`: readable JSON string for clarification text, or any exact JSON value for typed input. Never base64. |
| `Totals` | `tokens_by_model`, `model_calls_by_class`: sorted maps; `model_calls`, `adopted_model_calls`, `tool_calls`, `failed_tool_calls`, `repeated_tool_calls`: run-local counts; `wall_time`: record-event interval. Children are excluded. |
| Final message | `Role`, `Parts`, `Meta`: existing model wire representation, including nullable parts and metadata. Each part has a `Kind` discriminator. |
| Final part variants | `text`: `Text`; `thinking`: `Text`, `Signature`, `Redacted`, `Index`, `Final`; `image`: `Format`, `Bytes`; `document`: `Name`, `Format`, `Bytes`, `Text`, `Chunks`, `URI`, `Context`, `Cite`; `citations`: `Text`, `Citations`; `tool_use`: `ID`, `Name`, `Input`; `tool_result`: `ToolUseID`, `Content`, `IsError`; `cache_checkpoint`: no other fields. Binary model-part bytes use the existing base64 wire encoding. |
| Citation / location | Citation: `Title`, `Source`, `Location`, `SourceContent`. Location has nullable `DocumentChar`, `DocumentChunk`, `DocumentPage`, each with `DocumentIndex`, `Start`, `End`. |

`model` steps populate `Model`; `tool` steps populate `Tool`; `await` and `event`
steps populate `Event`. `answer` steps populate either `Answer` for stored
clarification/typed input or `Event` for observed tool authorization. Await event
types are the five `await_*` kinds. A step cannot populate multiple members.
`Verified` describes the evidence boundary, not whether a model or tool succeeded:
abandoned and incomplete recorded calls are still verified. BeforeEvent makes
observations unverified, while recorded calls, results and answers remain verified.

### Harness

Start with [Testing agents and tools](agent_testing.md) for compiled wiring,
selection and request-fixture recipes.

`agenttest.New(t, register, opts...)` constructs an isolated in-memory runtime
and reports script and lifecycle leaks at test cleanup. The `register` callback
wires application agents and real tool executors, including generated
`Register…Agent` and `RegisterUsedToolsets` functions. Scripted overrides are
registered afterward; other application model clients remain available.

<!-- snippet: runtime_harness -->
```go
h := agenttest.New(t, guideRegister,
    agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("done"))),
    agenttest.WithResponder(agenttest.Responder{agenttest.Clarify("yes")}),
)
result, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("hello"))
require.NoError(t, err)
expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed())
```

`Result` includes `Output`, `RunErr`, the sealed `Record`, `Trajectory`, and
answered `Awaits`. Agent failures populate `RunErr`; setup, timeout, unanswered
await and reconstruction failures return an error. The default wall-clock run
limit is 30 seconds; change it with `WithRunTimeout`. An unmatched await cancels
the run and returns `ErrUnansweredAwait`. Responder rules use the first match;
callbacks must return promptly and support concurrent runs.

`RunSession(ctx, agent, turns...)` returns ordered runs in one new session and
stops on the first harness error. Earlier history comes only from the agent's
memory policy; the harness carries no transcript between turns.
`WithRuntimeOptions` configures application policies, memory and interceptors,
but rejects overrides of the owned engine, stores, hook bus and tracer.
`WithRunID` is reserved for the harness's deterministic per-instance IDs.

`Runtime.HasEventInterceptors()` conservatively detects event middleware across
all registered agents. The harness marks observed status/events unverified when
it is true; recorded model, tool and answer evidence remains verified.
`WithTracerProvider(provider)` uses `telemetry.NewTracer(provider)` and opens a
root `agenttest.run` span without changing the global provider. Its trace ID is
available on `Result.TraceID` and optional `Trajectory.TraceID` (`trace_id` in
v1 JSON); Render prints `Trace: <id>` after `Agent:` when present. The caller owns
the provider's export configuration and shutdown. No live model is enabled by
default, and the harness has no Temporal mode.

### Capturing and deciding

`h.CaptureFirstRequest(ctx, agent, input, opts...)` returns an immutable
`agenttest.Request` for the first fully decorated model call, with its registered
`ModelID` and `Source` call key. The gate returns `ErrCaptureSentinel` before the
underlying client runs. The planner must propagate that error: swallowing or
retrying it returns `ErrCaptureHandled`, and no model call returns
`ErrCaptureNoModelCall`. Tools scheduled before the model call follow the normal
strict stub rules. The capture can run concurrently with ordinary harness runs.
Harness construction installs capture gates and then seals runtime registration.
Register all models, agents and toolsets in the construction callback; later
registrations, including model replacements, return `runtime.ErrRegistrationClosed`.

`agenttest.Decide(t, h.Model(id), request)` performs one validated Complete or
Stream call, returning `Decision` with the request, response, calls and latency.
It adds no `CallInfo`, writes no run record and executes no tools. Use
`expect.Require(t, expect.Calls(d.Calls), ...)` to check its calls.
`DecideContext` returns errors for callers without a test. `Repeat(t, n, minPass,
func(testing.TB))` runs all attempts sequentially, isolates errors, Fatal, panic
and cleanup failures, and reports the threshold with parent Errorf. Attempt
contexts are cancelled before LIFO cleanup. A skip without a failure skips the
parent and stops; an attempt that fails and then skips counts as failed and
remaining attempts continue. Setenv and Chdir (including PWD) are restored, and
TempDir directories are removed, during attempt cleanup before the next attempt.
Process-state changes retain the parent test's prohibition on parallel tests.

`WithUserMessage`, `WithSystem`, `WithToolSpecs`, `WithModel` and `WithRunID`
return new requests with deferred errors surfaced by `Err`, `Request`, Marshal
or Decide. `SaveRequest` and `LoadRequest` persist deterministic version-1 JSON
fixtures with a verified request digest. Fixtures contain full prompts; keep
application fixture files gitignored. Private completion validators are omitted,
and provider thinking signatures must not be replayed across providers. Tool
spec transforms replace definitions by name; re-capture planners that decorate
those definitions.

`LiveModel(id)` opts a named application client in only with
`LOOM_LIVE_MODEL=1`; it builds no provider and checks that registration supplied
that client. Disabled opt-in returns `ErrLiveModelDisabled` before registration,
and `agenttest.New` skips with the model ID. `SkipUnlessLive(t, id)` gates
standalone decision tests. Unset or `0` disables these helpers, `1` enables them,
and other values fail. This gating applies only to explicit LiveModel and
SkipUnlessLive calls; undeclared application clients remain available.
`Runtime.ModelIDs()` returns a sorted owned snapshot of registered identifiers.

### Stubbing tools

The harness is strict by default. Register real toolsets through the application's
normal registration path (including generated `RegisterUsedToolsets`); executors
may be omitted for stubbed tools. Then declare typed responses before Run:

<!-- snippet: runtime_stubs -->
```go
stub := agenttest.Stub(h, projected.ProjectedStatusToolTool)
stub.Return(&projected.ProjectedStatusToolResult{Status: "ready"})
```

Stubs replace activity execution only. Payload adaptation and validation,
AfterTool interceptors (including retry-and-reflect), result materialization,
codec encoding and bounds enforcement still use the runtime's normal path.
`Runtime.ToolDispatchMode(tool.Ident())` returns the resolved mode and whether the
tool is registered with a toolset. Inline and agent-as-tool stubbing are a runtime
follow-up; their declarations are rejected, as are unknown tools and duplicate
stubs.

`Return(value)`, `Fail(err)` and `Handle(func(context.Context, P) (R, error))`
append responses. Fail and Handle errors become model-visible tool errors; a
Handle error discards its result. Each response is used once unless configured
with positive `Times(n)` or `Always()`. Close reports unused finite responses;
Always responses are exempt. `After(d)` delays the response and respects
cancellation. Successful bounded responses need `Bounds(agent.Bounds{...})`;
a bounded Return without Bounds is rejected before Run and at cleanup, allowing
the modifier chain to finish first.

`When(matches...)` selects the first available response whose matchers all pass;
without matchers this is FIFO. `ArgMatch[P]` is `func(P) error`, where nil means a
match. `ArgsEqual(tool, want)` encodes want once using the descriptor's payload
codec, then compares each received payload's encoded JSON after canonicalizing
key order and whitespace. A want-encoding error is returned whenever matching
runs. `ArgsWhere(description, predicate)` supports custom predicates. Matchers
are never invoked at declaration. They run under the table lock and must not
call harness/stub methods; Handle callbacks run outside that lock. Parallel
identical calls race for FIFO responses, so use matchers to route parallel batches.

An unstubbed tool, exhausted stub (`ErrStubExhausted`), unmatched arguments or
stub payload decode failure is recorded as a `*StubError` with the tool, per-tool
call index, run/tool-call IDs and arguments. The runtime shows the model a tool
error and can continue. After completion, Run returns the populated Result
alongside a joined harness error containing that run's StubErrors; Close also
reports every failure. RawStub result-decoder rejection is also a StubError.
Rejection during subsequent runtime result encoding follows real-executor semantics:
it is a tool error and does not become a StubError. RunSession retains the
completed run's evidence before stopping on a harness error.

Use `h.Passthrough(tools...)` or `PassthroughAll()` to execute real activity tools.
Explicit stubs take precedence, including when exhausted. `stub.Calls()` records
typed payloads, responses and outcomes; `h.ToolCalls()` records all activity calls
reaching the interceptor in arrival order. Passthrough recordings contain raw
arguments and correlation IDs; consult the trajectory for real execution results.
Response indexes are one-based, or zero when no response ran. Snapshots copy raw
bytes and slice slots; typed pointer/map/slice values remain shallow copies.

`RawStub(h, toolID)` provides the same response API over `rawjson.Message` for
tools without descriptors. It decodes successful raw results through the
registered result codec before AfterTool and normal result materialization.
A decoder failure is recorded as a `*StubError` in both `stub.Calls()` and
`h.ToolCalls()`, returned by Run, and reported again by Close.

### Expectations

Use `expect` to assert tool behavior from recorded runs. Build a trajectory with
`trajectory.Build`, then construct a subject with `expect.Trajectory(tr)` or
`expect.Session(runs)`. Calls follow causal order, with child calls immediately
after their parent. `expect.TopLevel(subject)` excludes descendants from calls,
events, completion checks and budgets. A Session that lists a child separately
from a parent that already contains it counts that child twice, including its
calls, events and budgets.

<!-- snippet: runtime_expect -->
```go
subject := expect.Trajectory(tr)
expect.Require(t, subject,
    expect.Call(projected.ProjectedStatusToolTool, nil, nil),
    expect.Completed(),
    expect.MaxModelCalls(3),
    expect.MaxTokens(1000),
)
```

`Call` uses a typed tool descriptor and optional argument and result predicates;
nil predicates skip decoding. `Decoded` accepts a named tool and an explicit
argument codec. Codec failures quote the original error. Call expectations bind
greedily to the first later satisfying call. Wrap them in `Exact` for one-to-one
matching; `Forbid` checks every call and cannot be nested in `Exact`.

`Failed`, `Bounds` and `Await` inspect recorded structured errors, result bounds
and planner await items. `Completed` checks every selected run's successful
terminal status; `AllAttemptsSuccessful` includes earlier failed attempts.
`AllAttemptsSuccessful` fails with `no call to <tool>` when the tool was never
called. `Completed` and `Await` require verified observed evidence: unverified
status fails with `evidence unverified: status`, and an unverified requested await
fails with `evidence unverified: await_<kind>`. Building with
`trajectory.Options{EventsUnverified: true}` therefore prevents these checks
from passing; record-derived calls and budgets remain usable. `Await` decodes
only events of the requested kind.
`MaxModelCalls` counts adopted calls, while `MaxTokens` includes all recorded
attempts and their input, output and cache tokens. `expect.Calls(modelCalls)`
provides a model-choice subject: execution and run expectations fail with
`not applicable to a model-call subject`.

For data-driven checks, `expect.ExpectedTools` lists `expect.ExpectedTool` names
and optional JSON arguments, with `Ordered` and `Exact` flags. Objects match recursively
as subsets, arrays match exactly, and numbers compare by exact decimal value.
Empty or invalid call arguments are non-matches, reported in a failed check's
diagnostic; other calls still contribute to the score.
Its `Evaluate(subject)` returns a check and a `tool_correctness` score: matched
calls divided by calls made. With no calls, the score is 1 when nothing was
expected and 0 otherwise. `expect.Evaluate` returns checks for inspection;
`expect.Require` reports all failed diagnostics once, in declaration order, and
fails when given no expectations.

### Eval scenarios and datasets

`eval` binds a dataset item to an `agenttest.Harness` session. Declare immutable
Go datasets with `eval.Items(...)`, or read files eagerly with
`eval.JSONL(embedFS, "items.jsonl")` / `eval.JSONL(os.DirFS("testdata"), "items.jsonl")`.
Constructors defer errors to `Dataset.Err()` so they work in package variables.
`eval.Validate(scenarios...)` joins all scenario and item problems before running.
`Dataset.Items()` returns deep copies; `Hash()` is `sha256:<hex>` of canonical
version-1 JSONL, independent of the source file's whitespace and member order.
An invalid dataset has an empty hash; an empty dataset reports `ErrEmptyDataset`.

<!-- snippet: eval-scenarios -->
```go
scenario := eval.Scenario{
	ID:    "status",
	Agent: "assistant.assistant_runtime",
	Task:  "Report the current status",
	Items: eval.Items(eval.Item{
		ID:    "ready",
		Input: "What is the status?",
		ExpectedTools: &expect.ExpectedTools{
			Tools: []expect.ExpectedTool{{Name: "projected.projected_status_tool"}},
			Exact: true,
		},
	}),
}
```

JSONL starts with a mandatory header and has one item object per line:

```jsonl
{"format":"loom-mcp.eval.items","version":1}
{"id":"ready","input":"What is the status?","expected_tools":{"tools":[{"name":"projected.projected_status_tool"}],"ordered":false,"exact":true}}
{"id":"conversation","turns":["Hello","Tell me more"],"expected_outcome":"Explain the answer","data":{"ticket":9007199254740993}}
```

`DecodeJSONL` rejects unknown and duplicate members, invalid UTF-8, blank lines,
repeated headers, unsupported versions, non-object lines, multiple values per
line and lines over 16 MiB. Failures expose `*eval.DecodeError` with source and
one-based line through `errors.As`. `EncodeJSONL` sorts all object members, uses
LF endings and a final newline, and preserves number tokens in `Data` and tool
`Args`. Items require a unique ID and exactly one non-empty `Input` or non-empty
`Turns` without empty turns. `ExpectedOutcome` requires `Turns`; `Persona` is
reserved and rejected. `ExpectedTools: nil` supplies no tool assertion, while
an empty list explicitly expects no calls (set `Exact` to reject extra calls).

Call `eval.Execute(ctx, h, scenario, item)` to drive one attempt. Item `Task` and
`Reference` override scenario defaults; `Attempt.Item` receives the resolved
values. The default driver uses `RunSession` for both single and multiple turns.
`Drive` replaces it and must return the session evidence; `Assert` receives an
`Attempt` with `Subject()` and the last trajectory's `Final()` message.

Checks appear in order: mandatory `runs_succeeded`, scenario `Expect`, item
`expected_tools` (with the `tool_correctness` score), then `Assert` results.
The health check reads `Result.RunErr` and remains usable with event interceptors.
Duplicate check and score names receive `#2`, `#3`, and so on; claim IDs must be
unique. Claims are declarations for a later judge, with scenario claims first.
At least one user check or claim must remain after evaluation; the built-in
health check alone reports `ErrNoAssertions`.

A failed quality check leaves `Outcome.Err` nil. Harness/Drive errors preserve
the session prefix, leave checks and scores empty, and skip Assert. Assertion
errors, recovered callback panics (with stack), timeouts and invalid assertion
results populate `Outcome.Err`. The separate error return is only for invalid
scenario/item arguments or a nil harness. Scenario `Timeout` covers both Drive
and Assert; callbacks must cooperate with context cancellation. Concurrent
Execute calls may share a harness; callbacks and predicates must be safe for
concurrent use. Selection, repeats, variants, storage and judging are separate
layers.

### Eval runner

`eval.Plan(config)` validates every scenario, variant and selector before listing
attempt keys. `eval.Run(ctx, config)` executes that matrix with a fresh harness
per attempt. Supply the application's registration and scenarios:

<!-- snippet: eval-runner -->
```go
report, err := eval.Run(ctx, eval.Config{
	Harness:   eval.NewHarnessFunc(register, nil),
	Scenarios: scenarios,
	Variants: []eval.Variant{
		{ID: "baseline", Labels: map[string]string{"prompt": "baseline"}},
		{ID: "candidate", Policy: &api.PolicyOverrides{PerTurnMaxToolCalls: 1}},
	},
	Repeats:     5,
	Concurrency: 2,
	MaxTokens:   10000,
})
```

An omitted variant list creates `default`; zero `Repeats` or `Concurrency` means
one. Variant IDs are unique dataset-style IDs. Labels scope prompt overrides,
and `Policy` is cloned per run. Put model overrides and per-agent context settings
in `Variant.Options`, using `ScriptedModel` or
`WithRuntimeOptions(runtime.WithContextCompaction(...))`. `NewHarnessFunc` applies
base options before variant options, runs optional setup and closes failed setups.
A custom factory receives `(ctx, variant, opts...)`, must forward `opts` to
`NewHarness`, and must return a new harness each time. Use `ScriptedModel` to build
fresh scripts; sharing a `ScriptedModelFrom` script breaks attempt isolation.
`agenttest.WithDefaultRunOptions` applies labels/policy before caller run options
on Run, RunSession and CaptureFirstRequest; `WithRunID` stays reserved.

`Config.Select` supports variants, scenarios, tags and bare or `scenario/item`
IDs. Selectors use OR within a kind and AND across kinds. Validation joins empty,
duplicate, unknown, ineffective selectors and an empty resulting plan into one
error. All scenarios are validated, including unselected ones.

Keys are `variant:scenario:item:repeat` with repeats starting at one; records and
zero-based `Index` values keep variant, scenario, dataset item, repeat declaration
order. Starts use descending scenario timeout, with zero longest and stable ties.
Scenario timeout covers Drive and Assert, not factory setup or future judging.
Factories, Drive, Assert and predicates must cooperate with cancellation and be
safe for concurrent calls. Every newly built harness is closed once; reuse is an
error. Run/session IDs can repeat across attempts, so use `AttemptKey` as identity.

Outcome verdict precedence is `error`, `failed`, `pending`, `passed`: execution
errors win over failed checks or thresholded scores, which win over unjudged
gating claims. Informational claims do not gate. Factory/setup errors, panics
(with stack), reused harnesses and Close failures go in `HarnessErr` and give an
`error` verdict. Repeats all run; `Report.Groups()` counts each verdict without
gating an experiment. `OnAttempt` is serial and fires once per record in completion
order, then once for each skipped record in declaration order. Callback order
under concurrency is nondeterministic.

`MaxTokens` zero is unlimited. Usage sums every completed recorded model call,
including abandoned attempts and descendants, once per returned session run.
Tokens use positive TotalTokens, otherwise input + output + both cache counts.
Direct Decide calls in Assert and runs omitted by custom Drive are not counted.
At `Tokens >= MaxTokens`, new attempts stop; running attempts finish normally,
so up to `Concurrency-1` additional attempts may finish beyond the crossing one.
Skipped records have nil Outcome and `NotRun: token_limit`; this stop is not an
error. Concurrent completion order can change which keys are skipped.

Cancellation keeps finished evidence, cancels running work and marks unstarted
records `canceled`; Run returns the partial report with the context cause. The
runner never enables live clients. A factory error matching `ErrLiveModelDisabled`
stops new attempts, marks them `live_model_disabled` and returns the partial report
with that error. Other recorded failures leave Run's error nil. Invalid config
returns no report. `AttemptRecord.Runs()` exposes each completed run's version-1
`runrecord.Export`, session ID and optional trace ID, preserving a completed prefix
on errors. Reports retain all exports in memory; storage, CLI output and judging
belong to later layers.

### Run-log result references and upgrade

Every `ToolResultReceivedEvent` carries `Refs` for the stored eligible result,
server sidecar and executed payload. Small results retain their existing inline
`Result`, `ResultJSON` and `ServerData`. Above the 1,000,000-byte encoded hook
limit, all three bodies are omitted; refs and the original preview remain.
Without a configured preview, the bounded fallback says the result was omitted
from hooks because the payload exceeded limits.
`publishStoredToolContent` skips loading a result already known to exceed the
limit and stops loading sidecars once the result envelope cannot fit. The store
has no sidecar size probe, so a sidecar is checked after loading. `RunRecord`
resolves results from canonical index entries and their stored output refs, with
call records preserving model-visible evidence. Observer `tool_result_received`
refs never participate in reconstruction. Completed results from a batch that
later aborts are retained as refs and indexed on detached terminal cleanup before
sealing. Inline error sidecars are stored even when no eligible result body exists.
Normal result ordering and rendered messages remain unchanged.

K8 has these breaking changes and execution costs:

- Custom engines must implement both seal methods described above.
- Workflow protocol 6 requires draining older workflows before upgrading workers;
  the unchanged v5 history proves mismatch rejection before effects.
- The `hook_payload_too_large` truncation marker is removed. Consumers must use
  refs to retrieve omitted bytes and previews for display.
- Each resumed or recovery planner step adds one hook activity round trip for
  `planner_step`: approximately 97 ms median on the Temporal dev server (20 runs).
  The real-Temporal await test retains its approved 2s budget. Initial planning
  stays at two activities, batching adoption with its existing phase hook;
  resumed planning increases from one to two, and sealing adds one activity per run.

---

## Streaming Planners

When using model streaming, planners have two options for emitting events. Choose
**exactly one** per stream to avoid double-emitting.

### Option 1: Runtime-Decorated Client (Recommended)

`PlannerContext.ModelClient(id)` returns a client with an event decorator.
The decorator publishes text, thinking, and usage events from each accepted chunk.
Pass this stream to `planner.ConsumeStream` for canonical stream consumption:

```go
sum, err := planner.ConsumeStream(ctx, streamer, input.Events)
if err != nil {
    return nil, err
}
```

The helper detects that the decorated stream owns event publication.
It aggregates the result without duplicate planner events.

### Option 2: ConsumeStream without Runtime Event Decoration

When you have a validated `model.Client` that is not decorated to emit runtime
events, use `planner.ConsumeStream`:

```go
sum, err := planner.ConsumeStream(ctx, streamer, input.Events)
if err != nil {
    return nil, err
}
```

This helper drains the stream, emits events via `PlannerEvents` when the stream
does not own them, and returns a `StreamSummary` with accumulated text and tool
calls. Runtime planner events apply the same provisional presentation lifecycle
to this validated-but-undecorated path.

### Fallible Chunk Observers

Use `planner.ConsumeStreamWithObserver` when an application must process every
accepted chunk:

```go
observer := planner.StreamObserverFunc(func(ctx context.Context, chunk model.Chunk) error {
    return projection.WriteChunk(ctx, chunk)
})

sum, response, err := planner.ConsumeStreamWithObserver(
    ctx,
    streamer,
    input.Events,
    observer,
)
if err != nil {
    return nil, err
}
```

The observer receives text, thinking, tool-call, completion, usage, and stop
chunks. A `model.TextChunk` can contain text parts or citation parts.

The helper passes the exact observer error to `ValidatedStreamer.Finalize`.
`StreamConsumptionError.PrimaryError` returns that error.
`StreamConsumptionError.FinalizationError` returns the separate finalization
result. `Error` reports the primary error when one exists.
Inspect `FinalizationError` before you log provider cleanup details.
`errors.Is` and `errors.As` inspect both errors.

The helper reads `Response` only after literal `io.EOF`.
It returns the canonical response with the summary.
The response remains available when cleanup fails after that EOF boundary.
`ConsumeStream` remains the direct path when no fallible observer is required.
### Typed Structured Completions

`runtime/agent/completion` uses a validated `model.Client` with a generated
typed completion spec. The helper clones the request, installs
`model.StructuredOutput`, rejects tool calls on that request, and binds the
generated decoder into the immutable request contract. Schema-valid output that
the generated decoder rejects never leaves the client boundary:

```go
resp, err := completion.Complete(ctx, modelClient, req, completions.SpecDraft)
if err != nil {
    return err
}
fmt.Println(resp.Value)
```

For streaming completions, `completion.Stream` returns a
`model.ValidatedStreamer` that
allows `completion_delta`, thinking, and usage chunks, but withholds the
canonical `completion` until the complete stream is accepted. Decode the final
typed value with `completion.DecodeChunk`, drain to the literal `io.EOF`, and
finalize the stream.

The consumer must observe literal EOF before reading `Response` or finalizing
successfully. The first `Finalize` result is authoritative and includes provider
cleanup. Runtime tracing and adaptive rate limiting commit their lifecycle
outcome there rather than when `Recv` first reaches EOF.

Runtime-owned model streams also publish provisional presentation events. A
`model_presentation` event starts the presentation. Each live assistant or
thinking delta carries its `presentation_id`. Stream finalization stages valid
content, and the surrounding planner activity emits exactly one `accepted` or
`discarded` state. Treat accepted content as canonical and remove discarded
content. Partial tool-call JSON stays inside the model boundary until the
complete call passes validation. The runtime emits `accepted` only after the
planner succeeds and one atomic canonical response event for all ready
presentations is durable. That assistant-turn event carries the presentation
IDs and authoritative response messages. A later planner failure or failed
run-log write discards every presentation and fails the attempt. When a final
planner response matches an accepted presentation, the runtime suppresses the
legacy duplicate final-message path even if the planner omitted `Streamed`.
Canonical presentation commits support the full validated model-output bound;
the smaller ordinary-hook payload limit does not truncate accepted content.

---

## Tool Execution

### Typed Tool Descriptors

`tools.TypedTool[P, R]` pairs a canonical `Name` with typed `Payload` and
`Result` JSON codecs. Generated specs packages expose one descriptor per tool
in `codecs.go`, using the exact pointer or value types of those codecs:

```go
var EchoTool = tools.TypedTool[*EchoPayload, EchoResult]{
    Name: Echo,
    Payload: EchoPayloadCodec,
    Result: EchoResultCodec,
}
```

Use `EchoTool.Payload.ToJSON` and `EchoTool.Payload.FromJSON` to encode and
decode payloads. The result codec works the same way. A descriptor contains
only the identifier and codecs; schemas and runtime metadata remain in
`tools.ToolSpec`.

Both `tools.Ident` and `tools.TypedTool[P, R]` implement `tools.Named` through
`Ident() tools.Ident`. APIs that only need a tool's identity can accept either.
Registry toolsets expose untyped codecs and use `tools.Ident` as their
Named-only representation; they do not generate typed descriptors.

Descriptor names are `<Const>Tool`, with a scope suffix on collision. Constants
and types retain their existing names. Allocation also protects the derived
`Spec<Const>`, `Inject<Const>`, `Decode<Const>`, and `With<Const>` identifiers.
Exported-agent and used-toolset helper
packages re-export descriptors under the same resolved names as their specs
packages.

### Tool Payload and Result Flow

1. **Model emits tool call** — Provider adapter produces `model.ToolCall` with
   `jsontext.Value` payload
2. **Planner returns ToolRequest** — Payload stays as `jsontext.Value`
3. **Runtime decodes payload** — Uses generated codecs to validate and decode
4. **Executor runs tool** — Receives typed or raw payload depending on configuration
5. **Runtime encodes result** — Uses generated codecs for consistency
6. **Planner receives ToolOutput** — Gets refs and preview; loads result JSON through `PlanResumeInput.Content`

### ToolsetRegistration

Toolsets bundle execution logic for a group of tools:

```go
type ToolsetRegistration struct {
    Name        string                     // Qualified identifier (service.toolset)
    Description string                     // Human-readable context
    Metadata    policy.ToolMetadata        // Policy metadata
    Execute     func(ctx, *ToolRequest) (*ToolExecutionResult, error)  // Dispatcher
    Specs       []tools.ToolSpec           // JSON codecs and schemas
    TaskQueue   string                     // Optional queue override
    Inline      bool                       // Execute in workflow context
    CallHints   map[tools.Ident]*template.Template   // Tool call DisplayHint templates (typed payload only)
    ResultHints map[tools.Ident]*template.Template   // Tool result preview templates (typed result only)
    PayloadAdapter func(...)               // Pre-decode transformation
    ResultAdapter  func(...)               // Post-encode transformation
    AgentTool   *AgentToolConfig           // Agent-as-tool configuration
}
```

### Tool Call Display Hints (DisplayHint)

The runtime can surface user-facing hints for tool calls (for example in UIs) via the `DisplayHint` field on
hook + stream events.

Contract:

- Hook constructors do not render hints. Tool call scheduled events default to `DisplayHint==""`.
- The runtime may enrich and persist a **durable default** hint at publish time by decoding the typed tool
  payload using generated codecs and executing the `CallHintTemplate` (if registered).
- When typed decoding fails or no template is registered, the runtime leaves `DisplayHint` empty. Hints are
  never rendered against raw JSON bytes.
- If a producer explicitly sets `DisplayHint` (non-empty) before publishing the hook event, the runtime treats
  it as authoritative and does not overwrite it.

For per-consumer wording changes, configure `runtime.WithHintOverrides` on the runtime. Overrides take precedence
over DSL-authored templates for streamed `tool_start` events.

### Pre-child validation (breaking API change)

`AgentToolConfig.PreChildValidator` runs in the parent's typed
`<agentID>.child_dispatch` activity before child admission/start. Its
`AgentToolValidationInput` has `Call`, decoded `Payload`, `ParentRun`, and
`View runtime.ViewLoader`; the previous `Messages` field is removed.

```go
cfg.PreChildValidator = func(ctx context.Context, in *runtime.AgentToolValidationInput) *runtime.AgentToolValidationError {
    messages, err := in.View.LoadMessages(ctx)
    if err != nil {
        return runtime.NewAgentToolValidationError("parent evidence unavailable", nil, nil)
    }
    return validateEvidence(in.Call, in.Payload, messages)
}
```

`LoadMessages` returns an owned canonical snapshot of the parent's supplied view
in its root scope. It is lazy: validators that need only the call do not load
messages. Store I/O stays in the activity; parent message bytes never return to
workflow state. Validators must be retry-safe, like other activity callbacks.
When a load commits pending entries, the activity returns `IndexHead` on success or structured rejection and the workflow adopts it, so following dispatches and planner steps keep the cache warm.
A structured rejection becomes the existing tool error/retry hint and prevents
child start. Activity failures propagate before child admission. A failed view load fails the
activity even if the validator ignores the load error.

Custom engines must implement `RegisterChildDispatchActivity` and
`ExecuteChildDispatchActivity` using `engine.ChildDispatchActivityCall`,
`api.ChildDispatchInput`, and `api.ChildDispatchOutput`. `RegisterAgent` registers
this activity on the parent's workflow queue. The temporary parent-view loading
operation on `RecordContent` is removed.

### Tool Implementation Patterns

**Method-backed tools** — Generated from `BindTo` DSL:

```go
// Generated code maps tool payloads to service method calls
reg := helpers.NewHelpersToolsetRegistration(serviceClient)
rt.RegisterToolset(reg)
```

For method-backed toolset tools, codegen emits a shared
`Dispatch<Tool>Method(ctx, meta, raw, labels, opts)` helper in the owning
toolset package. Runtime service executors call that dispatcher instead of
duplicating the default `BindTo(...)` path. The dispatcher owns payload decode,
tool-to-method transform, label and metadata injection, interceptor wrapping,
method invocation, method-to-tool transform, retry hints, bounds projection, and
server-data projection.

For a tool with injected fields, `MapPayload` must return a non-nil bound method
payload. A mapper error or invalid mapped value becomes `planner.ToolResult.Error`.
The dispatcher does not panic.

When a method-backed toolset tool is also exposed to MCP with
`Expose(AgentRuntime, MCPSurface)` and `MCPPlacement(...)`, generated MCP
adapters call the same dispatcher. Design-time exposure only permits the
surface; runtime deployment still controls toolset registration and MCP server
construction separately.

### Registry-Routed Provider Execution (Service-Side)

Loom MCP supports cross-process tool invocation via the **Internal Tool Registry**. In this mode:

- The registry validates payload JSON, atomically admits or rejects one durable
  run/tool-call identity, and publishes admitted calls to
  `toolset:<toolsetID>:requests`.
- A provider loop claims the exact admitted call before execution. It submits
  deltas and terminal results through the registry, which owns canonical
  publication on `result:<toolUseID>`.

For method-backed service toolsets, codegen emits a provider adapter at:

- `gen/<service>/toolsets/<toolset>/provider.go`

That generated provider implements a dispatcher that decodes the tool payload JSON using generated codecs, adapts into the bound method payload (via generated transforms), calls the bound method, and re-encodes the tool result JSON together with any declared server-data (optional observer-facing server-data and always-on server-only metadata).

Provider failures cross a process and model trust boundary. The generated path
publishes only an explicit Loom error remedy safe message. If the error has no
non-empty safe message, the provider publishes `tool execution failed` instead.
Result-encoding failures use the same generic message. Internal error strings
remain local and must be logged by the service rather than returned in a
`ToolResultMessage`.

To run it, wire the generated provider into the runtime provider loop. The
callback names below stand for direct adapters to the corresponding generated
registry operations:

```go
handler := toolsetpkg.NewProvider(serviceImpl)
providerID := mustRequiredEnv("HOSTNAME") + "/" + toolsetID
admissionRevision := mustRequiredEnv("TOOL_REGISTRY_ADMISSION_REVISION")
go func() {
    err := toolprovider.Serve(
        ctx,
        pulseClient,
        toolsetID,
        handler,
        toolprovider.Registration{
            AdmissionRevision:  admissionRevision,
            Register:           registerProvider,
            Drain:              drainProvider,
            Release:            releaseProvider,
            Claim:              claimToolCall,
            Complete:           completeToolCall,
            PublishOutputDelta: publishToolOutputDelta,
            ReportOverload:     reportToolCallOverload,
        },
        toolprovider.Options{ProviderID: providerID, Pong: pongProvider},
    )
    if err != nil && !errors.Is(err, context.Canceled) {
        panic(err)
    }
}()
```

`Serve` creates one incarnation, registers it before opening shared
consumption, renews its lease, and performs drain/settle/release shutdown. Use
one admission revision for same-contract replicas and rolling updates. Change
it to create a new execution fence. Provider registration and consumer
CallTool/RetryTool requests must declare `toolregistry.WireProtocolVersion`.

The registration token is the immutable admission-generation fence. The exact
token follows the request through provider claim, output deltas, overload
control, completion, and consumer result filtering. Stale queued work is
settled as `stale_registration`; it is never executed under a replacement
provider generation.

This integration has separate ownership:

- **Registry gateway**: owns provider generations, exact-CAS membership,
  durable call decisions, deadlines, result retention, and canonical results.
- **Service provider loop**: claims admitted work and executes generated
  provider adapters under the registry-issued deadline.

### Registry-Routed Execution (Agent/Consumer Side)

On the consumer side (an agent calling registry-routed toolsets), the runtime needs a `ToolCallExecutor` that:

- calls the registry gateway with stable run and model tool-call identity to
  obtain the durable admitted or rejected decision, then
- reads the retained per-call result stream independently and decodes the exact
  token-matched result using compiled tool specs/codecs.

Loom MCP provides a reusable executor implementation in `runtime/toolregistry/executor` that implements `runtime.ToolCallExecutor`:

```go
import (
    toolregexec "github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry/executor"
)

exec := toolregexec.New(registryClient, pulseClient, specs)

// Use exec.Execute as the executor for registry-backed toolsets.
```

The registry wire protocol and deterministic stream IDs are defined in `runtime/toolregistry`:

- Toolset request stream: `toolset:<toolsetID>:requests`
- Per-call result stream: `result:<toolUseID>`

Identical retries observe the same decision and result stream. An admitted call
that later loses routing certainty resolves as `outcome_unknown`; callers must
not reclassify it as safely replannable. Queue publication rejects atomically
when unread backlog is full, and stream trimming is garbage collection only.
The registry owns one absolute execution deadline and one later absolute result
retention deadline for every call.

The executor carries `outcome_unknown` as typed error metadata across activity
and planner boundaries. The runtime stops ordinary recovery and requests an
`outcome_unknown` finalization turn with no tools. It bypasses configured
terminal tool plans and rejects any tool call returned by that finalizer.
If the run requires a completion tool, it fails without further planning or tool
execution instead of bypassing that completion requirement. Error message text
alone does not select this behavior.

### Registry discovery & catalog sync (runtime/registry)

If you need runtime discovery of toolsets and schemas (e.g., tool catalogs that change without a `loom gen`),
use the client-side components in `runtime/registry`:

- `GRPCClientAdapter`: wraps a generated gRPC registry client into a `RegistryClient` interface
- `Manager`: multi-registry discovery with caching and periodic sync (`StartSync`/`StopSync`)
- `SearchClient`: cross-registry search with semantic-first + keyword fallback when supported

Both plain `Manager.Search` and the richer `SearchClient.Search` use the same
concurrent fan-out and partial-failure contract: every selected registry starts
independently, successful results are retained when only some registries fail,
and the call fails only when every selected registry fails. `SearchClient`
adds semantic fallback and filtering. Before applying `MaxResults`, it orders
results by descending relevance, with ties ordered by ascending `Origin` and `ID`.

These are client-side helpers. The standalone registry service implementation lives under `loom-mcp/registry`.
Generated registry-backed agent registrations intentionally freeze their discovered tool specs at
`RegisterUsedToolsets`: call the generated `DiscoverAndPopulate` during startup, inspect through
`Specs()`, then register. Refresh attempts after registration fail so runtime schemas and codecs
cannot drift while workflows are executing; restart and re-register the agent to adopt a newer catalog.

**Inline tools** — Custom executor implementation:

```go
reg := runtime.ToolsetRegistration{
    Name: "myservice.helpers",
    Execute: func(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
        // Decode payload, execute logic, return the durable result envelope.
        return runtime.Executed(&planner.ToolResult{
            Name:       call.Name,
            ToolCallID: call.ToolCallID,
            Result:     /* ... */,
        }), nil
    },
    Specs: []tools.ToolSpec{...},
}
rt.RegisterToolset(reg)
```

### Runtime Interceptors

`runtime.WithInterceptors(...)` registers inline call-path interceptors. Hooks
publish durable observability events; interceptors run before or after execution
and may change requests, results, or event publication before durable events are
emitted.

Interceptors are opt-in typed interfaces. A value may implement one or more of:

- `RunInterceptor`: `BeforeRun` / `AfterRun`
- `ToolInterceptor`: `BeforeTool` / `AfterTool`
- `ModelInterceptor`: `BeforeModel` / `AfterModel`
- `EventInterceptor`: `BeforeEvent` / `AfterEvent`

Ordering is registration order. Runtime-level interceptors run before agent
interceptors. Generated `RunPolicy(func(){ Interceptors("audit") })` stores
design-owned IDs; application code supplies the implementations with
`runtime.WithNamedInterceptors(...)`.

Key mutation rules:

- `BeforeTool` can rewrite raw JSON payloads or return a `ToolExecutionResult`
  to skip the executor. `AfterTool` can replace the `planner.ToolResult`, the
  full `ToolExecutionResult`, or the error.
- `BeforeModel` wraps clients returned by `PlannerContext.ModelClient(id)` after
  cache/tool-policy decorators and before tracing. It can rewrite the
  `model.Request`. For non-streaming completions it can also short-circuit with
  a response. Streaming calls do not synthesize a `model.Streamer` from a
  response short-circuit; returning a response from `BeforeModel` on `Stream`
  fails the call. `AfterModel` can replace the response or error.
- `BeforeEvent` runs in `runtime.publish_hook` before runlog append, stream
  publish, and hook-bus publish. Returning `Drop` makes the event absent from all
  three surfaces.
- Empty `After*` decisions are observer no-ops. `AfterTool`, `AfterRun`, and
  `AfterModel` adopt the decision's `Err` (including a nil `Err`, which clears
  the current error) only when the decision also replaces the result
  (`Execution`/`Result`, `Output`, or `Response`) or carries a non-nil `Err`.
  `AfterEvent` adopts only a non-nil `Err`; observer interceptors can never
  clear an event publication error, so a failed canonical run-log append still
  fails the run.
- Interceptor errors short-circuit the active path.

`runtime.NewRetryAndReflectInterceptor(...)` implements the tool path to convert
tool execution errors into planner-visible tool errors with structured
`planner.RetryHint` guidance, keeping the run alive so the planner can repair
the call. Its generated error and retry message use fixed framework text. They
do not persist the raw service error because that error can contain submitted
arguments or secrets.
Failure counts are process-local. Each interceptor keeps at most 4096 run/tool
keys and refreshes a key after each failure. When full, it discards the
least-recently-failed keys. A discarded key starts at attempt one if it fails
again.

### Model-Facing Skills

`runtime.NewSkillToolsetRegistration(...)` exposes local skill directories as
ordinary model tools:

- `<toolset>.list_skills`
- `<toolset>.load_skill`
- `<toolset>.load_skill_resource`

This complements MCP `skill://` resources. MCP clients can discover and read
skills through resources, while model planners can advertise the same skill
roots as tool calls when the model needs to inspect skill instructions.

Skill metadata comes from `SKILL.md` YAML frontmatter:

```yaml
id: code-review
name: Code Review
description: Review code changes for correctness.
allowed_tools: [shell]
preload: on_start
reload: per_call
```

`list_skills` returns this metadata. `load_skill` and `load_skill_resource`
include metadata on the returned content. `preload: on_start` caches `SKILL.md`
when the toolset registration is built; `reload: per_call` reloads files from
disk for each load request. Duplicate skill IDs and invalid mode values fail
discovery.

### Artifact Store And Tools

Configure persisted run artifacts with `runtime.WithArtifactStore(...)`. Tools may attach
`artifact.Content` values to `planner.ToolResult.Artifacts`; the runtime saves those bodies
and propagates only `artifact.Ref` values across workflow-safe boundaries:

- `planner.ToolOutput.Artifacts`
- `api.ToolEvent.Artifacts` and `api.ToolCallOutput.Artifacts`
- `hooks.ToolResultReceivedEvent.Artifacts`
- durable memory `ToolResultData.Artifacts`

`runtime.NewArtifactToolsetRegistration(...)` exposes two model-facing tools:

- `<toolset>.list_artifacts` returns refs filtered by `mime_type`, metadata, and limit.
- `<toolset>.load_artifact` returns bounded `{content, mime_type, truncated, size_bytes}`.

Artifact metadata filters require every requested key to exist with the exact
value. An empty value matches a present empty value, not a missing key.

Artifact bodies are never embedded in hook/runlog payloads or planner workflow envelopes.

#### Executor result envelope: `runtime.ToolExecutionResult`

Tool executors return `*runtime.ToolExecutionResult` rather than `*planner.ToolResult` directly:

```go
type ToolExecutionResult struct {
    ToolResult *planner.ToolResult // Durable planner-visible result (always required)
    Pause      *api.ToolPause      // Optional runtime-owned pause signal for the current batch
}
```

- `ToolResult` is the durable, planner-facing tool outcome. It lands in the
  transcript, the cumulative `ToolOutputs` history, and the
  `ToolResultReceivedEvent` hook fanout exactly as before.
- `Pause` is a current-batch-only signal. When non-nil it is projected into the
  workflow's await queue (e.g. as an `AwaitClarification` item) and never
  persists into cumulative `ToolOutputs`, so replaying a pause envelope cannot
  re-trigger an await on a later turn.

For the common case where an executor just emits a durable result with no
runtime-owned pause, use the `runtime.Executed(...)` helper:

```go
return runtime.Executed(&planner.ToolResult{ /* ... */ }), nil
```

To request a clarification from the user from inside an executor, attach a
`ToolPause`:

```go
return &runtime.ToolExecutionResult{
    ToolResult: &planner.ToolResult{
        Name:       call.Name,
        ToolCallID: call.ToolCallID,
        Result:     partialResult,
    },
    Pause: &api.ToolPause{
        Clarification: &api.ToolPauseClarification{
            ID:       "ambiguous-target",
            Question: "Which target did you mean: A or B?",
        },
    },
}, nil
```

The runtime contract enforces that a `Pause` must not be paired with a tool
error and must carry a non-nil payload (validated by
`validateToolPauseContract`).

A tool result with `RetryHint.Reason == planner.RetryReasonMissingFields` must
provide a nonempty `ClarifyingQuestion`. Empty and whitespace-only questions fail
tool-result validation with a non-retryable `*engine.ApplicationError` whose
`Type` is `tool_result_contract`.

> **Breaking change (v0.52.0)**: the executor signature changed from
> `func(ctx, *ToolRequest) (*planner.ToolResult, error)` to
> `func(ctx, *ToolRequest) (*ToolExecutionResult, error)`. Custom executors,
> the registry-routed executor, the MCP executor, and example scaffolds all
> need to wrap their return value via `runtime.Executed(...)` or construct
> the envelope explicitly.

**Agent-as-tool** — Nested agent execution:

```go
reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{
    AgentID: agent.Ident("service.nested"),
    Route:   runtime.AgentRoute{...},
    // Optional per-tool prompts/templates
})
```

### ToolCallMeta

Executors receive explicit per-call metadata:

```go
type ToolCallMeta struct {
    RunID            string  // Workflow execution identifier
    SessionID        string  // Logical session grouping
    TurnID           string  // Conversational turn identifier
    ToolCallID       string  // Unique tool invocation ID
    ParentToolCallID string  // Parent tool call (for agent-as-tool)
}
```

### Optional server-data (reserved `"server_data"` payload field)

Tools can optionally produce **observer-facing server-data** (often projected into UI artifacts) that is never sent to model providers.
The runtime supports a per-call optional server-data toggle via a reserved top-level tool payload field:

- `{"server_data":"auto"}` — use the tool default
- `{"server_data":"on"}` — enable optional server-data (when the tool declares it)
- `{"server_data":"off"}` — disable optional server-data for this call

The runtime strips the reserved `"server_data"` field from the execution payload before decoding, and records the
normalized value on the tool call metadata (`ServerDataMode`). Tool payload schemas must not define a top-level
property named `"server_data"`.

### Bounded Results

Tools that return partial views of larger datasets should use the `BoundedResult`
DSL helper. This enforces a canonical bounded-result contract:
bounded tools declare their contract in `tools.ToolSpec.Bounds`, successful
executions must populate `planner.ToolResult.Bounds`, and the runtime projects
the canonical bounds fields (`returned`, `total`, `truncated`,
`refinement_hint`, and optional `next_cursor`) into the emitted result JSON and
hook/stream payloads.

The runtime enforces one strict contract across all result ingress paths
(regular execution and externally provided await results):

- unbounded tools must not return bounds metadata,
- error tool results must not return bounds metadata,
- successful bounded results must include bounds metadata,
- when `truncated=true`, bounds must include either `next_cursor` or
  `refinement_hint`.

```go
type Bounds struct {
    Returned       int     // Items in this response
    Total          *int    // Total items available (when known)
    Truncated      bool    // True if limits were applied
    RefinementHint string  // Guidance for narrowing queries
}
```

The runtime surfaces bounds via `ToolResult.Bounds`, encoded `tool_result` JSON,
result-hint templates under `.Bounds`, hook events, and stream events. Services
own truncation logic; the runtime only propagates and projects what tools
report.

Transcript-facing tool results use a stricter provider contract than execution
boundaries:

- canonical raw bytes live in `ToolOutput.Result`, `ToolResultReceivedEvent.ResultJSON`,
  and durable memory-event `result_json`,
- `model.ToolResultPart.Content` carries semantic provider-facing content only:
  decoded JSON-compatible values on success or plain error text with `IsError=true`,
- oversized successful transcript content projects to an explicit omission object:
  `{"omitted":true,"reason":"size_limit","preview":"...","bounds":{...}}`.

For method-backed `BindTo` tools, the bound service method result still needs to
carry the canonical bounded fields so the generated executor can build
`planner.ToolResult.Bounds` before runtime projection. Explicit tool-facing
`Return(...)` shapes must not duplicate those canonical fields. Within the bound
method result, only `returned` and `truncated` may be required; `total`,
`refinement_hint`, and `next_cursor` remain optional and are omitted from emitted
JSON whenever runtime bounds omit them. `BoundedResult(...)` still owns the
tool-facing contract exposed to models.

When a service boundary must assemble canonical result JSON outside
`ExecuteToolActivity` itself, use `runtime.EncodeCanonicalToolResult(...)`
instead of calling the generated result codec and bounded-result projection
helpers separately.

---

## Agent-as-Tool Composition

Agents can expose tools via `Export` blocks and consume them via `Use`. When invoked,
nested agents execute as child workflows with their own run IDs and event streams.
Temporal child workflows use request-cancel parent-close behavior so canceling
or closing a parent requests child cancellation instead of terminating the
child as a generic failure.

### How It Works

1. Parent planner requests tool (e.g., `"service.analysis.analyze"`)
2. Runtime identifies it as an agent-tool via `ToolSpec.IsAgentTool`
3. Runtime starts child workflow using `AgentToolConfig.Route`
4. Child agent executes its own plan/execute loop
5. Runtime returns a parent `ToolResult` derived from the child run output (final text and/or finalizer output, plus aggregated telemetry). **Artifacts are not propagated to the parent tool result**; they remain attached to the child tool events.
6. `ChildRunLinked` event links parent and child for streaming

### Configuration

```go
reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{
    AgentID:         agent.Ident("service.data-analyst"),
    Route:           runtime.AgentRoute{
        ID:               agent.Ident("service.data-analyst"),
        WorkflowName:     "DataAnalystWorkflow",
        DefaultTaskQueue: "orchestrator.data-analyst",
    },
    SystemPrompt:    "You are a data analysis expert.",
    AgentToolContent: runtime.AgentToolContent{
        Templates: compiledTemplates, // Per-tool user message templates (optional)
        Texts:     textMessages,      // Alternative to templates (optional)
    },
    JSONOnly:        true,                // Return structured results
    Finalizer:       myFinalizer,         // Custom result aggregation
})
```

### Per-Tool Content

Configure how tool payloads become the nested agent's initial user message.
When you do not configure consumer-side content, the runtime uses a deterministic
default: the canonical JSON tool payload bytes (verbatim) as the nested user
message.

```go
// Plain text for all tools
runtime.WithTextAll(toolIDs, "Process this: {{ . }}")

// Template for specific tool
runtime.WithTemplate(toolID, compiledTemplate)

// PromptSpec for a tool (optional; payload-only)
runtime.WithPromptSpec(toolID, "my.prompt.id")

// Custom prompt builder
cfg.Prompt = func(id tools.Ident, payload any) string {
    return fmt.Sprintf("Handle %s request: %v", id.Tool(), payload)
}
```

### Finalizers

Finalizers aggregate child results into the parent tool result:

```go
// Pass-through: use JSONOnly aggregation
runtime.PassThroughFinalizer()

// Tool-based: call a dedicated aggregation tool
runtime.ToolResultFinalizer(tools.Ident("helpers.aggregate"), func(ctx, input) (any, error) {
    return map[string]any{"children": input.Children}, nil
})

// Custom: full control over aggregation
runtime.FinalizerFunc(func(ctx, input FinalizerInput) (ToolResult, error) {
    // Build result from input.Children
    return planner.ToolResult{Result: aggregated}, nil
})
```

---

## Human-in-the-Loop

Runs can pause and resume via interrupt signals, enabling approval workflows,
clarification requests, and external tool integration.

### Pause and Resume

```go
// Pause a run (from outside the workflow)
err := rt.PauseRun(ctx, interrupt.PauseRequest{
    RunID:       "run-123",
    Reason:      "human_review",
    RequestedBy: "policy-engine",
})

// Resume after approval
err := rt.ResumeRun(ctx, interrupt.ResumeRequest{
    RunID:       "run-123",
    Notes:       "Approved by admin",
    Messages:    additionalMessages, // Optional
})
```

### Clarification Requests

Planners can request missing information:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        Clarification: &planner.AwaitClarification{
            ID:            "clarify-device",
            Question:      "Which device should I configure?",
            MissingFields: []string{"device_id"},
        },
    },
}
```

`AwaitClarification.Question` must be nonempty and must contain non-whitespace
text. Start and resume planner activities reject blank questions before committing
model presentations or storing planner-result content. The activity fails with a
non-retryable `*engine.ApplicationError` whose `Type` is `planner_contract`;
staged model presentations are discarded. Valid question text is preserved.

The runtime pauses the workflow and emits an `AwaitClarification` event. Callers
respond via:

```go
err := rt.ProvideClarification(ctx, interrupt.ClarificationAnswer{
    RunID:  "run-123",
    ID:     "clarify-device",
    Answer: "Device ID is ABC-123",
})
```

### External Tools

Planners can request tools that execute out-of-band:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        ExternalTools: &planner.AwaitExternalTools{
            ID: "external-1",
            Items: []planner.AwaitToolItem{{
                Name:       tools.Ident("external.fetch"),
                ToolCallID: "tc-ext-1",
                Payload:    jsontext.Value(`{"url":"..."}`),
            }},
        },
    },
}
```

Callers provide results via:

```go
err := rt.ProvideToolResults(ctx, &api.ToolResultsSet{
    RunID: "run-123",
    ID:    "external-1",
    Results: []*api.ProvidedToolResult{
        {
            ToolCallID: "toolcall-1",
            Name:       tools.Ident("chat.ask_question.ask_question"),
            // Contract: canonical JSON bytes matching the tool's Return schema.
            Result: jsontext.Value(`{"answers":[{"question_id":"...","selected_ids":["approve"]}]}`),
        },
    },
})
```

Provided tool results are strict boundary inputs:

- each item must be exactly one of: `Error` or non-null `Result`,
- if the tool is bounded and successful, `Bounds` must be present and satisfy
  bounded-result invariants.

Those rules apply only at execution/history boundaries. Once the runtime projects
tool output into transcript messages, models never see raw `Result` bytes or
structured Go error values.

### Typed Human Input

Graph workflow planners can request schema-typed human input without pretending
the answer is a tool result:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        Items: []planner.AwaitItem{planner.AwaitTypedInputItem(&planner.AwaitTypedInput{
            ID:     "approval",
            Title:  "Approval",
            Schema: jsontext.Value(`{"type":"object","properties":{"approved":{"type":"boolean"}}}`),
        })},
    },
}
```

The runtime emits an `AwaitTypedInput` hook event, which the stream subscriber
forwards to clients as an `await_typed_input` stream event (`AwaitTypedInputPayload`
with `id`, `title`, and `schema`) alongside a `workflow` event with
`phase="paused"`, so UIs can render an input form instead of showing the run as
hung. Callers resume with:

```go
err := rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
    RunID:   "run-123",
    ID:      "approval",
    Payload: jsontext.Value(`{"approved":true}`),
})
```

The resumed planner receives `ID` and `PayloadRef` in `PlanResumeInput.TypedInputs`,
with empty `Payload`. Load answer JSON with `input.Content.LoadTypedInput(ctx, answer.PayloadRef)`.
Typed-input answers are not copied into `ToolOutputs` or model messages, and are
not passed to the finalize planner.

### Tool Confirmation (Design-Time + Runtime Overrides)

Loom MCP supports **runtime-enforced** confirmation gates for sensitive tools.

There are two ways to enable confirmation:

- **Design-time (recommended, common case):** declare `Confirmation(...)` inside a tool DSL.
  Codegen stores the confirmation policy in the generated `tools.ToolSpec.Confirmation`.
- **Runtime (dynamic/override):** supply `runtime.WithToolConfirmation(...)` when constructing the
  runtime. This can require confirmation for additional tools and/or override the design-time behavior
  for specific tool IDs.

At execution time, the workflow:

- Emits an out-of-band confirmation request (using `AwaitConfirmation`) before executing the
  target tool call.
- Waits for a user approval/denial decision.
- Executes the tool only when approved.
- When denied, synthesizes a **schema-compliant** tool result (so the transcript remains valid and
  the planner can react to the denial deterministically).
- Schedules one logical planner resume after the entire await queue is satisfied.
  The workflow acts on that result, including its tools or final answer. This also
  applies to planner awaits in the same turn as tool calls and to tool pauses.
  Resume activities remain retryable.

#### Confirmation protocol

The runtime uses a runtime-owned confirmation protocol to obtain an explicit approval/denial
decision before executing a confirmed tool.

- **Await payload** (hook + stream event):

  ```json
  {
    "id": "...",
    "title": "...",
    "prompt": "...",
    "tool_name": "atlas.commands.change_setpoint",
    "tool_call_id": "toolcall-1",
    "payload": { "...": "canonical tool arguments (JSON)" }
  }
  ```

- **Provide decision** (using `ProvideConfirmation`):

  ```go
  err := rt.ProvideConfirmation(ctx, interrupt.ConfirmationDecision{
      RunID:       "run-123",
      ID:         "await-1",
      Approved:    true,              // or false
      RequestedBy: "user:123",        // optional, for audit
      Labels:      map[string]string{"source": "front-ui"},
      Metadata:    map[string]any{"ticket_id": "INC-42"},
  })
  ```

Consumers should treat confirmation as a **runtime protocol**, not as a user-defined tool:

- Use the accompanying `RunPaused` reason (`await_confirmation`) to decide when to display a confirmation UI.
- Do not couple UI behavior to a specific confirmation tool name; treat it as an internal transport detail.

This keeps the runtime generic: any UI/system can implement a compatible confirmation transport.

### Tool authorization events

When a decision is provided via `ProvideConfirmation`, the runtime emits a first-class authorization event:

- **Hook event**: `hooks.ToolAuthorization`
- **Stream event type**: `tool_authorization`

This event is emitted exactly once per confirmed tool call and captures the durable authorization record:

- `tool_name`: the tool being authorized
- `tool_call_id`: the tool call identifier
- `approved`: true/false decision
- `summary`: deterministic runtime-rendered summary (derived from the confirmation prompt)
- `approved_by`: copied from `interrupt.ConfirmationDecision.RequestedBy` and intended to be a stable principal identifier (for example, `user:<id>`)

The event is emitted immediately after the decision is received:

- **Approved**: emitted before the tool executes.
- **Denied**: emitted before the denied tool result is synthesized.

Consumers (UIs, audit stores, session recorders) should rely on `tool_authorization` for “who/when/what” rather than inferring authorization from tool results.

#### Runtime validation

The runtime treats confirmation as a boundary and validates:

- The confirmation `ID` matches the pending await identifier when provided.
- The decision object is well-formed (non-empty `RunID`, boolean `Approved` value).

Notes:

- Confirmation templates (`PromptTemplate` and `DeniedResultTemplate`) are Go `text/template` strings
  executed with `missingkey=error`. In addition to the standard template functions (e.g. `printf`),
  Loom MCP provides:
  - `json v` → JSON encodes `v` (useful for optional pointer fields or embedding structured values).
  - `quote s` → returns a Go-escaped quoted string (like `fmt.Sprintf("%q", s)`).

---

## Hooks and Streaming

### Hook Bus

The runtime publishes events to an internal bus (`hooks.Bus`). Default subscribers
handle memory persistence and stream forwarding.

**Determinism note:** When using a durable workflow engine (e.g., Temporal),
workflow code must be deterministic and must not trigger external I/O. The
runtime therefore routes workflow-emitted hook events through a dedicated hook
activity (`runtime.publish_hook`), which publishes to the bus outside the
workflow thread. Activities and other non-workflow code publish directly.

Hook activity payloads use JSON v2 with the standard nanosecond compatibility
format for `time.Duration`. Per-run policy budgets and timeouts therefore cross
the workflow/activity boundary as integer nanoseconds, while JSON field tags and
duration-valued map keys retain their normal encoding behavior.

**Event types:**

| Event                                       | When                                                |
| ------------------------------------------- | --------------------------------------------------- |
| `RunStarted`                                | Run begins                                          |
| `RunCompleted`                              | Run finishes (success, failed, canceled)            |
| `RunPaused` / `RunResumed`                  | Human-in-the-loop and await transitions              |
| `RunPhaseChanged`                           | Phase transitions (planning, executing_tools, etc.) |
| `PromptRendered`                            | Runtime resolves and renders a prompt spec          |
| `ToolCallScheduled`                         | Tool activity scheduled                             |
| `ToolResultReceived`                        | Tool completes                                      |
| `ToolCallUpdated`                           | Parent tool discovers more children                 |
| `ToolCallArgsDelta`                         | Best-effort streamed argument delta                 |
| `AssistantMessage` / `AssistantTurnCommitted` | Assistant output and committed-turn boundary      |
| `PlannerNote` / `ThinkingBlock`             | Planner reasoning                                   |
| `AwaitClarification` / `AwaitQuestions`     | Clarification pause requests                        |
| `AwaitTypedInput` / `AwaitExternalTools`    | Typed or externally executed work pause requests    |
| `AwaitConfirmation` / `ToolAuthorization`   | Approval request and recorded decision              |
| `RetryHintIssued` / `MemoryAppended`        | Derived retry and transcript projection events      |
| `PolicyDecision`                            | Policy evaluation result                            |
| `Usage`                                     | Token usage report                                  |
| `HardProtectionTriggered`                   | Runtime hard-protection circuit activation          |
| `ChildRunLinked`                            | Agent-as-tool child run link                        |

When the planner returns several tool calls in one turn, the runtime publishes
`ToolCallScheduled` for every call in the batch before it dispatches any of them.
A subscriber that returns an error for one of those events, for example because
it could not record durable evidence for the call, aborts the batch before any
tool has started. Calls that carry a request error, and calls to unknown tools,
receive their synthesized `ToolResultReceived` during this scheduling phase. A
batch that aborts can leave calls with a `ToolCallScheduled` event and no
`ToolResultReceived`: calls scheduled before a rejected event, calls not yet
dispatched, and started calls that were cancelled. A subscriber must treat any
such call as not known to have run, not as failed.

### Custom Subscribers

```go
sub := hooks.SubscriberFunc(func(ctx context.Context, evt hooks.Event) error {
    switch e := evt.(type) {
    case *hooks.ToolResultReceivedEvent:
        log.Printf("Tool %s completed in %v", e.ToolName, e.Duration)
    }
    return nil
})

subscription, _ := rt.Bus.Register(sub)
defer subscription.Close()
```

### Stream Sink

The `stream.Sink` interface delivers client-facing events:

```go
type Sink interface {
    Send(ctx context.Context, event Event) error
    Close(ctx context.Context) error
}
```

**Stream event types:**

| Event                  | Payload                                                                      |
| ---------------------- | ---------------------------------------------------------------------------- |
| `prompt_rendered`      | `PromptRenderedPayload` (`prompt_id`, `version`, `scope`)                    |
| `tool_start`           | `ToolStartPayload` (tool_call_id, tool_name, payload)                        |
| `tool_end`             | `ToolEndPayload` (result, error, duration, telemetry)                        |
| `tool_update`          | `ToolUpdatePayload` (expected_children_total)                                |
| `tool_call_args_delta` | Best-effort planner-authored argument progress; runtime model fragments stay private |
| `tool_output_delta`    | Incremental tool output                                                        |
| `assistant_reply`      | `AssistantReplyPayload` (`text`, optional `presentation_id`)                 |
| `assistant_turn`       | Atomic committed assistant content (`message` legacy, or `presentation_ids` plus ordered `messages`) |
| `planner_thought`      | `PlannerThoughtPayload` (note/thinking fields, optional `presentation_id`)   |
| `model_presentation`   | `ModelPresentationPayload` (`presentation_id`, `started`/`accepted`/`discarded`) |
| `await_clarification`  | `AwaitClarificationPayload`                                                  |
| `await_confirmation`   | `AwaitConfirmationPayload`                                                   |
| `await_questions`      | `AwaitQuestionsPayload` (`tool_name`, `tool_call_id`, `payload`, `questions`) |
| `await_typed_input`    | `AwaitTypedInputPayload` (`id`, `title`, `schema`)                           |
| `await_external_tools` | `AwaitExternalToolsPayload`                                                  |
| `tool_authorization`   | Recorded approval or denial for a tool call                                   |
| `usage`                | `UsagePayload` (input_tokens, output_tokens)                                 |
| `workflow`             | `WorkflowPayload` (phase, status, error_kind, retryable, error, debug_error) |
| `child_run_linked`     | `ChildRunLinkedPayload` (child run link)                                     |
| `session_stream_started` / `session_stream_end` | Session stream lifecycle                                  |
| `run_stream_end`       | Run stream lifecycle boundary                                                 |

### Stream Profiles

Control which events reach each audience:

```go
// All events, child runs linked
stream.DefaultProfile()

// User chat view (default for most UIs)
stream.UserChatProfile()

// Debug view (all events; child runs linked)
stream.AgentDebugProfile()

// Metrics only (usage, workflow)
stream.MetricsProfile()
```

### Workflow payload contract (phases, terminal status, and errors)

The runtime emits:

- `RunPhaseChanged` hook events for **non-terminal** phase transitions (`planning`, `executing_tools`, `synthesizing`, etc.)
- `RunPaused` hook events when execution is suspended awaiting external action (awaits, manual pause)
- a single `RunCompleted` hook event per run for the **terminal** lifecycle state

The stream subscriber translates these into `workflow` stream events:

- **Non-terminal updates** (from `RunPhaseChanged`): `phase` only.
- **Pause updates** (from `RunPaused`): `phase="paused"` plus `reason` (for example, `await_queue` or `await_clarification`). The stream does not end; the run resumes when the awaited input arrives.
- **Terminal update** (from `RunCompleted`): `status` + terminal `phase`.

Terminal status mapping:

- `status="success"` → `phase="completed"`
- `status="failed"` → `phase="failed"`
- `status="canceled"` → `phase="canceled"`

Cancellation is not an error:

- For `status="canceled"`, the workflow payload must not include a user-facing `error`.
- Every leaf in the error graph must be `context.Canceled` or a Temporal
  cancellation. The runtime treats mixed cancellation and failure graphs as
  failed runs and preserves each error leaf.
- Temporal records a mixed graph as a non-retryable application failure. The
  adapter restores its cancellation evidence after failure conversion. This
  applies to activities, children, top-level waits, and restart queries.
- `context.DeadlineExceeded` is a timeout failure. It is not a cancellation.
- A canceled tool activity or agent child stops the owning run. The runtime
  does not convert this condition into a failed tool result.
- The classifier makes each decision with one bounded traversal. It detects
  cycles. Invalid or oversized error graphs fail the run.
- Temporal replaces an invalid graph with a static, non-retryable failure. Its
  failure converter does not traverse the rejected graph.
- Workflow, one-shot, observed, and restart completion paths replace invalid
  graphs before interceptors, hook classification, logging, or serialization.

Failures are structured:

- For `status="failed"`, the workflow payload includes:
  - `error_kind`: stable classifier (provider kinds like `context_overflow`, `rate_limited`, `unavailable`, or runtime kinds like `timeout`/`internal`)
  - `retryable`: whether retrying may succeed without changing input
  - `error`: **user-safe** message suitable for direct display
  - `debug_error`: raw error string for logs/diagnostics (not for UI)

### Event reliability and authority

Runtime event surfaces are related projections, not interchangeable durable
logs:

| Surface | Role | Failure behavior |
| --- | --- | --- |
| Workflow transcript index | Live deterministic transcript references for the active run | Owned by the workflow engine; loaded into messages in activities |
| `runlog.Store` | Canonical append-only hook event record | Append or run-metadata update failure fails the hook activity so the engine can retry or stop the run |
| Session stream sink | Active-session client projection | Best effort; lookup/send failures are logged, ended sessions are skipped, and sessionless one-shot runs do not stream |
| Hook bus | Local subscriber and derived-projection fanout | Built-in subscribers are best effort; explicitly critical subscribers may propagate an error and fail the hook activity |
| `memory.Store` | Derived per-run transcript/event projection | The built-in memory subscriber is best effort and may be incomplete after a subscriber failure |
| `memory.Service` | Explicit long-term entry store | Written only through its `PutEntry`/ingest contract; it is not the transcript or run log |

Session stores also persist the coarse run lifecycle. The first committed
`completed`, `failed`, or `canceled` status is terminal. Later updates may add
metadata or prompt references, but they cannot replace that terminal outcome or
reopen the run as pending, running, or paused. This makes retrying concurrent
terminal hook projections safe across in-memory and Mongo-backed stores.

Ending a session is also idempotent: the first committed `EndedAt` remains
authoritative, including when concurrent callers request different timestamps.
Mongo run admission and child linking serialize with session termination: they
either commit before termination or reject the ended session. Existing run
metadata can still be updated after its session ends.

The in-memory session store and long-term memory service copy metadata on
input and output. Supported mutable metadata is acyclic JSON-shaped data:
string-keyed maps, nested slices, byte slices, and scalar values. Opaque Go
values must be treated as immutable; cyclic object graphs are unsupported.
Empty session label maps are copied too.

`ToolCallArgsDelta` is intentionally excluded from the durable run event log and
hook bus because it is a high-volume, best-effort UX signal. The finalized tool
call remains canonical. `transcript.BuildMessagesFromEvents` consumes
`memory.Event` values; it is not a direct runlog replay API. Applications that
need a durable projector must install a critical subscriber or build from a
separately defined durable contract rather than assuming stream or default
memory delivery is complete.

## Policy Enforcement

Policy engines decide which tools are available each turn and enforce caps.

### Policy Engine Interface

```go
type Engine interface {
    Decide(ctx context.Context, input Input) (Decision, error)
}
```

**Input:**

```go
type Input struct {
    RunContext    run.Context        // Run identifiers and labels
    Tools         []ToolMetadata     // Candidate tools
    RetryHint     *RetryHint         // Planner guidance after failures
    RemainingCaps CapsState          // Current execution budgets
    Requested     []tools.Ident      // Explicitly requested tools
    Labels        map[string]string  // Context labels
}
```

**Decision:**

```go
type Decision struct {
    AllowedTools []tools.Ident      // Tools permitted this turn
    Caps         CapsState          // Updated execution budgets
    DisableTools bool               // Force final response
    Labels       map[string]string  // Labels to propagate
    Metadata     map[string]any     // Audit trail data
}
```

`AllowedTools` is authoritative: a nil or empty list permits no ordinary tools.
The runtime applies it to both the planner catalog and execution, so a planner
cannot bypass it by returning a blocked call. With no policy engine configured,
all candidates remain available subject to per-run overrides.

The basic policy engine applies allow/block filters before retry hints. A
`RestrictToTool` hint can only narrow the filtered candidates; it cannot restore
a blocked tool or introduce a tool outside the requested set. Set
`basic.Options.DisableRetryHints` to ignore hint-based restrictions and cap
changes, even when no allow/block filters are configured.

### Caps State

```go
type CapsState struct {
    MaxToolCalls           int
    RemainingToolCalls     int
    MaxRecoveryTurns       int
    RemainingRecoveryTurns int
    ExpiresAt              time.Time // Deprecated; ignored by runtime
}
```

The default `MaxRecoveryTurns` value is 3. The runtime uses this default when
the design and the run do not set a positive value.

One recovery turn runs one replacement planner activity after rejected model
output or recoverable tool output. Successful registered domain work resets
the allowance. Failed work and `runtime.tool_unavailable` do not reset it.

Model recovery stores a fingerprint, byte count, usage, attempt, and bounded
framework guidance. It does not store rejected model output or submitted tool
arguments. The activity records each rejected unary call, including calls that
the planner catches. It matches recovery to the exact error that the planner
returns. Concurrent model calls cannot erase that match. Token totals include
all accepted and rejected billed calls. Every durable total uses checked
addition and fails if a count overflows. Generated retry hints are at most 4096
encoded bytes. For a stream, the runtime publishes provisional text and thinking
live but stages their canonical commitment until terminal validation and
provider cleanup both succeed. A rejected or cleanup-failed stream cannot append
its presentation content to the canonical run log.

Structured-output and output-limit recovery disables all tools for the
replacement turn. The runtime checks the same effective tool catalog at the
model boundary and the workflow boundary. A tool-call replacement must carry a
non-nil, bounded, unique copy of the rejected request catalog. The workflow
rejects a catalog that adds a tool outside the active policy. When the recovery
allowance is empty, a normal start or resume enters the tool-free failure
finalizer. A rejection from that finalizer ends the run and does not recurse.
The model and recovery contracts accept at most 256 tool definitions per
request, so every accepted request has a durable exact-catalog representation.

### Bookkeeping budgets

Generated policy metadata classifies each tool as `budgeted` or `bookkeeping`.
`MaxToolCalls` counts only budgeted domain calls. The per-turn limit counts all
calls.

The runtime accepts a provider tool-call batch only when the complete batch
fits. It never removes selected calls to make a batch fit the remaining budget.
A bookkeeping-only batch can run when the domain-call budget is empty.

Successful bookkeeping calls do not reset the recovery budget. Their requests
and results remain in the durable transcript. Their successful outputs do not
enter the next planner request.

`TerminalRun` tools must use the bookkeeping class. A successful terminal call
completes the run without another planner request. A failed terminal call fails
the run.


Caps constrain the calls a planner selected; they do not truncate the tool
catalog shown to the planner. The pre-plan policy envelope therefore carries
the full policy allowlist, and per-turn or remaining-call caps are applied only
after planning. The runtime-owned `tool_unavailable` recovery call remains
executable under an active allowlist so rewritten unavailable calls can produce
their structured recovery result. Per-run tool and tag filters also preserve
this internal recovery call. The internal tool is not a policy candidate. The
runtime replaces every direct call payload with the exact server-owned catalog
at the model boundary. The activity boundary preserves that narrower catalog
when the run policy allows more tools.

`CapsState` owns counter budgets only. Its legacy `ExpiresAt` field is retained
for source compatibility but is not merged or enforced. Absolute wall-clock
expiry would create a second deadline authority and would not preserve the
runtime's paused-wait semantics. Configure active-work time through
`RunPolicy.TimeBudget` or `runtime.WithRunTimeBudget(...)`; enforce an independent
end-to-end wall-clock SLA at the caller or workflow boundary.

### Per-Run Policy Overrides

Callers can override policy for specific runs:

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunMaxToolCalls(5),
    runtime.WithRunTimeBudget(2*time.Minute),
    runtime.WithRestrictToTool(tools.Ident("helpers.search")),
    runtime.WithAllowedTags([]string{"safe", "read-only"}),
    runtime.WithDeniedTags([]string{"destructive"}),
)
```

These overrides are included in the `RunStarted` hook payload. Duration-bearing
fields use integer nanoseconds on that durable boundary.

### Terminal run policies

`WithRunCompletionTool` requires one budgeted tool to succeed before the run
can complete. The first successful call returns its result and skips another
planner request.

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunCompletionTool(tools.Ident("reports.persist")),
)
```

The completion tool must belong to the agent. It cannot be a bookkeeping,
terminal, or confirmation tool. A completion-tool run cannot use whole-workflow
retries.

The planner must select the completion tool as the only action in its response.
Final responses and delegated completion calls fail the run. Limit exhaustion
also fails the run if the completion tool did not succeed.

`WithLimitTerminalPlans` supplies fixed terminal calls for three limit reasons:

- the active time budget
- the domain tool-call budget
- the recovery-turn budget

```go
plans := runtime.LimitTerminalPlans{
    TimeBudget: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"time_budget"}`),
    },
    ToolCallCap: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"tool_call_cap"}`),
    },
    RecoveryCap: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"recovery_cap"}`),
    },
}

client.Run(ctx, "session-1", msgs,
    runtime.WithLimitTerminalPlans(plans),
)
```

All three calls are required. Each call must target an agent-owned terminal
bookkeeping tool without confirmation. The runtime validates each payload before
the first planner request.

A fixed limit plan skips the finalizer model call. The runtime assigns a
deterministic tool-call ID before execution. A failed terminal side effect fails
the run.

Without a fixed plan, the finalizer advertises eligible terminal bookkeeping
tools. The finalizer can return a final response or select these terminal tools.
It cannot select a domain tool.

Each finalization tool receives `runtime.FinalizationReasonLabel`. The label
contains the exact termination reason. The runtime removes this reserved label
from ordinary tool calls and overwrites caller values during finalization.


### Runtime Policy Override

Override registered agent policy in-process:

```go
err := rt.OverridePolicy(agent.Ident("service.chat"), runtime.RunPolicy{
    MaxToolCalls:      10,
    MaxRecoveryTurns: 2,
    TimeBudget:        5 * time.Minute,
    InterruptsAllowed: true,
})
```

### Time budget and human waits

`TimeBudget` and `WithRunTimeBudget` limit active runtime work, not total
wall-clock age. Time spent waiting for clarification, confirmation, typed input,
or external tool results pauses the budget: the runtime extends both its budget
and hard deadlines by the measured wait duration. Manual human interaction can
therefore make elapsed wall time greater than `TimeBudget`.

Agent workflow submissions do not set a fixed engine-level timeout because any
workflow can enter an external await queue. This applies whether or not
`InterruptsAllowed` is true: planner await items and confirmation-gated tools
can wait without that policy flag. Deterministic active-work deadlines remain
the time-budget authority and move forward by each measured wait duration.

`FinalizerGrace` and `WithRunFinalizerGrace` reserve time after the active
budget ends. The finalizer can return a response or execute an advertised
terminal bookkeeping tool.

The grace period does not permit normal planning or domain tool execution.
At every planner resume boundary, including after an await barrier, an expired
active budget permits only the time-budget finalizer, once. A terminal planner
result already in hand (a final response or final tool result, with no tool calls
or await) completes the run even if adopting that result crosses the budget. The runtime keeps that result without calling
the finalizer again.

A finalizer that would start at or after the hard deadline (`Budget` plus
`FinalizerGrace`) is not scheduled. The run fails with `context deadline exceeded`.
Deployments can enforce a separate wall-clock limit at the workflow boundary.

---

## Memory and Stores

### Run Store

`runstore.Store` holds content by hash, opaque call records, and sequence
counters. Every operation requires a non-empty scope: the root run ID shared
by a run tree. The store treats this ID as opaque. It does not derive root run
IDs or expose content from another scope.

`Put` computes `sha256:` plus the lowercase hexadecimal SHA-256 digest of the
bytes. Identical bytes in one scope have one stored copy and the same hash.
`Get` returns `ErrNotFound` when the scope does not hold that hash.
`NextSeq` allocates 1, 2, 3, and subsequent values per scope and run, without
repetition under concurrent calls.

Call identity is `(scope, RunID, Seq, Attempt)`. `Seq` comes from `NextSeq` and
is unique across retries and parallel calls. `Attempt` identifies the activity
attempt. Callers own the encodings of `Started` (request and start metadata)
and `Completion`. The store compares these bodies byte for byte.

`PutCall` rejects invalid keys, states, and bodies before any write. Every
write requires `Started`. A started record requires an empty `Completion`.
Completed and failed records require a non-empty `Completion`.

| Existing record | Incoming record | Result |
| --- | --- | --- |
| None | Started | Insert |
| Started | Started with identical `Started` | Idempotent success |
| Started | Started with different `Started` | `ErrCallConflict` |
| Started | Completed or failed with identical `Started` | Conditional completion |
| Started | Completed or failed with different `Started` | `ErrCallConflict` |
| Completed or failed | Same state and identical bodies | Idempotent success |
| Completed or failed | Any other completed or failed record | `ErrCallConflict` |
| Completed or failed | Started | `ErrCallTransition` |
| None | Completed or failed | `ErrCallTransition` |

`ListCalls` returns every record of the run in that scope, ordered by `Seq`
then `Attempt`. A record that has no completion stays `CallStarted`. The reader
classifies this record as incomplete.

The default in-memory store copies bytes on input and output. It has no size
limit and does not survive process replacement. The Mongo adapter uses
`agent_run_content`, `agent_run_calls`, and `agent_run_sequences` by default.
Its `Options` can set each collection name and the operation timeout.
All three collections use majority write concern and primary read preference.

Mongo `Put` rejects content larger than `MaxContentBytes` (15 MiB) with
`ErrContentTooLarge` before it writes. This limit leaves room for metadata
within MongoDB's 16 MiB document limit. There is no chunking or GridFS support.

`DeleteScope` deletes the scope's content, call records, and sequence counters.
It preserves other scopes, and repeated deletion succeeds. The next sequence
allocation in a deleted scope starts at 1. Mongo deletes from three collections
without a transaction. If deletion fails, the caller can retry it.

### Memory Store

Stores a derived per-run transcript/event projection for planner context and
observability:

```go
type Store interface {
    LoadRun(ctx context.Context, agentID, runID string) (Snapshot, error)
    AppendEvents(ctx context.Context, agentID, runID string, events ...Event) error
}
```

**Event types:** `user_message`, `assistant_message`, `tool_call`, `tool_result`,
`planner_note`, `thinking`.

Use `memory.CloneEvent` at in-process event ownership boundaries. It copies
`Labels` and the canonical mutable values in `Data`. The in-memory store uses
this helper on append and load, so caller mutations cannot change stored history.
Its `LoadRun` snapshots are sorted by ascending event timestamp; equal timestamps
retain append order, including across batches.

The runtime automatically installs a best-effort hook subscriber when a memory
store is configured. A memory append failure is recorded but does not make the
projection canonical or fail the run; use the run event log for the durable
hook record and see "Event reliability and authority" above.

The Mongo adapter writes each `AppendEvents` batch as a new immutable document
in `agent_memory_events`; it never grows one run document with `$push`. During
an upgrade it still reads the legacy `agent_memory` snapshot document, then
loads event buckets in `created_at`, `_id` order. The combined snapshot is
stable-sorted by event timestamp, preserving the `memory.Snapshot` chronological
contract; equal timestamps retain legacy-before-new order and deterministic
bucket order. Existing transcripts therefore remain visible without an offline
migration. New writes go only to the events collection. If
`Options.Collection` is customized, its default companion is
`<collection>_events`; `Options.EventsCollection` can override it explicitly.
Keep both collections through the compatibility window, and apply retention or
deletion to both.

The workflow no longer maintains a `transcript.Ledger` or exposes the
`ledger_messages` query. Parent validation uses a `ViewLoader` in its activity;
planner activities reconstruct canonical messages from the transcript index.
Use the run log for introspection rather than querying a live message ledger.

### Memory Search And Tools

Memory search has two surfaces:

- transcript/event search, backed by `memory.Store` and `memory.Searcher`;
- long-term entry search, backed by `memory.Service`.

`runtime.WithMemoryStore(...)` enables current-run snapshots.
`runtime.WithMemorySearcher(...)` enables indexed or cross-run event queries
through the `memory.Searcher` contract:

```go
type Searcher interface {
    Query(ctx context.Context, query memory.Query) (memory.QueryResult, error)
}
```

Generated memory-backed toolsets use `runtime.NewMemoryToolsetRegistration(...)`.
The model-facing `memory.load_memory` tool accepts `scope`, `event_types`, `labels`, and
`limit`, and returns `events`, `truncated`, and `scope`.

- `scope:"current_run"` uses `MemoryStore.LoadRun`.
- `scope:"indexed"` requires `WithMemorySearcher`; without it, the tool returns a structured
  tool error with retry hint reason `unsupported_operation`.

`runtime.WithMemoryService(...)` enables long-term entry memory through:

```go
type Service interface {
    IngestRun(ctx context.Context, input memory.IngestRunInput) (memory.IngestResult, error)
    IngestEvents(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error)
    PutEntry(ctx context.Context, input memory.PutEntryInput) (memory.Entry, error)
    Search(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error)
}
```

Long-term memory calls require a resolved `memory.Scope`. The default resolver
derives a non-global namespace from the agent ID and reads reserved run labels
`memory.namespace` and `memory.user_id`; production runtimes should provide
`runtime.WithMemoryScopeResolver(...)` for account/project/user routing.

Use `FromMemory(MemoryLongTerm(), ...)` to expose `search_memory`. The
model-facing payload accepts `query`, optional `labels`, and optional `limit`;
visibility and scope are owned by the design/runtime configuration, not by model
arguments. `MemoryVisibilityUser()` is the default, and
`MemoryVisibilityShared()` is explicit opt-in for shared memory. Results are
model-facing hits with content, author, public labels, score, and snippet; raw
scope, source references, and metadata stay inside the runtime service.

Run policy can also opt into bounded planner-input preload:

```go
RunPolicy(func() {
    PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
    PreloadLongTermMemory(MemoryVisibilityUser(), MemoryMaxResults(5))
})
```

Preloaded memory appears on `planner.PlanInput.PreloadedMemory` and
`planner.PlanResumeInput.PreloadedMemory`. Long-term preload appears separately
on `PreloadedMemoryEntries`, so planners can distinguish raw transcript events
from durable extracted entries. Nil policy preserves the default no-preload
behavior; planners should use explicit memory tools for follow-up lookup.
Planners that need prompt text can render long-term entries with
`memory.FormatEntriesForPrompt(...)`.

### Run event store (runlog.Store)

The runtime also maintains a canonical, append-only run event log used for
introspection, audit/debug UIs, and deriving compact `run.Snapshot` values.

```go
type Store interface {
    Append(ctx context.Context, e *runlog.Event) (runlog.AppendResult, error)
    List(ctx context.Context, runID string, cursor string, limit int) (runlog.Page, error)
}
```

`AppendResult.Inserted` distinguishes a new append from an exact idempotent
replay of the same `(run_id, event_key)`; conflicting bodies for one event key
fail.

The Mongo adapter orders events by a per-run sequence stored atomically with
each event. Concurrent writers retry sequence conflicts, so a later append
cannot fall behind a previously returned cursor. Event IDs remain opaque
ObjectID tokens; the adapter resolves them to sequence positions rather than
sorting their bytes. Run-log reads use the primary, and standalone MongoDB
remains supported.

On startup, the adapter adds sequence positions to legacy records in their
previous ObjectID order. Existing event IDs and cursors remain usable. Stop
old-version writers before starting this upgrade and allow startup migration
to finish before resuming writes; mixing old and new writers is unsupported.
Migration preserves the old order, but cannot reconstruct historical append
order across workers. Startup fails if indexing or migration fails; a retry
resumes partial progress. Allow a larger client `Timeout` for large legacy logs.

The runtime exposes:

- `Runtime.ListRunEvents(ctx, runID, cursor, limit)` for cursor-paginated listing
- `Runtime.GetRunSnapshot(ctx, runID)` for a compact snapshot derived from replaying the run log

The snapshot projects every await variant (`await_clarification`,
`await_confirmation`, `await_questions`, `await_typed_input`,
`await_external_tools`) into `Snapshot.Await`, and maps `run_paused` /
`run_resumed` events to `Status` `"paused"` / `"running"`. A resumed or
completed run always reports `Await == nil`.

Configure the store via `runtime.WithRunEventStore(...)`. If not set, the runtime
defaults to an in-memory implementation (`runtime/agent/runlog/inmem`).

### Run Phases

Finer-grained lifecycle tracking for UIs:

```go
const (
    PhasePrompted       = "prompted"        // Input received
    PhasePlanning       = "planning"        // Planner deciding
    PhaseExecutingTools = "executing_tools" // Tools running
    PhaseSynthesizing   = "synthesizing"    // Final response
    PhaseCompleted      = "completed"
    PhaseFailed         = "failed"
    PhaseCanceled       = "canceled"
)
```

---

## History Policies

Control how conversation history is managed before each planner turn:

### KeepRecentTurns

Sliding window that preserves system messages and recent turns:

```go
// DSL
RunPolicy(func() {
    History(func() {
        KeepRecentTurns(20)
    })
})
```

### Compress

Model-assisted summarization for long conversations:

```go
// DSL
RunPolicy(func() {
    History(func() {
        CompressAtMaxInputTokens(120000)
        KeepMaxInputTokens(40000)
        KeepMaxTurns(12)
    })
})

// Registration
cfg := chat.ChatAgentConfig{
    Planner:      myPlanner,
    HistoryModel: smallModelClient, // For compression
}
```

Compression triggers are explicit. Use `CompressAtTurns` for a turn-count
threshold, `CompressAtMaxInputTokens` for an exact provider input-token
threshold, or both. Retention is explicit too: `KeepMaxTurns` caps the newest
exact turns, while `KeepMaxInputTokens` keeps newest whole turns that fit within
the measured token budget. Token-budget compression requires a history model
that implements `model.TokenCounter`; approximate estimators are rejected for
hard compression decisions.

---

## Prompt Caching

Configure automatic cache checkpoint placement:

```go
// DSL
RunPolicy(func() {
    Cache(func() {
        AfterSystem()  // Checkpoint after system messages
        AfterTools()   // Checkpoint after tool definitions
    })
})
```

The runtime populates `model.Request.Cache` when planners do not set it explicitly.
The Anthropic and Bedrock adapters map automatic and explicit boundaries to native ephemeral prompt-cache controls.
Policy boundaries do not apply when the related system or tool section is empty.

On Anthropic, an explicit checkpoint marks the nearest preceding cacheable block.
A checkpoint at the start of a message can mark a block in an earlier encoded message.
This behavior also applies to messages that contain only a checkpoint.
System blocks and conversation blocks have separate checkpoint histories.
Anthropic returns an error if the applicable history has no cacheable block.
Providers that do not support caching ignore these options.

---

## System Reminders

Deliver structured, rate-limited guidance to models:

```go
input.Agent.AddReminder(reminder.Reminder{
    ID:              "pending_todos",
    Text:            "Review pending todo items before proceeding.",
    Priority:        reminder.TierGuidance,
    Attachment:      reminder.Attachment{Kind: reminder.AttachmentUserTurn},
    MaxPerRun:       3,
    MinTurnsBetween: 2,
})

// Remove when no longer relevant
input.Agent.RemoveReminder("pending_todos")
```

**Tiers:**

| Tier           | Purpose                             |
| -------------- | ----------------------------------- |
| `TierSafety`   | Never suppressed (P0)               |
| `TierGuidance` | Soft nudges, first to suppress (P2) |

---

## Model Clients

Provider packages under `features/model/*` construct raw `model.Provider`
implementations. Runtime factory methods wrap them with `model.NewClient` and
return validated `model.Client` values. If an application constructs a provider
directly, it must call `model.NewClient(provider)` before registration. The
different `Stream` return types make a raw provider fail the `model.Client`
interface at compile time.

For deterministic model tests, use `agenttest.ScriptClient(t, turns...)` or
`agenttest.NewScript(turns...)` and its `Client()`. Build turns with `Respond`,
`Text`, `Thinking`, `ToolCall`, `RawToolCall`, `Usage`, and `Fail`; typed tool calls
use the descriptor's payload codec, and encoding failures are `NewScript` builder
errors. `NewScript` also rejects malformed turn shapes and stream failure offsets
beyond the turn's chunk count. Responses pass the real `model.NewClient`
validation against each request.
`HasTool`, `LacksTool`, and `LastToolResult` accept typed descriptors or `tools.Ident`.
Scripts consume turns in FIFO order, with optional `When` matchers;
`AnyOrder()` selects the first unconsumed
matching turn. `Requests()` and `Remaining()` expose progress, and `ScriptClient`
reports recorded diagnostics and unused turns at test cleanup. Streaming uses
the same responses, supports `Chunked(n)` and `FailStreamAfter(n, err)`, and must
be drained and finalized like any validated model stream. `WithContextLimits`
exposes configured limits through `model.ContextInfo` without token counting.

`model.TokenUsage` uses one cache accounting convention for every provider.
`InputTokens` contains only uncached input. `CacheReadTokens` and
`CacheWriteTokens` are disjoint input counts. A non-zero `TotalTokens` equals
the sum of uncached input, output, cache-read, and cache-write tokens. Provider
adapters normalize their native usage fields before runtime aggregation.

### Registration

```go
// Register model clients before Seal. RegisterModel returns
// ErrRegistrationClosed after Seal or the first submitted run.
err := rt.RegisterModel("bedrock", bedrockClient)

// Create Bedrock client via runtime helper
client, err := rt.NewBedrockModelClient(awsClient, runtime.BedrockConfig{
    DefaultModel:   "us.anthropic.claude-3-5-sonnet-20240620-v1:0",
    HighModel:      "us.anthropic.claude-3-opus-20240229-v1:0",
    SmallModel:     "us.anthropic.claude-3-haiku-20240307-v1:0",
    MaxTokens:      4096,
    ThinkingBudget: 10000,
})
```

Bedrock compatibility: `bedrock.New(awsClient, opts)` no longer takes a ledger
source. Requests carry only the planner's `model.Request.Messages`; Bedrock does
not query the workflow or prepend ledger messages.

Model lookup is immutable after registration closes. Loom does not define
post-`Seal` credential/client rotation or in-flight hot-swap semantics; build a
new runtime and move traffic to it when replacing a model client.

Validated clients bound request and response ownership, enforce the exact
current tool catalog and tool-choice rule, run structured-output and generated
completion validation, reject output-limit termination, and expose only
terminally accepted tool calls. `model.OutputValidationError` has a closed kind
and bounded fingerprint; its public message does not render rejected model text,
tool names, arguments, or schemas.

Create an OpenAI Responses client through the runtime helper:

```go
client, err := rt.NewOpenAIModelClient(runtime.OpenAIConfig{
    APIKey:       os.Getenv("OPENAI_API_KEY"),
    DefaultModel: "gpt-4.1",
    HighModel:    "gpt-4.1",
    SmallModel:   "gpt-4.1-mini",
})
```

If `model.Request.Thinking.Enable` is true, the OpenAI adapter requests an
automatic reasoning summary. It returns each summary as a typed
`model.ThinkingPart`.

Create a Codex subscription client when a desktop or local application supplies
ChatGPT credentials. The credential source runs once for each logical request.
It can refresh an expired token. Loom does not find or store credentials.

```go
source := codex.CredentialSourceFunc(func(ctx context.Context) (codex.Credentials, error) {
    return desktopAuth.CurrentCodexCredentials(ctx)
})

client, err := rt.NewCodexModelClient(runtime.CodexConfig{
    CredentialSource: source,
    DefaultModel:     "gpt-5.6-codex",
    Transport:        codex.TransportAuto,
    ResponsesLite:    false,
})
if err == nil {
    err = rt.RegisterModel("codex", client)
}
```

`TransportAuto` uses a new WebSocket first. It can retry once with SSE only
before it returns model output. Select `TransportSSE` or
`TransportWebSocket` when the application requires one network path.
`ClientVersion` changes the private compatibility version. `ResponsesLite`
enables the complete Lite request shape.

Set `ReasoningEffort` in `codex.Options` or `runtime.CodexConfig` to send
`reasoning.effort`. Empty means the provider default. Choose an effort from the
selected model's `supportedReasoningEfforts` in Codex `model/list`; the application
owns model-specific validation. Loom validates the identifier syntax without
freezing the provider catalog. Effort does not require thinking summaries and
preserves Responses Lite context settings.

`make test` runs an uncached live Codex smoke test after the unit tests.
The smoke test uses `gpt-5.6-terra` and the default `ClientVersion`.
It checks text responses and tool-call round trips, including a generated
catalog with `Int`, `Int64`, and `UInt64` fields.
`make test-codex-live` runs only this smoke test.

The test requires `CODEX_ACCESS_TOKEN` and `CODEX_ACCOUNT_ID` together.
If only one value is set, the test fails without reading the auth file.
If neither value is set, it reads subscription credentials from
`$CODEX_HOME/auth.json`, or `~/.codex/auth.json` when `CODEX_HOME` is unset.
This credential discovery applies only to tests. The provider still requires
an injected credential source.

The smoke test skips in CI, in short test runs, or without local subscription
credentials. Set `CODEX_INTEGRATION=0` to skip it locally.
Set `CODEX_INTEGRATION=1` to require credentials during a local live run.
An enabled live run fails on authentication or service errors.
The test does not refresh credentials; log in to Codex again if they expire.
`CODEX_MODEL`, `CODEX_TRANSPORT`, and `CODEX_RESIDENCY` override the test configuration.

The provider uses fixed ChatGPT Codex endpoints. It does not accept an arbitrary
base URL. An injected HTTP client or WebSocket dialer can apply desktop network
policy. The injection does not change the endpoint identity. The provider sends
access tokens only in request headers. Errors do not include tokens, account
IDs, headers, or response bodies.

Codex rejects structured output, temperature, numeric output limits, cache
options, cache checkpoints, and numeric thinking budgets before network access.
The provider does not implement `model.TokenCounter`. It returns `Response()`
only after the caller receives literal `io.EOF`.

Codex sends the authored tool schemas with exact 64-bit integer bounds through
both SSE and WebSocket transports. It explicitly sets `strict: false`, as the
[Codex tool encoder](https://github.com/openai/codex/blob/main/codex-rs/tools/src/tool_spec_tests.rs) does.
This prevents Responses from automatically converting the schemas into its
strict subset. See [OpenAI's strict-mode contract](https://developers.openai.com/api/docs/guides/function-calling#strict-mode).
The validated `model.Client` still enforces the complete authored schema before
it accepts tool arguments. It compares numeric bounds without floating-point
rounding. No numeric bounds are removed or narrowed.

An explicit `model.Request.Model` value has highest priority. If `Model` is
empty, `ModelClass` selects `HighModel` or `SmallModel`. `DefaultModel` applies
if a class-specific model is not configured.

Successful non-streaming Ollama response bodies are limited to 16 MiB before
JSON decoding, including for chunked responses without a content length.
Oversized responses fail without returning a partial model response.

Create a local Ollama chat client through the runtime helper. Ollama uses the
`/api/chat` endpoint and supports text, images, streaming text, function tools,
native thinking output, and schema-backed structured output for models that
support those features. When `model.Request.Thinking` is set, the adapter maps
it to Ollama's top-level `think` flag and surfaces `message.thinking` as typed
`model.ThinkingPart` / `model.ChunkTypeThinking` content instead of assistant
text. Some Gemma 4 variants, including MLX builds, also require the model-level
`<|think|>` control token at the start of the system prompt to activate
thinking; keep that prompt concern separate from the adapter's response-side
typed thinking contract:

```go
client, err := rt.NewOllamaModelClient(runtime.OllamaConfig{
    ServerURL:    "http://localhost:11434",
    DefaultModel: "llama3.1",
    HighModel:    "qwen3:32b",
    SmallModel:   "llama3.2",
    MaxTokens:    4096,
})
```

Gemini is backed by Google's official `google.golang.org/genai` SDK. Use the
Gemini API helper for API-key deployments:

```go
client, err := rt.NewGeminiModelClient(ctx, runtime.GeminiConfig{
    APIKey:         os.Getenv("GEMINI_API_KEY"),
    DefaultModel:   "gemini-2.5-flash",
    HighModel:      "gemini-2.5-pro",
    SmallModel:     "gemini-2.5-flash-lite",
    MaxTokens:      4096,
    ThinkingBudget: 8192,
})
```

Use the Vertex helper when Google Cloud project and location should own auth and
routing. The Gen AI SDK uses Application Default Credentials for the Vertex
backend unless `APIKey` or explicit `Credentials` are set:

```go
client, err := rt.NewVertexGeminiModelClient(ctx, runtime.VertexConfig{
    ProjectID:      "my-gcp-project",
    Location:       "global",
    DefaultModel:   "gemini-2.5-pro",
    SmallModel:     "gemini-2.5-flash",
    MaxTokens:      4096,
    ThinkingBudget: 8192,
})
```

### Structured Output

`model.Request.StructuredOutput` asks the provider to enforce a JSON Schema for
the assistant response:

```go
resp, err := modelClient.Complete(ctx, &model.Request{
    Messages: input.Messages,
    StructuredOutput: &model.StructuredOutput{
        Name: "draft",
        Schema: []byte(`{
            "type": "object",
            "required": ["title", "summary"],
            "properties": {
                "title": {"type": "string"},
                "summary": {"type": "string"}
            }
        }`),
    },
})
```

Gemini, OpenAI, Bedrock, and Ollama `Complete` map this to provider-native or
provider-supported schema controls. Bedrock, OpenAI, and Ollama also support
streamed structured output. Anthropic rejects structured output, and Gemini
does not implement streaming. Structured output cannot be combined with model
tools in the current Gemini, OpenAI, and Ollama adapters.

### Provider errors and context overflow

`model.ProviderError` kinds are `auth`, `invalid_request`, `context_overflow`,
`rate_limited`, `unavailable`, and `unknown`. Use `errors.As` or
`model.AsProviderError` to inspect the kind, provider code, HTTP status, request
ID, and retryability when the adapter supplies them.

`ProviderErrorKindContextOverflow` (`context_overflow`) means the request exceeded
the model's context window. It is not retryable with the same request. Complete
returns an error instead of a response; a streaming overflow terminates the
stream without a canonical response, even if provisional chunks were delivered.

| Adapter | Overflow signal |
| --- | --- |
| Anthropic | HTTP 400 `invalid_request_error` with `prompt is too long` or older Claude `input length and max_tokens exceed context limit`; status-200 SSE `error` events with either text; `model_context_window_exceeded` in Complete or Stream. [Context-window documentation](https://platform.claude.com/docs/en/build-with-claude/context-windows). |
| Bedrock | `ValidationException` with `Input is too long for requested model`, `prompt is too long`, or `too many input tokens`, or older Claude `input length and max_tokens exceed context limit`, including receive-time stream errors; `model_context_window_exceeded` stops. Sources: [AWS validation guide](https://repost.aws/knowledge-center/bedrock-validation-exception-errors), [Claude context windows](https://platform.claude.com/docs/en/build-with-claude/context-windows), [captured Titan error](https://www.philschmid.de/amazon-titan-embeddings), [AWS stop reasons](https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages-request-response.html). |
| OpenAI | `context_length_exceeded` in SDK errors or streamed `response.failed` / `error` events. Sources: [Responses events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [OpenAI's recorded overflow fixture](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs). |
| Gemini / Vertex | HTTP 400 `INVALID_ARGUMENT` with `exceeds the maximum number of tokens`, with or without the input count. Sources: [captured SDK error](https://github.com/googleapis/python-genai/issues/2050), [message variant](https://github.com/google-gemini/gemini-cli/issues/22609), [Google API errors](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/api-errors). Streaming remains unsupported. |
| Codex | The code `context_length_exceeded` in HTTP errors or streamed `response.failed` / `error` events. Detection uses the code only and keeps the fixed redacted message. [Recorded provider fixture](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs). |
| Ollama | No documented chat overflow error is mapped. Chat truncates input to `num_ctx`; see [Ollama's prompt implementation](https://github.com/ollama/ollama/blob/main/server/prompt.go). |
| Gateway | No mapping: this transport-agnostic wrapper has no provider wire error contract. |

Bedrock's context-window stop no longer sets `Response.OutputLimited` or triggers
`OutputBounds` recovery (the shorter-answer retry with tools disabled).
Anthropic's `model_context_window_exceeded` stop previously returned a successful
`Response` with that `StopReason`; it now fails with `context_overflow`.
`max_tokens` still means output-limited. Context overflows return an activity
output for bounded workflow recovery.
Other errors keep their existing behavior: ordinary Bedrock and Codex 400s remain
`invalid_request`; Anthropic, OpenAI, Gemini, and Ollama retain their existing
error wrapping rather than gaining new classifications.

### Provider capability matrix

This table describes the adapter contract in this repository. Individual model
families may impose additional provider-side restrictions.

| Provider | Complete | Stream | Structured output | Tool choice | Cache checkpoints | Thinking/reasoning | Exact `TokenCounter` | Context limits |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Anthropic | yes | yes | no | yes | yes | typed thinking and redacted thinking | no | Models API; verified table fallback. API `input limit + OutputReserve(req, limits)` |
| Bedrock | yes | yes | unary and stream | yes | yes | typed thinking, including adaptive models | yes | table empty; unknown unless overridden |
| OpenAI Responses | yes | yes | unary and stream; not with tools | yes | ignored on replay | reasoning summaries become typed thinking | no | verified table; unlisted models unknown |
| Codex subscription | yes | SSE and WebSocket | no | yes | no | typed summaries and encrypted replay | no | table empty; unknown unless overridden |
| Gemini / Vertex | yes | no (`ErrStreamingUnsupported`) | unary; not with tools | yes | no | yes | yes | Models.Get `input limit + OutputReserve(req, limits)`; empty table fallback |
| Ollama | yes | yes | unary and stream; not with tools | limited by local API/model | no | native typed thinking | no | effective num_ctx unknown unless overridden |

### ContextLimits

`model.ContextInfo` is optional. Its `ContextLimits(ctx, req)` method resolves
completion's model routing and returns `(model.ContextLimits, resolvedModel,
known)`. `known` means the context window is known. `ContextWindow` and
`MaxOutputTokens` are token limits; `DefaultMaxTokens` is the configured output
cap sent when `Request.MaxTokens` is zero. `ImageTokens == 0` selects the later
measurement default of 1,500. Limits alone do not activate compaction or a guard.

Anthropic optionally accepts `anthropic.Options.Models` (`ModelsClient`);
`NewFromAPIKey` wires the SDK Models API automatically. Gemini discovers the
optional `Get` interface on its models client. Query from activities, never
workflow code, with a two-second deadline bounded by the caller's context.
Successful raw metadata is cached per requested model for the client's lifetime.
A failure caused by the caller's cancelled or expired context is never cached;
other failures retain the table fallback for 60 seconds before retrying.

Their APIs publish **input** limits. After each cache lookup, map them for the
current request as `ContextWindow = input limit + contextmeasure.OutputReserve(req, limits)`.
The reserve uses request `MaxTokens`, then configured `DefaultMaxTokens`, then
`min(MaxOutputTokens, ContextWindow/4)`. It is stable when evaluated against the
returned window, so subtracting it preserves the published input allowance even
when the request changes its completion cap. When the quarter-window fallback
applies to an input-only limit, the reserve is `min(MaxOutputTokens, input limit/3)`
with integer division. Shared windows from tables are used directly. Tables
contain only eligible SDK constants or non-test adapter IDs with verified
official figures; unknown IDs stay unknown.

Configure overrides before registration:

```go
rt := runtime.New(runtime.WithContextLimits(map[string]model.ContextLimits{
    "my-provider-model": {ContextWindow: 200000, MaxOutputTokens: 8192},
    "gateway":           {ContextWindow: 128000, MaxOutputTokens: 4096},
}))
```

Positive fields replace adapter values independently. Zero fields retain them.
An entry for the resolved provider model takes precedence; if absent, the
registered client ID is the fallback. Overrides are copied at construction,
which rejects negative fields and `MaxOutputTokens > ContextWindow` (include a
window when overriding the output limit). A window override can make an unknown
model known. Ollama's deployment-specific `num_ctx` and gateway models need
application-supplied overrides. A gateway has no remote capability channel;
its override key is `Request.Model`, falling back to its registered client ID.
`model.NewClient` and the rate-limit middleware preserve optional limits and
counting capabilities through `model.WithCapabilities`.

Every provider runs `testutil.RunProviderConformance`. The suite requires one
supported or unsupported case for each provider capability. These capabilities
include multimodal input, typed thinking, exact token counting, context limits,
and tool-name round trips. Streaming providers prove setup, receive, terminal, and
receive-time rate-limit behavior. Every adapter also supplies a recorded-body
context-overflow case, including a streaming case where streaming is supported.
Ollama explicitly proves its absence of an overflow mapping.

Streaming provider adapters assemble tool arguments only until the provider
closes the content block. Anthropic and Bedrock normalize empty arguments to
`{}`; OpenAI preserves its existing empty-argument representation. Malformed
or truncated JSON fails the stream with a provider-scoped error instead of
being replaced with an empty object.

Bedrock normalizes structured-output schemas to its supported subset. It closes
object schemas, removes unsupported keywords and formats, and rejects shapes
whose `additionalProperties` semantics cannot be represented instead of
silently weakening the schema. Its exact token counter uses the provider's
count-tokens path and removes replayed thinking blocks before counting. Unary
and streaming responses both preserve provider reasoning as typed thinking.

The OpenAI adapter projects tool and structured-output schemas into the
provider's strict-mode JSON Schema subset and canonicalizes strict-mode `null`
omissions back to absent fields before returning tool or structured payloads.
Like the Anthropic and Bedrock adapters, it also translates canonical dotted
tool identifiers (`toolset.tool`) into provider-safe function names
(`[a-zA-Z0-9_-]`, at most 64 characters) on the wire and maps returned
function-call names back to canonical identifiers, failing fast when two tool
names sanitize to the same provider name.

Tool names and tool-use correlation IDs use separate request-scoped codecs.
When Anthropic or Bedrock replays a transcript, provider-safe tool-use IDs pass
through unchanged and reserve their wire values before unsafe internal IDs are
assigned synthetic `tN` values. Synthetic allocation skips every occupied ID,
so a safe `t1` cannot collide with a run-scoped ID that requires substitution;
tool-use and tool-result blocks retain the same correlation ID.

Construct the direct Anthropic adapter with the default SDK client or an
application-owned Messages client:

```go
client, err := anthropic.NewFromAPIKey(
    os.Getenv("ANTHROPIC_API_KEY"),
    "claude-sonnet-4-20250514",
)
```

Use `anthropic.New(messagesClient, anthropic.Options{...})` when the application
owns SDK configuration. Anthropic supports tools, streaming, prompt-cache
checkpoints, and thinking blocks, but not structured output or exact token
counting.

When planners render prompts through `RenderPrompt`, copy prompt provenance into model requests:

```go
content, err := input.Agent.RenderPrompt(ctx, "aura.chat.system", map[string]any{
    "AssistantName": "Ops Assistant",
})
if err != nil {
    return nil, err
}

resp, err := modelClient.Complete(ctx, &model.Request{
    RunID:      input.RunContext.RunID,
    Messages:   input.Messages,
    PromptRefs: []prompt.PromptRef{content.Ref},
})
```

### RequestFacts

Facts use snake_case JSON field names in durable call records and exports.

Planner model calls record `*contextmeasure.RequestFacts` in
`callrecord.Completion.Facts`. The recorder measures the effective request after
interceptors and request decoration, using `ContextInfo` on the registered
client so runtime limits overrides apply. It keeps those pre-send measurements
through unary and streaming completion. Direct `Runtime.ModelClient` calls and
interceptor short-circuits remain outside planner call recording.

Facts include the call key, resolved model, context window, output reserve,
view participation, text/fixed byte counts, non-text token estimate, normalized
input usage and outcome. Unknown limits give a zero window. Without a resolved
provider model, the request model or registered client ID supplies the key.
Output reserve uses request `MaxTokens`, then configured `DefaultMaxTokens`,
then `min(MaxOutputTokens, ContextWindow / 4)`.

`TextBytes` includes provider-facing text, tool definitions and JSON payloads,
document names/context/text/chunks, and replayed thinking text/redacted bytes.
`FixedBytes` is the system/tool portion of those bytes. Image and binary-document
uploads contribute only to `NonText`: the configured image cost (default 1,500),
ceiling-rounded document bytes at one token per two bytes, and the image cost
for a document URI. Metadata, cache points and thinking signatures do not count
as text. `ViewBearing` matches canonical role/parts hashes against the committed
pre-history-policy view snapshot, excluding `Meta` and cache points. This is
separate from the full-encoding request match used to reconstruct call evidence.

`ViewTextBytes` and `ViewNonText` measure the request messages matching that
snapshot, in text bytes and non-text tokens respectively. Each matching request
occurrence counts once; duplicate snapshot entries do not multiply the counts.
Both fields use `omitzero`; older records without them decode as zero.

Every terminal call has facts: unary success/failure, stream setup failure,
`Finalize`, `Close`, and `stream_abandoned`. Only started records have no facts.
`InputTokens` sums input, cache reads and cache writes; missing usage gives zero.
Streaming facts use the usage chunks observed by the recorder, including those
received before closing or abandoning a stream. Outcomes are `ok`, `overflow`
for provider `context_overflow`, and `error` for other failures.
`pre_send_rejected` records guard rejection. `CountLatency` records optional
counting latency; progress rejection records an error outcome.

`runrecord.Read` exposes facts through each call's `Completion`; `Export` and
`FromExport` preserve them, including nanosecond durations. K10 adds no workflow
or activity-output fields and does not change the workflow protocol. K12 adds
`Facts` to `PlanActivityOutput`, sorted `Models` to activity input,
trigger decisions and `context_unmeasured` / `context_limits_unknown` events;
K14 applies Observe, Models, guard checks, recovery and diagnostics to every
agent. Only threshold transformations remain opt-in.

`contextmeasure.GuardTokens` uses the upper clamp rate (1/2) to decide whether
to ask a token counter before calibration. Its fallback estimate still uses
the supplied rate; an exact count enables `GuardOn` with known limits.

`runtime/agent/overflow` supplies the pure pre-send decision only. Initialize
`Step.ProviderLeft` to two and retain the smallest `Size` rejected by each
provider model in `Step.Rejected`. `Size` estimates all request text at 1/3,
adds non-text tokens and output reserve, and saturates at `MaxInt`. It never
changes with calibration. `Check` returns `GuardReject` first when eligible and
tokens strictly exceed `Limits.Budget × (1 + GuardMargin)`; otherwise it returns
`NoProgress` if size is at least that model's baseline, or `Send`. The margin is
rational: pass `contextmeasure.Rate{Tokens: 1, Bytes: 10}` for ten percent; zero
means zero margin. Check does not mutate state or spend the provider budget.
The recorder owns rejection persistence; the workflow owns view recovery.

### Rate Limiting

Apply adaptive rate limiting:

```go
import mdlmw "github.com/CaliLuke/loom-mcp/v2/features/model/middleware"

rl := mdlmw.NewAdaptiveRateLimiter(
    ctx,
    throughputMap,     // *rmap.Map for cluster-wide state (nil for local)
    "bedrock:sonnet",  // Model family key
    80_000,            // Initial TPM
    1_000_000,         // Max TPM
)

limitedClient := rl.Middleware()(rawClient)
rt.RegisterModel("bedrock", limitedClient)
```

`NewAdaptiveRateLimiter` preserves its estimated-input-only admission contract.
Use `NewOutputReservationAdaptiveRateLimiter` when every request sets a
positive `MaxTokens` value and the provider implements exact
`model.TokenCounter`; that mode reserves both values before the provider call.

---

## Run Options

Customize run behavior with functional options:

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunID("custom-run-id"),
    runtime.WithTurnID("turn-1"),
    runtime.WithLabels(map[string]string{"tenant": "acme"}),
    runtime.WithMetadata(map[string]any{"request_id": "abc"}),
    runtime.WithTaskQueue("custom-queue"),
    runtime.WithMemo(map[string]any{"workflow_name": "Chat"}),
    runtime.WithSearchAttributes(map[string]any{"tenant": "acme"}),
    runtime.WithTiming(runtime.Timing{
        Budget: 2 * time.Minute,
        Plan:   30 * time.Second,
        Tools:  60 * time.Second,
    }),
)
```

Search attributes are passed through to the workflow engine as caller-owned
index metadata. The runtime does not mirror `SessionID` into engine search
attributes automatically.

`Timing.Plan` and `Timing.Tools` are semantic attempt budgets. They bound how
long a healthy planner or tool attempt may run once execution starts. Queue-wait
timeouts and heartbeat-based liveness detection are engine-specific concerns and
belong in the engine adapter, not the generic runtime API.

---

## Introspection

Query registered agents and tools:

```go
// List registered agents
agents := rt.ListAgents()  // []agent.Ident

// List registered toolsets
toolsets := rt.ListToolsets()  // []string

// Get tool spec
spec, ok := rt.ToolSpec(tools.Ident("helpers.search"))

// Get parsed tool schema
schema, ok := rt.ToolSchema(tools.Ident("helpers.search"))

// Get specs for an agent
specs := rt.ToolSpecsForAgent(agent.Ident("service.chat"))
```

---

## Engine Integration

### Engine Interface

```go
type Engine interface {
    RegisterWorkflow(ctx, def WorkflowDefinition) error
    RegisterHookActivity(ctx, name, opts, fn) error
    RegisterPlannerActivity(ctx, name, opts, fn) error
    RegisterExecuteToolActivity(ctx, name, opts, fn) error
    RegisterRecordContentActivity(ctx, name, opts, fn) error
    RegisterFinishRunActivity(ctx, name, opts, fn) error
    RegisterChildDispatchActivity(ctx, name, opts, fn) error
    StartWorkflow(ctx, req WorkflowStartRequest) (WorkflowHandle, error)
    QueryRunStatus(ctx, runID string) (RunStatus, error)
}
```

`StartWorkflow` requires unique workflow IDs; an already-running ID returns `engine.ErrWorkflowAlreadyStarted`, which does not match `engine.ErrWorkflowStartRejected`.

### WorkflowContext

Workflow handlers receive a context for deterministic operations:

```go
type WorkflowContext interface {
    Context() context.Context
    WorkflowID() string
    RunID() string
    Now() time.Time  // Deterministic time
    PublishHook(ctx, call) error
    ExecutePlannerActivity(ctx, call) (*api.PlanActivityOutput, error)
    ExecuteRecordContentActivity(ctx, call) (*api.RecordContentOutput, error)
    ExecuteFinishRunActivity(ctx, call) (*api.FinishRunOutput, error)
    ExecuteChildDispatchActivity(ctx, call) (*api.ChildDispatchOutput, error)
    ExecuteToolActivity(ctx, call) (*api.ToolOutput, error)
    ExecuteToolActivityAsync(ctx, call) (Future[*api.ToolOutput], error)
    PauseRequests() Receiver[api.PauseRequest]
    ResumeRequests() Receiver[api.ResumeRequest]
    ClarificationAnswers() Receiver[api.ClarificationAnswer]
    ExternalToolResults() Receiver[api.ToolResultsSet]
    ConfirmationDecisions() Receiver[api.ConfirmationDecision]
    StartChildWorkflow(ctx, req) (ChildWorkflowHandle, error)
    SetQueryHandler(name, handler) error
}
```

`Await` takes the owning workflow's `Context()` or a context derived only to
add values. For cancellation while waiting, create a scope with `WithCancel()`
and call `Await` on that returned workflow context with its own `Context()`.
Temporal rejects unscoped contexts and active native Go cancellation or deadline
contexts, which cannot cancel a yielded deterministic wait. Workflow-scope
cancellation returns the underlying engine error.

If you register a workflow directly with a Temporal worker, own the adapter lifetime:

```go
wfCtx, release := temporal.NewWorkflowContext(eng, ctx)
defer release()
```

Defer `release` immediately after construction.

The release function removes the workflow context from the engine registry when the workflow finishes. This cleanup prevents workers from retaining completed external workflows.

Temporal signal receivers select between the signal channel and workflow
cancellation. Canceling a workflow therefore releases a receiver blocked in
`Receive` or `ReceiveWithTimeout`, and durable completion records the terminal
status as `canceled` when all error leaves are cancellations. An independent
cleanup, provider, hook, or transport failure makes the run fail.

`Receiver.Pending()` counts buffered, unreceived signals without side effects, reflects workflow delivery in history order under replay, and is safe inside an `Await` predicate.

### Engine errors

Custom engines must return `engine.ApplicationError`, `engine.TimeoutError`, and
`engine.ErrCanceled` from activity, child-workflow, and timer boundaries so runtime
code can inspect failures with `errors.As` and `engine.IsCanceled`/`engine.IsTimeout`.
An activity returning an `ApplicationError` with `NonRetryable: true` must never be
retried. Preserve its `Type`, `Message`, and single serializable `Details` value;
retain the backend error as its cause. Provider failures use
`loom_mcp.provider_error` and `hooks.ProviderErrorDetails`.

`ErrCanceled` shares `context.Canceled`'s identity. Run cancellation still requires
every exact error leaf to be a cancellation; a joined cleanup failure fails the
run. Native context deadlines are timeout failures. The Temporal adapter owns SDK
failure conversion and preserves backend error text in ToolResult and hook
payloads. Conversion restores only root engine errors or adapter-owned wrappers;
caller joins and reason wrappers retain their text and retryability. The in-memory engine passes
application errors through, marks execution deadlines, and never retries.

Run the shared `enginetest.RunConformance` suite for custom adapters. Its
`application_errors` scenarios check type/details preservation and one attempt
for a non-retryable failure with a retry policy allowing three attempts.
`runtime/temporaltrace` is an opt-in Temporal-specific tracing package.

### Available Engines

**Temporal worker** — Production-grade durable execution:

```go
import temporal "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"

eng, _ := temporal.NewWorker(temporal.Options{
    ClientOptions: &client.Options{
        HostPort:  "temporal:7233",
        Namespace: "default",
    },
    WorkerOptions: temporal.WorkerOptions{
        TaskQueue: "orchestrator.chat",
    },
    ActivityDefaults: temporal.ActivityDefaults{
        Planner: temporal.ActivityTimeoutDefaults{
            QueueWaitTimeout: 30 * time.Second,
            LivenessTimeout:  20 * time.Second,
        },
        Tool: temporal.ActivityTimeoutDefaults{
            QueueWaitTimeout: 2 * time.Minute,
            LivenessTimeout:  20 * time.Second,
        },
    },
})
```

**Temporal client** — Start/query/signal without local polling:

```go
eng, _ := temporal.NewClient(temporal.Options{
    ClientOptions: &client.Options{
        HostPort:  "temporal:7233",
        Namespace: "default",
    },
})
```

In this split:

- `RunPolicy.Timing.Plan` / `runtime.WithTiming(...).Plan` set the planner
  attempt budget.
- `RunPolicy.Timing.Tools` / `runtime.WithTiming(...).Tools` set the tool
  attempt budget.
- `temporal.Options.ActivityDefaults` sets Temporal-only queue-wait and
  heartbeat liveness behavior.

**Replaying recorded histories** — After registering agents on a Temporal worker
engine, load the recorded JSON with `client.HistoryFromJSON` and call
`eng.ReplayWorkflowHistory(logger, history)`. No running worker or
Temporal server is needed. The replayer snapshots registered workflow names and
uses the same handler wrapping and data converter as live execution. Configure
`Options.ClientOptions.DataConverter` as for the worker; when supplying an
external `Client`, also supply `ClientOptions` with that client's converter
(an explicit nil converter means the SDK default). The harness rejects an
external client whose converter configuration was not supplied.

The generated fixture keeps a completed history under
`integration_tests/fixtures/agent_features/testdata/workflow_protocol_v12.json`.
The scrubbed recording includes overflow recovery, a confirmation-gated tool turn
with clarification, its recorded assistant question and user answer, one resume
that selects child validation and context lookup, seed/result/question content
batches, planner-step commits and terminal finish activity. A second recording,
`workflow_protocol_v12_budget.json`, exercises an external-await resume whose
terminal answer arrives within budget, but whose adoption crosses the budget.
It completes without a finalizer call.
The unchanged protocol 5–11 fixtures verify rejection before workflow effects;
protocol versions must match, so workers running protocol 12 cannot replay them
as successful runs. Run
`go test -C integration_tests/fixtures/agent_features ./... -run '^TestGeneratedFeatureWorkflowReplay$'`
for offline replay. To re-record it against the pinned Temporal development
server, run
`LOOM_MCP_RECORD_WORKFLOW_HISTORY=1 go test -C integration_tests/fixtures/agent_features . -run '^TestGeneratedFeatureRealTemporalContracts/record_workflow_history$' -count=1`.
Review and check in the resulting history with each protocol bump.

**In-memory** — Fast iteration, no durability:

```go
import inmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"

eng := inmem.New()
```

The in-memory engine uses the context passed to `StartWorkflow` for request acceptance and retains its values for the accepted run. Canceling that request after `StartWorkflow` returns does not cancel the run. Use the workflow handle or `CancelByID` to cancel an accepted in-memory workflow; child workflows started with their parent workflow's context remain tied to its cancellation.

A positive in-memory `WorkflowStartRequest.RunTimeout` sets a fixed execution
deadline; zero leaves the run without one. Timeout cancellation is cooperative,
and a timed-out run fails with `context.DeadlineExceeded`. Agent runtime starts
continue to omit this fixed timeout so external waits can pause their active-time
budget.

---

## Telemetry

### Logger Interface

```go
type Logger interface {
    Debug(ctx context.Context, msg string, keyvals ...any)
    Info(ctx context.Context, msg string, keyvals ...any)
    Warn(ctx context.Context, msg string, keyvals ...any)
    Error(ctx context.Context, msg string, keyvals ...any)
}
```

### Metrics Interface

```go
type Metrics interface {
    IncCounter(name string, value float64, tags ...string)
    RecordTimer(name string, duration time.Duration, tags ...string)
    RecordGauge(name string, value float64, tags ...string)
}
```

### Tracer Interface

```go
type Tracer interface {
    Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span)
    Span(ctx context.Context) Span
}
```

`runtime.WithMetrics` and `runtime.WithTracer` drive semantic runtime
instrumentation independently of the selected workflow engine. The stable core
metrics are:

| Metric | Kind | Dimensions | Meaning |
| --- | --- | --- | --- |
| `loom_mcp.runtime.run.started` | counter | `agent` | Canonical run-start events |
| `loom_mcp.runtime.run.completed` | counter | `agent`, `status` | Canonical terminal run events |
| `loom_mcp.runtime.planner.attempts` | counter | `agent`, `operation`, `status` | Planner activity attempts (`start` or `resume`) |
| `loom_mcp.runtime.planner.duration` | timer | `agent`, `operation`, `status` | Planner attempt duration |
| `loom_mcp.runtime.tool.completed` | counter | `agent`, `tool`, `status` | Canonical tool-result events |
| `loom_mcp.runtime.tool.duration` | timer | `agent`, `tool`, `status` | Tool duration reported by the canonical result |

Run and tool metrics are emitted only when the canonical run log inserts a new
event key, so normal hook-activity retries do not double-count them. Planner
metrics are deliberately attempt-level and may repeat when the workflow engine
retries a planner activity. Metric dimensions are bounded registry/outcome
values; run, session, turn, and tool-call IDs are trace attributes rather than
metric dimensions.
Unregistered or model-invented tool names use the metric value `unknown`; the
raw name remains available on the corresponding span.

`telemetry.NewClueMetrics` reports instrument-creation failures through the
OpenTelemetry global error handler. It reports at most one error per effective
instrument name for each recorder, then skips that measurement. (`RecordGauge`
uses the effective name `<name>_gauge`.) Applications can use
`otel.SetErrorHandler` to send these diagnostics to logs or alerts.

Planner calls use `planner.plan_start` and `planner.plan_resume` spans. Every
new canonical tool result also emits a `tool.execute` semantic span covering
the result's reported execution interval, with `loom_mcp.agent_id`,
`loom_mcp.run_id`, `loom_mcp.session_id`, `loom_mcp.turn_id`,
`loom_mcp.tool.name`, and `loom_mcp.tool_call_id` attributes. Because this
instrumentation runs at planner activity and canonical hook boundaries, it is
the same for in-memory, Temporal, and custom engines. Nil telemetry options
still resolve to no-op implementations.

Model calls emit both Loom-specific correlation attributes (`loom_mcp.*`) and
standard OpenTelemetry GenAI attributes (`gen_ai.*`) on `model.complete` and
`model.stream` spans. Token usage, finish reasons, requested model, resolved
model, and streaming time-to-first-chunk are recorded as span attributes.

Full GenAI input/output message capture is available but disabled by default
because message payloads can contain sensitive data:

```go
rt := runtime.New(
    runtime.WithTracer(telemetry.NewClueTracer()),
    runtime.WithCaptureGenAIMessages(true),
)
```

When enabled, model spans include `gen_ai.input.messages` and
`gen_ai.output.messages`. Reasoning/thinking parts are deliberately omitted,
streamed text deltas are coalesced into one output message at stream end, and
serialization failures are recorded as span events instead of failing the model
call. Capture uses the request after cache, tool policy, and model interceptors
apply their changes. A rejected response or stream does not publish output
messages. Internal `runtime.tool_unavailable` payloads contain only the exact
effective tool catalog.

### Temporal trace domains

Temporal engine tracing and metrics are enabled by default. Set
`temporal.InstrumentationOptions.DisableTracing` or `DisableMetrics` to opt out;
`MetricsOptions` configures the Temporal OTEL metrics handler.

Durable scheduling is a trace-domain boundary. Each Temporal activity starts a
new root span with a new trace ID and attaches the originating request span as
an OpenTelemetry link. Activities are therefore correlated with, but are not
children in, one long request trace. This avoids treating queue and replay time
as synchronous parent/child latency. `InstrumentationOptions.TracerOptions` is
retained for source compatibility but is ignored by this trace-domain
implementation.

Origin `traceparent` values are validated by the OpenTelemetry W3C propagator.
Malformed wire headers are rejected; valid sampled and unsampled origins retain
their trace and span IDs as links.

This engine instrumentation is separate from `runtime.WithTracer`. That option
owns runtime spans, model spans, generated MCP adapter tracing, and the SDK
`TransportObserver`. The local debug server and Pulse stream sink are separate
surfaces.

---

## Feature Modules

| Package                     | Purpose                              |
| --------------------------- | ------------------------------------ |
| `features/memory/mongo`     | MongoDB-backed memory store          |
| `features/prompt/mongo`     | MongoDB-backed prompt override store |
| `features/runlog/mongo`     | MongoDB-backed run event log store   |
| `features/session/mongo`    | MongoDB-backed session store         |
| `features/stream/pulse`     | Pulse message bus sink               |
| `features/model/bedrock`    | AWS Bedrock model client             |
| `features/model/openai`     | OpenAI-compatible model client       |
| `features/model/anthropic`  | Direct Anthropic Claude API client   |
| `features/model/ollama`     | Local Ollama chat model client       |
| `features/model/gemini`     | Google Gemini API and Vertex AI model client |
| `features/model/gateway`    | Remote model gateway client          |
| `features/model/middleware` | Rate limiting, logging, metrics      |
| `features/policy/basic`     | Basic policy engine                  |

`features/session/mongo` writes through multi-document transactions and so
requires a replica set or a sharded cluster; see the MongoDB deployment
requirement in `docs/operations.md`.

---

## MCP Callers

The `runtime/mcp` package provides three caller implementations for different MCP server
transports. Each constructor establishes an initialized MCP session and returns a
caller that can be reused across multiple tool invocations.

Generated SDK-backed MCP servers also expose server-to-client features through
request context. `mcp.Elicit` requests structured user input through official
multi-round-trip `inputRequests`/`inputResponses`, so the implementation is
re-entered and code before the runtime helper must be retry-safe.
`mcp.ReportProgress` sends request-scoped progress notifications using the
original client token. Both fail closed with an unavailable error outside a
generated SDK request context. Sampling and roots are deprecated in MCP
`2026-07-28` and are not exposed as Loom runtime client features. Multi-step
elicitation requires a `2026-07-28` client and uses one round trip per
`mcp.Elicit` call. Its `requestState` is AES-GCM protected, bound to the
original logical request, and portable across SDK server replicas that share a
stable 32-byte `SDKServerOptions.RequestStateKey`. The protected responses
remain client assertions, not an authorization or server-state channel. See
[`mcp_sdk_server.md`](mcp_sdk_server.md) for the generated-server contracts and
examples.

### StdioCaller

Spawns an MCP server as a subprocess and communicates via stdin/stdout:

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"

caller, err := mcp.NewStdioCaller(ctx, mcp.StdioOptions{
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-filesystem"},
    Env:     []string{"HOME=" + os.Getenv("HOME")},
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

### HTTPCaller

MCP HTTP/SSE callers and Ollama preserve a custom `http.DefaultTransport` when
constructing their default clients. Phase timeout tuning applies only when the
default is a standard `*http.Transport`; a custom transport owns its timeouts.
Explicitly supplied HTTP clients are used unchanged.

Connects to an MCP server that exposes the streamable HTTP transport:

```go
caller, err := mcp.NewHTTPCaller(ctx, mcp.HTTPOptions{
    Endpoint:    "https://mcp-server.example.com/mcp",
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

### SSECaller

Connects to an MCP server that exposes the legacy SSE transport:

```go
caller, err := mcp.NewSSECaller(ctx, mcp.HTTPOptions{
    Endpoint:    "https://mcp-server.example.com/sse",
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

All three constructors return a caller that implements `mcp.Caller`. The
initialization timeout only bounds session establishment; it does not cancel the
live session after `Connect` succeeds.
For MCP `2025-11-25` and earlier, each constructor completes the legacy
handshake before it returns. The official SDK sends `notifications/initialized`
after the `initialize` response. If a Streamable HTTP server assigns a session
ID, the HTTP caller includes it in this notification and all later requests. The
SDK sends normal requests only after the notification. For MCP `2026-07-28`, the
SDK uses the stateless `server/discover` handshake instead.

### Normalized tool results

MCP callers normalize `tools/call` results into a consistent shape:

- Text content items are concatenated in order.
- If the combined text is valid JSON, it becomes `CallResponse.Result`.
- Otherwise the combined text is returned as a JSON string.
- `CallResponse.Structured` contains the full structured MCP content payload.
- If a tool returns no text content, callers fall back to marshaling the first
  structured content item into `CallResponse.Result`.

All outbound MCP callers use the official SDK and the same normalization
contract.

The same package also exposes canonical JSON helpers used at MCP boundaries.
Those helpers accept string-keyed maps, including named string aliases, and
fail fast on unsupported map key kinds instead of silently dropping entries.

Generated resource adapters use `mcp.CoerceQueryTyped` for resource URI query
parameters. The generator supplies the string and repeated-field shapes from the
resource method payload. It also registers an RFC 6570 resource template for
query-bearing URIs. Template variable names percent-encode unsupported bytes.
Thus, `true`, `42`, and RFC 3339 text stay strings for
`String` and `ArrayOf(String)` fields. An array stays an array when the URI
contains only one value. Other fields use value inference. Repeated parameters
stay in input order.

Use the same helper in a custom adapter. Describe each string or repeated field:

```go
coerced := mcp.CoerceQueryTyped(query, map[string]mcp.QueryField{
    "name": {String: true},
    "tags": {String: true, Repeated: true},
})
```

### SDK transport ownership

The official MCP Go SDK owns protocol negotiation, Streamable HTTP sessions,
cancellation, standard SSE, and wire-level JSON-RPC. Loom does not generate a
native MCP client or server transport.

The generated SDK wrapper binds each session to one verified principal. It
checks that binding on POST, GET, and DELETE requests. Authentication
middleware must wrap the generated handler.
### Generated MCP tool search

Generated MCP adapters can opt into progressive discovery with
`MCPAdapterOptions.ToolSearch`. Disabled mode keeps the full generated
`tools/list` catalog. Enabled mode makes `tools/list` the authoritative compact
public surface: it returns synthetic `search_tools` and `call_tool` entries plus
real tools named in `ToolSearchOptions.AlwaysVisible`.

`search_tools` accepts either a plain `query` or a case-insensitive regex
`pattern`, never both. It also accepts `category`, `tags`, `max_results`, and
`include_schemas`. Plain query matching normalizes snake_case names into words,
singularizes simple plurals, drops common instruction words, and ranks matches
before limiting. The default policy prioritizes exact and normalized tool-name
or title matches, then prefix/contains name and title matches, then fuzzy
name/title matches using generated token scoring, then broader discovery
metadata, description, parameter, and schema matches. In the default `narrow`
exact-match mode, high-confidence name/title matches suppress weaker broad
and fuzzy matches. High-confidence includes exact, normalized, prefix, and
contains tiers, so a query such as `sentiment` does not retain an unrelated
fuzzy subsequence candidate beside `analyze_sentiment`.

Products can tune generated defaults in the MCP DSL with
`ToolSearch(...)`: `ToolSearchMaxResults`, `ToolSearchMinScore`,
`ToolSearchExactMatch(ToolSearchExactMatchNarrow|ToolSearchExactMatchBoost|ToolSearchExactMatchOff)`,
`ToolSearchFuzzyNameMatching`, `ToolSearchBroadFallback`, and
`ToolSearchWeights(...)`. Generated `ToolSearchOptions` expose the same runtime
knobs for deployment-specific overrides.

The result includes model-readable text with exact `call_tool` JSON examples,
not only a prose invocation hint. Structured content includes `tools`,
`total_matches`, `truncated`, and the supplied `query` or `pattern`. Tool
descriptors preserve MCP Tool-shaped fields including `inputSchema`,
`outputSchema`, `_meta`, `annotations`, and `icons`; schemas are omitted by
default and included only when `include_schemas` is true. Each descriptor also
includes `why_matched`, `call_tool_name`, `call_tool_arguments`, and
`call_tool_json` so clients and models can invoke the hidden target through the
wrapper without guessing the schema. Tool declarations can use
`ToolDiscoveryCallTemplateArg` to include useful optional arguments in those
examples without changing payload validation.

`call_tool` invokes a discovered real tool by name with an `arguments` object. It
rejects synthetic targets and unknown real tool names as tool errors. In compact
mode, direct public `tools/call` requests for hidden real tools are rejected by
default. Pinned `AlwaysVisible` tools remain directly callable. Hidden tools
remain callable only through `call_tool`; direct hidden tool calls are rejected.
Synthetic name collisions and unknown `AlwaysVisible` pins fail
fast during adapter construction.

Toolset tools projected into MCP participate in the same generated catalog as
method-level MCP tools. Their `ToolInfo` schemas come from the generated toolset
`tools.ToolSpec` payload and result schemas, and their `tools/call` cases route
through the shared method-backed dispatcher. Compact discovery treats projected
tools like any other real tool: they may be pinned with `AlwaysVisible`, found
through `search_tools`, and invoked through `call_tool`.

For agents in the same Go process, codegen also emits
`New<Service><MCP>LocalToolsetRegistration(adapter)`. The returned
`runtime.ToolsetRegistration` exposes the same synthetic tools and
`AlwaysVisible` pins as the adapter's compact `tools/list`. Search and
`call_tool` run directly through the adapter's catalog, interceptors, and
generated method and projected dispatchers. They do not open an HTTP
connection, initialize MCP, or create session state. The registration converts
structured MCP results and tool errors into ordinary planner-visible tool
results.

```go
adapter := mcpassistant.NewMCPAdapter(service, promptProvider,
    &mcpassistant.MCPAdapterOptions{
        ToolSearch: &mcpassistant.ToolSearchOptions{
            AlwaysVisible: []string{"search"},
        },
    })
localTools, err := mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
if err != nil {
    return err
}
if err := rt.RegisterToolset(localTools); err != nil {
    return err
}
```

### MCP resource authorization boundaries

Generated adapters apply `MCPAdapterOptions.AllowedResourceURIs`,
`AllowedResourceNames`, `DeniedResourceURIs`, and `DeniedResourceNames` to DSL
resources and `skill://` resources. Exact URI policies match one resource;
policies ending in `/` match a URI prefix. A skill name resolves to its
`skill://<name>/` prefix.

Adapter allow policies are the server's maximum grant. Request-scoped allowed
names are a separate narrowing constraint. When both exist, a resource must
satisfy both. Request and adapter deny policies are additive and take
precedence over every allow.

Headers are untrusted input and do not authenticate a caller or create a
grant. Applications must authenticate before the generated handler, derive any
principal or tenant policy from verified credentials, and configure the
adapter's maximum grant from trusted deployment policy. OAuth DSL declarations
generate metadata, challenges, and audience helpers; they do not install this
application authorization layer.

### Resource updates

Declare `WatchableResource` to enable standard MCP subscriptions. Call the
generated `SDKServer.ResourceUpdated(ctx, uri)` method after the resource
changes. Watchable resources require stateful Streamable HTTP sessions.

`MCPAdapterOptions.ToolCallInterceptors` wrap generated `tools/call` execution
in declaration order, with the first interceptor outermost. Each interceptor
may short-circuit or invoke `next` and receives the tool name plus raw arguments.
Those arguments may contain credentials or user data; do not log them by
default.

### Repair prompts for invalid params (retry.RetryableError)

When an MCP server reports invalid parameters and a structured repair prompt is available, generated
clients may return `retry.RetryableError` with a deterministic `Prompt`. This is intended for LLM-driven
correction: the model returns JSON-only corrected params, which are decoded into the operation payload and retried.

---

## OAuth 2.0 Protected Resource

When the MCP design declares `OAuth(...)`, loom-mcp generates spec-compliant
discovery and challenge plumbing. Runtime helpers in `runtime/mcp/oauth.go`
back that generated code. Consumers interact with three surfaces: the DSL,
the generated helpers, and the runtime primitives below.

Declared `OAuthScope` values populate protected-resource metadata and Bearer
challenges. They do not implement per-operation scope authorization; enforce
required scopes in the application's verifier or authorization middleware.

### Canonicalization

Two functions derive URLs used in PRM and challenges. They differ in how
they handle forwarded headers and failure.

- `CanonicalizeResourceURL(r, trustProxy bool) (string, error)` — strict.
  Derives the RFC 8707 canonical resource URI. When `trustProxy` is false
  (the default for any generated server without `TrustProxyHeaders()` in
  the DSL), forwarded headers are ignored entirely and the origin is
  derived from `r.Host` + `r.TLS` only. When `trustProxy` is true,
  `X-Forwarded-Proto`, `X-Forwarded-Host`, and RFC 7239 `Forwarded` are
  consumed; a header present but malformed is rejected with
  `ErrInvalidForwardedHeaders`. Callers MUST surface errors as 400 Bad
  Request — never silently substitute `r.Host`.
- `CanonicalizeChallengeOrigin(r, trustProxy bool) string` — lenient.
  Used for challenge emission only. Never returns an error: if the strict
  canonicalizer would fail, the function falls back to `r.Host` + `r.TLS`
  so the 401 response still carries some `resource_metadata` URL. This is
  deliberate — a challenge is a formatting artifact inside an already-401
  response, not an identity claim.

### Challenge helpers

- `BuildBearerChallenge(resourceMetadataURL, scope string) string` — RFC 6750
  §3 Bearer challenge. Scope parameter is omitted when empty.
- `BuildInvalidTokenChallenge(resourceMetadataURL, errorDescription string) string` —
  RFC 6750 §3.1 `invalid_token` challenge. Used when a decoded token
  fails audience binding, is expired, or is revoked.
- `WriteUnauthorized(w, resourceMetadataURL, scope string)` —
  convenience wrapper that writes the 401 + JSON body + challenge.
- `WriteInvalidToken(w, resourceMetadataURL, errorDescription string)` —
  same, but for the `invalid_token` form.

`escapeHeaderQuoted` strips CR and LF from header values — defense in depth
against response-splitting even if a misconfigured DSL constant or bypassed
canonicalizer feeds a URL with raw newlines into a challenge builder.

### WithOAuthChallenge middleware

```go
handler := mcpruntime.WithOAuthChallenge(
    mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
    "/rpc",
    mcpassistant.OAuthChallengeHeader,
)
```

Intercepts responses and augments any 401 that does NOT already carry a
`resource_metadata=` parameter (case-insensitively, per RFC 7235) with the
spec-compliant challenge from `challenge(r, mountPath)`. Existing upstream
challenges are preserved verbatim, so consumer middleware can still override
the default. The interceptor passes through `Flush()` so SSE streaming is
unaffected.

The middleware is error-agnostic. It cannot tell a missing token from an
expired, revoked, or wrong-audience token, so it does not add
`error="invalid_token"` automatically. Error-aware application auth middleware
can use the generated `OAuthInvalidTokenChallengeHeader` or runtime
`WriteInvalidToken` helper when that RFC 6750 distinction is required.

### Audience binding

When the DSL declares `ResourceIdentifier(...)`, the generated package
exposes `EnforceAudience(base mcpauth.TokenVerifier) mcpauth.TokenVerifier`.
Wrap the consumer verifier once at mount time:

```go
verifier := mcpassistant.EnforceAudience(consumerVerifier)
handler := mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler)
```

The wrapper reads `TokenInfo.Extra["aud"]` and accepts `string`,
`[]string`, and `[]any` (the shape JSON decoding produces for
a JWT `aud` array). Missing or wrong-typed claims fail closed — there is
no silent admission path. Mismatches return `ErrAudienceMismatch`, which
wraps `mcpauth.ErrInvalidToken` so `RequireBearerToken` responds 401 and
`WithOAuthChallenge` adds the standard resource-metadata Bearer challenge. It
does not add the `invalid_token` parameter automatically.

Without `ResourceIdentifier(...)`, the framework cannot know the expected
audience (it would be per-request), so `EnforceAudience` is not generated.
Audience enforcement in that mode remains the consumer's responsibility.

### Forwarded-header trust posture

loom-mcp's default is to NOT trust `X-Forwarded-*` or RFC 7239 `Forwarded`
headers. Enable `TrustProxyHeaders()` in the DSL only when every request
reaches the server through a reverse proxy the operator fully controls and
that strips these headers from direct-client requests. Otherwise an
attacker with direct network access can set forwarded headers to control
the PRM `resource` field advertised to clients.

For most deployments, pinning `ResourceIdentifier(...)` is preferred. A
declared identifier bypasses forwarded-header derivation entirely and is
the spec's recommended posture. The PRM handler also short-circuits the
strict canonicalizer when the identifier is pinned, so a malformed
forwarded header cannot fail the PRM request in that configuration.

### Sentinels

- `ErrInvalidForwardedHeaders` — a `Forwarded` or `X-Forwarded-*` header was
  present but malformed (control characters, path/fragment/userinfo
  delimiters, etc.). Surface as 400.
- `ErrEmptyResourceURL` — no scheme+host could be derived. Surface as 400
  with a diagnostic message; if this fires in steady state, pin
  `ResourceIdentifier` or enable `TrustProxyHeaders()` as appropriate for
  the deployment.

---

## Stream Profile Reference

Stream profiles control which events reach different audiences. Use profiles to filter
events for specific use cases.

| Profile               | Purpose                       | Events Included          |
| --------------------- | ----------------------------- | ------------------------ |
| `DefaultProfile()`    | All events, child runs linked | All event types          |
| `UserChatProfile()`   | End-user chat UIs             | Same as default          |
| `AgentDebugProfile()` | Debug view                    | All event types          |
| `MetricsProfile()`    | Telemetry and monitoring      | `usage`, `workflow` only |

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"

// Get a profile
profile := stream.AgentDebugProfile()

// Profiles are used internally by stream subscribers
// to filter events before delivery
```

---

## Tool Errors

The `runtime/agent/toolerrors` package provides structured error types for tool execution
failures that integrate with the planner retry system.

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/toolerrors"

// Create a tool error with retry hint
err := toolerrors.New(
    toolerrors.WithMessage("Database connection failed"),
    toolerrors.WithRetryable(true),
    toolerrors.WithHint("Check database connectivity and retry"),
)

// Check if error is retryable
if toolerrors.IsRetryable(err) {
    // Handle retry logic
}

// Tool errors are automatically converted to planner.RetryHint
// for planners to handle gracefully
```

### Validation Issues and Retry Hints

When a custom registry provider handler returns a Go error, `provider.Serve`
keeps its details in local logs and traces. The completed tool result exposes
only an explicit Loom error remedy's safe message, or `tool execution failed`
when no safe message is provided. Do not rely on raw error strings reaching
registry consumers or model input.

Tool calls can fail because the input payload is missing fields, violates constraints,
or has the wrong JSON shape. When that happens, callers generally need actionable,
field-level feedback rather than a generic failure string.

Loom MCP supports two complementary paths that produce `planner.RetryHint`:

1. **Decode‑time validation (generated codecs)**

   The generated tool codec validates the tool JSON payload before execution.
   If validation fails, the codec returns a generated validation error that exposes
   structured issues (`Issues() []*tools.FieldIssue`) and descriptions. The runtime
   converts these into `planner.RetryHint` automatically (missing fields, enum values,
   etc.). Generated union decoders report invalid discriminators with the allowed
   enum values and report a missing nested union `value` as a missing field, so
   MCP callers receive actionable retry guidance instead of transport-level JSON
   decoder failures. The generated error uses fixed framework text. It does not
   retain the raw Goa validation message or the submitted value.

2. **Execution‑time validation (service / tool provider errors)**

   When a tool provider calls a bound service method, the method may return a structured
   validation error (for example `loom.MissingFieldError`, `loom.InvalidLengthError`, …).
   Providers should surface these as **structured validation issues** in the tool result
   message so consumers can build a `RetryHint` without parsing error strings.
   - **Provider behavior (generated)**: generated providers call
     `toolregistry.ValidationIssues(err)` and, when issues are present, emit an error
     result that includes them.
   - **Wire protocol**: tool result errors may include `issues` (`[]FieldIssue`).
   - **Consumer behavior**: registry executors convert `issues` into a `RetryHint`
     (e.g., `missing_fields`). They do not attach examples or submitted input.

The runtime bounds all generated retry hints to 4096 encoded bytes after final
enrichment. The bound applies to local and registry-routed tool execution.

This keeps the contract strong and deterministic: validation stays at boundaries,
and “what to retry with” is computed from structured data, not heuristics.

---

## Model Middleware

The `features/model/middleware` package provides middleware for model clients.

### Adaptive Rate Limiter

Apply adaptive rate limiting to handle provider throttling:

```go
import mdlmw "github.com/CaliLuke/loom-mcp/v2/features/model/middleware"

rl := mdlmw.NewAdaptiveRateLimiter(
    ctx,
    throughputMap,     // *rmap.Map for cluster-wide state (nil for local)
    "bedrock:sonnet",  // Model family key
    80_000,            // Initial TPM (tokens per minute)
    1_000_000,         // Max TPM
)

limitedClient := rl.Middleware()(rawClient)
rt.RegisterModel("bedrock", limitedClient)
```

The rate limiter adjusts throughput with additive-increase/multiplicative-
decrease (AIMD) when provider calls report rate limits or successful probes. It
does not retry the request or implement time-based exponential backoff.
Cluster coordination preserves fractional TPM values in the shared rate, including
backoff floors and probe ceilings.
Capacity reduction and recovery adjust only the bucket's refill rate; the burst
capacity stays pinned at the
configured max TPM, so a request whose estimated cost fits within max TPM always
waits for capacity rather than being rejected after backoffs shrink the budget.
A request estimated above max TPM can never be admitted and fails fast with
`middleware.ErrRequestTooLarge`; raise the limiter's max TPM or reduce the
request size.

`NewAdaptiveRateLimiter` admits requests using estimated input tokens and does
not reserve output tokens. When the wrapped provider implements exact
`model.TokenCounter`, the middleware preserves that optional capability for
callers, but input-only admission still uses the estimator.

Use output reservation when provider quota accounting charges the requested
maximum output as well as the input:

```go
rl := mdlmw.NewOutputReservationAdaptiveRateLimiter(
    ctx,
    throughputMap,
    "bedrock:sonnet",
    80_000,
    1_000_000,
)
limitedClient := rl.Middleware()(rawClient)
```

Output-reservation admission requires the wrapped provider to implement
`model.TokenCounter` with `Exact: true` and each request to set `MaxTokens > 0`.
It reserves `InputTokens + MaxTokens` before calling `Complete` or `Stream`.
The counter can make its own provider request. Missing or inexact counting, an
invalid output limit, integer overflow, or a combined cost above the fixed
maximum burst fails before model generation starts. A rate limit from the
counter triggers the same adaptive backoff as a generation call. The versioned
Pulse key isolates this accounting mode from input-only limiters during rolling
upgrades. When the provider does not implement `model.TokenCounter`, the
wrapped client still does not advertise that optional interface.

---

## Common Patterns

### Bootstrap Helper

`loom example` emits a bootstrap at
`internal/agents/<service>/bootstrap/bootstrap.go`. Each service also gets
separate planner and executor scaffolds under
`internal/agents/<service>/<agent>/`. The command at `cmd/<service>/main.go`
imports that service's bootstrap:

```go
// Bootstrap creates runtime with Temporal, stores, and registers agents
rt, cleanup, err := bootstrap.New(ctx)
if err != nil {
    log.Fatal(err)
}
defer cleanup()
```

These files are application-owned. A later `loom example` command does not
overwrite a file that already exists.

### Pulse Streaming

```go
import pulsestream "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse"

streams, _ := pulsestream.NewRuntimeStreams(pulsestream.RuntimeStreamsOptions{
    Client: pulseClient,
})

rt := runtime.New(
    runtime.WithEngine(eng),
    runtime.WithStream(streams.Sink()),
)

// Subscribe to session events
sub, _ := streams.NewSubscriber(pulsestream.SubscriberOptions{SinkName: "ui"})
events, errs, cancel, _ := sub.Subscribe(ctx, "session/session-123")
defer cancel()

// Consume until you observe `type=="run_stream_end"` for the active run ID.
```

`Subscribe` is the UI/convenience API: it acknowledges each valid entry after
placing it on the local event channel, and acknowledges malformed poison
messages after reporting their decode error. This does not prove that a
downstream database transaction or side effect committed.

Durable consumers use manual deliveries and acknowledge only after committing
their own work:

```go
deliveries, errs, cancel, err := sub.SubscribeManual(ctx, "session/session-123")
if err != nil {
    return err
}
defer cancel()

for delivery := range deliveries {
    if err := delivery.DecodeError(); err != nil {
        if err := deadLetters.Put(ctx, delivery.PulseID(), delivery.RawPayload(), err); err != nil {
            return err // unacked: retry dead-lettering after reclamation
        }
        if err := delivery.Ack(ctx); err != nil {
            return err
        }
        continue
    }
    if err := projection.Apply(ctx, delivery.Event()); err != nil {
        return err // unacked: remains pending for Pulse redelivery/claim
    }
    if err := delivery.Ack(ctx); err != nil {
        return err // retry is safe until Ack succeeds
    }
}
```

Manual mode exposes malformed entries with `DecodeError`, `RawPayload`, and
`PulseID`; it never acknowledges them automatically. Consumers can commit them
to a dead-letter store and then acknowledge, or leave them pending for a
reclamation policy. Pulse delivery is at least once: persist `EventKey()` (or
another stable application key) with the projection to make reprocessing
idempotent; manual acknowledgement alone is not exactly once.

The `errs` channel is best-effort and never blocks event delivery, so draining
it is optional. Decode errors that arrive while its one-slot buffer is full are
dropped and counted (see `Subscriber.DroppedErrors`). Terminal errors (ack
failures) evict any pending decode error so the terminal cause is always
delivered, then both channels close in auto-ack mode — ranging over `events`
alone is enough to observe termination. In manual mode, `Delivery.Ack` returns
ack errors directly and may be retried; they are not sent through `errs`.

### Custom Tool Executor

```go
executor := runtime.ToolCallExecutorFunc(func(ctx context.Context, meta *runtime.ToolCallMeta, call *planner.ToolRequest) (*planner.ToolResult, error) {
    // Access explicit metadata
    log.Printf("Executing %s in run %s, session %s", call.Name, meta.RunID, meta.SessionID)

    // Call your service
    result, err := myService.Execute(ctx, call.Payload)
    if err != nil {
        return nil, err
    }

    return &planner.ToolResult{
        Name:   call.Name,
        Result: result,
    }, nil
})
```

---

## Error Handling

### Sentinel Errors

```go
var (
    ErrAgentNotFound       = errors.New("agent not found")
    ErrEngineNotConfigured = errors.New("runtime engine not configured")
    ErrInvalidConfig       = errors.New("invalid configuration")
    ErrMissingSessionID    = errors.New("session id is required")
    ErrWorkflowStartFailed = errors.New("workflow start failed")
    ErrRegistrationClosed  = errors.New("registration closed after first run")
)
```

### Run Store Errors

```go
var ErrNotFound = errors.New("run not found")  // run.ErrNotFound
```

### Model Errors

```go
var ErrStreamingUnsupported = errors.New("model: streaming not supported")
var ErrRateLimited = errors.New("model: rate limited")
```

---

## Best Practices

1. **Register before running.** All agents and models must be registered before
   the first `Run` or `Start` call. Registration closes afterward.

2. **Use generated clients.** The typed `<agent>.NewClient(rt)` embeds route
   information and provides compile-time safety.

3. **Choose one event owner.** Use a decorated model stream directly or through
   `planner.ConsumeStream`. For another validated stream, give
   `planner.ConsumeStream` the runtime planner-event sink.

4. **Set SessionID for sessionful runs.** `Run` and `Start` require a session ID
   for grouping and memory association. `OneShotRun` is explicitly sessionless.

5. **Trust the contracts.** Don't add defensive checks for values guaranteed by
   generated validation or construction. Let violations fail fast.

6. **Configure stores for production.** In-memory defaults are suitable for
   development; use MongoDB stores for persistence.

7. **Stream events, don't poll.** Use `SubscribeRun` or Pulse subscriptions
   instead of polling run status.

8. **Keep planners focused.** Planners decide what to do (final answer vs. tools).
   Tool implementations handle how.

---

## Glossary

| Term             | Definition                                                                                         |
| ---------------- | -------------------------------------------------------------------------------------------------- |
| **Run**          | A single workflow execution. Has a unique RunID.                                                   |
| **Session**      | Groups related runs (e.g., multi-turn conversation).                                               |
| **Turn**         | A user message → agent response cycle. May span multiple runs if interrupted.                      |
| **Planner**      | Decision-maker that analyzes messages and returns tool calls or final responses.                   |
| **Toolset**      | Collection of related tools with shared execution logic.                                           |
| **Tool Spec**    | Metadata and JSON codecs for a tool (name, schema, codec functions).                               |
| **Bounds**       | Metadata describing how a tool result was truncated or limited.                                    |
| **Engine**       | Workflow backend such as in-memory or Temporal; owns start, signal, query, and durable execution mechanics. |
| **Hook**         | Internal runtime event. Most hooks are durably appended to the runlog before projection.           |
| **Runlog**       | Canonical append-only hook event record for a run.                                                  |
| **Transcript memory** | Derived per-run `memory.Event` projection used for planner history and event search.             |
| **Long-term memory** | Durable `memory.Entry` values managed by `memory.Service`; separate from transcript events.       |
| **Stream Event** | Client-facing, best-effort projection delivered through a runtime stream sink.                     |
| **MCP server**   | Generated protocol surface exposing designed tools, resources, and prompts.                        |
| **MCP caller**   | Runtime client-side adapter that consumes an external or generated MCP server.                     |
| **Tool registry** | Catalog/search/execution infrastructure for tools; distinct from the prompt registry.              |
| **Prompt registry** | Baseline prompt specs plus optional scoped overrides.                                              |
| **MCP skill resource** | `SkillDirectory(...)` projection exposed as `skill://` MCP resources.                             |
| **Model-facing skill tools** | `FromSkills(...)` toolset used by an agent to list and load local instruction packages.          |
| **Finalizer**    | Aggregates child results into parent tool result for agent-as-tool (does not propagate artifacts). |
| **Reminder**     | Structured backstage guidance injected into planner prompts.                                       |

## Pure context checkpoint rendering

`runtime/agent/transcript/checkpoint` implements pure selection and rendering
without changing workflow state, stored index encodings or the protocol
version. `Units` groups assistant batches, their results, bookkeeping
operations, reminders and attached typed/finalize entries; `openFrom` makes
an unresolved await suffix atomic. `CutPoints` permits only boundaries
before assistant-starting units with a closed preceding call set.
`ChooseTail` always retains the newest unit. `RoleOf` loads stored Message
roles and must succeed; a failed role read panics with a wrapped cause.

`Input` contains record-authored `Users`, stored `Pinned` IDs, `CitedUsers`,
summary provenance, a repeatable newest-first `iter.Seq[ReceiptItem]` over
the receipt chain, and bounded `EarlierCounts`. Entry IDs in `Users` identify
pins in the current index; `Origin` supplies original order and labels.
`SelectPins` chooses the first user and a contiguous newest suffix within
half of `KeepUserTokens` and `MaxPins`. `Render` rechecks pins against the
current budget, first user then newest first, stopping at the first pin that
does not fit. Citations select index lines before uncited newer lines,
but displayed user text always sorts oldest first by original run position
and message ID. Text comes from `Users`, including cited corrections; a
summary cannot supply user wording.

`DefaultLimits` is `{MaxPins: 32, UserIndexLineBytes: 160, MaxPinnedParts: 2}`.
Non-text parts have a separate count and quarter-of-`KeepUserTokens` token
allowance. `PartTokens` measures each part independently; nil uses 1,500
tokens for images/URIs and half a token per document byte, rounded up.
Text/chunk documents use text previews. Unshown parts name their original
message, part number, format and size. `HeadTail` is deterministic and
UTF-8 safe. `ClearedResult` supplies the tier-1 placeholder and error first
line; K12 retains the original result's error flag.

`ParseCitations` accepts exact `[message N]` and `[rK message N]` forms.
`Qualify` binds bare citations to the run that wrote a copied summary.
`ValidateCitations` uses an authorized `CitationResolver` returning entry
kind and conversation role; user-role tool results are not user citations.
Its failures wrap `ErrCitesNonUser`, `ErrCitesUnknown` or `ErrCitesNotBehind`,
and preserve resolver errors.

`BoundSummary` preserves whole summaries that fit. Otherwise it reserves a
closing reference, keeps ordered Markdown headings while they fit, shares
remaining bytes equally and passes short-section surplus forward.
`ReceiptItem` can supply both a call and assistant prose; these receive
separate discovery lines. Individual receipt lines are capped at 512 bytes;
older items collapse to at most two aggregate lines, each with a handle.
Tiny component allowances omit whole lines rather than partial handles.
`Handle` hashes `part || recordID` into `rc-` plus eight hex digits; part names
are `users`, `receipt`, `earlier`. `CheckpointID` hashes the record ID into
`ck-` plus eight hex digits. Copied-summary closing references retain the
summary's run and record ID.

`Render` produces one user-role message, text first and kept non-text parts
after it. Its text bound is `MaxBytes(b) = 2 × (KeepUserTokens +
MaxSummaryTokens + ReceiptTokens + FramingTokens)` under the current budget.
Framing is measured exactly; deficits consume receipt bytes above its two
compact-line floor, then summary bytes above its compact closing-line floor
when a summary exists. Compact fallbacks preserve `[receipt rc-xxxxxxxx]`,
`[earlier rc-xxxxxxxx]`, `[users rc-xxxxxxxx]` and
`[full text: checkpoint rN:ck-xxxxxxxx]`. Tiny component budgets borrow unused
framing or donor capacity for these references; assembled text is never clipped.
`MinCheckpointBytes(b)` reserves the full user allowance (at least the 24-byte
compact users line), largest header/labels, 52 receipt bytes and the largest
compact summary closing line (56 bytes on the supported 64-bit platform):
`max(512, max(24, 2 × KeepUserTokens) + 500)` bytes.
Smaller total budgets return `ErrCheckpointBudget`. The default framing remains
200 tokens. At B=2,900, default checkpoint budgets give a 790-byte minimum and
1,444-byte maximum. Renderer labels and placeholders recommend no tool.
`ReceiptItem.Lookup` renders a retained lookup call as
`toolu_x (context lookup: <bounded args>)`, preserving the call ID and hiding
its tool name. K12's `FromRecord` must set this flag for the runtime lookup tool.

`transcript.AssembleView` strips thinking, including opaque redacted
reasoning, before the tail and retains tail blocks intact. It marks the
checkpoint with `CheckpointMetaKey`. `ValidateView` ignores system messages,
requires a first user and an assistant after the checkpoint, validates
bidirectional complete tool handshakes, and applies `GrammarBedrock` role
alternation/thinking or `GrammarAnthropic`.
Compacted parent views loaded for child admission permit trailing unanswered
calls from the in-flight assistant turn; every other grammar check and planner
request validation remains unchanged.

K12 implements `FromRecord`, user content and citation metadata loading,
repeatable newest-first receipt traversal, selected pins, view assembly, grammar
validation and caches. Stored receipt lines include tool-use IDs, entry kinds
and unsummarised ranges. The loader and RunRecord share the tail projection
rules, including hidden calls and cleared placeholders. K13 owns handle resolution;
K15 calls citation validation/qualification; K17 owns settings validation;
K18 supplies continuation positions, carried origins and earlier totals.

Golden checkpoints are in
`runtime/agent/transcript/checkpoint/testdata/{correction,cross_run}.golden`.

## Pure recall contracts

`runtime/agent/transcript/recall` defines the spec for
`tools.RecallContext` (`runtime.recall_context`), with all usage guidance in
`Description`. `ParseInput` accepts exactly one operation: `read` with `id`,
`part` (`result` or `arguments`), byte `offset` and optional `run`; `list` with
a handle or inclusive `from`/`through` range and optional run; or literal,
case-sensitive UTF-8 `find` text. Lists and searches accept a continuation
cursor. Invalid known operations retain `Input.Kind` for allowance charging;
an ambiguous or unrecognized kind is empty and charges discovery. Parser
errors expose only typed `recall_invalid_input`, never input fragments.

The workflow advertises this tool on the first shortened view, including an
eviction intent whose checkpoint is stored by the loader, and on subsequent
steps. It adds the definition to the model catalog and active AllowedTools.
Tools-disabled, recovery-disabled and finalization turns omit it. Unadvertised
calls follow ToolUnavailable.

Configure allowances with `ContextCompaction.RecallDiscoveryCalls` and
`RecallReadCalls` (zero selects 20; negative values fail registration options).
`compaction.State.Recall` charges read calls separately from list/find, in
original call order before timeout grouping, including malformed and rejected
calls. Every committed compaction refills each allowance with
`compaction.Refill(a, initial) = min(a + ceil(initial/2), 3*initial)`.
Exhaustion stores an ordinary eligible result with `code`, `allowance`,
`other_remaining` and `refills_at: "next compaction"`.

`runtime.WithContextRecovery(agentID, runtime.ContextRecovery{DisableRecall: true})`
turns lookup off and emits `context_recall_disabled` once at the first shortened
view. The same struct also carries GuardMargin, zero resolving to 10%.
Resolved settings and child-agent settings are stamped into RunInput and the
canonical RunRecord. ContextCompaction does not own DisableRecall.

`RecallActivity` performs all lookup I/O and stores its model-facing output
before returning a result reference and bounded preview. Custom engines implement
`RegisterRecallActivity` and `ExecuteRecallActivity`. The handler derives the
session from the trusted run, walks Prev before From, and authorizes each entered
run before reading content. Install `WithRecallAuthorizer(func(context.Context,
RecallAccess) error)` for application authorization. Its access envelope contains
SessionID, RunID, TargetRunID and Scope. Only eligible hashes are read; Record,
Output and server-only metadata never supply lookup text.

Receipt handles list all lines from the addressed record through its current-run
Prev chain, newest first; earlier handles list predecessor-run lines; users
handles list original user first lines. Range listings include model-visible
text, calls, results and structural messages. Reads pair parallel results by
ToolUseID, and original user text always comes from index entries. Full stored
summaries page from their ck- IDs. Removed content returns recall_unavailable;
unknown evidence returns recall_not_found; authorization denial returns
recall_unauthorized. Images/documents return recall_unsupported_part with
format/bytes and available text until K22.

Read pages use max(512, ProtectRecentToolTokens) bytes and snap offsets to UTF-8
boundaries. Follow next_offset. Find scans hidden eligible entries and cleared
results, current run then From runs, newest first, up to 8 MB or ten matches;
each match contains id, run, original byte offset and a snippet of at most 160
bytes. Follow the same literal selector with its cursor. EncodeCursor/DecodeCursor
use deterministic unpadded base32 over kind, handle/range/scan and position.
Every call binds and re-resolves the selector; cursors grant no authority.
Run-qualified rN labels resolve only through recall.RunChain; K13's default
resolves none. K18 supplies production continuation labels. Thirty-two-bit handle
collisions select the first matching reachable record deterministically.

Each invocation records one keyed context_recalled event with kind, outcome
(ok/exhausted/rejected/unsupported), code, tool_use_id, bytes, remaining counters
and step. Exhaustion emits recall_budget_exhausted once per allowance episode,
reset by refill. RunRecord rebuilds and validates recorded charges and settings.
Workflow protocol 9 adds overflow recovery to these lookup contracts. The recorded
v12 fixture exercises bounded recovery, lookup and a single resume after
confirmation and clarification, including the recorded question/answer pair.
The v12 budget fixture covers terminal adoption across the active budget without a finalizer call; v5–v11 fail
before effects.
Structural templates remain version 5.

FromRecord preserves the canonical tool name in the stored record and marks
lookup receipt items so rendered lines say context lookup. Runtime-generated
lookup IDs also omit the reserved name. Compaction-enabled registration rejects
application identifiers or provider names containing recall_context.

## Engine conformance

`runtime/agent/engine/enginetest.RunConformance` checks the public engine contract:
workflow registration, results and status; typed activity inputs, outputs, errors,
options and attempts; parallel tool futures; all typed signals buffered during
activities; timers and monotonic workflow time; children; cancellation and detached
cleanup; and live root-run queries. Retry-capable engines must recover a failed
activity on attempt two. Durable engines must recover after worker replacement
without executing an already completed activity again. Value preservation on
in-memory execution checks pointer passing; it does not prove a codec round trip.
Serializing adapters, such as Temporal, exercise their codecs through the suite.

Custom adapters call `enginetest.RunConformance(t, enginetest.Factory{...})` from a
`TestConformance`. Set `Durable` and `Retries` truthfully and implement `New` to
return a fresh `Fixture` with isolated workflow/activity queues and test cleanup.
Provide `VerifyActivityOptions` to inspect actual backend scheduling and, when
`Durable` is true, `Restart` to replace workers on the same backend, re-register
the supplied definitions and activate the new worker. Unsupported capabilities
produce explicit skips. `KnownGaps` uses exact scenario names with a defect and
follow-up explanation. Executable gaps must return a typed `enginetest.GapError`
wrapping an observed behavioral difference. Unexpected success requires removal
of the obsolete entry. Deadlines, cancellations, expired scenario contexts and
untyped errors fail the test; they cannot count as expected failures. Unexpressible
API gaps remain explicit skips.

Run `go test -race ./runtime/agent/engine/inmem -run '^TestConformance$'` for the
in-memory suite, or `make test-engine-conformance` for both adapters. The latter
starts a real Temporal dev server using the SDK's CLI download mechanism, has a
five-minute timeout, and runs under `make itest`. Temporal is skipped with
`-short`. Current known gaps are in-memory queue/schedule/heartbeat options.
Conformance workers use one-second workflow-task and sticky timeouts
to bound restart recovery independently of the old worker taking its next task.
