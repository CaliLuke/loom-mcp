# Context compaction: survey and design direction

Status: revision 9, 2026-10-09. Revision 8 was approved by design review for
architectural freeze and implementation; revision 9 closes record gaps found
by checking the design against the eval plan and the store backends. Nothing
here is implemented, and no acceptance proof has run yet. Revisions 2 and 3
incorporate a code-accuracy review, a design review and a second-round review.
Revision 4 answers an architecture review: bounded checkpoints, session
handoff, request-level budgeting, the durable record, and release criteria for
turning compaction on by default. Revision 5 answers its follow-up:
checkpoints that adapt when the budget shrinks, a bounded continuation kept
apart from its evidence, explicit cross-run identity, bounded planner
histories, receipt handles for discovery, and a non-inferiority release gate.
Revision 6 answers a third review: precedence by message number with a user
index, a model-facing projection, byte bounds kept apart from token estimates,
non-text parts, explicit session lineage, a commit protocol, discoverable
assistant text, split recall allowances, the remaining growth, a recall
measure that does not penalise good summaries, and the tickets moved to an
execution plan. Revision 7 fixes what a review of revision 6 found: the
summary can no longer outrank a user message, adoption is per activity
attempt, non-text parts have their own bound and recall path, the release gate
states its power, and continuations use a separate store interface. Revision 8
resolves four contract conflicts from the next review: eligible evidence is
separated from its bounded rendering, call records store a lossless message
sequence, user-message order and summary citations are defined across runs,
and continuation publication is idempotent; it also sets a per-provider
default for non-text content, calibrates the whole release gate, and reuses
`model.TokenCounter`. Revision 9 makes the record what the eval plan needs:
record events skip `BeforeEvent` interceptors, the request record matches
messages by their full encoding and lists every request parameter, call
records name their activity and step and keep their request facts, `RunRecord`
has a self-contained export, and the store contracts state their conditional
writes.

This document records what loom-mcp's compaction does today, what the leading
open-source agent harnesses and frameworks do, and the design direction that
follows. loom-mcp designs its own approach; upstream goa-ai is one input among
the others. It is input to a ROADMAP workstream; its architecture is frozen
pending maintainer sign-off, and the execution plan is the next step.

## 1. Current state in loom-mcp

Compaction is the `HistoryPolicy` in `runtime/agent/runtime/history.go`,
declared per agent with `RunPolicy(History(...))` and generated into the
registration (`codegen/agent/registry_render.go:113`). Two policies exist:
`KeepRecentTurns(n)` and `Compress(client, cfg)`.

| # | Gap | Evidence |
|---|---|---|
| G1 | Cannot compact inside one run. A turn is a user message plus everything after it, the newest turn is always kept whole, and an autonomous run is one turn: compaction silently does nothing, or fails with "newest history turn cannot fit" when a token tail budget is set. | `history.go:286-288,400-440`, `parseTurns` |
| G2 | The workflow carries the full raw transcript into every planner activity, capped at 1 MB. Compaction runs inside the activity and its output is discarded, so long runs fail at the cap regardless of policy. | `workflow_helpers.go:381-397`, `activity_input_budget.go:21` |
| G3 | Once triggered, every later planner call re-summarises the whole older history: one extra model call per step, non-deterministic across steps and retries. | `runtime_policy_helpers.go:30`, `planner_activities.go:63,217` |
| G4 | The summariser's own input is unbounded. | `compressionRequest` |
| G5 | The summariser never sees tool data: calls render as `[Tool Call: name]`, results as `[Tool Result]`. | `formatMessage` |
| G6 | The default prompt is a generic copy of a coding-assistant compaction prompt. It asks the model to "wrap your analysis in tags" without naming them, so the analysis cannot be stripped and lands in the summary. | `history.go:95`, `extractResponseText` |
| G7 | Opt-in per agent with hand-picked thresholds; no model context-window metadata. | `dsl/history.go` |
| G8 | Token triggers require an exact counter; only Gemini and Bedrock clients implement one. `model.TokenEstimator` exists but `Compress` rejects inexact counts. | `history.go:473`, `model/model.go:422` |
| G9 | No recovery from provider "context too long" errors, and the overflow stop reason is mishandled (F6). | `features/model/bedrock/client.go:893`, `features/model/anthropic/client.go:730` |
| G10 | No event records that compaction happened. | `runtime/agent/hooks/events.go` |

## 2. Survey

Each system was read from source at the commit listed in section 9. "Provider"
means the model API vendor.

### 2.1 Comparison

| System | Trigger | Checked | Cut / kept verbatim | Cheap tier before summary | Summary | Persisted | Overflow recovery | Default |
|---|---|---|---|---|---|---|---|---|
| Codex CLI | min(config, 90% window); window from bundled catalog | before turn and between tool steps | drops all tool data; keeps 20k tokens of user messages | tool output truncated on write (~10 KB) | handoff prompt; remote encrypted compaction on OpenAI | `Compacted` record with replacement history; originals on disk | fails the turn, compacts next turn | on |
| Gemini CLI | 50% window; last usage | before every model call | newest 30%; cuts only at user messages | masks old outputs to files, keeps preview + path (50k protected, ≥30k saving) | XML state snapshot, merges prior snapshot, second "probe" self-check | chat restarted; append-only patches | pre-flight warning only | on |
| opencode | last usage ≥ input limit − reserve; models.dev | every step, aborts stream | recent tail 2k–15k tokens; `splitTurn` cuts inside a turn | prune old outputs (40k protected, ≥20k saving); off by default | incremental on `<prior-summary>`; tool args and 2k-char results | compaction part + tail pointer; originals kept | ~30 error regexes, compact, replay the user message | on |
| Goose | 80% window; usage + tiktoken delta; models.dev | before inference | whole history; keeps last user message | optional LLM one-liners per tool pair | JSON sections, full tool text; middle-out drop on overflow | originals hidden, kept | compact, max 2 attempts | on |
| Cline SDK | 90% max input; chars/4 scaled by measured usage | before every model request | 20k-token tail; never splits pairs | deterministic "basic" strategy, budget projector | incremental, sections, file lists pre-filled | hash-checked sidecar projection | one retry using the deterministic strategy only | off in SDK, on in app |
| pi / oh-my-pi | window − max(15%, 16k); max(usage, local estimate) | after turn; omp also between tool steps and speculatively | `firstKeptEntryId`, ~20k tail; split-turn summary | omp: prune (40k protected, ≥20k saving), artifact refs | incremental, sections, file lists, injection-hardened prompt | `CompactionEntry` in append-only JSONL | ~25 error patterns, compact, retry once; no-progress guard | on |
| OpenHands SDK | min(condenser cap, model window); event count | every step | keep first N + tail; cut points respect pairs, batches, thinking loops | outputs truncated at creation | incremental structured summary | `Condensation` event; view is a fold over the log | overflow or malformed history emits a condensation request | preset on |
| Aider | min(max(window/16, 1k), 8k) chat-history tokens | after turn | tail ~half budget | repo map has its own budget | brief, user voice, no tool data | in memory | none; history wiped if every summariser fails (bug) | on |
| LangChain v1 | typed fraction/tokens/messages clauses, AND/OR | before every model call | last 20 messages; safe cutoff | `ClearToolUsesEdit` per request, not persisted | structured; input capped at 4k tokens | destructive rewrite of state | middle-cut retry for the summariser only | off |
| Google ADK (Py/Go) | interval or token threshold; usage + estimate | before model calls and after the run | retention size; no open calls, confirmations or auth requests | none; "worth compacting" and progress gates | rolling; "durable facts, copy verbatim"; per-item 2k cap, 200k total cap (Go) | compaction event over a range; nothing deleted | none | off |
| OpenAI Agents SDK | ≥10 candidate items | after turn | opaque, provider-side | `ToolOutputTrimmer` per call | `/responses/compact` (encrypted) | destructive session replace | none | off |
| Pydantic AI | messages, tokens or fraction of window (pricing registry) | before every model request | keep 20 messages | `TieredCompaction`: clamp, clear results, dedupe reads, then summary | anchored incremental summary; durable operation under Temporal | history mutated in place | none | off |
| Letta | 90% window | after step | sliding 30% | clip | model per provider | evicted messages searchable | compact and retry, fallback ladder | on |
| goa-ai v0.87.1 | `CompressAtMaxInputTokens`; exact counts unless `AllowEstimatedTokens` | each model request | turns group assistant + results; system messages kept in place | none | evidence-preserving prompt; attachments native | `HistoryContext` (summary + source hashes + policy fingerprint) in workflow state, reused | none | opt-in |

Provider-side options, for completeness: Anthropic has `clear_tool_uses`,
`clear_thinking` and two compaction modes (threshold and on-demand, the latter
with a signed block the client must persist); OpenAI has `compact_threshold`
and `/responses/compact` with encrypted items; Gemini has none; Bedrock exposes
Anthropic edits only through `additionalModelRequestFields`. Overflow errors
differ per provider: Anthropic 400 "prompt is too long" or stop reason
`model_context_window_exceeded`; OpenAI `context_length_exceeded`; Gemini 400
`INVALID_ARGUMENT` with a token message; Bedrock `ValidationException` with
model-specific text or stop reason `model_context_window_exceeded`.

### 2.2 Where the field agrees

1. **Check before every model call, including inside a tool loop.** Codex,
   opencode, Cline, OpenHands, ADK, LangChain, Pydantic AI and Gemini CLI all
   do. oh-my-pi added mid-run checks because subagents run one long turn and
   never reached the end-of-turn check, which is loom-mcp's G1.
2. **Trigger on a fraction of the context window** (80–90% typical), from a
   model catalog with a per-model override. Pydantic AI found its registry
   reports 1M for a 200K model, so the override is required.
3. **Count tokens as last provider usage plus a local estimate of what was
   added since.** Exact counting before each call is rare; Cline scales its
   estimate by measured usage, oh-my-pi takes the larger of the two.
4. **Cut only at safe boundaries, but allow cuts inside a turn.** Never split a
   call from its result, a parallel batch, a thinking loop, or an open
   confirmation. Keep a verbatim tail measured in tokens (about 20k).
5. **Clear old tool results before summarising.** Protect a recent window and
   act only when the saving is large enough to justify breaking the prompt
   cache. Gemini CLI and opencode move the full output to storage and leave a
   reference. Evidence: OpenHands reports under half the per-turn cost at the
   same solve rate (54% vs 53%), and JetBrains' "The Complexity Trap"
   (NeurIPS 2025) found observation masking matches LLM summarisation at half
   the cost, with a hybrid 7–11% cheaper still.
6. **Summarise incrementally.** Feed the previous summary back and update it,
   instead of summarising a summary. Use fixed sections (goal, decisions,
   progress, next steps, critical facts), keep exact identifiers verbatim, show
   tool arguments and capped tool results, and treat history as untrusted data.
7. **Record compaction as an event and never delete the original.** OpenHands,
   ADK, pi, opencode, Codex and goa-ai all keep the source and rebuild the
   model view from "latest compaction + kept tail + newer items".
8. **Recover from overflow, boundedly.** Classify provider errors into one
   type, compact, retry once or twice. Cline uses only a model-free strategy for
   recovery, because recovery must not depend on another successful model call.
