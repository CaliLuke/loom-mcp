# Context compaction models

These models check the context-compaction design in
`docs/wips/context_compaction.md` (revision 9). Run them with TLA+ tools 1.7.4
and Java:

```
make context-model-test TLA2TOOLS_JAR=/path/to/tla2tools-1.7.4.jar
```

Eight modules check the design. Each has a safe configuration, fault
configurations that each break one design rule, and, where it matters,
witness and liveness configurations. The harness checks four things:

- **Safe configurations pass**, with deadlock checking on, and explore at
  least a recorded number of distinct states, so a change that collapses the
  state space fails instead of passing quietly. Every module names its
  legitimate end states with an explicit stuttering action (`Done` or
  `Stop`), so any other state without a successor is reported.
- **Fault configurations fail** with the counterexample for the invariant
  their rule protects. A fault that passes means the invariant no longer
  protects anything.
- **Witness configurations fail.** A witness states an interesting scenario
  as a negated invariant (`Witness...`), so its counterexample proves the
  scenario is reachable within the bounds.
- **Liveness configurations pass, and their faults fail.** They check
  progress on smaller constants, because liveness checking is far more
  expensive. Fairness is placed on what the environment and workflow
  eventually do (a result arrives, an acknowledgement is delivered, a planner
  attempt has some outcome), never on which outcome happens, so the provider
  may keep rejecting. Each meaningful liveness property has a fault
  configuration that must violate it, so it is not vacuous.

| Module | Scope | Safe states | Faults | Witnesses | Liveness |
|---|---|---|---|---|---|
| `ContextCompaction.tla` | One run: store, index, view, checkpoint, compaction, overflow recovery, shrinking budgets, model access | 82.6M; 15.2M (limit changes twice); 60k (summary cap 4) | 16 | 1 | `RecoveryEnds` (+1 fault), `Terminates` (sanity) |
| `SessionContinuation.tla` | One session of several runs: publishing, lineage, cross-run order and citations | 68.7M (three roots); 3.3M (two roots and a child) | 9 | 2 | `SealedRootsResolve` (sanity) |
| `CallCommit.tla` | Planner steps under the commit protocol: crashes, timeouts, zombies, lost returns | 9.2M (one step, three attempts); 239k (two steps, two attempts, one timeout) | 8 | 1 | `StepsEnd` (+1 fault) |
| `RequestRecord.tla` | The call record's lossless message sequence, every request up to five messages | 9.3k | 5 | — | — |
| `ContinuationCarry.tla` | Carried forms, cap, copy bound, receipt chain and placeholders across three runs | 1.37M | 8 | 2 | — |
| `RecallDiscovery.tla` | Discoverability, handle confinement, separate allowances, recall exposure, user text | 23.8M | 8 | 1 | `RefillRestoresReads` (+1 fault) |
| `OverflowGuard.tla` | The advisory guard, per-request progress, unavailable recovery and step budgets | 2.3k | 4 | 1 | `Terminates` (+1 fault) |
| `StepPayload.tla` | The planner-input bound and index segments | 10.4k | 5 | 1 | — |

The full suite takes about three hours with all cores and a 10 GB heap; TLC's disk
queue needs several GB free.

**How to read the invariant tables.** Each invariant is marked *observed*
when it compares an outcome the actions produce (a rendered view, a
reconstruction, a history of requests) with ground truth or with a rule
stated independently, and *rule* when it restates the design rule the
actions encode. A rule invariant guards against a change to that encoding;
its fault configuration shows the encoding is load-bearing, but it does not
check the rule against anything else. Where only a rule invariant exists,
the ticket's Go test is the real proof.

**Not modelled, by design.** These are pure functions or measurements with no
interleaving, so table and property tests check them better than a model:
the estimator's arithmetic (clamping, no negative estimate, images counted
once; K10), pin and user-index selection, their budget at small windows and
non-text parts in pins (K11), the bounded summary's rendering function (K11),
the per-provider non-text default rule (K21), provider transcript grammar
(K11), the release gate's statistics (K19), and task quality (evals, design
7.4).

**Findings.** Writing the run model found two gaps in an earlier draft:
summaries were not bound to the cut they were made for, and overflow
eviction replaced the checkpoint's summary with a receipt. A fresh review of
the models found two more, both fixed in the design (revision 8): a
summariser re-summarising a copied summary could turn run 1's bare citation
into a bare citation of run 2 (4.9; fault `raw_previous_summary`), and the
failure rule did not say that "no progress" compares with the last
*provider* rejection, so a guard rejection could change the comparison
(4.15; fault `compare_last_rejection`). Five more faults reproduce defects
found in design review: a non-idempotent publish, ordering by local message
ID, adoption read from transcript entries, revision 7's request encoding, and
today's accumulated planner histories. Checking revision 8 against the eval
plan and the store backends found three more, fixed in revision 9: an
interceptor could drop a step event (4.5; fault `intercepted_record_event`),
request messages were matched by a hash that ignores `Meta` (4.13; fault
`match_canonical_hash`), and nothing required `AdvanceContinuation` to be one
transaction (4.16; fault `advance_two_writes`).

# ContextCompaction.tla

## What is modelled

- The run store as a growing set of content ids named after their producer
  (planner turn, tool call, summary). This module abstracts activity
  attempts: each producer writes once. Different bytes from retried or zombie
  attempts, and which attempt's output the index adopts, are modelled in
  `CallCommit.tla` (fault `index_first_stored`).
- The transcript index: user, assistant (with one or two tool calls, so
  parallel batches occur), tool result, and structural reminder entries.
- Producers: the planner activity, tool activities (store, then complete),
  and external results (`Runtime.Provide*` stores, then signals the hash).
- The view: `tailFrom`, the checkpoint, and cleared results.
- Tier 1 bulk clearing; summaries written by `SummarizeContext` for one cut
  and applied only at that cut, or discarded when stale; receipt-only
  eviction that keeps the previous summary; and overflow recovery with its
  budget of two, which may clear, evict, or clear and evict at once. An
  overflow is two steps: the activity reports the failed request's facts,
  which history records, and the workflow later applies recovery from them;
  the adapter's limit may change in between.
- What the checkpoint carries, derived from what it references (`Carried`).
- The size of the request the model receives: the rendered checkpoint plus
  the tail.
- Recorded planner facts, and trigger decisions that record the view they
  measured (`tailFrom`, cleared results and index length), as the ViewRef
  does.
- The adapter's reported limit, which may change once: a model switch. The
  view's budget comes from the latest planner facts, or from the failed
  request's model during recovery; a summary records the budget it was
  written under.
- Model access: large results render as an omission; some results carry
  server-only data under its own id. Every model-facing read is tracked
  (`exposed`): each planner request's view and checkpoint (planner turns,
  the final turn without tool calls, and requests the provider rejected),
  and `SummarizeContext`'s input.
- Compaction is enabled whenever its preconditions hold, not only after a
  triggering decision: a superset of the triggered behaviour.

## Invariants and properties