9. **Guard against useless compaction.** Reject summaries that are empty,
   truncated, or not smaller; stop after repeated failure (Gemini CLI, ADK,
   oh-my-pi). Never drop history on summariser failure (Aider's bug).
10. **Make it visible.** Start and end events with reason, tokens before and
    after, and summariser usage.

### 2.3 Ideas noted but not adopted first

- Speculative compaction started before the threshold, discarded if its cut
  goes stale (oh-my-pi, pi's durable mode).
- A verification pass over the draft summary (Gemini CLI); doubles cost.
- Switching to a larger-window model before compacting (oh-my-pi), and
  compacting with the previous model before downshifting (Codex).
- Pinned messages and "receipts" telling the model what was dropped
  (Pydantic AI harness).
- A context-usage hint shown to the model (Goose).
- Provider-native compaction blocks. They bind the transcript to one vendor and
  must never be the canonical record.

## 3. Additional findings in the runtime

Paths are under `runtime/agent/runtime/` unless stated.

| # | Finding | Evidence |
|---|---|---|
| F1 | Planners, not the runtime, issue model calls. The runtime decorates the client a planner obtains. Without interceptors the innermost layer is `recoveryCapturingClient`, with cache, tool policy and `tool_unavailable` applied by outer wrappers; with interceptors, `modelInterceptedClient` applies them after `BeforeModel`. Either way the innermost layer sees the final `model.Request` as given to the registered client; adapters still sanitise names and encode cache points. Planners can bypass the chain through `Runtime.ModelClient`. | `agent_context.go:68-108`, `model_interceptors.go:24-60`, `runtime_models.go:120` |
| F2 | Every planner activity input carries the full transcript and the full `ToolOutputs`, so tool result bytes cross the boundary twice per step. Temporal records every input, so history bytes grow with the square of run length, and each input is capped at 1 MB. | `api/types.go:357`, `workflow_helpers.go:381-397`, `activity_input_budget.go:21` |
| F3 | `PlanActivityOutput.Usage` aggregates all calls in the activity. The run log records per-call `usage` events with model identity. `TokenUsage.InputTokens` excludes cache read and write tokens. | `planner_events.go:110-122`, `model/model.go:370` |
| F4 | Planner messages are built and changed in many places: tool results and reminders (role system) appended; bookkeeping calls removed in place; await, clarification and interrupt messages appended; finalization adds synthetic tool results and a hint; recovery appends a correction inside the activity; the first `PlanStart` and the finalize paths build their own input; `PreChildValidator` receives the parent's messages. | `workflow_helpers.go:97-255`, `workflow_await_wait.go:78`, `workflow_clarification.go:178`, `workflow_interrupts.go:96`, `workflow_finalize.go:299,364`, `model_recovery.go:328`, `workflow.go:221,368`, `agent_tools_validation.go:44` |
| F5 | `RunOutput` returns no transcript, but `RunOutput.ToolEvents` carries every full tool result, so workflow state and the workflow result both hold all result bytes. A new run's history comes from `RunInput.Messages` or memory preload. | `api/types.go:201`, `workflow_finish.go:189-211`, `memory_preload.go` |
| F6 | `model.ProviderError` classifies failures by kind; context overflow is not one. Bedrock maps `model_context_window_exceeded` to `OutputLimited`, which recovery treats as "write a shorter answer" with tools disabled; Anthropic does not map it. | `model/provider_error.go`, `features/model/bedrock/client.go:893`, `model_recovery.go:247`, `features/model/anthropic/client.go:730` |
| F7 | No client exposes its context window or output limit. | `features/model/*` |
| F8 | Model-facing `load_artifact` and `load_memory`/`search_memory` tools exist; the latter read run transcripts through the memory projection. | `artifact_toolset.go`, `memory_toolset.go`, `memory_projection.go` |
| F9 | The run log is not a complete record. A `tool_result_received` payload over 1 MB is replaced with `{"truncated":true,...}`; any other event over 1 MB fails the run. Clarification answers, interrupt messages, reminders, recovery corrections, system prompts, tool definitions and memory preloads are not recorded; `prompt_rendered` has only ID and version. | `runtime_hook_helpers.go:34,79-105`, `workflow_clarification.go:191`, `workflow_interrupts.go:123` |
| F10 | Hook events are published from the workflow through activities, so their payloads sit in Temporal history, and each event adds history events. | `engine/temporal/workflow_context.go:196` |
| F11 | Under Temporal, the Bedrock client prepends the `ledger_messages` query result (the current turn's output only) below the runtime wrapper: a hidden message source. | `features/model/bedrock/client.go:288-301`, `runtime_models.go:142`, `workflow_turn.go:425` |
| F12 | No workflow versioning exists (`GetVersion` unused); the roadmap requires explicit migrations with fail-fast version checks. There is no `ContinueAsNew`. | `ROADMAP.md` non-goals |
| F13 | Content reaches workflow code without an activity: signals (clarification answers, await answers, resume messages, external tool results, typed inputs, confirmations), inline tools executed in workflow code, results created in workflow code (unknown tool, policy denial, cancellation, finalize synthetics, reminders), and child agent results adapted in the parent. | `workflow_await_wait.go:36-78`, `runtime_runs.go:289-361`, `tool_calls_dispatch.go:183-209`, `tool_calls_collect.go:228-253` |
| F14 | Registrations reference no models. Planners call `ModelClient(id)` with any ID and adapters resolve model classes, so the runtime cannot know an agent's models before it calls them. | `planner/planner.go:111` |
| F15 | The existing recovery loop resends the same messages with a correction, records only `OutputValidationError`, carries a rejected-output fingerprint, and spends `RemainingRecoveryTurns`. It cannot change the view. | `workflow_plan_activity.go:215-260`, `model_recovery.go:102-108`, `recovery_turns.go:10` |

## 4. Design

### 4.1 Guarantees

The design makes seven separate promises, labelled P1–P7 (section 1's G1–G10
are gaps in today's runtime). Each has its own acceptance proof; the
execution plan assigns each proof to a ticket.

| # | Guarantee | Acceptance proof |
|---|---|---|
| P1 | **Record (evidence preservation).** Every message, tool call, tool result and checkpoint, and every model request and response that passes through the runtime wrapper, including failed attempts and rejected summaries, is in the run store before anything refers to it. The run can be rebuilt from the store and the run log after Temporal history is gone, and every call is classified as adopted, abandoned or incomplete the same way whatever the crash point (4.5). Calls that bypass the wrapper are not recorded (4.13). Content outside the store, such as a document given by URI, is recorded as its reference only (4.10). | Reconstruction test: run a fixture, drop the workflow history, rebuild `RunRecord` from the store and run log, and compare it with the transcript observed live. Crash-injection tests at each window of the commit protocol (4.5) give the same classification. One fault test per content path (4.4). |
| P2 | **Payload.** The serialised input of every workflow step has a bound that depends on neither content size nor run length: a fixed part under 4 KB, plus about 40 bytes per distinct model the run has called (its rate and limits), plus about 400 bytes per index entry the step added and about 100 bytes per tool call an entry holds; the number of tool calls in one planner turn limits both (4.6). The bound covers the whole serialised input, not only the `ViewRef`: no input carries an accumulated history (4.7). The 1 MB envelope check stays as a fail-fast guard. P2 bounds payloads, not execution cost; 4.23 lists what still grows. | A run with 1,000 steps and batches of 50 parallel 100 KB results, and a run with 500 typed-await rounds of 10 KB answers, keep every serialised `PlanActivityInput` under 4 KB + 40 bytes × (models called) + 400 bytes × (entries added) + 100 bytes × (calls held); the envelope check still fails with a typed error above 1 MB. |
| P3 | **Fit.** The rendered checkpoint's text has a hard *byte* bound under the current budget, including a summary made under a larger one, and its non-text parts are bounded in number (4.10). Its *token* size is estimated, not guaranteed: an estimate is exact only for an adapter that counts tokens (4.8). When a model's limits are known and the request can be counted exactly or the estimate is calibrated, the pre-send guard stops requests well over budget. The guard is advisory: the provider's overflow error is authoritative, and only it can fail a step. After an overflow, recovery retries only with a request that is measurably smaller. | Unit tests of the pre-send guard and of recovery: no usage reported, unknown limits, planner-rewritten requests; a 16k-token summary made for a large model renders within the byte bound of an 8k-window model, mid-run and in a continued run. Formal model: `RecoveryShrinks` and `CheckpointBounded`. |
| P4 | **Exposure.** Compaction never shows a model anything that was not model-facing in the session it came from. A continued run does show earlier runs' model-facing tool results, which callers never pass back (4.16); it never shows anything else. Summariser input, recall output, checkpoints, previews and carried entries are all built from eligible evidence (4.4), which is chosen by what content is, never by its size: no `ServerData`, no injected argument values, no hidden bookkeeping calls, and no reasoning blocks outside the current run's tail. | One fixture proves both sides: a 5 MB result holds a planted fact in its middle and has a marker in its `ServerData`; markers also go in an injected field and a hidden bookkeeping call. After compacting with a summariser, recalling every ID, listing every handle and searching for the fact, the fact reaches the summariser and `find`, and no model request in the call record contains a marker. |
| P5 | **Discoverability.** Every model-visible message behind the checkpoint (user text, assistant text, tool calls, tool results) can be reached from what the checkpoint renders, by a finite sequence of `recall_context` calls starting at a pin, an index line, a receipt line or a handle. P5 is about access paths only. Whether the agent can afford the path *now* depends on its allowances (4.18), which are measured, not guaranteed (7.4). | With allowances disabled, a scripted agent reaches every evicted message of a 1,000-step run from the rendered checkpoint and the tool description alone, at a 200k and an 8k window, including assistant text with no tool call; the test records the number of calls needed at each window. |
| P6 | **Session.** For an agent with threshold compaction, a continued session starts with the previous run's final view, bounded by the continuation cap: the checkpoint, its pins and the tail after it, with every entry in the form it rendered in, or a smaller one when the tail exceeds the cap. The *view* is carried within the cap; everything it no longer shows stays *retrievable* from the earlier run's record (P1, P5). Only the run that holds the session's latest continuation can publish the next one (4.16). | A decision made after the last compaction of run 1 is in the first request of run 2; across three runs, pins, cleared results, checkpoint coverage and recalled evidence all resolve to the run that holds them; an oversized `KeepResultInContext` result cleared at seal stays a placeholder in runs 2 and 3 and is recalled in full from run 1; two overlapping root runs and a late-finishing older run publish at most one successor. |
| P7 | **Quality.** Compaction meets the non-inferiority margins of 7.4 against an uncompacted baseline: task success and evidence use no more than 5 points lower, unsupported claims no more than 2 points higher, at the gate's calibrated confidence. This is an eval result, not a structural property, and it gates turning threshold compaction on by default (7.4). | Release criteria (7.4). |

Also:

- Long runs and long sessions no longer fail on activity payload size, and
  context growth is compacted or recovered without per-agent tuning, on
  windows from 8k (local models) to 1M tokens. A step can still fail with
  `ContextBudgetExceeded` when the fixed overhead and the newest unit at its
  smallest do not fit (4.6), or with `ContextOverflowNoProgress` (4.15). Other
  costs still grow with run length, linearly in most cases and quadratically
  in the cases 4.23 lists; `ContinueAsNew` is a follow-up that P1 and P2 make
  possible.
- Compaction is computed once, recorded, and replayed.
- The cheapest sufficient technique runs first.
- Prompts and settings are declarable and overridable per run, so the eval
  tool can compare them (section 7).

Non-goals: provider-native compaction as the canonical record, speculative
background compaction, summary verification passes (section 2.3).

### 4.2 Architecture

Large content stops travelling through the workflow. Content is written to a
content-addressed **run store** by whoever produces it, before the workflow
receives its hash. The workflow keeps a small **transcript index**, persists
it to the store in **segments**, and decides the **view**: a constant-size
reference into the index that a **view loader** in the planner activity turns
into messages.

```
 producers (activities, runtime client, child finalizer) ── Put ──► run store ◄── Get ── view loader (planner activity)
          │ hash + small metadata                                  ▲     ▲                 ▲    │ Put index segment,
          ▼                                                        │     │                 │    │ receipt-only checkpoint
  workflow: transcript index ─► compaction decision ─► ViewRef ────┼─────┼─────────────────┘    ▼
                                   │ tier 1 inline (structure only)│     └──────────────────── run store
                                   └─► SummarizeContext activity ──┘ Put
  RunRecord reader (evals, show, recall, Opik export) ── Get ──► run store (index segments, content) + run log (index head)
```

One mechanism serves four needs: the full record (nothing is referenced before
it is stored), bounded step size, cheap compaction (changing the view moves no
bytes), and evals (one reader).

### 4.3 The run store

A store beside the run log with two parts. Content is addressed by hash:
`Put(scope, bytes) → Hash`, `Get(scope, hash)`; `Put` is idempotent. Call
records are keyed: `NextSeq(scope, runID) → Seq`, `PutCall(scope, CallRecord)`,
`ListCalls(scope, runID)`. The keyed part is how a reader finds the call
records of an activity attempt that failed or timed out, whose keys never
reached the workflow. `PutCall` only moves a record forward: it inserts
`started` idempotently (the same body again succeeds) and writes `completed`
or `failed` only over `started`. In-memory and Mongo implementations, matching
the run log; Mongo writes use majority write concern explicitly.

**Scope is the root run.** A run tree (a root run and its agent-as-tool
children) shares one scope, so a child can write its final output where its
parent will read it. Redaction, retention and deletion follow the run log,
keyed by root run (D7). A continued session copies the bounded content it
carries forward into the new root scope (4.16), so a loader only ever reads
its own scope.

**Authorization across runs.** The one exception is `recall_context`, which
can read an earlier root run of the same session. Neither the run log
(`runlog.Store` lists by run ID) nor the session store defines access checks,
so the run store cannot inherit any. The **recall handler in the runtime**
enforces them, and nothing else reads across scopes:

- the current run's session comes from the run context, never from tool
  arguments;
- a chain label resolves only by walking the current run's continuation chain
  (`Prev` links, 4.16), whose runs are root runs that published a
  continuation; the handler confirms with `session.Store.LoadRun` that each
  has the current session's `SessionID`. Child, rejected and superseded runs
  have no chain label;
- a receipt handle resolves only by walking the current checkpoint chain
  (4.18).

loom-mcp has no tenant model in the run log, session store or run store; tenant
isolation is the deployment's, by store partitioning, as for the run log
today. Recall adds no path across sessions, so it adds no path across
tenants. A deployment that needs a finer rule installs a `RecallAuthorizer`,
which the handler calls for each run it enters (D15).

### 4.4 Content paths

Every source of content, who writes it, and when. The rule: **content is in the
store before any workflow state or history refers to it.**

| Source | Writer | When | Workflow receives |
|---|---|---|---|
| Caller history (`RunInput.Messages`) | Run start activity | First activity of the run | Hashes (the input itself stays in history once, as today) |
| Memory preload (`PlanInput.PreloadedMemory`, `PreloadedMemoryEntries`) | Not indexed: the planner activity reads it from the memory store each step and hands it to the planner | — | Nothing; whatever the planner sends of it is in the call record's message sequence (4.13) |
| Assistant output and tool calls | Planner activity: the streamed transcript (text, thinking) and each tool call separately | Before returning | Transcript hash, tool call hashes, and tool call arguments inline (needed for dispatch), bounded by the existing output bounds. The index entry holds the transcript hash, the hashes of the *allowed* tool calls and the producing call's key; the loader assembles the assistant message (4.7) |
| Activity tool results | Tool activity: the eligible result, and separately the server-only parts (`ServerData`) | Before returning | Eligible hash, bounded model-facing preview, server-only hashes, metadata |
| External tool results, clarification and await answers, typed inputs, confirmations, resume messages | `Runtime.Provide*` / `ResumeRun` client methods | Before signalling | Hashes in the signal |
| Inline tool results | `RecordContent` activity called by the inline dispatch path | Before the result enters the index | Hash |
| Child agent final output | Child's finalization activity, into the shared root scope | Before the child completes | Hash in the child's `RunOutput` |
| Workflow-originated text (unknown tool, policy denial, cancellation, finalize synthetic results and hint, reminders, placeholders, hidden bookkeeping calls) | Nobody: these are **structural entries**. The index stores a kind and small, bounded parameters; the loader renders them with fixed templates. | — | Already in the index |
| Recovery corrections | Planner activity (framework-authored and bounded) | Before returning | Hash |
| Summary checkpoints (`CheckpointRecord` with a new summary) | `SummarizeContext` | Before returning | Record hash |
| Receipt-only checkpoints (threshold or overflow eviction; seal-time bounding; continuation) | The next planner activity's loader, from the previous record and the evicted index range it reads from stored segments (4.6); `SealRecord` when it bounds a continuation tail, and the run start activity when it seeds a continued run (4.16) | Before returning | Record hash; the workflow refers to the new record only after receiving it |
| Index segments | The planner activity's loader, from `ViewRef.Pending`; `SealRecord` on terminal paths (4.5) | Before returning | Segment hash, the new index head |
| Model requests and responses | Innermost runtime model wrapper, every attempt, keyed by `NextSeq` (4.3) | Request at send or stream open; response or error at completion or stream `Finalize`; the activity waits for pending writes before returning | Call keys in `PlanActivityOutput`; the keyed listing finds the rest |

Structural entries keep the record complete without a store write: the record
is the index plus the templates, which are versioned by
`WorkflowProtocolVersion` (4.22).

**Model access (P4).** The store holds more than any model may see, and what
a model may see is more than any one request can hold. Three layers are kept
apart:

- **Eligible evidence** (`MessageEntry.Hash`, `Calls`): everything a model
  may read, at full size. Eligibility depends on *what* the content is, never
  on its size.
- **Its bounded rendering:** what a given request shows of it: a view
  rendering, a preview, a placeholder, a page. Size limits apply here and only
  here.
- **Server-only evidence** (`MessageEntry.Record`): never shown to a model.

| Content | Eligible evidence | Rendered in a view | Server-only |
|---|---|---|---|
| Tool call | The arguments the model emitted | As emitted | The executed payload with `Inject` field values |
| Tool result | The canonical result, at full size | `ProjectToolResultContent` (`transcript/tool_result_content.go`), which replaces a result over 64 KiB with an omission object carrying the preview; or a placeholder after clearing | `ServerData` |
| Assistant message | Text and tool uses, minus calls hidden by a bookkeeping structural entry; reasoning blocks, rendered only inside the tail (4.6) | As assembled by the loader | Hidden calls stay in the record and are never rendered |
| User message, typed input | The message or answer | As given, or a pin preview or index line (4.9) | — |

The 64 KiB omission is a rendering, not an access rule. `SummarizeContext`
folds the full eligible result in chunks (4.12), and `recall_context` pages
it and `find` searches it (4.18), so a fact in the middle of a 5 MB result is
reachable. Typed codecs also read the eligible hash, so typed results are
never truncated (4.7).

Every model-facing path (the view loader, `SummarizeContext`'s input,
`recall_context`'s output, receipt lines, previews, the user index and
carried entries) reads eligible hashes through one access layer, which
never resolves `Record` hashes. Each path then applies its own size limit:
the view renderer's 64 KiB omission, the summariser's chunks, recall's pages,
`find`'s scan limit. Only `RunRecord` and evals read
server-only parts. Reasoning blocks are excluded from summariser input and
recall: they are opaque or provider-signed, and a model never needs an
earlier turn's reasoning to continue.

Signal payloads carry only hashes. A raw Temporal signal sent without
`Runtime.Provide*` therefore cannot carry content past the store, and the loader
fails fast on a hash the store does not hold.

### 4.5 The durable record

Content addressing stores bytes. It does not by itself preserve transcript
order, structural entries, which attempt was accepted, or the mapping from
message and tool use IDs to content. The index segments, the call record and
the run log do, under the commit protocol below.

**Index segments.** The workflow keeps the index in memory and persists it as
an append-only chain in the store:

```go
type IndexSegment struct {
    Prev    *Hash          // previous segment, nil for the first
    First   MessageID      // ID of Entries[0]
    Entries []MessageEntry
}
```

`ViewRef.Pending` carries the entries appended since the last stored segment,
which is one step's worth. The run start activity stores the seeded entries
(caller history or a continuation) as the first segments itself, so `Pending`
never holds them, however long the caller history is. The loader stores them as a segment before it
renders and returns the segment's hash, which becomes the workflow's
`IndexHead`; the `planner_step` run-log event records it. On every terminal
path (finish, failure, cancellation) a `SealRecord` activity stores the
remaining entries and writes `run_sealed{IndexHead}`. A run terminated without
running workflow code is unsealed: its index is complete up to the last
planner step, and the run log says so.

**Entries never change.** Segments are immutable, so nothing edits an entry
after it is indexed. Hiding successful bookkeeping calls, which today edits
earlier assistant messages (`hideSuccessfulBookkeepingCallsFromPlanner`,
`workflow_helpers.go:226`), becomes a later structural entry that names the
hidden tool use IDs; the loader omits those calls when it assembles the
assistant message, and the record keeps them.

**Attempts and commits.** The store holds every attempt's output. A retried
planner or tool activity can store different bytes under a different hash, and
the call record keeps every model call. Only content that an index entry
references is part of the transcript: the transcript is the index, not the contents of the
store. Each assistant entry names the call that produced it
(`MessageEntry.Call`), so a reader can tell the adopted attempt from the
others. Because `Put` is idempotent, writing the same bytes twice is
harmless. Retries are not assumed to produce the same bytes, and nothing
depends on that.

**Commit protocol.** Each record has one writer, a defined incomplete state,
and one authoritative source. Writes are ordered so that a crash at any point
leaves a state the reader classifies the same way.

| Record | Write order | Authoritative source | After a crash in between |
|---|---|---|---|
| Call record (4.13) | `PutCall(started)` with the request, *then* send; `PutCall(completed)` or `PutCall(failed)` with the response or error. A failed `started` write fails the call unsent. | The keyed record | `started` with no completion is **incomplete**: the response, if the provider sent one, is lost, and usage is unknown. Reported, never silently dropped. |
| Content and checkpoint records | `Put` before the hash is returned | The store | An unreferenced hash is an orphan, never part of the transcript. |
| Index segment | The loader stores it, then returns its hash; the workflow adopts the output and appends `planner_step{IndexHead, ViewRef}` | The latest `planner_step` (or `run_sealed`) | A stored segment no event names is an orphan. A retry stores the same segment: segments are built from workflow state, so the bytes and hash repeat. |
| Seal | `SealRecord` stores the last segment, then appends `run_sealed{IndexHead}` | `run_sealed` | The run log's append is idempotent on `(run_id, event_key)` (`runlog.go`) and compares the whole event, timestamp included, so the workflow fixes the event key and timestamp in `SealRecord`'s input; a retried seal then writes the same event once. Without it, the run is unsealed (4.20). |
| Continuation | `SealRecord` appends `run_sealed` first, then `Put`s the carried content and calls `AdvanceContinuation`, which stores the continuation and advances the head in one atomic step, so a published continuation always comes from a sealed run; a run sealed without one leaves the head at its predecessor | The session's continuations, keyed by publishing run | A retry gets *already published* if the first attempt advanced the head, so a lost acknowledgement is never reported as superseded (4.16). |
| Model presentation | The planner activity commits `assistant_presentation_committed`, carrying the call key, when the planner returns successfully | What the user was shown, not the transcript | See below. |

**Record events are canonical.** `planner_step`, `run_sealed`,
`context_compacted`, `context_compaction_skipped` and `context_overflow`
decide the index head and which calls were adopted, so they skip `BeforeEvent`
interceptors, as the presentation commit already does
(`hook_activity.go:49`). An interceptor that dropped or rewrote one would
change what `RunRecord` reports as the transcript. The continuation events
`continuation_started`, `continuation_published` and `continuation_superseded`
also skip `BeforeEvent`: they record predecessor selection and head publication.

**Adopted, abandoned, incomplete.** Adoption is per activity attempt, not
per call. Every activity that calls models (planner, `SummarizeContext`)
returns the keys of all its calls, and the workflow records them in the event
for that output: `planner_step`, `context_compacted` or
`context_compaction_skipped`, or `context_overflow`. A call is **adopted**
when such an event names it, whatever its own outcome (a planner's auxiliary
calls, every chunk call of a summary, an overflowing request, a summary that
validation rejected); its record keeps that outcome. It is **abandoned** when
its record is completed or failed but no event names it (an attempt that was
retried or timed out). It is **incomplete** when it was started and never
completed. Separately, `MessageEntry.Call` marks the call whose response
became an assistant entry, and `CheckpointRecord.Calls` the summariser calls
behind a summary. One step's call keys are bounded by the calls of one
attempt. `RunRecord` derives every class from the store and the run log
alone, so every crash point yields the same answer.

**Presentations are not the transcript.** Today the planner activity commits
a model presentation before the workflow adopts its output
(`planner_events.go:135`). That stays, because users must see output
promptly, but the two are kept apart. An activity that returns
`ContextOverflow` finishes its presentations with `accepted=false` and commits
none (4.15). An activity whose presentation committed but whose completion
was lost is retried, and both presentations are in the run log. `RunRecord`
reports each committed presentation whose call key is not adopted as
**presented, not adopted**, so an eval sees exactly what the user saw and
what the transcript holds.

**Reconstruction.** `RunRecord` reads the index head from `run_sealed` or the
latest `planner_step` event, walks the segments back, resolves hashes,
renders structural entries with the templates of the recorded
`WorkflowProtocolVersion`, and classifies every call record listed by
`ListCalls`. It needs no Temporal history.

**Export.** `RunRecord` also writes a self-contained export of a run tree: the
index entries, checkpoint records, call records with their requests rebuilt,
content by hash, and the run-log events it read. A reader that has neither the
run store nor the renderer of an older `WorkflowProtocolVersion`, such as the
eval plan's experiment store (T13), reads the export.

### 4.6 Transcript index and view

```go
type MessageEntry struct {
    ID         MessageID          // sequence within the run
    Hash       Hash               // content in the run store; zero for structural entries
    Calls      []Hash             // assistant entries: stored tool calls that were allowed
    Call       *CallKey           // assistant entries: the model call that produced it (4.13)
    Structural *StructuralEntry   // kind + bounded parameters, rendered by the loader
    Role       model.ConversationRole
    ToolUseIDs []string
    Bytes      int
    NonText    int                // estimated tokens of image and document parts (4.10)
    Record     []Hash             // server-only parts (4.4): never read by a model-facing path
    Keep       bool               // result of a KeepResultInContext tool
    Origin     *Origin            // set on entries seeded from an earlier run (4.16)
    Chain      Hash               // hash(previous Chain, Hash, Calls or structural encoding)
}

type Origin struct {
    Scope   string                // root run: the store scope that holds the content
    Run     string                // the run whose index holds the original; equals Scope for carried entries, which come from a root run's view
    Pos     int                   // that run's position in the continuation chain (rN); orders and labels without a chain walk
    ID      MessageID             // the original's ID in that run's index
    Form    CarriedForm           // full, preview or placeholder (4.16)
}

type ViewRef struct {
    IndexHead      Hash           // latest stored index segment
    Pending        []MessageEntry // entries since IndexHead: one step's worth
    Checkpoint     *Hash          // a stored CheckpointRecord, nil before the first
    TailFrom       MessageID      // first entry of the tail
    ClearedThrough MessageID      // results at or before this render as placeholders, unless Keep
    Pinned         []MessageID    // pinned user messages, at most MaxPins (4.9)
    Budget         RenderBudget   // token budgets for checkpoint parts and previews (4.17)
}

type CheckpointRecord struct {
    Prev        *Hash             // the record this one extends, in this run
    From        *RunCheckpoint    // first record of a continued run: the previous run's latest record
    Earlier     EarlierReceipt    // behind From: counts, the first and last run positions, and the carried user index lines; bounded, and rendered from this run's scope
    Summary     *Hash             // latest summary text, nil if only receipts so far
    SummaryRun  string            // the run the summary was written in: bare citations refer to it
    SummaryPos  int               // recorded chain position of SummaryRun; copied with the summary
    CitedUsers  []IndexLine       // index lines of the user messages the summary cites, each with its Origin; within KeepUserTokens
    Receipt     []ReceiptLine     // evicted tool calls (tool, bounded argument preview, unsummarised ranges) and assistant messages with text (first line) added by this record, each with its Origin; the full receipt is the chain through Prev
    Through     MessageID         // last entry behind the checkpoint
    Chain       Hash              // Chain value at Through: what the record covers
    Fingerprint Fingerprint       // ContextConfigFingerprint (4.17)
    Prompt      *prompt.PromptRef // prompt that produced Summary
    Calls       []CallKey         // summariser calls that produced Summary, at most MaxSummaryChunks
}
```

K12 stores the following receipt fields (continuation origins are K18):

```go
type ByteRange struct { From, Through int }
type ReceiptLine struct {
    From, Through MessageID
    Kind        string       // "call" or "assistant"
    Tool        string
    ToolUseID   string
    Text        string       // eligible argument preview (160 bytes) or assistant first line
    Unsummarised []ByteRange  // empty until K15
}
```

Receipt-only records preserve `Summary`, `SummaryRun`, `SummaryPos` and `CitedUsers`; their
`Prev`, `Through` and `Chain` name the exact newly evicted interval. Deterministic
JSON v2 is stored before the returned hash can enter a planner-step record.

**`ViewRef` has constant size** apart from `Pending`, which holds one step's
entries, and `Pinned`, which has at most `MaxPins` IDs. A step adds at most
one entry per tool call of one planner turn plus a few structural entries;
the planner output bounds, and `WithPerTurnMaxToolCalls` when set, limit those
calls. The loader reads the
index from the stored segments; segments are immutable, so it can cache them
per worker. This gives P2 without copying the view into every input, and the
1 MB envelope check stays as a fail-fast guard.

When `TailFrom` is past the checkpoint's `Through + 1` and no summary record
has been adopted for that range, the loader stores a receipt-only record: the
previous record plus receipt lines for the evicted entries, which it reads
from the stored index. It returns the record's hash, which the workflow
adopts. A receipt-only record keeps the previous summary. Each record stores
only the lines it adds and points to the previous record, so storage grows
linearly with evictions.

**References are qualified by run.** Every receipt line, placeholder and range
reference carries its root run, and an entry seeded from an earlier run
renders with its `Origin`, not its new ID. References from the current run
render as plain IDs; those from an earlier run of the session render with a
chain label, its position in the continuation chain (`r1` for the first
publishing run, `r2` for the next), so a recall never resolves against the
wrong index (4.16). The position is stored in `Origin.Pos` and in each
`Continuation`, so the loader labels and orders references from its own scope
without walking the chain; only the recall handler walks it, to resolve a
label to a run.

`Chain` is a running hash over the canonical sequence. A checkpoint records
the `Chain` value of the last entry it covers, so a reader of the record can
verify what it covers. Session continuity matches on caller-visible messages
instead (4.16).

**The checkpoint message.** The checkpoint renders as one user-role message,
because the providers require the conversation to start with one. Its
sections are labelled so that generated or tool-derived text never reads as a
user instruction:

```
[Context checkpoint. Messages 1–212 are not shown. This message is written by
the framework; it is not from the user.]

User messages, verbatim from the record, oldest first. A later message
overrides an earlier one, whether it is shown in full or by its first line:
  [message 1] Deploy the fix to production and …              (in full)
  … 31 user messages in 2–48 are not listed and may override earlier ones [users rc-02d9e5f6]
  [message 57, first line] Use staging only, not production.
  [message 98, first line] Also skip the migration step.
  [message 140] …                                             (in full)

Summary of earlier history, generated by a model. It can be incomplete or
wrong, and the user's messages above and the conversation below take
precedence over it:
  …

Behind this checkpoint (framework record):
  toolu_201 search_orders(customer_id="c-42", …)
  message 187, assistant: "Using the v2 endpoint, because v1 drops refunds…"
  … 37 earlier calls and 12 assistant messages in 3–180 [receipt rc-7f3a6b8c]
  … 112 calls and 40 assistant messages in earlier runs r1–r2 [receipt rc-c41e5a6b]
```

The record lists every evicted tool call and every evicted assistant message
that has text, by its first line, so a decision made in prose without a tool
call is discoverable like a call (P5).

The bracketed handles name a part of the stored receipt (4.18). Handles and
checkpoint IDs are qualified by run like every other reference: `r1:ck-91b2c3d4`
names a record in run 1. Each identifier uses eight hexadecimal digits:
`Handle(part, recordID)` is `rc-` plus the first four SHA-256 bytes of
`part || recordID`; `CheckpointID(recordID)` is `ck-` plus the first four
SHA-256 bytes of `recordID`. A handle is opaque (4.18). A summary
copied into a later run keeps naming the record in the run that made it.

A continued session (4.16) adds one header line, `Continued from run r2.`, to
this message; it is not a separate entry.

The user-message section holds the pins and the user index (4.9). Precedence
is chronological among the user's own words, by origin across runs (4.9), so
a correction listed by its first line overrides a pinned instruction it
follows; the summary never
outranks a user message.

**Bound.** The loader renders the checkpoint within a hard *byte* bound,
`CheckpointTokens` × 2 bytes, where `CheckpointTokens` is
`KeepUserTokens + MaxSummaryTokens + ReceiptTokens + FramingTokens`, whatever
the record holds. Its token size is an estimate, not a guarantee: a
tokenizer can spend more than one token per two bytes (short, digit- or
symbol-heavy text often does), and only an adapter that counts tokens (4.8)
makes it exact. The bound is the *current* one: the loader
computes every budget from the `RenderBudget` in the `ViewRef`, which the
workflow recomputes whenever the run's smallest `B` falls (4.8), not from the
budgets in force when the record was made. Every token budget is
converted to bytes at a fixed 2 bytes per token, not at a calibrated rate, so
a rendering changes only when the budget does, which keeps it byte-stable
(4.21). The conversion is a sizing convention; the guard and overflow
recovery handle the cases where it underestimates.

The pure renderer measures framing bytes exactly before allocating section
space. Any framing deficit beyond `FramingTokens × 2` is taken from the
receipt allowance above its two compact-line floor first, then the summary
allowance above its compact closing-line floor when a summary exists. User
text keeps its allowance, with at least a compact users line. It never clips
assembled text to establish the bound. When full aggregates do not fit, use
`… [receipt rc-xxxxxxxx]`, `… [earlier rc-xxxxxxxx]` and
`… [users rc-xxxxxxxx]`; a shortened summary uses
`[full text: checkpoint rN:ck-xxxxxxxx]` when its full closing line does not fit.
`MinCheckpointBytes(b)` is
`max(512, max(24, 2 × KeepUserTokens) + Fmax + Rmin + Smin)` bytes.
On the supported 64-bit platform, `Fmax=392`, `Rmin=52` and `Smin=56`.
Tiny nominal components borrow available capacity for these floors. At or
above the threshold, every omitted item keeps its discovery reference.
Smaller total budgets return `ErrCheckpointBudget`; K17 rejects them during
override validation. At default B=2,900 budgets, the minimum is 790 bytes,
below the 1,444-byte maximum.
The default `FramingTokens` remains 200.

- A summary within the current `MaxSummaryTokens` renders whole. A larger one,
  made under a larger budget, renders as a **bounded summary**: a pure
  function of the text and the budget that keeps the Markdown headings in
  order while they fit (a budget smaller than the headings renders only the
  closing line), gives each kept section an equal share of the budget (a short section passes
  what it does not use to the sections after it), cuts each section at a
  line boundary within its share, and ends with
  `[summary shortened from 16,000 tokens; full text: checkpoint r1:ck-91b2c3d4]`.
  The stored summary is never changed, and `recall_context` pages its full
  text. A summary without headings is one section. The next summary replaces
  it under the current budget (4.12).
- Pins and the user index are rendered within the current `KeepUserTokens`
  by the rule of 4.9; a pin that no longer fits becomes an index line.
- Message 1, when larger than a quarter of `KeepUserTokens`, renders as a
  head-and-tail preview with its ID; other pins are whole messages (4.9).
- The receipt renders its newest lines up to `ReceiptTokens` and collapses all
  older lines into at most two lines: one for the current run's earlier calls
  and one for earlier runs of the session. Each collapsed line carries a
  receipt handle. The full receipt stays in the stored records, and
  `recall_context` pages through it from the handle (4.18).

**No rendered text names `recall_context`.** A retained recall call uses
`toolu_x (context lookup: <bounded args>)`; its full Tool remains in the record.
Compaction-enabled registration rejects application tool identifiers or
provider names containing the reserved name. Placeholders, pin previews and
receipt lines give IDs and ranges only. How to use them is in `recall_context`'s own
description (4.18), which the model sees only while the tool is advertised
and callable, so nothing in the view recommends a tool that is disabled,
exhausted or unavailable in a tools-disabled turn.

**Precedence.** When the parts of a request compete, they are kept in this
order: the fixed overhead (system prompt, tool definitions); the newest unit;
the checkpoint, within its bound; then older tail units, newest first. When
the newest unit alone does not fit, its tool results render as previews that
shrink evenly, down to placeholders. A view in that state is minimal. If the
provider still reports an overflow for a minimal view, the step fails with
`ContextBudgetExceeded`, naming the unit's first message ID and the estimated
fixed overhead, which covers a system prompt and tools that alone exceed the
window.

**View grammar.** Every rendered view satisfies each provider's transcript
rules. K11 supplies pure `transcript.ValidateView` with `GrammarBedrock`
(including its thinking flag) and `GrammarAnthropic`, plus `AssembleView`.
Validation ignores system messages, requires a first user message and a
post-checkpoint assistant, enforces Bedrock alternation, and checks calls
and results in both directions. K12 wires these helpers into loaded views;
the existing `ValidateBedrock` call remains unchanged until then:

```
view       := system* (checkpoint tail | history)
checkpoint := the checkpoint message above
tail       := starts with an assistant message whose thinking block, if any,
              is intact; roles alternate as the provider requires
history    := the run from its first user message, before any compaction;
              roles alternate as the provider requires
```

Thinking blocks and provider-opaque reasoning items (OpenAI encrypted
reasoning) before the tail are dropped; those inside the tail are kept.
Thinking never prevents a cut.

### 4.7 Workflow-side consumers of content

Workflow code that reads message bytes today, and what it does instead:

| Consumer | Decision |
|---|---|
| Tool dispatch (`PlanResult.ToolCalls` arguments) | Unchanged: arguments stay inline, bounded by output bounds. |
| `transcript.ValidateBedrock` before planning | Moves to the view loader, on the loaded view. |
| Bookkeeping-call removal and transcript dedup | Index structure only: a structural entry naming the hidden tool use IDs (4.5). |
| `prepareFinalizationMessages` | Structural entries; the loader renders them. |
| `ProjectToolResultContent` | Moves to the loader. |
| `PreChildValidator`, `buildAgentChildRequest` | `PreChildValidator` receives the tool call and a loader for the parent's view, and runs in the child-dispatch activity. Public API change (D11). |
| Completion tool and `FinalToolResult` finishing | A finish activity loads the typed result by hash and decodes it with the generated codec; the final result keeps today's 1 MB bound. |
| `RunOutput.ToolEvents` | A stored list referenced by one hash, plus a count; the client resolves events and full results through `RunRecord`. `RunOutput` no longer grows with the number of calls. |
| Assistant assembly (`recordAssistantTurn`, `PlanActivityOutput.Transcript`, the run-loop `Ledger`) | The index entry holds the stored transcript hash and the hashes of the allowed tool calls (`MessageEntry.Calls`); the loader assembles the message from them. The `Ledger` and the `ledger_messages` query handler are removed (K7). |
| Memory projection (`memory_projection.go`, `load_memory`, `search_memory`) | Sees previews and hashes instead of result bytes; loads full results through `RunRecord` where it needs them. |
| Accumulated planner histories (`PlanResumeInput.ToolOutputs` and `TypedInputs`, held in workflow state and copied into every `PlanActivityInput` by `buildNextResumeRequest`, `workflow_helpers.go:395`) | Removed from workflow state and from `PlanActivityInput`. Typed inputs become index entries when they arrive, like tool results. The planner activity builds both slices from the stored index through the loader, so the planner API keeps its shape while no activity input carries them. `ToolOutput` holds the hash and preview (D4). |

**No accumulated history crosses a boundary.** Every history that grows with
the run (messages, tool outputs, typed inputs, tool events) lives in the
index and the store; activity inputs carry the `ViewRef` and one step's new
entries. P2's test measures the whole serialised input.

**Typed results are never truncated.** Previews are model-facing only.
`ToolOutputs`, typed codecs, completion tools and agent aggregation load full
results by hash, so the roadmap rule against weakening generated codecs holds.

### 4.8 Measurement, the trigger and the pre-send guard

**Facts per request.** The model wrapper reports one `RequestFacts` per model
request in `PlanActivityOutput`:

```go
type RequestFacts struct {
    LimitsInvalid bool `json:"limits_invalid,omitzero"` // invalid merged limits override, distinct from unknown limits
    Call          CallKey // (RunID, Seq, Attempt), see 4.13
    Model         string
    ContextWindow int     // from the adapter's ContextLimits (4.14); 0 if unknown
    OutputReserve int     // MaxTokens, else DefaultMaxTokens, else min(MaxOutputTokens, ContextWindow / 4)
    ViewBearing   bool    // the request contained messages whose hashes are in the view
    ViewTextBytes int     // request text bytes matching the view; omitzero
    ViewNonText   int     // request non-text tokens matching the view; omitzero
    InputTokens   int     // input + cache read + cache write, provider-reported; 0 if not reported
    TextBytes     int     // all text the request carried: system, tools, messages
    FixedBytes    int     // system prompt and tool definitions
    NonText       int     // estimated tokens of image and document parts (4.10)
    Outcome       RequestOutcome // ok, overflow, pre-send rejected, other error
    RecoveryToolBytes int `json:"recovery_tool_bytes,omitzero"` // tool definitions added only after view shortening; excluded from failed TextBytes/FixedBytes
}
```

`ViewBearing` is computed by hashing each request message's canonical role and
parts (excluding `Meta` and cache points) against the view, so it does not
depend on planners preserving `Message.Meta`. This canonical hash serves
measurement only; the request record matches full encodings (4.13). Each
`RequestFacts` is also written with its call record's completion, so trigger
and guard decisions can be replayed after Temporal history is gone (7.1). If
every call in an activity is non-view-bearing (a planner that rewrites
messages), the runtime emits `context_unmeasured` once per run.

**Output reserve.** Use the request's positive `MaxTokens`, otherwise the
adapter's configured `ContextLimits.DefaultMaxTokens`, otherwise
`min(MaxOutputTokens, ContextWindow / 4)`. This accounts for adapters that send
a configured output cap even when the request leaves `MaxTokens` unset.

**Budget.** For a model with known limits, `B(m) = ContextWindow − OutputReserve`.
Budgets, like rates, are kept per model; settings that are fractions of B use
the smallest `B(m)` among the view-bearing models seen in the run. Models are
learned from their facts, so the runtime needs no model list (F14). When that
B is below 2,900 tokens, the resident budgets cannot fit (4.17): threshold
compaction is off, overflow recovery stays on, and `context_budget_too_small`
is emitted once per run.

**Budgets can shrink.** A model with a smaller B can appear mid-run (a model
switch, a fallback) or in a continued run. The first facts for it, including
an overflow's, lower the smallest B; the workflow then recomputes
`ViewRef.Budget`, and every later load renders under it, including a
checkpoint made under the old one (4.6). Nothing already stored changes.

**Estimate.** The estimate is a calibrated rate times the bytes the next
request will carry, never a difference of measurements, so it cannot go
negative:

```
rate(m)  = clamp((InputTokens − NonText) / TextBytes, 1/6, 1/2)
           from the last request to model m that reported usage; 1/3 before any
estimate(m) = rate(m) × (FixedBytes + rendered view text bytes) + non-text tokens in view
```

The pure `runtime/agent/contextmeasure` package uses `Rate{Tokens, Bytes}`
and integer ceiling arithmetic. Negative byte/token contributions are treated
as zero; an unrepresentable estimate saturates at the largest `int`, rather
than wrapping. Binary document costs round up to a whole token. Canonical
measurement hashes preserve JSON numeric tokens and sort object keys; metadata
and cache points do not affect view matching.

Rates are kept per model, because tokenizers differ; the activity input
carries the rate of every model seen in the run.

Non-text parts are measured by 4.10's rule (`ImageTokens` is 1,500 unless the
adapter reports a figure). They are subtracted before calibrating, so they are
not counted twice. The rendered view bytes include the checkpoint at its
rendered size.

**Exact counts.** The model package already has an optional capability,
`model.TokenCounter` (`CountTokens(ctx, *Request) (TokenCount, error)`, in
`model/model.go`), whose result reports `Exact` when the count is
provider-native. Having the interface proves nothing; the guard trusts a
count only when that call returns `Exact = true`, and treats any other count
as an estimate. The counter excludes replayed thinking blocks, so the guard
adds them at the estimate rate. With an exact count and known limits, the
guard is on even before the rate is calibrated (below), and the token budget
is exact for that request apart from thinking blocks. Counting can cost a
round trip to the provider, so the guard counts only requests whose estimate
is above `B × TriggerAt`, near the limit, and records the count's latency in
the call record. The trigger keeps the estimate, because it runs in workflow
code and a count needs the rendered request.

The measurement-only `GuardTokens` helper accepts a rational trigger fraction
and `GuardBudget{Tokens, ImageTokens}` so its fallback estimate honors the
adapter's image cost. `GuardCount.GuardOn` reports eligibility from a positive
known budget and an exact count or calibrated rate; the helper never rejects a
request. Before calibration, count eligibility uses the upper clamp rate (1/2),
so an exact-capable counter is consulted even when the default estimate would
miss the trigger. This affects whether counting runs, not the fallback estimate.
K14 wires rejection after persisting the started call. Without a counter, every token figure
in this design is an estimate and the byte bounds are the only hard ones (P3).

The estimate is a heuristic. An overestimate causes an early compaction; an
underestimate causes an overflow that recovery handles (4.15). It never fails
a run on its own: a run fails only on a provider overflow (4.15).

**Trigger.** Before every planner step, the workflow compacts when, for any
view-bearing model seen in the run, `estimate(m) > B(m) × TriggerAt`, so long
autonomous turns are compacted. The
estimate is computed from the current view, so it resets after every
compaction. Before the first request there is no budget; the first step sends
the full caller history.

**No-progress guard.** If the estimate after compaction still exceeds the
trigger (overhead, the checkpoint and the newest unit alone are too large),
the trigger does not fire again until the view grows by `MinSavingTokens`.

**Pre-send guard.** A planner activity can make several model calls, switch
models, change tools, or rewrite its messages, so a check before the step
does not cover every request. The innermost wrapper therefore checks each
request before sending it: it estimates the request with the rate from the
activity input and that model's limits. If the estimate exceeds that model's
`B` by `GuardMargin` (default 10%), the wrapper does not send. It fails the call with `ErrContextBudget`,
records it in the call record, and marks the activity. The planner activity
then returns `ContextOverflow` with the facts, whatever the planner did with
the error; the runtime discards the planner's result.

The guard is advisory and never fails a step. It is off for a model whose
limits are unknown. With known limits it is on when the request can be
counted exactly, or when the rate is calibrated; it is off for an estimate
from an uncalibrated rate (no request to that model in this run has reported
usage, as on the first request or after a switch to another model). Guard rejections do not spend the overflow budget (4.15); they
have their own budget of one per step. When that is spent, when the rejected
request is not view-bearing, or when recovery cannot reach its target, the
guard is off for the rest of the step and the request goes to the provider,
which decides.

### 4.9 Units, cuts and pins

A pure function groups index entries into atomic units: an assistant message
with all its tool calls and every result for them (parallel batches
together); a reminder with the results it annotates; an open await,
confirmation or clarification and everything after it; a resolved
confirmation with its decision.

Cuts fall between units anywhere in the run. The tail keeps the newest units
up to `KeepRecentTokens`; the newest unit is always kept, within the
precedence rule of 4.6.

**User messages behind the checkpoint** render in two forms within
`KeepUserTokens`:

- **Pins**, within half of `KeepUserTokens` and `MaxPins`: the first user
  message (the task statement, as a preview if it is larger than a quarter of
  `KeepUserTokens`), then the newest user messages, newest first, stopping at
  the first one that does not fit, so the pinned run has no gaps.
- **The user index**, in the rest of `KeepUserTokens`: other user messages
  behind the checkpoint, one line each with its number and its first
  `UserIndexLineBytes` bytes. Messages the current summary cites by number go
  first, then the rest newest first. What does not fit collapses into one
  line with a handle that `recall_context` lists (4.18).

Both render oldest first. **Precedence is chronological among the user's own
words.** Messages are ordered across a session by their origin: first by the
run's position in the continuation chain (4.16), with the current run last,
then by the message's ID in that run. So `r2 message 5` comes after `r1 message 57`, although 5 < 57; an
entry seeded into a later run keeps the position of its origin, not its new
ID. Every user message in the checkpoint renders with its qualified origin
(`[r1 message 57]`, or `[message 5]` in the current run), in that order.

Every user text in the checkpoint, pinned or indexed, is taken from the
record, never from the summary. A later user message overrides an earlier one
whichever form it renders in. The summary has no precedence of its own: it
cannot put words in the user's mouth, because a citation only selects a
message for the index, and the framework renders that message from the
record.

**Citations are qualified references.** A summariser sees messages with the
labels the view gives them and cites them the same way: `[message 57]` for
its own run, `[r1 message 57]` for an earlier one. `CheckpointRecord.SummaryRun`
names the run the summary was written in, so a bare citation always means a
message of that run, including after the summary is copied into run 2 or 3,
where the renderer prints it qualified. The same rule applies wherever a
summary is shown to a model, including `SummarizeContext`'s own input: the
previous summary reaches the summariser with every bare citation rendered
qualified by its `SummaryRun` when that run is not the current one, so a
summariser that repeats a citation from the previous summary repeats the
qualified form, and never turns `[message 5]` of run 1 into a bare citation
of run 2's message 5. Validation (4.12) resolves each
citation to an origin and checks that it is a user message behind the
checkpoint: from the run's own index, or, for an earlier run, from the
carried pins and index lines, else by reading that run's index through the
recall handler (4.3). The index line of every cited message is stored in the
new record's `CitedUsers`, so the loader renders citations from its own scope
and never re-resolves an old citation.

So when message 1 says "use production" and message 57 says "use staging
only", message 57 stays visible as an index line after it leaves the pins,
and wins over the pinned message 1 because it is later. The summary helps where the
index is small: by citing message 57, it keeps that line ahead of newer
messages that do not matter. Two cases remain:

- The index line holds only the message's first bytes, so a correction later
  in a long message is cut. The line shows its size, and recall reads the
  rest.
- At small windows the index is small: at an 8k window, `KeepUserTokens` is
  about 300 tokens, so the user section holds message 1's preview and one or
  two index lines. A correction that is neither cited nor among the newest
  collapses into the "not listed, and may override earlier messages" line.

Both are eval scenarios (7.4). The goal and constraints come from the user's
words, not from a paraphrase that drifts.

### 4.10 Non-text parts

Messages can carry images (`ImagePart`, user messages only) and documents
(`DocumentPart`: bytes such as a PDF, text, chunks, or a URI). Every
operation that is specified for text says what it does with them:

| Operation | Image | Document: bytes | Document: text or chunks | Document: URI |
|---|---|---|---|---|
| Record (P1) | Stored by hash | Stored by hash | Stored by hash | The URI only; the content is outside the store and is not preserved |
| Measure (4.8) | `ImageTokens` (adapter figure, else 1,500) | Adapter figure, else bytes at 1/2 token per byte | As text | Adapter figure, else `ImageTokens` |
| Pin | Whole while it fits the non-text allowance below; else a placeholder naming the message, part, format and size | Same as image | As text: a head-and-tail preview | Whole (it is small) |
| Tail | Whole; the tail is measured in tokens, including these parts (4.8) | Same | As text | Whole |
| Clear (tier 1, 4.11) | Placeholder | Placeholder | Placeholder | Kept |
| Summarise (4.12) | Passed as an image when the summariser model accepts images; else a placeholder, and the receipt line says "not summarised: image" | Same, for documents | Folded in chunks as text | Passed when the summariser's adapter accepts that scheme; else "not summarised" |
| Recall (4.18) | Returned as the part in a multimodal tool result (K22) when the provider accepts one; else `recall_unsupported_part` with format and size | Same | Paged as text | Returned as the part in a multimodal tool result (K22) when supported |
| Carry to the next run (4.16) | Full when it fits the cap; else placeholder | Same | Preview | Whole |

An unsupported summary part adds a stored receipt line with `Kind: part`,
`MessageID`, `Part`, `Format` and `Bytes`. It renders `not summarised: image` or
`not summarised: document`; the placeholder and line preserve the original identity.

**Non-text parts in the checkpoint.** P3's byte bound covers the
checkpoint's text. Non-text parts in pins have their own allowance: at most
`MaxPinnedParts` (2) parts, whose estimated tokens fit a quarter of
`KeepUserTokens`; a larger or further part renders as a placeholder. So the
checkpoint stays bounded in bytes of text, in number of parts, and in
estimated tokens of parts.

**Recall needs multimodal tool results.** K22a adds runtime-owned image and
document attachments to `model.ToolResultPart.Parts`. Anthropic and Bedrock
Claude encode them; other adapters reject them before provider I/O. Capability
forwarding, byte limits and non-text measurement landed in K22a under workflow
protocol 17; the current workflow protocol is 18. Ordinary tools still return JSON in `Content`.

K22b implements attachment reads, support folding, result blobs and continuation
part dropping under protocol 18. See section 4.18. K22a retained protocol 17.

**Default boundary.** A placeholder makes an evicted part discoverable, not
usable. So the default is decided per provider configuration, not globally:

- A configuration whose adapter supports multimodal tool results (after K22)
  gets threshold compaction by default when its release suite passes,
  including the multimodal scenario.
- A configuration whose adapter does not support them, but accepts images or
  documents in user messages, gets threshold compaction by default only for
  runs without non-text parts. In a run that has one, threshold compaction
  stays off for the run and `context_nontext_resident` is emitted once:
  non-text parts stay in the view, and only overflow recovery can evict them,
  as it can evict anything today. An agent that opts in explicitly accepts
  the loss of model access, which its configuration states.
- Before K22 no configuration supports multimodal tool results, so every
  configuration follows the second rule.

The multimodal release scenario runs for each provider configuration that
would receive the default (7.4). On a configuration under the second rule it
does not exercise eviction; it checks that the run completes with its parts
resident and that the non-text rule costs no success against the baseline.

Nothing is silently dropped: every part that is not shown, summarised or
returnable is named by a placeholder or a receipt line, so the model knows it
exists. Content given only by URI is the one thing the record cannot
preserve; reconstruction reports the URI. Citations parts render as their
text. Reasoning blocks are covered in 4.6.

### 4.11 Tier 1: clear old tool results

Workflow code, index only.

- Eligible: results before the newest `ProtectRecentToolTokens`, except those
  marked `KeepResultInContext`. Clearing moves `ViewRef.ClearedThrough`
  forward, so the cleared set is one ID.
- Each becomes a byte-stable structural placeholder that keeps the call:
  `[result cleared: 18.2 KB, toolu_123]`. Errors keep their error flag and
  first line.
- Clear in bulk, and only when the saving reaches `MinSavingTokens`, so the
  prompt cache breaks once and for a real gain.

### 4.12 Tier 2: incremental summary

`SummarizeContext` loads, by hash: the previous summary, the units being
evicted, and the agent's prompt (4.17). Tool arguments and results are given
in full: the input is folded in chunks sized to the summariser's window, and a
result larger than a chunk is split across chunks, not truncated, up to
`MaxSummarisedResultTokens`. A compaction makes at most `MaxSummaryChunks`
successful folds, so its cost and latency are bounded. Each fold allows two
transient provider retries as separate recorded calls. Binding K15 decision 7 sets
`Out = min(MaxSummaryTokens, floor(window/5))`; `Bs = window - Out`. This replaces
the implicit `Out = min(MaxSummaryTokens, Bs/4)` equation, which has no integer
solution for some windows. Every chunk holds the
previous summary, new input and the output, even when the summariser's window
is smaller than the agent's. Content beyond either
limit is recorded in the receipt line (`toolu_123: bytes 40,000–1,200,000 not
summarised`) and `recall_context` can page it, so nothing is hidden from
discovery.

**Output contract**, deliberately small so custom prompts work:

- non-empty and not truncated;
- at most `MaxSummaryTokens` under the current budget, so a summary made
  after a budget shrank is again rendered whole;
- the new checkpoint renders smaller than what it replaces: the previous
  checkpoint plus the evicted units as they rendered in the view;
- every citation (`[message N]` or `[rK message N]`) resolves to a user
  message behind the checkpoint, by the rule of 4.9;
- for the default prompt only, the required Markdown headings (Goal,
  Decisions, Facts, Progress, Open questions, Next).

The default prompt carries facts forward unless superseded, lists superseded
constraints explicitly with both message numbers ("message 57 replaces
message 1: use staging only"), keeps user constraints verbatim with their
message numbers, and cites by number every user message that supersedes an
earlier one, so the framework keeps it in the user index (4.9), and treats the transcript as quoted data.

**Recording and validation.** The call record (4.13) stores every summariser
response when it completes, including responses that validation then rejects:
P1 keeps them for evals. The guarantee about rejected output is different:
no checkpoint, view or index entry ever refers to it. `SummarizeContext`
validates before it builds a `CheckpointRecord`, so a rejected summary never
gets one, and `context_compaction_skipped` carries the rejection kind and the
call key.

**Bound to its cut.** A summary covers the previous checkpoint plus the units
from the view start at request time up to one cut, and the activity returns
that `(from, to)` range with it. The workflow applies it only at that cut. If
the view has meanwhile moved to or past `to` (an overflow eviction ran while
the summary was in flight), the summary is stale and discarded. Applying it at
any other cut would drop the units in between without a summary or receipt;
the formal model (section 8) shows that counterexample.

**Receipt.** The chain of checkpoint records holds the full receipt: every
tool call behind the checkpoint (`toolu_123 search_orders(customer_id="c-42", …)`)
and every assistant message with text, by its first line, each with its run
and any unsummarised ranges, so recall references are exact
whatever prompt produced the summary. The receipt is rendered within `ReceiptTokens` (4.6);
its stored size grows with the run, and nothing in a request does.

Transient rate-limit, 5xx and timeout errors retry twice inside the activity,
with a fresh recorded Seq each time. Exhaustion returns `summariser_error`;
overflow returns `summariser_overflow` without retry. Store and infrastructure
errors fail the activity rather than returning a rejected checkpoint. Successful
rejections adopt every started call and fall back to a receipt-only checkpoint
at the same cut. Three non-stale rejections disable tier 2 for the run; the third
also emits one `summaries_disabled` record with a distinct `:disabled` key.

Validation rejects text containing the internal lookup tool name. Loose forms
such as `[Message 57]` or `messages 5–7` are counted as `LooseRefs`; they are not
citations and do not populate CitedUsers. The ordered size rule uses the old view's
actual omission, preview and cleared-placeholder sizes and renders the candidate
checkpoint with reselected pins before any record is stored.

### 4.13 The call record

One record per model call attempt, written by the innermost runtime model
wrapper (F1) to the run store, always on:

```go
type CallKey struct {
    RunID   string
    Seq     int // from NextSeq (4.3), unique per run across retries and parallel calls
    Attempt int // attempt of the activity that made the call, as the engine reports it (eval plan T0)
}

type CallRecord struct {
    Key      CallKey
    Activity ActivityKind // planner or SummarizeContext
    TurnID   string       // the planner step the activity attempt served
    Request  RequestParts // the system and tool hashes, the message sequence (below), and parameters
    Response *Hash        // the response or final streamed response, including ones later rejected
    Error    *CallError   // provider error kind and message, pre-send rejection, or output-validation kind
    Usage    model.TokenUsage // for a stream, the sum of its usage deltas
    Started  time.Time    // when the started record was written
    Latency  time.Duration
    Facts    *RequestFacts // written with the completion (4.8)
    Model    string
    Class    model.ModelClass
    Context  Fingerprint  // ContextConfigFingerprint (4.17)
    Prompts  []prompt.PromptRef
}
```

**The message sequence is lossless.** A request's messages are recorded as
an ordered list of segments, each either a span of the rendered view or one
message hash:

```go
type MessageSegment struct {
    ViewFrom, ViewTo int  // a run of consecutive messages of the rendered view (by position), or
    Hash             Hash // one message not taken from the view in place
}

type RequestParts struct {
    System, Tools Hash
    View          *ViewRef          // the view the spans index into; nil if no span is used
    Messages      []MessageSegment  // in request order
    Params        Hash
    Request       Hash              // the whole model.Request, to verify a rebuild
}
```

`model.Request` has no system field: system text is in its messages. `System`
is therefore a derived hash of the request's system-role messages, used to
compare requests; the messages themselves are in `Messages`. `Params` holds
every other field of `model.Request` (`RunID`, `Temperature`, `ToolChoice`,
`MaxTokens`, `StructuredOutput`, `Stream`, `Thinking`, `Cache`); `Model`,
`Class` and `Prompts` are on the record.

The wrapper builds the list by matching each request message's full encoding,
`Meta` and cache points included, in order, against the rendered view: a
message that continues the current span extends it; anything else starts a new
segment. A subset, a reordering, a duplicate, an inserted message or a view
message with changed `Meta` therefore costs one segment each, and replaying
the segments rebuilds the exact request, which the wrapper checks against
`Request` before writing (a mismatch records every message by hash). A request
that sends the view unchanged plus the planner's additions is one span plus
the additions.

**Growth.** For planners that send the view in place, a request's record is
constant-size apart from its additions, so the call record grows linearly with
the number of calls, with compaction on or off: a run without compaction has a
longer view, but a span costs the same. A planner that builds requests whose
messages are not in view order pays one segment per break, and one whose
requests grow independently of the view (a planner that accumulates its own
history) pays for every message in every request, which is quadratic over the
run. The wrapper emits `context_request_growth` once per run when a request
carries more bytes outside the view than inside it, whether or not the request
is view-bearing, so that case is reported (4.23). Whether a response was
committed is not part of the record: it is committed when an index entry names
its key (4.5). Calls that bypass the wrapper (F1) or are short-circuited by an
interceptor are not recorded; this is documented. The key is placed in the
call context as `model.CallInfo`, the eval plan's T0, which K8 depends on; T0
takes `Seq` from K2's `NextSeq`, the only source unique across retries and
parallel calls under Temporal, and exposes the activity attempt through the
engine, which does not report it today. A call that bypasses the wrapper
leaves its assistant entry with a nil `MessageEntry.Call`, which `RunRecord`
reports as an unrecorded call.

This replaces the eval plan's harness-installed capture decorator for agent
runs (section 7.3). It also closes the eval plan's open item on capture with
several Temporal workers, because the record lives in the shared store.

### 4.14 Model limits

```go
type ContextLimits struct {
    ContextWindow    int
    MaxOutputTokens  int
    DefaultMaxTokens int // configured completion cap when Request.MaxTokens is zero
    ImageTokens      int // zero selects 1,500 tokens per image for measurement
}

type ContextInfo interface {
    ContextLimits(ctx context.Context, req *model.Request) (ContextLimits, string /* resolved model */, bool)
}
```

The capability resolves the same request model as completion, including model
class routing. It returns the resolved provider model ID even when the window
is unknown. Anthropic's Models API and Gemini's `Models.Get` discover limits in
activities with a caller-bounded two-second deadline. Successful raw metadata
is cached per requested model for the client's lifetime. Caller cancellation or
expiry failures are never cached; other failures retain the table fallback for
60 seconds before retrying. Provider calls never run in workflow code.

Anthropic `max_input_tokens` and Gemini `InputTokenLimit` describe input limits.
Cache the raw input allowance and map it after lookup for each request as
`ContextWindow = input limit + contextmeasure.OutputReserve(req, limits)`.
The reserve uses request MaxTokens, then DefaultMaxTokens, then the smaller of
max output and a quarter of the returned window. For that last case, use
`min(MaxOutputTokens, input limit/3)` with integer division to keep the reserve
stable on the returned window. Thus `Budget = ContextWindow - OutputReserve`
equals the published input allowance without subtracting it twice (decision 4),
including explicit caps that differ from the configured default. Shared context
windows published in tables are used directly. Table keys must be SDK
constants or IDs in adapter non-test code, with every figure verified against
an official provider page. Unlisted models remain unknown; Bedrock, Codex and
Gemini currently have empty tables under this rule.

`runtime.Options.ContextLimits` (or `WithContextLimits`) overrides positive
fields by resolved model ID, falling back to the registered client ID when no
resolved-model entry exists. Zero fields retain adapter values. Construction
rejects negative fields and `MaxOutputTokens > ContextWindow` when both are set.
Output-only overrides are accepted. After merging with adapter limits, an invalid
max-output/window pair or reserve at least the window produces unknown limits.
Facts mark `LimitsInvalid`; the workflow emits one `context_limits_unknown` with
reason `invalid_override` and the model. Overrides are
copied at construction. Ollama's effective `num_ctx` and gateway limits require
an override. `model.NewClient` and the rate-limit middleware preserve both
`ContextInfo` and `TokenCounter` through the shared capability construction.

When a model's limits are unknown, the later compaction trigger and pre-send
guard are off for that model; overflow recovery (4.15) still applies. K12 owns
`context_limits_unknown` emission, and K16/K17 own explicit `Context(...)`
fail-fast behavior, with an error naming the model and override to set (D12).
The adapters expose limits and the recorder stores request measurements; neither
automatically activates compaction, guard rejection, or unknown-limit events.

### 4.15 Overflow

The overflow stop reason and provider "too long" errors become one new kind,
`ProviderErrorKindContextOverflow`, in every adapter (Anthropic error and
stop reason; OpenAI `context_length_exceeded`; Gemini `INVALID_ARGUMENT`
token message; Bedrock `ValidationException` text and stop reason, replacing
today's `OutputLimited` mapping; Ollama and the gateway by documented
messages), each with a conformance test on a recorded body.

Streamed output already presented before an overflow is retracted with
`FinishModelPresentation(ctx, id, accepted=false)` (`planner/planner.go:184-189`),
as rejected output is today.

The planner activity returns a new `PlanActivityOutput.ContextOverflow`, after
a provider overflow or a pre-send rejection (4.8). It carries the failed
request's facts and is not a `ModelRecovery` (F15). The planning loop gains a
view-recompute step. On overflow the workflow:

1. recomputes the rendering budget from the failed request facts, then applies
   tier 1 with no protected window and no minimum saving;
2. if that is not enough, evicts the oldest units after the checkpoint (no
   model call). The new checkpoint keeps the previous summary, if any, and
   adds receipt lines for the evicted units. Recovery first recomputes the
   budget from the failed request's model, so a summary made under a larger
   budget renders as a bounded summary (4.6);
3. if still too large, halves every newest-unit result preview evenly down to
   256 bytes, then forces placeholders for that unit, including Keep results;
4. reschedules immediately with the new `ViewRef`. Its PreviewThrough and
   ForceClearThrough watermarks are monotone. A trailing user unit remains joined
   to the latest legal assistant cut.

**Enough** is measured on the failed request, not on the framework's view
alone. When the model's limits are known and the rate is calibrated, the
target is an estimate below `B × TriggerAt`. Otherwise (no usage on a
rejected first request, or unknown limits) the target is a rendered view at
most half the view bytes of the failed request. Enough counts the whole
retried request, rendered under the budget the retry will use: when the
failed request's model has a larger budget than the view was rendered for,
the checkpoint can render larger on the retry, and eviction continues until
the whole request meets the target. A conservative target may remain unreachable:
the guard turns off and the shrunk view goes to the provider. A provider overflow
on the minimal view fails with the typed error. The retry is the next request of the
rescheduled planner step: no compaction step (clearing, eviction, applying a
summary) runs between a recovery and its retry, so nothing undoes "enough".
If a request the provider
rejected was not view-bearing, a smaller view cannot reach it, and the step
fails at once with `ContextOverflowNoProgress`.

**Progress.** Every retried call to model m must be smaller than the smallest
whole-request size that model's provider rejected in this logical step (binding
decision 1). Call ordinal is diagnostic only. The wrapper checks the advisory
guard first, then progress. A rewritten request no smaller than that baseline
fails with `ContextOverflowNoProgress`, naming the planner and both call keys.

**Only provider overflows fail a step.** Every failure condition in this
section (not view-bearing, no progress, a minimal view, the spent budget)
applies to provider overflows. When one of them is met after a guard
rejection, the guard turns off for the rest of the step and the request goes
to the provider (4.8); the step fails only if the provider then rejects it.
The no-progress check is a provider condition too: it compares the request
about to be sent with the smallest request the *provider* rejected for that
model in this step, and fails the step without sending only when the new
request is no smaller than that one,
so the provider has already rejected a request at least as large. A guard
rejection in between, or a calibration the overflow's usage report enabled,
does not change that comparison. A step that fails is therefore always
explained by a provider overflow: of the last request sent, or of a request
no smaller than the one it would send.

Progress size is the whole-request estimate at the uncalibrated default rate
(1/3 token per text byte), plus non-text tokens and output reserve. It never
uses calibrated usage, the current view alone, a guard rejection, or call ordinal
as its baseline. `overflow.Check` checks the advisory guard first, then this
per-model baseline; the caller retains each model's smallest rejected size.

For an agent that still uses `HistoryPolicy`, the policy runs on the loaded
view inside the planner activity, so recovery shrinks the view the policy
receives.

Provider overflows have their own budget of two per step, separate from
`RemainingRecoveryTurns` and from guard rejections. The step fails with a typed error carrying the
facts when the budget is spent, when there is no progress, or when the view is
already minimal: the checkpoint and the newest unit at its smallest rendering.
Recovery never depends on a model call.

**Budget shortfall (binding decision 5).** If the recorded B-derived defaults
produce `checkpoint.MaxBytes < checkpoint.MinCheckpointBytes`, recovery is not
attempted. Capacity never increases above those defaults, and rendering never
switches to an alternate projection. The observed provider overflow is wrapped
in a typed error naming both the provider cause and checkpoint byte shortfall;
`context_budget_too_small` is emitted and no recovery retry is consumed. A guard
rejection in this situation only disables the guard and sends the original view.

### 4.16 Sessions

Continuation applies to session-backed roots of agents with threshold compaction (4.17). Sessionless OneShotRun calls keep their existing path. Agents
without it keep today's behaviour: the next run's history is the caller's
messages. Otherwise the continuation would carry every tool result of an
uncompacted run into the next one.

**Two guarantees.** The continuation carries the previous run's final *view*,
bounded by a cap. It does not carry the run's *evidence*: whatever the view
shows in a smaller form, or no longer shows, stays in the earlier run's record
and is retrievable by a run-qualified reference (4.18) while retention keeps
it. The handoff therefore never loses evidence, and it reproduces the final
view exactly only when that view fits the cap.

**Continuation record.** Only a root that completed with a final response publishes. Failed and canceled roots append `run_sealed` with outcome `skipped` and do not advance the head. At successful run end, `SealRecord` builds the final view in a
bounded *carried* form, stores it, and publishes it as the session's head
(below):

```go
type Continuation struct {
    ID         string    // the publishing root run
    Prev       *string   // the continuation this run started from; nil for a session's first run
    Pos        int       // position in the continuation chain: Prev's Pos + 1, or 1
    Checkpoint *Hash     // latest CheckpointRecord in the publishing run's scope; nil if the run never compacted and its tail fit the cap
    Pins       []Carried // the view's pins, oldest first, as rendered
    UserIndex  []Carried // the user index lines, as rendered (4.9)
    Tail       []Carried // TailFrom to the end, including the final response, within the cap
    Visible    []Hash    // caller-visible messages: RunInput.Messages and RunOutput.Final
}

type Carried struct {
    Entry MessageEntry   // role, tool use IDs, sizes, structural kind; Entry.Origin always set
    Bytes *Hash          // full: the content; preview: the stored preview; nil for a placeholder
}
```

Carried entries never include reasoning blocks: no provider needs them from
a finished turn, and they are not carried across runs (P4). Every carried
entry has an `Origin` (4.6). It is copied from the source entry
when that entry already has one, so an origin always names the run whose
record holds the original, however many runs the entry has been carried
through. `Origin.Form` is one of:

- **full:** an entry that rendered whole;
- **placeholder:** a result that rendered as a placeholder, or that
  `SealRecord` cleared. A placeholder is a structural entry, so it stays a
  placeholder in every later run whatever its `Keep` flag; the next run does
  not inherit a `ClearedThrough` watermark;
- **preview:** an entry that rendered as a preview (a tool result's
  model-facing preview), or a part shortened at step 3 below, stored as new
  content. Tool arguments become an object holding the preview text, so the
  tool use stays valid for the provider.

`Carried.Bytes` always holds what rendered, never the original behind it.

**Cap.** The tail is bounded even when the trigger never fired. The cap is
`KeepRecentTokens` plus `ProtectRecentToolTokens` for the run's smallest B, or
their caps (60k tokens) when no B is known, measured at 2 bytes per token as in 4.6. At seal
no await stays open, so units are grouped without the open-await rule (4.9)
and the newest unit is one planner turn with its results. Above the cap,
`SealRecord` applies these steps in order and stops as soon as the
tail fits:

1. evict the oldest units into a receipt-only checkpoint, as overflow
   recovery does, keeping the newest unit;
2. clear every result of the newest unit, `KeepResultInContext` ones
   included;
3. render the newest unit's remaining parts (assistant text, tool arguments,
   user messages) as previews that shrink evenly down to `MinPreviewBytes`
   (256) each.

After step 3 the tail is the newest unit in its minimal carried form. Its
size is bounded by the planner output bounds, as in P2: that is the hard
bound, and the cap is the target. If the next run's model is smaller still,
its own budget applies (4.8) and overflow recovery handles the rest.

**Copying is bounded by the view.** The run start activity copies only what
the carried form references: `Carried.Bytes` and the checkpoint's summary. A
placeholder copies nothing, and a preview copies only the preview, however
large the result behind it. So a copy is at most the larger of the cap and
the newest unit's minimal carried form, plus `KeepUserTokens`, plus one
summary.

**Who publishes.** Identity is explicit; nothing is selected by recency.
Only a root run of an agent with threshold compaction publishes, never a
child run. A store holds one head per session and every continuation ever
published in it, keyed by its `ID` (the publishing run). `SealRecord`
publishes with `AdvanceContinuation(session, expected, next)`, which returns
one of four results:

- **published:** the head was `expected`; it is now `next`;
- **already published:** a continuation with `next.ID` is already in the
  session, whatever the head is now. A retried `SealRecord` whose first
  attempt advanced the head, and whose acknowledgement was lost, gets this,
  even if a later run has since advanced the head again. The stored
  continuation must equal `next` (it is built from the sealed index, so a
  retry rebuilds the same bytes, and it holds no publish timestamp);
- **conflict:** a continuation with `next.ID` is stored with a different body.
  `SealRecord` fails loudly, like a conflicting run-log append;
- **superseded:** neither; another run advanced the head first.

The ID check comes before the head check, and the checks, the insert and the
head update are one atomic operation in the store: one transaction in Mongo,
with a unique `(session, ID)` index. Two writes are not enough: a crash
between the insert and the head update would leave a stored continuation that
a retry reports as already published although it never became the head, and a
second run could then publish from the same head. `session.ContinuationStore`
also reads the head and a continuation by ID. The
method is on a new interface, `session.ContinuationStore`, not on
`session.Store`, so existing store implementations keep compiling; the
in-memory and Mongo stores implement both, and registering an agent with
threshold compaction fails fast when the configured store does not (D17). Of two overlapping root runs that
started from the same head, the first to seal advances it; the other's update
fails, it records `continuation_superseded`, and its view is not carried. A
late-finishing older run fails the same way.

**Next run.** The existing `RecordContent` activity, extended with `Continue`, reads the session's head continuation
and records its ID as the run's `Prev`, whether or not the run then continues
from it; in an empty session `Prev` is nil. A run that starts fresh because
its messages do not match can therefore still advance the head.
`Visible` hashes canonical roles and parts, excluding Meta and cache hints. The recorded decision is `continued` or `fresh` with reason `no_head`, `no_match` or `missing_content`. The start output records ContextLimits and the derived render budget: the smallest known registered model window, with deterministic model-name ordering, or the predecessor budget when unknown.
When a prefix of the incoming `RunInput.Messages` matches `Visible`, it copies
those bytes into the new root scope and seeds the index with new IDs, in this
order:

| New entries | Seeded from | New `ViewRef` |
|---|---|---|
| 1 … p | `Pins` | `Pinned` = 1 … p |
| p+1 … p+t | `Tail` | `TailFrom` = p+1; `ClearedThrough` = 0, because placeholders are structural |
| p+t+1 … | incoming messages after the matched prefix | — |

If the previous run had a checkpoint, the activity stores a
`CheckpointRecord` with `From` naming the previous run's latest record,
`Summary` the copied summary, `SummaryRun`, `SummaryPos` and `CitedUsers` copied
unchanged (so bare citations still mean the run that wrote the summary), no
receipt lines of its own, `Earlier` the counts and the first and last run
positions behind `From`, and the carried user index lines
(copied, so the loader renders the earlier-runs lines and the user index
from its own scope), and `Through` = p. It covers the pins directly and, through `From`,
everything the previous run's checkpoint covered. Only `recall_context` walks
`From` into earlier scopes (4.3). Without a checkpoint, the tail starts at the previous run's
first user message and the new view is a `history` (4.6). All of this is
bounded however long the session is. The next run starts with the previous
run's final view plus the new messages, so a decision made after the last
compaction is not lost. Otherwise the run starts fresh. The decision is
recorded, so replay is deterministic.

**Identity across runs.** New IDs are internal to the new run's index.
Everything the model sees, and everything `recall_context` accepts, uses the
`Origin`: a pin carried from run 1 renders as `[r1 message 1]` in runs 2 and
3, a placeholder as `[result cleared: 18.2 KB, r1 toolu_123]`, and recall
resolves both in run 1's record. Tool use IDs keep their provider values, so
calls and results still pair within the tail. A chain of `From` links can
cross any number of runs; each link names the run whose scope holds the
record.

Callers rebuild the next run's history from what they saw, without tool
results. That is why the match uses `Visible`, not the index `Chain`. It also
means a continued run sees the previous run's tool results in the tail, which
the caller never passed back; compaction bounds them.

**References across runs.** `recall_context` resolves an ID qualified by an
earlier run through the continuation chain, under the recall handler's checks
(4.3), and pages a collapsed earlier-runs receipt from its handle (4.18). When
retention has removed the content, the result is `recall_unavailable`.

### 4.17 Settings, prompts and overrides

**Defaults are fractions of the usable budget B, with caps only**, so they fit
any window:

| Setting | Default |
|---|---|
| `TriggerAt` | 0.85 (fraction of B) |
| `KeepRecentTokens` | min(0.10 × B, 20k) |
| `KeepUserTokens` | min(0.05 × B, 10k) |
| `MaxPins` | 32 |
| `ProtectRecentToolTokens` | min(0.15 × B, 40k) |
| `MinSavingTokens` | min(0.10 × B, 20k) |
| `MaxSummaryTokens` | min(0.10 × B, 16k) |
| `ReceiptTokens` | min(0.03 × B, 6k) |
| `FramingTokens` | 200 (checkpoint labels and headers; exact deficit charged to receipt, then summary) |
| `MinCheckpointBytes(b)` | max(512, max(24, 2 × KeepUserTokens) + largest framing bytes + compact receipt and summary floors), a renderer threshold |
| `GuardMargin` | 0.10 |
| `MaxSummarisedResultTokens` | 0.5 × the summariser's B |
| `MaxSummaryChunks` | 8 |
| `MinPreviewBytes` | 256 (smallest preview of a carried part, 4.16) |
| `UserIndexLineBytes` | 160 (one user index line, 4.9) |
| `MaxPinnedParts` | 2 (non-text parts rendered in pins, 4.10) |
| `RecallDiscoveryCalls`, `RecallReadCalls` | 20 each, plus 10 each per compaction, accumulating to at most 60 (4.18) |
| `RecallScanBytes` | 8 MB per `find` call (4.18) |

The resident budgets (`KeepRecentTokens`, `KeepUserTokens`,
`ProtectRecentToolTokens`, `MaxSummaryTokens`, `ReceiptTokens`) sum to at most
0.43 × B for every window. With `FramingTokens` they stay under 0.5 × B for
any B of 2,900 tokens or more, which covers 4k windows. Overrides are validated when B is first known, and fail fast
if the resident budgets plus `FramingTokens` exceed 0.5 × B. K17 also rejects
checkpoint overrides whose `MaxBytes(b)` is below `MinCheckpointBytes(b)`:
max(512, max(24, 2 × KeepUserTokens) + largest framing bytes + compact receipt
and summary floors). This guarantees capacity for handles and exact framing
after receipt and summary donations. `MinCheckpointBytes`
is a renderer function of the current budget, not a token setting. At B=2,900,
K12 budgets give MinCheckpointBytes=790 and MaxBytes=1,444.

**Enablement.** Overflow recovery (4.15) is on for every agent: without it the
run would fail. Threshold compaction (the trigger, tier 1, eviction,
summaries) is opt-in through `Context(...)`, or the runtime option before the
DSL lands, until the release criteria (7.4) pass. Then it becomes the default
(K21, D1), per provider configuration and with the non-text rule of 4.10. An agent cannot declare both `Context(...)` and `History(...)`;
registration fails fast.

**Runtime option (K12).** Before the DSL lands, use
`runtime.WithContextCompaction(agentID, runtime.ContextCompaction{TriggerAt:
contextmeasure.Rate{Tokens: 85, Bytes: 100}, KeepResultTools: []tools.Ident{...}})`.
Zero token settings select the defaults above. Registration rejects an agent
with both compaction and `HistoryPolicy`. Workflow start also rejects stamped
compaction settings against a worker registration with `HistoryPolicy`, so split
client/worker deployments fail before effects. Observe, sorted Models and the
three diagnostic hooks run for every agent under protocol 9. `KeepResultTools` sets immutable result
entry `Keep` bits. `DisableRecall` belongs to `ContextRecovery` (K13), not this
option.

**Runtime recovery settings (K13/K14).** `WithContextRecovery(agentID,
ContextRecovery{GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10},
DisableRecall: true})` configures the existing recovery struct. Zero GuardMargin
resolves to 10%. RunInput stamps ContextSettings for every agent, including
Threshold, GuardMargin and DisableRecall. `ContextCompaction` carries
`RecallDiscoveryCalls` and `RecallReadCalls` (zero resolves to 20, negatives
are rejected). Protocol 9 extends the K13 lookup contract with overflow outputs and recovery.
K13 resolves current-run evidence and injected From records; its default
RunChain resolves no rN labels until K18.

**DSL.**

```go
RunPolicy(func() {
    Context(func() {
        TriggerAt(0.85)
        Summarize(func() {
            Instructions("Keep every order ID and customer ID verbatim.")
            // or replace the prompt entirely:
            // Prompt(`You maintain the working memory of a support agent...`)
        })
        // KeepRecentTokens(8000)
        // Recall(false)
        // Disable()
    })
})
```

Tools opt out of clearing with `KeepResultInContext()`. The summariser is an
explicit `ContextModel *runtime.ContextModel` in the generated agent config
when Context is declared and not disabled. Config.Validate requires it when
Summarize is declared; otherwise it is optional. Without it, compaction runs
tier 1, receipt-only eviction and recall, and logs once that summaries are
off. With `Recall(false)`, `recall_context` is never advertised and
`context_recall_disabled` is emitted once per run: the agent then loses
evicted evidence from its view, and the configuration says so. The runtime
option for `Recall(false)` ships with the tool (K13), before overflow recovery
can add it to any agent (K14).

The K15 runtime option is `ContextCompaction.ContextModel *ContextModel`, with
`ID string`, `Class model.ModelClass`, `AcceptsImages bool` and
`AcceptsDocuments bool`. `ID` selects a registered client; its adapter still
selects the provider model. Seal rejects an unregistered ID. `Summary` carries
`Instructions` and `Prompt` strings. These are owned snapshots in run inputs,
child inputs and recorded context settings. K16 supplies the DSL shown above;
K17 owns per-run overrides and fingerprints below.

K16 emits `RunPolicy.ContextCompaction` and `RunPolicy.ContextRecovery` on the
agent registration. The runtime resolves these with the same normalizers as
the runtime options, rejecting DSL plus option configuration for the same agent
and setting. `Recall(false)` sets only `ContextRecovery.DisableRecall`.
`KeepResultInContext()` emits a tool-spec flag; registration unions flagged
specs with `ContextCompaction.KeepResultTools` and stamps the set. Later toolset
registration does not change it. `Disable()` omits threshold configuration
before K21's default transition, and is compatible only with recall settings.
It does not override an explicit runtime compaction option.

**Prompts.** The summary prompt is a prompt-registry prompt,
`<agent>.context.summary`. K15 registers its embedded baseline, replaced by `Summary.Prompt`; K16 adds DSL configuration. Stored overrides scoped
by session or run labels replace it at run time, so experiments try prompts
without code changes. Rendering happens in `SummarizeContext`; the checkpoint
records the `PromptRef`. Template data: `.Previous`, `.Evicted`,
`.Instructions`, `.Budget`.

**Per-run overrides and fingerprint (K17).**

Per-run context settings use `runtime.WithContextOverrides(runtime.ContextOverrides{...})`
or `api.PolicyOverrides.Context`. Nil pointers inherit registration values; explicit
pointers replace them. `Ratio{Num, Den}` uses reduced integer fractions. TriggerAt
must be strictly between zero and one; GuardMargin includes zero and one half.
Absolute token and count overrides must be positive; MaxSummaryChunks is at most
64. MaxTier is clear, evict or summary and requires threshold compaction. An
explicit ContextModel pointer to nil disables summaries. Children use their own
registration settings and resolve their own prompt, without inheriting overrides.

Start validates overrides before submitting a workflow. It resolves the active
`<agent>.context.summary` template for the run scope, stores the bytes, and pins
`SummaryPrompt{Ref, Template}`. Summary activities render those bytes even if a
stored prompt override changes later. Canonical version-1 JSON records all settings,
reduced ratios, the pinned prompt and the unresolved B-dependent budget rules.
Its content hash is the run's ContextFingerprint. Every planner/summarizer call,
checkpoint and compacted/skipped event carries this fingerprint. RunRecord resolves
and strictly decodes the canonical settings, rejects call/checkpoint mismatches,
and exports the settings and template bytes.

ContextWindow is a pretend window for threshold decisions. With a known real
window it cannot exceed that window; its usable budget must be at least 2,900 and
its output reserve must fit. With unknown real limits it supplies the trigger
budget while the guard stays off. Without a pretend window, threshold compaction
requires known limits. The guard and overflow recovery use recorded real limits;
recovery cannot enlarge the current checkpoint budget. ValidateAt checks resident
budgets plus framing against B/2 and checkpoint capacity against MinCheckpointBytes
when B first becomes known and whenever MinB falls, but only for an explicit
per-run override (`Policy.Context` present) or a pretend window. Default and
registration-only runs retain the existing budget diagnostics and continue.
Override presence is recorded separately from the canonical settings fingerprint.
For a validated run, failure terminates the run with a non-retryable
ContextSettingsError naming the field, model and B.

### 4.18 recall_context

A runtime-owned tool, added through the tool-policy envelope after the first
compaction of any tier in a run (a cleared result is enough), or from the
start of a continued run whose seeded view shows anything less than whole: a
checkpoint, a placeholder or a preview. Adding it changes the tool set once, at a compaction, when the
prompt cache breaks anyway. It ships before overflow recovery turns eviction
on for every agent (K13, K14): without it, a receipt-only checkpoint leaves
the model no way back to the evidence.

Its description carries all usage guidance: that placeholders, pins, user
index lines, receipt lines, collapsed lines and shortened summaries name IDs
and handles it accepts, how to page, how to search, and the two allowances. Rendered views
never mention it (4.6).

### Multimodal recall (K22b, protocol 18)

A result read pages text, including document Text and Chunks in original part
order. At offset zero it lists every image and byte/URI document in
`attachments`, with the original 1-based part index, kind, format, bytes and
estimated tokens. Read one attachment with
`{"read":{"id":"1","part":"attachment","index":2,"offset":0}}`.
Only attachment reads accept `index`; their offset must be zero. Each spends one
read allowance. Text parts and missing indices return `recall_not_found`.
Malformed input returns `recall_invalid_input`.

The activity selects only original user-message parts in the current run or an
authorized earlier run reached through the recorded continuation chain. It
never follows server-only Record/Output content or a tool result's attachments.
Reading a recall result returns its JSON source descriptor without attachments.
Successful reads clear document Cite, copy a canonical user-message part blob
into the current scope, and return its hash; attachment bytes never cross the
workflow boundary. The next model request includes that part in
`ToolResultPart.Parts`. Clearing, hiding and eviction remove these parts from the
rendered result. They are not checkpoint pins.

`RequestFacts.tool_result_parts` records support from the registered client.
The first view-bearing observation fixes each model's support. Compaction and
RunRecord intersect all observed models in sorted order; support starts empty
and can narrow only when another model appears. Recovery without threshold
compaction uses the same facts. The workflow stamps this intersection and
ProtectRecentToolTokens resolved at MinB into each recall activity.
Image and byte-document uploads must fit the provider byte ceiling and the token
allowance. Images cost 1,500 tokens; byte documents cost ceil(bytes/2). URI
documents cost 1,500 tokens with no byte ceiling; their descriptor reports zero
uploaded bytes. Unsupported reads return `recall_unsupported_part` with format,
bytes and reason (provider, too_large or uri). Size rejections add `limit` and
`limit_unit` (`bytes` or `tokens`); other rejections omit the unit. A switch
to narrower support after recall fails with the typed non-retryable adapter error;
automatic degradation remains follow-up work.

`context_recalled.parts` counts returned attachments (omitted at zero). Carried
tool results drop attachment blobs and NonText, retain their JSON descriptor,
and become previews. Summary inputs and GenAI telemetry omit nested attachments.
Request snapshots preserve them; `agenttest.LastToolResultParts` matches them.
Protocol 18 includes main, budget, summary, child, child_run, confirmed_pause,
continued and multimodal real Temporal histories. Protocol 17 rejects before
workflow effects; its run-record exports remain readable. Drain v17 workflows
before deployment. Structural TemplateProtocolVersion stays 5.


Input, one of three kinds. **Reads** return content:

- a tool use ID or message ID from a receipt line, placeholder, pin or user
  index line, optionally qualified by a chain label, with a required nonnegative
  byte `offset` (start at zero) to page a large result or message, and
  `part: result|arguments` to select eligible text
  or the original tool arguments;
- a checkpoint ID from a bounded summary (`r1:ck-91b2c3d4`), with an `offset`,
  which pages the full stored summary.

**Listings** return lines, newest first, each with its ID and run, plus a
cursor for the next page:

- `list` with a handle from a collapsed line (`rc-c41e5a6b` for receipts,
  `rc-02d9e5f6` for the user index). The agent needs no run ID or range it has
  not been shown: the handle is on the line;
- `list` with a message range, optionally qualified by a chain label, which
  lists *every* model-visible message in it: user and assistant text by first
  line, tool calls by receipt line, results by size and error flag.

**Searches** find content by text:

- `find` with a literal string, which scans the eligible evidence behind the
  checkpoint (eligible index messages and cleared results; the current run first, then
  earlier runs through `From`), newest first. A call scans at most
  `RecallScanBytes` and returns at most ten matches, each an ID, an offset
  and a one-line snippet, plus a cursor that continues the scan. A match's
  offset lets a read jump into a large result instead of paging from its
  start.

Rendered handles use `rc-` followed by eight hexadecimal digits; checkpoint
IDs use `ck-` followed by eight hexadecimal digits (4.6). Collapsed users,
current-run receipts and earlier-run receipts use distinct part names
`users`, `receipt` and `earlier` when hashing the checkpoint record ID.

A handle and a cursor are opaque. A handle names a checkpoint record and the
part its line collapsed; a cursor names a position in that chain or scan.
The handler resolves a handle only by walking the chain of the current
checkpoint, including its `From` links, so a handle cannot name a record
outside it, and every run the walk enters passes the checks of 4.3.

Every output goes through the model-facing renderer (4.4, P4). `PageBytes` is
`max(512, ProtectRecentToolTokens)` bytes. Read offsets snap to UTF-8 rune
boundaries and outputs give `next_offset`. Cursors are opaque unpadded base32
of `{kind, handle|range|scan, position}`; decoding checks shape only, and every
call re-resolves the selector within the current chain. Each call emits
`context_recalled`.
In a parent run, a child's tool result is the child's final output.

**Budget (D16).** Discovery (listings and searches) and reads have separate
allowances. Rejected and malformed calls are charged too; malformed known
operations keep their kind, while an absent or ambiguous kind charges
discovery. Paging through receipts cannot use up the reads that fetch
the evidence. Each starts at `RecallDiscoveryCalls` and `RecallReadCalls`
(20 each) and gains half that again, rounded up, at every compaction, because each
compaction hides new evidence; unused allowance accumulates up to three times
the initial value. When one is spent, the tool stays advertised (for
prompt-cache stability) and returns `recall_budget_exhausted` naming which
allowance, what remains of the other, and that the next compaction refills
it. The budget bounds loops; it also means evidence can be preserved (P1) and
reachable (P5) without being readable *now*. The evals measure how often that
happens (7.4).

### 4.19 Observability

Hook events, recorded in the run log: `context_compacted` (reason
`threshold`, `overflow` or `manual`; tier `clear`, `summary`, `evict`, `preview` or `placeholder`;
units and calls affected; estimated tokens before and after; summariser model
and usage; checkpoint sequence; fingerprint), `context_compaction_skipped`
(reason, rejection kind, call key), `context_overflow` (source `provider`, `guard` or `no_progress`; failed call and
all adopted call keys, outcome `recovered`, `guard_off` or `failed:<reason>`;
head, actual view, step, facts, stamped settings and pure recomputation), `context_recalled`, `context_unmeasured`,
`context_limits_unknown`, `context_budget_too_small`, `context_recall_disabled`,
`context_request_growth`,
`recall_budget_exhausted` (which allowance),
`continuation_started` (prev, pos, outcome continued|fresh, reason, seeded),
`continuation_published` (id, prev, pos, outcome published|already),
`continuation_superseded` (prev, head),
`context_nontext_resident`.
Each newly inserted compacted or skipped record produces one `context.compact`
internal span. Duplicate event keys produce no additional span. It ends at the
event timestamp and lasts the recorded summary latency (zero for other tiers).
Attributes under `loom_mcp.context.*` are reason, tier, outcome, kind, units,
calls, before, after, checkpoint, fingerprint, step, model, usage token counts,
chunks, effective_window and pretend. Agent, run, session and turn IDs provide
correlation. These record events reach the canonical telemetry subscriber.

### 4.20 Failure modes

| Failure | Behaviour |
|---|---|
| Run store write fails | The writing activity or client call fails; the run fails if retries fail. No degraded mode. |
| Summariser error, timeout or overflow | Retries per policy, then rejected; chunk folding prevents overflow on retry. |
| Summary invalid, truncated, too large or not smaller than what it replaces | Rejected; its response stays in the call record; tier 1 and a receipt-only checkpoint apply. |
| Three rejected summaries | Tier 2 stops for the run. |
| Pre-send guard rejects a request | `ContextOverflow`, recovered as an overflow (4.15); if recovery cannot reach its target, the guard lets the next request through. |
| Provider overflow with no progress, after two recoveries, or with a minimal view | The step fails with a typed error carrying the facts and call keys. |
| Provider overflow with an unrenderable B-derived checkpoint budget | Typed budget shortfall preserves the provider cause; context_budget_too_small; no retry spent. |
| Other non-retryable provider errors | Non-retryable engine application error provider_error.<kind>; one activity attempt. |
| Provider overflow on a request that is not view-bearing | `ContextOverflowNoProgress` at once. |
| Newest unit does not fit | Its results shrink to previews, then placeholders; if still too large, `ContextBudgetExceeded` naming the unit's first message ID (no tool names or arguments, per the roadmap non-goals). |
| Activity input over 1 MB | Fails fast with the existing typed error; P2 makes it a bug, not a run-length effect. |
| Unknown model limits | Trigger and guard off for that model, overflow recovery on, `context_limits_unknown` once; fail fast only with an explicit `Context(...)` (D12). |
| Budget shrinks below a stored checkpoint (smaller model mid-run or in a continued run) | The checkpoint renders under the current budget: bounded summary, re-selected pins, bounded receipt (4.6); the full record is unchanged and recallable. |
| Continuation tail over its cap | Eviction, then clearing, then minimal previews (4.16); the evidence stays in the earlier run. |
| Session continuation does not match | Ignored, recorded, fresh start. |
| Another run advanced the session head first | `continuation_superseded`; this run's view is not carried (4.16). |
| Crash between a call's start and completion | The call record stays `started` and `RunRecord` reports it incomplete (4.5). |
| Recall allowance spent | `recall_budget_exhausted` naming the allowance; the next compaction refills it (4.18). |
| A run with non-text parts on a provider without multimodal tool results | Threshold compaction off for the run by default; `context_nontext_resident` once (4.10). |
| A part a model or provider cannot take (an image for a text-only summariser, a document in a tool result) | Placeholder, "not summarised" receipt line, or `recall_unsupported_part` (4.10); never dropped silently. |
| Earlier run's content removed by retention | `recall_context` returns `recall_unavailable`; a continuation whose content is missing is ignored, recorded, fresh start. |
| Run terminated outside workflow code | Unsealed record, complete to the last planner step; the run log says so. No continuation is written, so the head stays at the previous continuation and the session's next run continues from it when its messages match, else starts fresh, and that decision is recorded. |

### 4.21 Prompt caching

The view changes only when compaction runs, and tier 1 clears in bulk behind
a minimum saving, so the prefix breaks rarely. Placeholders are byte-stable,
and the rendered receipt changes only at a compaction.

### 4.22 Workflow compatibility

Activity inputs, outputs, signals and workflow state change, and running
workflows cannot replay on new code. A `WorkflowProtocolVersion` is stamped
into run input and checked at workflow start and on replay; a mismatch fails
fast. Every ticket that changes workflow code bumps it and adds a replay test.
Operators drain running workflows before upgrading.

K17 adds Policy.Context, canonical stamped settings and fingerprint, pinned
SummaryPrompt, child-start context resolution activities, effective-budget
validation and context telemetry fields in protocol 15. FR1 advances the workflow
protocol to 16: clarification-only text enters the question content activity,
confirmed-tool pauses are published and answered before resume, and provided-result
publication identities remain stable across retries. The template
version stays 5. The real `workflow_protocol_v16.json`,
`workflow_protocol_v16_budget.json` and `workflow_protocol_v16_summary.json`
fixtures cover overrides, recovery and summary/budget adoption.
`workflow_protocol_v16_child.json` and `workflow_protocol_v16_child_run.json`
record and replay child context preparation followed by the child start and run.
`workflow_protocol_v16_confirmed_pause.json` proves approval, tool-returned
clarification, answer, recording and one resume; the summary fixture also proves
clarification-only text retention. Protocol 15 is rejected before workflow effects; drain older runs before deployment.

K18 advanced the workflow to protocol 17 for session continuation, compact optional
seal projection fields, cross-run identity, and overflow-retry usage accounting.
K22b advances the current workflow to protocol 18 and records the same seven
histories plus multimodal, rejects 17 before effects, and retains earlier fixtures.
Genuine v17 main, budget, summary, child, child_run, confirmed_pause and continued
histories preserve FR1 behavior. Keep FR1's v16 recordings unchanged and reject
protocol 16 before effects. Argument previews use one wrapper; every shrink pass
reads the original rendered part and re-carry preserves omitted-byte counts.

K12 uses protocol 7, including entry-size metadata, `PlanActivityOutput.View`
and `Facts`, sorted `PlanActivityInput.Models`, and run settings. K12 applies
Observe, Models and diagnostics only when compaction settings are stamped.
`planner_step.recovery` marks rejected outputs; `recovering` marks correction
retries. Every step records the loader-built view its calls used, including pins.
Rejected outputs leave the stable view and decision state unchanged, but their
adopted facts update State; correction retries preserve the stable view.
The loader derives canonical pins from record users and Budget, returning the
exact list in ViewUpdate and CallView. Rendering the recorded ViewRef pins is
therefore equivalent to loader selection, warm or cold and after budget shrink;
incoming pins do not override canonical selection. Structural
`TemplateProtocolVersion` remains 5. The client stamps resolved compaction
settings into `RunInput` at start and overwrites caller values. Child settings
are snapshotted with that input. Canonical planner-step and seal records preserve
the settings in RunRecord; replay and offline decisions use recorded settings,
limits, rates and entry sizes rather than worker configuration. Code changes
still require draining; changing worker options cannot change an existing run.

The roadmap's durable cross-workflow continuations must carry the index head
and latest checkpoint as part of the successor's replay seed. The index
segments are what make `ContinueAsNew` possible later.

### 4.23 Remaining growth

P2 bounds activity payloads and P3 bounds requests. These costs still grow
with run length:

| Cost | Growth | Bound or remedy |
|---|---|---|
| Temporal history events | One set per step | `ContinueAsNew` (follow-up), which the index segments make possible |
| The index in workflow memory and replay | About 400 bytes per entry | The same follow-up; at 1,000 steps with batches of 50 calls, about 20 MB |
| Store size: content, segments, call records | Linear | Retention (D7) |
| All recorder writes (addressed content + every `PutCall` body, K12 run) | At 1,000 steps: off 2,212.230 bytes/step (max residual 990.001 bytes); `keep_recent` 2,242.624 (1,239.443); `compress` 2,669.240 (3,937.941); `real_compaction` 2,740.337 (542.128), two evictions after 1,000 seed turns | Entry-size metadata moved all four modes from K8. Same 500–4,000 bytes/step slope and <16 KiB residual bounds; first request full; repeated recorder writes included; index/checkpoint setup excluded |
| Building `ToolOutputs` and `TypedInputs` (4.7) | A cold worker walks every segment: work linear in the run *per step*, so quadratic over a run whose steps all land on cold workers | The loader keeps both slices per worker and appends only new entries, so a warm step does work linear in its new entries |
| Planner memory | `ToolOutputs` holds a hash and a bounded preview per call | Linear in calls. If measurements show it matters, a lazy accessor is a follow-up planner API change |
| Recall `find` | Scans up to `RecallScanBytes` per call | Bounded per call; the allowance bounds calls (4.18) |
| Call record of planners that build their own growing requests | Every message of every request, by hash: quadratic | Inherent to such planners; reported by `context_request_growth` (4.13) |

K7 and K8 measure each row with cold and warm worker caches at 1,000 and
5,000 steps (store reads, bytes loaded, planner heap, workflow memory,
history size) and record the figures as regression thresholds. "Long runs
keep working" (4.1) means these linear costs, not the failures this design
removes.

## 5. Execution plan

The tickets, their order and their acceptance proofs are in
[`context_compaction_plan.md`](context_compaction_plan.md). Ticket numbers
(K1–K22) cited in this design refer to it.

## 6. Decisions to confirm

| # | Decision | Proposal |
|---|---|---|
| D1 | Enablement | Overflow recovery on for every agent from K14. Threshold compaction opt-in until the release criteria pass, then on by default per provider configuration (K21); on a provider without multimodal tool results, runs with non-text parts keep it off (4.10). |
| D2 | Remove `History`, `KeepRecentTurns`, `Compress` without an alias | Yes, in K21, once compaction is the default. |
| D3 | Advertise `recall_context` after the first compaction | Yes, through the policy envelope; ships before overflow recovery adds eviction to every agent; opt-out per agent with `Recall(false)` (runtime option in K13, DSL in K16); rendered views never name it. |
| D4 | Planners receive the projected view; `ToolOutputs` carries references | Yes. This changes the documented `ToolOutputs` contract. |
| D5 | Session continuity | For agents with threshold compaction: a continuation record holding the final view in bounded carried forms (full, preview, placeholder) with each entry's `Origin`, matched on caller-visible message hashes; only the carried bytes are copied into the new root scope; the evidence stays in the earlier run and is recalled by run-qualified reference. |
| D6 | Summariser model | Explicit `ContextModel`; without it, tier 1, receipt-only eviction and recall. |
| D7 | Run store | Content-addressed, beside the run log, scoped by root run, same retention, redaction and deletion; tenant isolation by deployment, as for the run log. |
| D8 | Custom summary schemas | No: free-form custom prompts, small validation, framework receipt. |
| D9 | Content by reference | Yes. |
| D10 | Upgrade path | Drain running workflows; fail fast on protocol mismatch. |
| D11 | `PreChildValidator` API | Receives the tool call and a view loader, and runs in an activity. |
| D12 | Unknown model limits | Trigger and guard off for that model, overflow recovery on, one `context_limits_unknown` event; fail fast only when the agent declares `Context(...)`. |
| D13 | Rejected summaries | Kept in the call record for evals; never referenced by a checkpoint, view or index entry. |
| D14 | `ViewRef` shape | A constant-size reference into the stored index, not a copy of the view. |
| D15 | Authorization for cross-run recall | Enforced by the runtime's recall handler: session from run context, chain labels resolved through the session store, handles through the checkpoint chain; an optional `RecallAuthorizer` hook; no tenant model added. |
| D16 | Recall budget | Separate discovery and read allowances, replenished at each compaction (4.18). |
| D17 | Continuation store | A new `session.ContinuationStore` interface with an atomic, idempotent `AdvanceContinuation` (published, already published, conflict, superseded; one transaction in Mongo) and reads of the head and of a continuation by ID; `session.Store` is unchanged; registration fails fast when an agent with threshold compaction has a store without it. |

## 7. Evaluating compaction

Compaction quality is behavioural: a bad prompt or setting produces runs that
look normal and quietly forget things. The eval tool is how builders choose
prompts and settings for long-running agents, and how the project decides to
turn compaction on by default (7.4).

### 7.1 Offline: replay compaction on recorded runs

K20 replays fixed recorded cut points independently, using the recorded previous
summary and evicted evidence. Only the summarizer runs again, with up to
`MaxSummaryChunks × 3` provider calls per cut, plus optional judging. Baseline
verification reuses recorded responses and makes no provider calls.

- **Retention:** built-in rubric claims cover identifiers, numbers, decisions,
  user constraints, progress and faithfulness. Applications can supply facts.
  The existing eval judge evaluates old and new summary text separately against
  identical evidence. Retention is the entailed fraction, also per category.
- **Cost:** summary bytes, compression ratio, rendered checkpoint bytes,
  summarizer tokens, chunks and latency.
- **Scope:** cut-moving variants, recomputed cuts, chained drift replay and
  probe answers are deferred to K23. No model extracts facts. Unverified cuts
  remain visible but are excluded from aggregates.

```sh
<app> compaction run --from <experiment> --variant prompt=v2
```

The application's command embeds `evalcli.Main` with `Config.Compaction`.
Templates, instructions, model and chunk limits are fixed-cut variant options.
See `docs/runtime.md`, "Offline compaction evaluation", for storage, verification,
caching, live gating and cost controls.

### 7.2 End-to-end: variants on long-running scenarios

The eval plan's variants (B2) run the same scenarios under several
configurations: run labels (selecting prompt overrides) and
`PolicyOverrides.Context`. Per variant: task success and claims, plus
compactions, tokens and cost per run, `recall_context` calls and overflow
recoveries. Long-running scenarios use the pretend window.

### 7.3 Contract with the eval plan

- The trajectory is built from the canonical record through `RunRecord`, so
  compaction never removes evidence.
- For agent runs, the call record (4.13) replaces the harness capture
  decorator (eval plan 5.3): one schema, one layer, one store. Calls outside
  a run, such as `Decide`, are not recorded; `Decide` returns its own request
  and response.
- Calls are keyed by `(RunID, Seq, Attempt)` from `model.CallInfo`; child runs
  have their own keys. T0 takes `Seq` from `NextSeq`, so it follows K2.
- An attempt in the experiment store (eval plan T13) holds `RunRecord`'s
  export (4.5), so K20's replay and `show` work after the run's store is gone.
- Record events skip `BeforeEvent` interceptors (4.5), so the transcript and
  the classification of calls hold even under an application interceptor. The
  other run-log events the trajectory reads stay interceptable, so the
  trajectory marks the fields it takes from them as unverified
  (`EventsUnverified`, eval plan 5.4) when a `BeforeEvent` interceptor is
  registered.
- Experiment identity (eval plan B3) includes the fingerprint and prompt
  reference per variant; `compare` does not warn about identical system
  messages and tools when those differ.
- The release gate (7.4) needs paired comparison by item and repeat index,
  bootstrap intervals over items on differences and rates, held-out item
  pools, and per-run evidence classification from the record (present in the
  view or absent when needed).
  K19 adds them to the eval tool if the eval plan has not.

### 7.4 Release criteria

K21 turns threshold compaction on by default only when the K19 suite passes
these criteria.

**Variants.** An uncompacted baseline (a real window large enough for the
whole scenario, compaction off); clearing only; receipt-only eviction with
recall; and the hybrid (clearing, summaries, recall). The compacted variants
use the pretend window, so each scenario crosses the trigger at least three
times.

Two variants are gated, because they are what agents get by default: the
receipt-only variant (the default without a `ContextModel`) and the hybrid
(the default with one). Clearing only is reported for comparison.

**Scenarios**, each with planted evidence and judged outcomes:

- a fact planted before the first of three or more compactions, needed at the
  end;
- **obsolete instruction:** message 1 says "use production"; a later user
  message says "use staging only"; enough user messages follow that the
  correction leaves the pins, and the run crosses at least three compactions.
  The correction must win. Four variants, each with and without a
  summariser: the correction as an index line; the correction collapsed out
  of the user index; the correction buried after the first 160 bytes of a
  long message; and the correction made in a later run of the session
  (`r2 message 5` over `r1 message 57`). These are the residual risks of 4.9,
  so they are gated like any other scenario;
- a fact in the middle of a large tool result;
- **decisions and dependencies:** the answer depends on a decision the agent
  made in prose, without a tool call, and on combining results from several
  calls, all evicted before they are needed;
- **recall under pressure:** a long run on an 8k-window model with more
  evidence needed than one recall allowance covers, so the agent must search
  and read across a refill;
- **multimodal:** an image in message 1 and a PDF in a later user message,
  each needed after eviction, run once for each provider configuration that
  would receive the default (4.10);
- no summariser configured;
- a summariser whose output is rejected (forced by a test prompt);
- a session continued across three runs, with a decision made after the last
  compaction of the first run, needed in the third;
- a model change mid-run to a smaller window (reported, not gated: no
  uncompacted baseline can hold the run in the smaller window).

**Items.** Each scenario has at least 12 independent items drawn from at
least three task templates that differ in domain, tools and wording. Items
are written before any variant runs and are not tuned to one. Every item runs
twice per variant. Repeats measure model variance; only items measure
generality, so the looks below add items, not repeats.

**Measures** per variant and scenario: task success, unsupported claims,
**evidence use** (runs whose answer used the needed evidence, from whatever
source: the tail, the summary, the user index or a recall), **recall success**
(below), recall allowance exhaustion, latency, and total cost including failed
attempts and summariser calls.

Recall success is measured only on runs where the needed evidence was absent
from the view when it was needed, checked from the record: the receipt-only
variant, a fact in the unsummarised range of a large result, or more planted
facts than `MaxSummaryTokens` can hold. A run counts as a success when it
recalled that evidence and its answer used it. A hybrid run whose summary kept
the fact counts toward evidence use, not against recall.

**Design.** Runs are paired: a pair is one item and repeat index under the
baseline and under a variant. The gate is a non-inferiority test, pooled over
the gated scenarios with each scenario weighted equally. Interval levels are
set by the looks below.

- **Success:** the interval of the success-rate difference (variant minus
  baseline) has its lower bound above −5 percentage points.
- **Unsupported claims:** the interval of the difference in the rate of
  runs with an unsupported claim has its upper bound below +2 percentage
  points.
- **Evidence use:** the interval of the evidence-use difference has its lower
  bound above −5 percentage points.
- **Recall:** the lower bound of the recall success rate is at least 80%.

Every difference interval is a bootstrap interval over items, so the repeats
of one item are not treated as independent (percentile, 10,000 resamples,
fixed seed). The recall rate uses a Wilson interval on the effective sample
size, the number of qualifying runs divided by the design effect estimated
from the agreement between an item's repeats (at least 1), so a rate of 100%
on few runs still has a wide interval.

A guard runs beside the interval criteria: no gated scenario's point estimate
of the success difference may be below −10 points, so a pooled pass cannot
hide one broken scenario. The guard can only fail.

**Calibration.** The gate's operating characteristics are measured, not
asserted. Before any real run, K19 simulates the complete gate (every
criterion, the guard, the three looks and the stopping rule) on clustered
synthetic outcomes: per-item random effects, correlated repeats, and success,
claim and recall outcomes drawn jointly. It reports the probability that the
whole gate passes under stated assumptions:

- true success difference 0, −3, −5 and −8 points;
- true unsupported-claim difference 0, +1 and +2 points;
- discordance from 0.1 to 0.3, and within-item correlation from 0.3 to 0.7;
- true evidence-use difference 0, −3 and −5 points, and true recall success
  rate 0.75, 0.80 and 0.90;
- baseline rates from a pilot of the baseline variant only.

The margins (−5 and +2 points), the item counts and the looks are fixed from
that report before any variant runs, and are not changed afterwards. The
target is a pass probability of at least 80% for a variant equal to the
baseline, and at most 5% for one at either margin. If no item count up to the
pool's size meets the target (the 2-point claims margin is the likely limit),
the report says so, and the release decision is made knowing that the gate
cannot certify that margin. The suite never relabels inconclusive as pass.

**Outcome.** Each criterion is *pass* when its interval clears the margin,
*fail* when the interval lies entirely on the wrong side of it, and
*inconclusive* otherwise. The suite starts at 12 items per scenario and adds
held-out items at two fixed later looks (24 and 48). The three looks share
the error rate, so each uses a two-sided 1 − 0.05/3 interval. A fail at any
look is final and stops the suite. K21 proceeds only when every criterion
passes for both gated variants and the guard holds. A criterion still
inconclusive at 48 items counts as not passed. The report then says that
precision, not a measured regression, blocked it.

The session scenario is gated like the others, with today's behaviour as its
baseline: an agent without compaction, a real window large enough for the
whole session, and the next run's history built from the caller's messages.
The structural part of P6 is K18's test, not this gate.

Latency, total cost and allowance exhaustion are reported, not gated: a
slower or dearer default that keeps runs working is a trade the release notes
state. The suite reports each criterion, so a failure names the scenario and
measure.

## 8. Formal model

Eight TLA+ modules in `docs/formal/` check the design, all run by
`make context-model-test`; `docs/formal/ContextCompaction.md` lists each
module's states, invariants, faults, assumptions and limits.

| Module | Checks |
|---|---|
| `ContextCompaction.tla` (one run) | Nothing refers to unstored content; cuts never split a call from its result; every position before the view is carried by summary or receipt, including a large result the view renders as an omission; trigger decisions replay from the recorded facts and ViewRef; the retried request is strictly smaller than the failed one and renders under the failed model's recorded budget; recovery ends even when the provider keeps rejecting; the checkpoint stays within the current budget; no planner request, rejected request or summariser input carries server-only data. |
| `SessionContinuation.tla` | A retry after a lost acknowledgement is never reported superseded, and "already published" is returned only for the stored body; published continuations come from sealed runs and form one chain with unambiguous positions; child runs never publish; ordering by origin is chronological across runs that reuse message numbers; citations resolve to the intended message after copying and after re-summarising; completed sealed roots learn their publication outcome; unsuccessful roots record skipped without advancing; publishing is one atomic step, without which a second run publishes from the same head. |
| `CallCommit.tla` | Under crashes, zombies that write into the next step, lost returns and step failure, reconstruction classifies every call (adopted, abandoned, incomplete) and every presentation as what happened; the index references the adopted attempt's output, which needs step events that no interceptor can drop; sequence numbers are unique; no request is sent unrecorded; every step ends. |
| `RequestRecord.tla` | The segment encoding rebuilds every request exactly, own messages distinct, view messages sent with a changed `Meta`, and the view moved on, and the in-place case costs one span. |
| `ContinuationCarry.tla` | The carried tail fits the cap or is minimal (and the minimal case is reached); every part of runs 1 and 2 is carried or in the receipt chain; what the next run renders whole was copied whole; no reasoning crosses runs; placeholders stick. |
| `RecallDiscovery.tla` | Every evicted message, assistant text and results of parallel calls included, is reachable from the rendered checkpoint; any token, forged ones included, resolves only within the From chain; recall never returns server-only data; listing never spends reads; refills restore reads. |
| `OverflowGuard.tla` | With calibration that can change mid-step, every failure is explained by a provider overflow; no retry is sent that is no smaller than a provider-rejected request; budget failures are honest; every step ends. |
| `StepPayload.tla` | Activity inputs depend on neither run length nor initial history; walking the segments from the head finds exactly the committed entries. |

The harness runs each safe configuration with deadlock checking on and a
floor on the states explored, 63 safety fault configurations that must each
produce the counterexample for the rule they break, 9 witness configurations
that prove interesting scenarios reachable, and 5 liveness configurations,
four of whose properties each have a liveness fault that must break them. The
README marks every invariant as *observed* (compared with ground truth or an
independent statement) or *rule* (a restatement of the encoded rule, whose
real proof is the ticket's Go test).

A fresh review of the models found two design defects, both fixed above: a
summariser could turn a copied summary's bare citation into one of the
current run (4.9), and the no-progress check was not tied to the last
provider rejection (4.15). Ten faults reproduce defects found in review:
those two, a non-idempotent publish, ordering by local message ID, adoption
read from transcript entries, revision 7's request encoding, today's
accumulated planner histories, and three found by checking revision 8 against
the eval plan and the store backends: a step event dropped by an interceptor,
request messages matched by the canonical hash, and `AdvanceContinuation`
written as two operations.

The models' guarantees are structural. `NothingSilentlyDropped` shows that
every evicted position is covered by a summary or receipt under the model's
assumption that a summary carries what it was given; it says nothing about
whether a real summary preserves the facts, which is what section 7 measures.

Not modelled, because they are pure functions or measurements with no
interleaving: the estimator's arithmetic, pin and user-index selection,
the bounded summary's rendering function, the per-provider non-text default,
provider transcript grammar and the release gate's statistics. Their tickets'
table and property tests are the proofs. Task quality is measured by evals
(7.4), never by a model.

Writing the run model found two gaps in an earlier draft: summaries were not
bound to the cut they were made for, and overflow eviction replaced the
checkpoint's summary with a receipt.

**The models are kept in step with the design.** Any design change to a
modelled rule updates the model and its fault configurations in the same
revision, and `make context-model-test` must pass before the revision is
called done. Each ticket that implements a modelled rule adds a Go test for
the corresponding invariant (execution plan).

## 9. Sources

Commits read (shallow clones, source only):

| Repository | Commit |
|---|---|
| openai/codex | `e45069d7` |
| google-gemini/gemini-cli | `2ce1a696` |
| sst/opencode | `38840623` |
| block/goose | `7bdeadb9` |
| cline/cline | `fa840c74` |
| RooCodeInc/Roo-Code | `b867ec91` |
| can1357/oh-my-pi | `579da1d6` |
| badlogic/pi-mono | `6fb2e781` |
| OpenHands/software-agent-sdk | `33044182` |
| All-Hands-AI/OpenHands (0.62.0) | `7fbb48c4` |
| Aider-AI/aider | `5dc9490b` |
| langchain-ai/langchain | `34489d61` |
| langchain-ai/langmem | `48e3c11f` |
| google/adk-python | `128faabb` |
| google/adk-go | `49904c0d` |
| openai/openai-agents-python | `125efa02` |
| pydantic/pydantic-ai | `36529f3a` |
| letta-ai/letta (0.16.8) | `11315357` |
| goadesign/goa-ai (v0.87.1) | `b4a62832` |

Further reading: OpenHands context condensation blog post; Lindenbauer et al.,
"The Complexity Trap", arXiv 2508.21433.