| Name | Kind | Design rule |
|---|---|---|
| `RecordBeforeReference` | observed | Nothing in the index or checkpoint refers to content missing from the store (4.4). |
| `ViewWellFormed` | observed | The view starts at an assistant message and no tool result appears without its call (4.6, 4.9). |
| `NothingSilentlyDropped` | observed | Every position before the view is carried by the summary the checkpoint references or listed in its receipt (4.12, 4.15), including a large result: a summary carries only what it read. Structural only: whether a real summary keeps the facts is measured by evals. |
| `ReplayDeterministic` | observed | Recomputing each trigger decision from its recorded facts and recorded ViewRef gives the recorded answer (4.8). |
| `RecoveryShrinks` | rule | Every recovery makes the request strictly smaller, checkpoint included, measured on the retried request itself (the next planner turn, final turn or rejected request) under its budget, against the size of the failed request recorded with its facts (4.15). Recovery chooses only a view whose whole request, rendered under the failed model's budget, is below that size, as the design's "evict until enough" does; otherwise the step fails. Nothing changes the view between the report and the recovery, as in the workflow, which applies an activity result in one step; between the recovery and the retry nothing is appended and no compaction step runs, because the workflow reschedules the planner step at once (4.15). Recovery chooses only a view below the failed size, so this guards that rule and the ordering. The summary size rule (4.12) is not modelled; K15 tests it. |
| `RecoveryReplays` | rule | Each recovery uses the failed request's recorded limit, not the adapter's current one (4.15 step 2, 4.21). |
| `RetryFitsFailedModel` | rule | After a recovery, the checkpoint fits the failed model's budget. |
| `ServerOnlyNeverExposed` | rule | No planner request or summariser input contains server-only data (4.4, P4); server-only ids are never indexed, so this guards the rendering and summariser read rules. Recall is in `RecallDiscovery.tla`. |
| `ClearedSound` | observed | Placeholders only stand for results that exist. |
| `CheckpointBounded` | rule | The rendered checkpoint stays within the current budget (4.6). |
| `Coverage`, `OverflowBounded` | rule | Everything before the view is behind the checkpoint; at most two recoveries per step. |
| `StoreGrows` | observed | The store only grows. |
| `IndexAppendOnly`, `ViewOnlyAdvances` | rule | The index is append-only; the view only moves forward. |
| `RecoveryEnds` (liveness) | observed | A recovery ends in a later successful turn or a failed step, even when the provider keeps rejecting (liveness configuration, two planner turns). Within the bounds, strict shrinking is what ends it; the budget of two is the rule invariant `OverflowBounded`. |
| `Terminates` (liveness) | sanity | The run ends; it follows from the bounds and fairness. |

## Fault and witness configurations

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeReferenceBeforeRecord` | `Provide*` signals a hash before storing the content | `RecordBeforeReference` |
| `UnsafeCutInsideUnit` | Cuts may fall anywhere, not only between units | `ViewWellFormed` |
| `UnsafeUnrecordedLimit` | The trigger reads the adapter's live limit | `ReplayDeterministic` |
| `UnsafeUnrecordedSize` | The trigger measures a size replay cannot reproduce | `ReplayDeterministic` |
| `UnsafeRecoveryWithoutNewView` | Recovery reschedules with the same view (F15) | `RecoveryShrinks` |
| `UnsafeRecoveryIgnoresTarget` | Recovery evicts at any cut instead of until the request is below the failed size; with the limit changing twice, a larger budget makes the summary render larger | `RecoveryShrinks` |
| `UnsafeCompactDuringRecovery` | A pending summary is applied between a recovery and its retry; with a summary cap of 4 (`Limits = {4, 16}`) it renders larger than what it replaces | `RecoveryShrinks` |
| `UnsafeRecoveryKeepsBudget` | Recovery keeps the previous model's budget | `RecoveryReplays` |
| `UnsafeRecoveryReadsAdapter` | Recovery reads the adapter's live limit instead of the recorded facts | `RecoveryReplays` |
| `LivenessUnsafeUnboundedRecovery` | Recovery reschedules without shrinking the view and never fails, so a rejecting provider loops it forever | `RecoveryEnds` |
| `UnsafeSummaryAtOtherCut` | A summary is applied at another cut | `NothingSilentlyDropped` |
| `UnsafeEvictDropsSummary` | Eviction drops the previous summary | `NothingSilentlyDropped` |
| `UnsafeAccessByRendering` | The summariser reads the view's rendering, so a large result's omission replaces its content | `NothingSilentlyDropped` |
| `UnsafeUnboundedReceipt` | Every receipt line renders | `CheckpointBounded` |
| `UnsafeRenderStoredSummary` | A summary renders at the size it was written under after the budget shrinks | `CheckpointBounded` |
| `UnsafeReadServerOnly` | The summariser resolves server-only data | `ServerOnlyNeverExposed` |
| `UnsafeViewRendersServer` | A planner request renders server-only data | `ServerOnlyNeverExposed` |
| `WitnessShrinkThenRecover` | A summary written at limit 8 renders at limit 4 after a recovery that evicted | its witness |

## Assumptions and limits

- One run. Sessions, carried forms, recall, the guard and activity inputs are
  in their own modules; child runs and the root-run store scope are K2's and
  K6's tests.
- `ReceiptCap` is 0, so a receipt renders as one collapsed line: with three
  tool calls and the newest unit kept, at most two calls are ever evicted, so
  a larger cap could never be exceeded and the collapse logic with a cap
  above zero is not exercised; K11's property test covers it.
- The adapter's limit changes once in the main configuration (8 to 4 or 4 to 8,
  with summary caps 2 and 1) and twice in `ContextCompactionTwoChanges.cfg`
  (MaxDecisions 0, no large or server-only results), so a recovery can switch
  to a model with a larger budget than the view was rendered for.
- Planner turns are sequential, and awaits are not modelled separately from
  external results, so the cut rule's condition on calls before the cut is
  never exercised; K11's table tests cover it.
- Adopting a receipt-only `CheckpointRecord` only after the loader stores it
  is abstracted: eviction applies directly.
- Summary rejection and the byte/token size rule are not modelled; K15 tests it
  in `TestSummarizeActivityRejectedResponseRecorded`, `TestWorkflowSummaryAdoption`,
  `TestSummarySizeUsesRecoveryProjection` and `TestSummaryStoredWholeThenBounded`.
  `TestWorkflowSummaryDisableAndRecovery` checks three-rejection disablement and
  record reconstruction. `TestWorkflowSummaryBeforePlanner` proves canonical
  adoption precedes the planner and fallback uses the same cut. A rejected
  response is in the call record; no checkpoint, view or index names it.
- Token arithmetic is abstract: an uncleared result weighs 3, other entries
  1, a summary its cap, a receipt line 1.
- The estimator, the guard and planners that rewrite requests are in
  `OverflowGuard.tla`; pins in K11.
- Temporal replay is modelled through the determinism of decision inputs, not
  by replaying histories.

# SessionContinuation.tla

## What is modelled

- Root runs read the session's head at start and record it as `Prev`, then
  continue (seeding the predecessor's user messages and summary) or start
  fresh; positions follow the chain. Child runs (second safe configuration)
  start, add messages, summarise and seal, and never publish.
- User messages with IDs local to each run, so numbers are reused; carried
  messages keep their origin.
- Summaries cite one user message: one in the view, as the view labels it, or
  one repeated from the previous (possibly copied) summary, as
  `SummarizeContext`'s input shows it.
- `SealRecord` appends `run_sealed`, then publishes with
  `AdvanceContinuation`: one atomic store step with four results (published,
  already published, conflict, superseded). The acknowledgement can be lost,
  so the publish is retried; a retry may present a different body, which the
  store must refuse. Runs can be terminated, overlap and finish late.

## Invariants and properties

| Name | Kind | Design rule |
|---|---|---|
| `NoFalseSupersede` | observed | A run told "superseded" did not publish (4.16). |
| `NoSilentBodyMismatch` | rule | "Already published" is returned only for the stored body; a different body fails loudly (4.16). |
| `PublishedImpliesSealed` | rule | A published continuation comes from a sealed run (4.5). |
| `OneSuccessor` | observed | Published continuations form one chain (4.16). |
| `PositionsFollowChain` | observed | Positions, and so chain labels, follow the chain (4.6). |
| `OrderIsChronological` | observed | Ordering user messages by origin is the order the user wrote them (4.9). |
| `CitationsResolve` | observed | Every citation resolves to the intended message in every run the summary reaches, including after re-summarising (4.9). |
| `ChildNeverPublishes` | rule | Only root runs publish (4.16). |
| `SealedRootsResolve` (liveness) | sanity | A sealed root that is not terminated eventually learns its outcome. It follows from fairness on delivering an acknowledgement and checks only that nothing blocks it. |

## Fault and witness configurations

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeNonIdempotentPublish` | The publish checks only the head | `NoFalseSupersede` |
| `UnsafeAlreadyWithoutCompare` | "Already published" without comparing bodies | `NoSilentBodyMismatch` |
| `UnsafePublishBeforeSeal` | Publish before `run_sealed` | `PublishedImpliesSealed` |
| `UnsafeUnconditionalHead` | The head is set without comparing it to `Prev` | `OneSuccessor` |
| `UnsafeChildPublishes` | A child run publishes | `ChildNeverPublishes` |
| `UnsafeOrderByLocalId` | Messages are ordered by local ID | `OrderIsChronological` |
| `UnsafeBareCitesCurrentRun` | A bare citation in a copied summary resolves in the current run | `CitationsResolve` |
| `UnsafeRawPreviousSummary` | The summariser sees the previous summary's citations unqualified | `CitationsResolve` |
| `UnsafeAdvanceTwoWrites` | `AdvanceContinuation` inserts the continuation, then the worker crashes before the head update; a second run then publishes from the same head | `OneSuccessor` |
| `WitnessAlreadyAfterAdvance` | A retry recognises its own publication after a later run advanced the head | its witness |
| `WitnessResummarizedCopy` | A summary repeats a citation from a summary copied from an earlier run | its witness |

## Assumptions and limits

- Continuing copies every user message of the predecessor's view; carried
  forms and the copy bound are in `ContinuationCarry.tla`, pin and
  user-index selection in K11.
- Whether a run continues is a free choice; matching on `Visible` is K18's
  test. Retention is not modelled.
- The store's conditional update is atomic, and `UnsafeAdvanceTwoWrites`
  shows why: as two writes, a second run publishes from the same head. K18's
  conformance tests check atomicity for the in-memory store and for Mongo
  against a real replica set.

# CallCommit.tla

## What is modelled

- One planner step with up to three attempts (the main configuration), and
  two steps with up to two attempts each and at most one timeout in the run
  (`CallCommitTwoSteps.cfg`, constraint `OneTimeout`), which is enough for a
  zombie of step 1 to write into step 2; the unconstrained two-step space is
  over 50 million states. Each attempt makes a producing call and an
  auxiliary call in order; for each call the wrapper
  takes a sequence number, writes a started record, sends, receives, and
  writes a completed or failed record.
- Crashes between any two steps, timeouts with zombie attempts that keep
  writing records into the next step, lost returns, and a typed step failure
  once the last attempt has timed out.
- A finished attempt stores its output, commits its presentation unless it
  reports an overflow, and returns. The workflow adopts the first return it
  receives, indexes that attempt's output and records its call keys in the
  step event, which skips `BeforeEvent` interceptors.
- Reconstruction (`Classify`, `AdoptedAttempt`, `PresentationAdopted`)
  reads only the store and the run log; ground truth (`adopted`,
  `doneWritten`, each presentation's attempt) is recorded separately.

## Invariants and properties

| Name | Kind | Design rule |
|---|---|---|
| `ClassificationCorrect` | observed | Every call is classified as what happened to it: adopted (auxiliary calls included) against the workflow's actual adoption; abandoned or incomplete against the wrapper's writes, which is close to a restatement because the store is the only record of them (4.5). |
| `IndexMatchesAdoption` | rule | The index references the output of the attempt reconstruction reports as adopted (4.5). |
| `PresentationsClassified` | observed | Every committed presentation is classified correctly as adopted or "presented, not adopted" (4.5). |
| `KeysUnique` | observed | Every started call has its own sequence number (4.13). |
| `SentImpliesRecorded` | rule | No request reaches a provider without a call record (4.5). |
| `NoPresentationFromOverflow` | rule | An overflowing attempt commits no presentation (4.5, 4.15). |
| `StepsEnd` (liveness) | rule | Every step is eventually adopted or fails with a typed error (liveness configuration, one step). |

## Fault and witness configurations

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeSendBeforeStarted` | Send before the started record | `SentImpliesRecorded` |
| `UnsafeAdoptByEntry` | Adoption read from the call a transcript entry names | `ClassificationCorrect` |
| `UnsafeIncompleteAsAbandoned` | Started-only calls reported as abandoned | `ClassificationCorrect` |
| `UnsafeSeqPerAttempt` | Sequence numbers restart per attempt | `KeysUnique` |
| `UnsafeIndexFirstStored` | The index takes the first output stored in the step | `IndexMatchesAdoption` |
| `UnsafePresentationWithoutKey` | Presentation events carry no call key | `PresentationsClassified` |
| `UnsafeOverflowCommitsPresentation` | An overflowing attempt commits its presentation | `NoPresentationFromOverflow` |
| `UnsafeInterceptedRecordEvent` | A `BeforeEvent` interceptor may drop the step event | `ClassificationCorrect` |
| `LivenessUnsafeRetrySameAttempt` | Retries reschedule the same attempt without counting it | `StepsEnd` |
| `WitnessZombieAfterAdoption` | A zombie attempt completes a call record after its step adopted another attempt and the run moved on | its witness |

## Assumptions and limits

- One kind of model-calling activity. `SummarizeContext` attempts follow the
  same rule, with keys in `context_compacted` or `context_compaction_skipped`;
  K8 and K15 test it. K15's `TestSummaryCallCommitCrashWindows`,
  `TestSummaryZombieAttemptAbandoned`, `TestSummaryAdoptionRequiresRecordEvent`,
  `TestWorkflowSummaryAdoption` and `TestSummarizeActivityFailurePolicy` cover
  crash classification, late attempts, canonical adoption and fresh retry keys.
  The three protocol-15 histories in `agent_features` exercise real Temporal
  replay with recorded per-run settings and pinned prompts; protocol 14 is
  rejected before effects. The main and summary histories enable overrides.
  Two further protocol-15 histories record the parent's child-context preparation
  and child-start command, and the independently configured child execution.
- Store writes succeed when they happen; a failed write is a crash at that
  point.

# RequestRecord.tla

Every request of up to five messages, built from a three-message view in any
order, subset or repetition, from two distinct messages of the planner's own,
and from a view message sent with a different `Meta` (the same role and parts,
so the canonical hash of 4.8 matches it), is an initial state. Encoding uses
the view at call time; decoding uses the recorded ViewRef after the view has
moved on.

| Name | Kind | Design rule |
|---|---|---|
| `RoundTrip` | observed | Decoding the segments against the recorded view rebuilds the exact request (4.13). |
| `InPlaceIsOneSpan` | observed | The view sent in place plus additions costs one span plus one segment per addition. |

| Configuration | Broken rule | Expected counterexample |
|---|---|---|
| `UnsafeViewrefPlusAdded` | Revision 7's encoding | `RoundTrip` |
| `UnsafeSpanIgnoresOrder` | Spans record positions, not order | `RoundTrip` |
| `UnsafeHashFirstOwn` | Every own message is hashed as the first | `RoundTrip` |
| `UnsafeDecodeCurrentView` | Decoding against the view at read time | `RoundTrip` |
| `UnsafeMatchCanonicalHash` | Messages matched to the view by the canonical hash, which ignores `Meta` | `RoundTrip` |

Limits: the wrapper's check against the whole-request hash before writing,
and the request fields other than messages, are K8's tests.

# ContinuationCarry.tla

Every final tail of one or two units, the newest with up to three parts
(results, assistant text, tool arguments, reasoning; small or large;
protected or not; rendered whole, as a preview or as a placeholder), is an
initial state; a three-part unit's third part is large text or arguments.
Run 1 seals; run 2 renders the carried entries, may clear results itself,
adds a turn of its own (small or large, so run 1's entries can be carried
again) and seals for run 3.

| Name | Kind | Design rule |
|---|---|---|
| `CarryBounded` | observed | The carried tail fits the cap, or is the newest unit in its minimal form; the witness shows the minimal case is reached (4.16). |
| `EveryPartAccounted` | rule | Every model-visible part of run 1 and run 2 is carried or in the receipt chain. |
| `RenderableFromCopy` | observed | What the next run renders whole was copied whole, against the bytes each carried entry records the copy took. |
| `CopyIsRendered` | rule | The copy holds only what entries render as. |
| `NoReasoningCarried` | rule | No reasoning block crosses runs. |
| `PlaceholdersStick` | rule | A placeholder stays one in the next run. |

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeCopyOriginal` | The copy takes the original behind a preview or placeholder | `CopyIsRendered` |
| `UnsafeCarryReasoning` | Reasoning blocks are carried | `NoReasoningCarried` |
| `UnsafeDropOnEvict` | Evicted units get no receipt lines | `EveryPartAccounted` |
| `UnsafeChainRestarts` | Run 2's receipt does not extend run 1's | `EveryPartAccounted` |
| `UnsafeInheritWatermark` | The next run inherits a cleared watermark | `PlaceholdersStick` |
| `UnsafeNoForcedClear` | Seal does not clear protected results | `CarryBounded` |
| `UnsafeCopyTruncatesFull` | The copy drops the content of an entry that renders whole | `RenderableFromCopy` |
| `UnsafeRecarryRestoresFull` | A result of run 1 carried again into run 3 is restored to whole | `CarryBounded` |
| `WitnessMinimalNeeded` | The minimal form exceeds the cap and is the bound | its witness |
| `WitnessCarriedTwice` | An entry of run 1 is carried into run 3 | its witness |

Limits: sizes are abstract units (small 1, large 3, a minimal preview 1, the
cap 2); the hard bound's dependence on the planner output bounds is P2's
test.

# RecallDiscovery.tla

Every sequence of four message kinds (user, assistant text, tool call,
result, with results paired to earlier calls, so parallel calls occur), split
between run 1 and run 2, and every summary citation with record or forged
text, is an initial state. Run 2 evicts one message per compaction. The agent
may present any token, including a forged one, to `list`; the handler
resolves it only by walking the current checkpoint's `From` chain.

| Name | Kind | Design rule |
|---|---|---|
| `Discoverable` | observed | Every evicted model-visible message is reachable from what the checkpoint renders, through the handler's own resolution of each rendered handle, given enough allowance (P5). |
| `HandlesConfined` | rule | A token resolves only within the chain, and no message of another session is learned (4.3, 4.18). |
| `RecallNeverExposesServerData` | rule | Recall returns eligible evidence only (P4). |
| `UserTextFromRecord` | rule | A cited user message renders the record's text (4.9). |
| `AllowancesBounded` | rule | Refills stop at three times the initial value. |
| `ListingKeepsReads` (action) | rule | Listing never spends the read allowance. |
| `RefillRestoresReads` (liveness) | rule | A spent read allowance is restored by the next compaction, rounding the refill up (liveness configuration, three messages, an allowance of one). |

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeAssistantTextOmitted` | Assistant text gets no receipt line | `Discoverable` |
| `UnsafeCollapseWithoutHandle` | The earlier runs' line has no handle | `Discoverable` |
| `UnsafePairByPrevious` | A result is paired with the message before it | `Discoverable` |
| `UnsafeResolveAnyRecord` | A token resolves to any record in the store | `HandlesConfined` |
| `UnsafeResolveCurrentOnly` | The handler never walks past the current record | `Discoverable` |
| `LivenessUnsafeRefillRoundsDown` | The refill rounds down, so an allowance of one never refills | `RefillRestoresReads` |
| `UnsafeSharedAllowance` | Listings spend reads too | `ListingKeepsReads` |
| `UnsafeCiteTextFromSummary` | A cited message renders the summary's text | `UserTextFromRecord` |
| `UnsafeRecallReadsRecord` | Recall resolves server-only data | `RecallNeverExposesServerData` |
| `WitnessReadAfterRefill` | The agent reads again after exhausting its allowance | its witness |

Limits: a listing returns a handle's lines in one call. K13's
`TestHandlerPagingOffsetsAndFindContinuation` and `TestCursorRoundTrip`
prove page offsets, scan continuation and selector binding;
`TestHandlerFindMiddleLargeResultAndUnsupported` proves literal find in a
5 MB result. `TestHandlerUserTextFromRecordAndSummaryPages` reproduces full
stored UTF-8 summaries. The model has one earlier run and two records;
`TestRecallDiscoverableKindSequences` exhausts short kind sequences with
one to three Prev records and injected From, and `TestRecallScript1000Steps`
proves rendered-input discovery at 200k/8k with call-count testdata.
The RecallDiscovery module is unchanged; all 12 checked-in configurations
pass with their expected success, witness or fault counterexample.

# OverflowGuard.tla

Every combination of view size, planner (passes the view, rewrites it, sends
unrelated messages), calibration, exact counting, known limits and estimate
error and recovery availability is an initial state. A provider overflow may report usage, which
calibrates the estimate for the rest of the step. The invariants read the
history of requests sent and the recorded failure, not the variables the
actions branch on.

| Name | Kind | Design rule |
|---|---|---|
| `FailsOnlyOnProvider` | rule | A failed step is explained by a provider overflow: of the last request sent, or of a request no smaller than the one it would send (4.15). Read from the history of requests, but it mirrors the failure branches. |
| `RetriesShrink` | rule | No request is sent that is no smaller than an earlier provider-rejected one. |
| `BudgetFailureHonest` | rule | A budget failure follows exactly three provider overflows. |
| `SuccessFits` | rule | A successful step's last request fit the window. |
| `Terminates` (liveness) | rule | Every step ends: the guard's rejections and the provider's are bounded (liveness configuration). |

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeGuardFailsStep` | A guard rejection recovery cannot satisfy fails the step | `FailsOnlyOnProvider` |
| `UnsafeCompareLastRejection` | No progress compares with the last rejection of any kind | `FailsOnlyOnProvider` |
| `UnsafeCompareViewOnly` | Progress is measured on the framework's view | `RetriesShrink` |
| `UnsafeGuardSpendsOverflowBudget` | Guard rejections spend the provider budget | `BudgetFailureHonest` |
| `WitnessGuardThenProvider` | A step with a guard rejection and a provider overflow that still succeeds | its witness |
| `LivenessUnsafeGuardNeverOff` | The guard neither turns off nor counts its rejections | `Terminates` |

Limits: one model and one call per attempt; K14 tests matching by model against
the smallest provider-rejected whole-request size in the step, independent of
ordinal or calibration. Here strictly shrinking sends make lastProv that
minimum; sizes are abstract units. Recovery availability abstracts decision 5's
B-derived checkpoint shortfall: only an observed provider overflow can fail that
branch, with no recovery budget spent. Baseline and liveness each explore 2,311
distinct states (864 initial states). All five fault configurations fail their
expected invariant or temporal property; the witness reaches a guard rejection,
provider overflow and success. K14's Go tests drive real Check and Recover across
the core formal flags and calibration branches, scaled together to renderable
budgets, with zero through two units of outside-view overhead. Separate typed
shortfall tests cover unavailable recovery and its workflow diagnostic.


# StepPayload.tla

A run starts with ten entries of caller history, more than three steps'
worth, then runs up to four planner steps of up to two tool calls each, over
two models, with at most one await round between steps; an attempt can crash
after storing its segment.
Reconstruction walks the stored segments back from the head.

| Name | Kind | Design rule |
|---|---|---|
| `InputBounded` | observed | An activity input depends on neither run length nor the initial history (P2). |
| `Reconstructs` | observed | Walking the segments from the head finds exactly the committed entries, with no gap (4.5). |

| Configuration | Broken rule or scenario | Expected counterexample |
|---|---|---|
| `UnsafeSeedInPending` | The caller's history is left pending for the first step | `InputBounded` |
| `UnsafePendingNotFlushed` | The loader does not store pending entries | `InputBounded` |
| `UnsafeAccumulatedHistory` | Inputs carry the planner histories, as today | `InputBounded` |
| `UnsafeHeadBeforeStore` | New entries count as durable before a segment holds them | `Reconstructs` |
| `UnsafeSegmentPrevWrong` | A segment links past its predecessor | `Reconstructs` |
| `WitnessCrashRetry` | A run whose attempt crashed after storing completes every step | its witness |

Limits: sizes count entries, not bytes; P2's byte figures are K7's test.
