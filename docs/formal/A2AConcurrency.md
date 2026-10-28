# A2A concurrency, clarification and projection models

`A2AConcurrency.tla` checks the concurrency core of
[`a2a_interoperability_design.md`](../wips/a2a_interoperability_design.md).
`A2AClarification.tla` adds the acknowledged-clarification slice: competing
commands, occurrence identity, durable outcomes and recovery. `A2AProtocol.tla` reuses both through `INSTANCE` to check
the shared public-projection seam. These are bounded
safety models of the proposed contract, not verification of runtime code or
evidence that an engine already satisfies that contract.

Run with Java and TLA+ tools 1.7.4:

```sh
make a2a-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools-1.7.4.jar
```

The runner copies inputs into a temporary directory, runs exhaustive TLC
breadth-first checks with four workers and a 1 GB heap, and removes traces and
state files on exit. Safe configurations must exit zero and exceed recorded
state-count floors. Every unsafe configuration and reachability witness must
exit 12 and name its expected invariant; parser errors and unrelated failures
do not count as successful negative controls.

## Contract and behavioral matrix

Scope is one immutable task/start identity, one immutable answer command, two
replicas, two lease generations, and up to two dispatch attempts. Store writes
are separate transitions; none is an implicit cross-store transaction.

| Interleaving | Required result | Executable evidence |
| --- | --- | --- |
| Concurrent initial creation, claim and suppression, including an absent admission record | One atomic runtime decision; suppression never clears | Both safe configurations; `UnsafeCrossStoreSuppression`, `UnsafeResetSuppression` |
| Lease replacement with an older process still holding a prepared request | Admission rejects fences below its last committed fence; public commits require the current lease | `UnsafeStaleAdmission`, `UnsafeStaleProjection` |
| Claimed request lost before I/O | Claim remains consumed; task may stay pending forever | `WitnessLostBeforeIO`, `UnsafeZeroHorizonRetry` |
| Timeout, cancel intent and delayed engine acceptance | Timeout is not cancellation evidence; delayed first acceptance remains possible | `UnsafeTimeoutNotFound`, `WitnessDelayedAcceptance` |
| Two certified submissions of one fixed identity | At most one execution, including after engine history expires | `WitnessRetryDedup`, `UnsafeDuplicateExecution`, `UnsafeLateAfterRetention` |
| Crash after suppression and before public projection | Durable suppression supports later canceled projection | `WitnessSuppressionRecovery` |
| Receipt append versus canonical terminal closure | Existing acceptance survives closure; nothing accepts after closure | `UnsafeCloseLosesReceipt`, `UnsafeAppendAfterClose` |
| Activity retry and crash after receipt commit | One receipt; later lookup recovers acceptance after closure | `UnsafeDuplicateReceipt`, `WitnessReceiptRecovery` |
| Cancel intent versus successful completion | Publish the actual completion and preserve terminal public state | `WitnessCompletionWins`, `UnsafeStaleProjection` |
| Definite engine rejection of the sole zero-horizon attempt | Seal the admission and publish failed; a rejection under a positive horizon leaves the identity pending | `WitnessStartRejected`, `UnsafeRejectClosesRetry` |

Required surfaces are the module, its configurations, this evidence file,
`scripts/a2a_model_test.sh`, and `make a2a-model-test`. Runtime implementation,
SDK conformance, schema validation, generated code, dependency work and commits
are excluded.

## State and atomic boundaries

- `Acquire` represents store-time lease expiry and replacement. A replica
  retains an old local fence and prepared request until it crashes or sends it.
  Runtime admission compares the last fence committed in that store; it does
  not pretend the protocol lease and runtime record share a transaction.
  Acquisition does not register the new fence in that separate record. Thus
  `Acquire(r1)`, `Prepare(r1)`, `Acquire(r2)`, `Claim(r1)` is allowed while the
  record remains absent or has not committed a higher fence. The claim/state
  CAS protects suppression versus dispatch; the global lease alone does not
  eliminate this window. `staleClaim` records a successful non-current claim.
  A zero-horizon stale claim lost before I/O still consumes the one attempt and
  leaves cancellation pending; this is an intentional availability tradeoff.
  `Acquire` by the replica that already holds the lease discards its prepared
  request; a real process keeps it, but `Claim` already uses the holder's
  current fence and ignores cancel intent, so no interleaving is lost.
- `Initialize`, `Claim` and `Suppress` compete on the same unique admission
  identity. `Suppress` can create the tombstone directly from `absent`.
  `suppressedEver` records historical truth independently of current admission.
- `Prepare`, `Claim`, `Send`, `Timeout`, `Accept` and `Reject` are distinct
  transitions.
  A committed claim can be lost before I/O; an already authorized sender or
  packet can survive a process crash, lease replacement or timeout. Cancel
  intent prevents preparation of new work; it cannot recall prepared requests.
  `Claim` deliberately does not reread cancel intent: its prepared request may
  reflect a protocol-store read before cancellation. `Suppress` also permits a
  non-current holder when its fence passes the record CAS, although intended
  adapter callers require their current lease. This explores additional safe
  interleavings rather than promising that implementations should dispatch
  suppression from an expired lease.
- `engineRetained` represents the engine's retained duplicate exclusion;
  `executions` counts actual executions and is never forgotten. A successful
  completion can race with cancellation. `Finish("canceled")` abstracts both a
  cancellation request and actual engine termination; durable cancel intent
  alone never means termination. Canonical failures of accepted executions are
  folded into completion for these races. `Reject` is the engine's definitive
  refusal of one in-flight attempt (`ErrWorkflowStartRejected`); it creates no
  execution and is never taken by duplicates or transport errors. At zero
  horizon `SealRejected` moves a claimed admission to `rejected` under the
  record fence; the sealed record admits no claim, initialization or
  suppression, and projection publishes `failed`. Under a positive horizon the
  seal is disabled: a rejection proves nothing about other authorized attempts,
  so the identity stays pending. `ForgetEngine` drops duplicate
  exclusion after the horizon ends, but preserves canonical outcome as durable
  evidence available to projection/completion repair.
- `Reject` records the engine's authoritative refusal as model truth, not a
  durable rejection-response receipt. This abstraction allows more recovery
  than an implementation that loses the response before sealing: that case
  must stay pending, and not-found cannot replace the lost proof. The model
  separates rejection, sealing and public projection but does not model session
  reservation settlement. After a durable seal, implementation recovery must
  retain the original reservation attempt ID, idempotently settle that
  reservation and confirm the bound run, agent and session identity with failed
  status before projecting failure. A nil `RejectRun` result is not settlement
  evidence: both built-in stores permit conditional no-ops. Missing records,
  read errors, identity mismatches and nonfailed statuses leave repair pending.
  The seal, not another engine query, authorizes those repairs. Implementation
  ordering is claim, successful reservation under the stored attempt identity,
  then engine submission; a reservation failure leaves the claim consumed and
  permits no send. Store-level crash/read-back tests must establish this path;
  the existing TLC results do not check it.
- `ScheduleReceipt` represents the workflow's durable decision for the one
  command and schedules an activity. Activity arrival is independent of engine
  termination. `AppendReceipt` and `CloseInput` serialize in the receipt store;
  append is idempotent by command identity. `ResolveReceipt` looks up an
  existing receipt first, then uses the closed fence for a terminal rejection.
- `Project` requires the current task lease and canonical evidence. Public
  terminal projection may precede input closure: the closure prerequisite is
  for resolving an *unreceipted command* as terminal. `CloseInput` represents
  live completion or completion repair, and may be delayed indefinitely.

A crash removes process-local state without deleting durable records or
in-flight effects. Process restart/replacement is another lease acquisition.
Network loss before first I/O and indefinitely delayed actions are allowed.
Storage unavailability is represented by a transition not occurring. There is
no fairness condition. `Next` explicitly permits waiting in every state, so
TLC's enabled deadlock checker is not a progress proof.

## Invariants

“Observed” compares independently retained truth with the externally visible
or reconstructed outcome. “Rule” checks a rule encoded by the model; its
mutation shows that the guard matters, not that a real store implements it.

| Invariant | Kind | Meaning |
| --- | --- | --- |
| `TypeOK` | Rule | Bounded domains and state shapes remain valid |
| `AtMostOneExecution` | Observed | Actual execution count per immutable identity never exceeds one |
| `ZeroHorizonSingleClaim` | Rule | Zero-horizon mode cannot claim a second dispatch, despite two available attempt slots |
| `SuppressionIrreversible` | Observed | Once suppression commits, initialization/replacement cannot remove it |
| `SuppressionExcludesDispatch` | Observed | Committed suppression implies no dispatch ever claimed or executed |
| `NoFalseCanceled` | Observed | Public cancellation has either pre-claim suppression or canonical canceled-execution evidence |
| `NoFalseCompleted` | Observed | Public success corresponds to actual canonical successful completion |
| `NoFalseFailed` | Observed | Public failure is exactly a sealed rejection of the sole attempt: one claim, that packet rejected, no execution and no engine state |
| `RejectionSealed` | Observed | Once `rejectedEver` records a seal it never clears; sealing is zero-horizon-only, with exactly one rejected attempt, no execution and no engine state |
| `TerminalPublicImmutable` | Observed | Public terminal state agrees with the first terminal publication |
| `AdmissionFenced` | Rule | A successful claim never carries a fence below the runtime admission record's last committed fence; this does not require current global lease ownership |
| `ProjectionFenced` | Rule | Public projection requires the current task lease |
| `NoLostReceipt` | Observed | Committed acceptance remains queryable; an accepted reply or resume has that receipt; terminal rejection cannot contradict acceptance |
| `InputCloseFenced` | Observed | Acceptance history cannot change after the input-close snapshot, even before any caller resolves the command |
| `ReceiptUnique` | Observed | At most one durable append for the command despite activity retries |

“No lost receipt” is **durable recoverability and immutable outcome safety**.
It does not mean every scheduled activity eventually commits, every client
receives an answer, or every accepted answer resumes/completes. Cancellation
may stop execution after acceptance but before resume. A pending command can
remain pending forever if the stores/engine never provide further evidence.

## Engine certification finding

The model found why “retry within a finite horizon” alone is insufficient.
`UnsafeLateAfterRetention` changes only `LateExclusion` to false; engine
reject-duplicate behavior while an identity remains retained is unchanged.
TLC finds this sequence:

1. Claim two requests while the certified retry window is open.
2. Accept one request and send the other, whose acceptance is delayed.
3. Complete the first execution, expire the horizon and forget the engine ID.
4. Accept the outstanding request as a new execution: `executions = 2`.

This was a real missing certification requirement in the earlier design.
A bounded client RPC duration does not bound server acceptance. A certificate
may establish duplicate exclusion with an absolute server-side acceptance bound
and retention constraints that keep every authorized request within retained
ID protection. Another engine could provide a different independently verified
guarantee. The model does not assert that Temporal preserves duplicate exclusion
after its ID history is deleted, and it supplies no actual Temporal or Selvedge
certificate.

`CertifiedHorizon` sets `LateExclusion = TRUE` to abstract **only the
no-second-execution consequence** of such a certificate: after retention loss,
an old request cannot create another execution of an already executed identity.
The model does not represent an absolute acceptance deadline and does not
prevent delayed *first* acceptance. It therefore deliberately overapproximates
a real certificate that also excludes every first acceptance after its bound.
`WitnessPositiveHorizonLateFirstAcceptance` captures first acceptance after
client timeout, cancel intent and horizon expiry in the **pre-acceptance state**;
it cannot pass merely because expiry happened later. Both delayed-acceptance
witnesses additionally use the reachability-only state constraint
`horizonOpen \/ claims > 0`, requiring the claim to precede expiry. Safe
configurations retain the broader environment ordering (including early expiry)
and check every safety invariant without this constraint; it selects a causally
meaningful witness rather than narrowing the safety proof.

V1 keeps conservative cancellation: it does not use stronger timing evidence
to turn a claimed-but-unknown execution into public cancellation. This
availability choice is distinct from relying on a certificate for duplicate
exclusion. Canonical terminal evidence or committed pre-claim suppression is
still required. The zero-horizon configuration sets `LateExclusion = FALSE`,
so it does not borrow the positive-horizon assumption. Retention changes,
reset/deletion and namespace replacement must not silently invalidate a real
certificate, whose conditions remain outside this model.

## Negative controls and witnesses

| Configuration suffix | Expected invariant | Defect |
| --- | --- | --- |
| `UnsafeZeroHorizonRetry` | `ZeroHorizonSingleClaim` | Treat zero horizon as permitting a second dispatch |
| `UnsafeCrossStoreSuppression` | `NoFalseCanceled` | Read “unclaimed,” let a competing claim commit, then treat a separate suppression write as success |
| `UnsafeTimeoutNotFound` | `NoFalseCanceled` | Publish cancellation from timeout plus absent engine execution |
| `UnsafeResetSuppression` | `SuppressionIrreversible` | Initialization overwrites a suppression tombstone |
| `UnsafeCloseLosesReceipt` | `NoLostReceipt` | Input closure erases a previously committed receipt |
| `UnsafeAppendAfterClose` | `InputCloseFenced` | Delayed receipt activity accepts after closure |
| `UnsafeDuplicateReceipt` | `ReceiptUnique` | Retried activity appends a second receipt |
| `UnsafeDuplicateExecution` | `AtMostOneExecution` | Engine fails to reject a duplicate retained identity |
| `UnsafeLateAfterRetention` | `AtMostOneExecution` | Engine forgets identity while an authorized duplicate can still arrive |
| `UnsafeStaleProjection` | `TerminalPublicImmutable` | Old lease holder replaces terminal projection with stale nonterminal state |
| `UnsafeStaleAdmission` | `AdmissionFenced` | Old process claims below the runtime record's committed fence |
| `UnsafeRejectClosesRetry` | `NoFalseFailed` | Seal on one rejected attempt under a positive horizon while a second claim exists |
| `UnsafeResetRejection` | `RejectionSealed` | Initialization clears a rejection seal before public failure has been projected |
| `UnsafePositiveHorizonSeal` | `RejectionSealed` | A positive-horizon admission seals even with only one claimed attempt |

The ten `Witness...` configurations keep `Fault = "none"` and all safety
invariants. Their additional negated reachability assertion must fail:
`DelayedAcceptance`, `SuppressionRecovery`, `ReceiptRecovery`,
`CompletionWins`, `LostBeforeIO`, `RetryDedup`, `StaleClaim`,
`StaleClaimLostBeforeIO`, `PositiveHorizonLateFirstAcceptance` and
`StartRejected`, which reaches a failed task with cancel intent. The witness
does not prescribe whether cancellation preceded projection; the checked
terminal-immutability invariant establishes that cancellation cannot reopen it.
The stale
claim witness permits takeover before the new holder's first runtime-record
write; the lost-before-I/O witness identifies that same sole claimed attempt,
cancel intent and a still-nonterminal public task. Recovery witnesses require
crash of the current lease holder after the relevant durable commit and before its observation, then an
observation with the maximum lease generation reached. These show reachable
scenarios; they do not claim that every execution eventually reaches them.

## Recorded verification

Verified on 2026-10-27 with TLA+ tools 1.7.4 and Homebrew OpenJDK
27.0.1.0.3 (`aarch64`), with a 1 GB heap and four TLC workers:

```sh
PATH="/opt/homebrew/opt/openjdk/bin:$PATH" make a2a-model-test \
  TLA2TOOLS_JAR=/Users/luca/.local/share/tlaplus/tla2tools-1.7.4.jar
bash -n scripts/a2a_model_test.sh
git diff --check
```

| Safe configuration | Replicas | Lease generations | Attempt slots | Generated states | Distinct states | Depth |
| --- | --- | --- | --- | --- | --- | --- |
| `ZeroHorizon` | 2 | 2 | 2 (one claim allowed) | 134,831 | 31,248 | 20 |
| `CertifiedHorizon` | 2 | 2 | 2 | 3,791,595 | 717,748 | 25 |

Both safe searches completed with no error and an empty queue. All fourteen
unsafe controls and all ten witnesses produced their exact expected invariant
violation. The runner enforces safe-state floors of 31,000 and 717,000.
The stale-claim and delayed-first-acceptance observers, and the rejection and
seal transitions, distinguish additional histories; the table and floors
reflect them. The later `rejectedEver` observer strengthens the seal property
without adding safe behaviors: in safe executions it is true exactly when the
record is sealed. The reset and positive-horizon controls check permanence
before projection and zero-horizon-only closure separately.

## Assumptions and limits

### Readiness assessment

The original model covers admission/suppression, delayed starts, terminal
projection and a single-command receipt-close race. The companion
`A2AClarification` model now covers the previously identified clarification
readiness gap within explicit finite bounds: competing remote/host commands,
successive occurrences with the same planner label, immutable bindings and
outcomes, identity-scoped cleanup, and recovery across decision/receipt/public
projection boundaries. Its negative controls check each of those safeguards;
its witnesses show the required interleavings are reachable.

This closes the requested *formal coverage* gap for that clarification slice.
The bounded integrated instance below additionally checks selected component
actions together at the public-projection seam. This is not an unbounded
compositional/refinement proof or verification of production stores, workflow
replay, protocol plumbing or engine certification. Those implementation
obligations remain.
The positive-horizon result is conditional on `LateExclusion`; it does not
certify a production engine. Keep zero horizon as the implementation default
until the adapter has independent evidence for that guarantee. Finite state counts do not certify the assumed engine/store contracts.

The model assumes linearizable per-identity admission and receipt operations,
unique immutable identity binding, durable records, monotonically increasing
lease fences, authenticated current-lease projection commits, canonical engine
terminal evidence, and the extra engine guarantee only where explicitly
configured. It does not prove those store/engine implementations.

In `A2AConcurrency`, one identity avoids unrelated cross-task state explosion. Request digests,
tenant authorization, multiple await occurrences, competing answer bodies,
multiple contexts, session reservation, SDK/internal HTTP retries beyond the modeled
attempts, artifact projection, streaming, pagination and garbage collection
are not modeled. Each claimed physical request has one packet delivery. SDK/internal HTTP
retries are not independently modeled: zero horizon must disable unclaimed
physical retries, or every engine-bound submission must be counted under a
certified admission guarantee. The single receipt command has accepted-or-closed
outcomes; this does not establish stale/conflict, digest or multi-command
correctness. Suppression records have no deletion transition; permanent
identity tombstones are an assumption to test in the actual retention system.
Canonical failures of accepted executions are abstracted by terminal completion
because they do not change the cancellation/receipt race. The only public
failure is a sealed zero-horizon start rejection; `NoFalseFailed` and
`RejectionSealed` check it, and `UnsafeRejectClosesRetry` shows that the same
seal under a positive horizon publishes failure beside a live second attempt.
Positive-horizon rejection closure remains deferred and unmodeled. No unbounded inductive proof,
performance guarantee or liveness claim follows from these finite checks.


## A2AClarification: acknowledged-answer model

### Contract, boundaries and finite bounds

The companion starts from one already running task/run identity; the two
standalone clarification configurations do not compose the modules. The
separate integrated instance below reuses selected actions from both; it does
not establish an unbounded refinement relation.
The main configuration has three immutable command IDs: two remote commands
and one host command. It explores two distinct answer bodies and two successive
runtime await occurrences. Both occurrences deliberately share the same
planner-facing label. `CrashRecovery` uses one remote and one host command,
two bodies, two occurrences and one process crash/restart. Both configurations
allow counters to reach two; single-owner, single-public-answer and single-resume
properties are not imposed by one-element domains. The split is a reduction
boundary: crashes only stop transitions while preserving durable state; the
three-command configuration exhausts competition without crashes, while the
two-command configuration exhausts the modeled crash/restart boundaries. It
does not prove every three-command crash interleaving by composition.

`Submit` atomically binds a command ID to an expected occurrence and body.
Remote submission reserves the task only if its single provisional reservation
is free. A different unresolved remote answer gets `answer_pending` without a
binding or effects. Host commands bypass that protocol reservation and can
therefore win against an already admitted remote answer. They participate in
runtime ownership, receipts and resume, but their bodies are never automatically
published into A2A history.

`Decide` is the durable workflow-history transition. It compares the runtime
occurrence sequence, then current ownership, producing accepted, stale or
conflict. Its owner/occurrence snapshot supports an independent classification
check. Only one command can consume an occurrence, even before its receipt
activity finishes. `Persist` is a distinct receipt-store transition. Closure
may beat any as-yet uncommitted decision, producing terminal; an existing receipt
always wins. No decision, including an accepted one, becomes a receipt merely
because a caller observed workflow state.

`Project` is a separate atomic protocol-store transition: record the receipt
outcome, append a remote accepted answer at most once, and release the
provisional reservation only if its command ID still matches. Repeated
projection/cleanup remains enabled after the first commit. Thus an old rejected
command can replay cleanup while a newer remote command owns the reservation.
It cannot erase that reservation or the runtime's winning host/remote owner.

`Resume` requires an accepted canonical receipt and records one logical resume
for that occurrence. `Advance` creates the next runtime occurrence after that
resume, reusing the same planner label. Termination may win before a receipt or
resume. After input closure, unreceipted commands resolve terminal; previously
committed accepted, stale or conflict receipts remain unchanged.

`Retry` uses the retained receipt for an equal binding. Changed body or
occurrence under the same command ID returns conflict without rebinding it or
changing the original receipt. The model records the **last** retry response
and its observed occurrence/closed state; checking every successor state is
sufficient to detect any incorrect response without accumulating an arbitrary
set of independent client observations.

Crashes occur after a remote command's durable workflow decision but before its
receipt, or after its receipt but before protocol projection. Workers and the
reconciler stop until restart; durable state survives. Engine termination can
still occur while they are down. No fairness assumption forces restart, receipt
commit, projection or resume. Unknown work may remain pending indefinitely.

### Behavioral proof matrix

| Interleaving | Required behavior | Invariant/control or witness |
| --- | --- | --- |
| Two commands compete for one occurrence | One durable workflow owner; winner survives loser cleanup | `OneAcceptedOwner`, `OwnershipPreserved`; competing-acceptance and rejection-erases-owner faults |
| Old command arrives after same planner label is reused | Sequence comparison rejects it as stale | `AcceptedMatchesOccurrence`, `DecisionSound`; planner-label-only and stale-as-conflict faults |
| Equal retry after await advance or closure | Return the original accepted/stale/conflict/terminal receipt | `ReceiptImmutable`, `RetryOutcomeStable`; retry-redecides fault, stale/conflict retry witnesses |
| Same command ID with changed body/occurrence | Conflict reply; preserve original binding and receipt | `BindingImmutable`, `ReceiptMatchesRequest`; changed-body-rebinds fault and witness |
| Projection commits its receipt outcome | Release its own reservation and include exactly one accepted answer in that commit | `ProjectionComplete`; retain-reservation and omit-public-answer faults |
| Rejected cleanup retries after a new reservation | Release only the old command's own reservation; preserve winner | `ReservationAccounted`; release-other fault and cleanup-after-new-reservation witness |
| Workflow decision is visible before receipt commit | Neither publish nor resume from that decision alone | `PublicFromReceipt`, `ResumeFromReceipt`; project-decision and resume-decision faults |
| A rejected command is delivered/retried | No resume or public answer | Receipt/public/resume provenance invariants; resume-rejection fault |
| Accepted projection or resume is retried | One public answer and one logical resume per occurrence | `PublicAnswerOnce`, `ResumeOnce`; duplicate-public-answer and duplicate-resume faults |
| Input closes before pending receipt activity | Terminal outcome without late acceptance or resume | `InputCloseFenced`, `ReceiptAuthority`; append-after-close fault and close-wins witness |
| Crash at either durable boundary | Restart recovers canonical outcome and publishes exactly once | Decision-crash-recovery and receipt-crash-recovery witnesses |
| Consecutive remote answers | Both occurrences can accept and resume despite a reused planner label | Two-occurrences witness |
| Another remote answer arrives while reserved | No second provisional binding or runtime command | Answer-pending witness plus `ReservationAccounted` |

`ReceiptImmutable` and `ReceiptMatchesRequest` compare the current durable
record with the first committed outcome/binding. `DecisionSound` compares the
recorded decision with the occurrence and owner snapshot at that decision.
`OneAcceptedOwner` checks a historical set of accepted command identities per
occurrence, independently of the mutable current owner. `OwnershipPreserved`
checks that cleanup never deletes that winner. `ProjectionComplete`
checks that recording a public outcome releases its own provisional slot and
includes exactly one accepted answer (or no rejected answer) in the same atomic commit; it is a postcondition, not a liveness assumption. `ResumeFromReceipt` checks each
actual resume's command and occurrence; its count can reach two under the
negative control. `PublicFromReceipt` checks publication against canonical
receipts and restricts it to admitted remote commands.

### Clarification negative controls

All configurations below use the prefix `A2AClarification`. Each fault must
fail its named invariant with TLC exit code 12.

| Suffix | Expected invariant |
| --- | --- |
| `UnsafeCompetingAcceptance` | `OneAcceptedOwner` |
| `UnsafePlannerLabelOnly` | `AcceptedMatchesOccurrence` |
| `UnsafeStaleAsConflict` | `DecisionSound` |
| `UnsafeRetryRedecides` | `ReceiptImmutable` |
| `UnsafeChangedBodyRebinds` | `BindingImmutable` |
| `UnsafeReleaseOther` | `ReservationAccounted` |
| `UnsafeRetainReservation` | `ProjectionComplete` |
| `UnsafeOmitPublicAnswer` | `ProjectionComplete` |
| `UnsafeRejectionErasesOwner` | `OwnershipPreserved` |
| `UnsafeProjectDecision` | `PublicFromReceipt` |
| `UnsafeDuplicatePublicAnswer` | `PublicAnswerOnce` |
| `UnsafeResumeDecision` | `ResumeFromReceipt` |
| `UnsafeResumeRejection` | `ResumeFromReceipt` |
| `UnsafeDuplicateResume` | `ResumeOnce` |
| `UnsafeAppendAfterClose` | `InputCloseFenced` |

The ten `Witness...` configurations keep all safety invariants and no fault:
`TwoOccurrences`, `HostConflictCleanup`, `StaleRetry`, `ConflictRetry`,
`CleanupAfterNewReservation`, `ChangedBodyRetry`, `DecisionCrashRecovery`,
`ReceiptCrashRecovery`, `CloseWins`, and `AnswerPending`. Only the negated
reachability assertion may fail. Retry witnesses explicitly record that the
retry happened after closure or await advance; recovery witnesses identify the
same crashed command subsequently published as accepted.

### Clarification verification and limits

The `make a2a-model-test` command above retains both component suites. The
frozen clarification module was checked on 2026-10-27 with TLA+ tools 1.7.4
and Homebrew OpenJDK 27.0.1.0.3:

| Safe configuration | Remote / host commands | Bodies / occurrences | Crashes | Generated states | Distinct states | Depth | Runner floor |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `A2AClarification` | 2 / 1 | 2 / 2 | 0 | 23,310,940 | 1,053,425 | 20 | 1,053,000 |
| `A2AClarificationCrashRecovery` | 1 / 1 | 2 / 2 | 1 | 622,926 | 47,143 | 16 | 47,000 |

Both safe searches completed without errors and with empty queues. The fifteen
clarification fault controls target the exact invariants listed above; the ten
witnesses retain every safe invariant and fail only their intended negated
reachability check. `bash -n scripts/a2a_model_test.sh` and `git diff --check` also pass.

Parallel-worker depth diagnostics may vary between runs. Distinct-state
counts, empty queues and invariant results are the completion evidence, and the
runner does not assert a fixed depth.

Independent review added two atomic projection postconditions: the published
outcome must release its own reservation, and accepted publication must include
one public answer. `UnsafeRetainReservation` and `UnsafeOmitPublicAnswer` both
exit 12 with `ProjectionComplete` violations. Without those checks, a model
could publish an outcome while leaving the task permanently busy or omitting
the accepted answer; reachability witnesses alone would not detect that defect.

These checks abstract body/digest equality as two distinct body values. They do
not verify canonical JSON serialization, cryptographic hashes, arbitrary text,
await-kind validation or all missing-fields/tool-pause paths. The occurrence
sequence is monotone by construction; increment/replay correctness requires
runtime tests. `Project` assumes the accepted public-history append, outcome
update and identity-checked reservation release are one atomic protocol-store
commit. Individual backend writes inside that transaction are not modeled.

The model uses a single workflow's serial durable decisions. Physical activity
attempts are abstracted by repeated enabled receipt/projection/resume operations
and one process replacement; it does not verify an engine's history semantics.
It assumes durable store writes, immutable identity keys and retained receipt
lookup. Authorization, cross-task interactions, admission/lease fencing and
engine deduplication remain in the original model or implementation obligations.
No liveness, unbounded-induction or whole-protocol verification claim follows.


## A2AProtocol: bounded integrated projection seam

### Gap addressed and contract

Separate component checks could not catch a receipt reconciler, holding a valid
current lease, reopening an already completed/canceled public task or hiding a
newer clarification await. They also could not distinguish an obsolete logical
projection from a correctly rebuilt candidate using a fresh database revision.
`A2AProtocol` adds that shared public lifecycle and ordered event consumption.
It instantiates the existing modules without copying or modifying their actions:
`C` maps `A2AConcurrency.s` to `a`; `Q` maps `A2AClarification.s` to `q`.

| Interleaving | Required result | Check |
| --- | --- | --- |
| Claimed start remains delayed or uncertain | No runtime answer decision/receipt before accepted execution | `WorkHasExecution`; work-before-start fault |
| Accepted receipt commits before planner resume | Its fresh public projection may set `WORKING` and append the answer | Fresh-receipt-working witness |
| Completion/cancellation repair wins before answer projection | Terminal state remains absorbing while accepted history and reservation bookkeeping finish | `TerminalAbsorbing`, component `ProjectionComplete`; separate completed/canceled late-history witnesses |
| An accepted receipt is replayed after await 2 is published | Preserve await 2 and its input-required state; history stays once-only | `NewerAwaitPreserved`, `PublicLifecycleMatches`; receipt-hides-await fault and replay witness |
| Current lease holder prepares against an older public revision | Reject the stale candidate, then rebuild from current state/source | Rebase-terminal/rebase-await faults and stale-candidate-rebuilt witness |
| A newer lease holder resumes reconciliation | Old candidate fence is rejected; current holder still performs the same terminal/source checks | Lease-replacement safe configuration |
| Terminal repair precedes unconsumed receipt events | Preserve the event cursor and consume remaining events in order | `CursorComplete`; late-history witnesses |

### Mapping, event order and atomicity

The composed start path reuses `C.Acquire`, crash, prepare, initialization,
claim, send/loss/timeout, acceptance, cancellation intent and suppression.
It uses zero horizon with **two available attempt slots**: the original claim
guard supplies the single-send restriction. Runtime acceptance creates the
first canonical await event. Commands cannot decide or persist before that
accepted execution exists. Remote command admission also requires the publicly
advertised matching input-required occurrence.

`C.Finish` and `Q.Terminate` are one transition representing the same canonical
engine termination fact. `C.CloseInput` and `Q.CloseInput` are one transition
representing the same input fence; they are not two independently committed
stores. The original single-receipt submachine in `C` is disabled and replaced
by `Q`'s command/receipt semantics. Workflow decisions, receipt appends,
resumes and await advancement reuse `Q` actions. Receipt append, resume and
await creation add distinct events to the ordered canonical event sequence.
Canonical failures of accepted executions remain abstracted by completion;
the admission instance keeps `Reject` and the zero-horizon `SealRejected`, so
a failed public task is reachable here too and `PublicTerminalTruth` requires a
sealed admission with no execution behind it.

The shared public record has separate status, occurrence, lifecycle-source
position, consumed event cursor, row revision and terminal-evidence latch.
A new cursor step consumes exactly the next event. Receipt consumption performs
`Q.Project` for remote commands in the same protocol-store transaction as the
cursor update: accepted history, outcome and identity-scoped reservation release
are not split writes. Replays of consumed events are allowed and remain
idempotent. A fresh **accepted** receipt is lifecycle evidence for `WORKING`
even before resume; rejected receipts are bookkeeping only. Await and resume
events also carry lifecycle evidence.

Completion repair reuses `C.Project` and writes the shared public terminal
state/revision from canonical engine or suppression evidence. It **does not
advance the event cursor**. Pending accepted receipt projection can therefore
still append history and release its reservation after public completion or
cancellation. The later receipt transaction preserves terminal status. This is
separate from normal ordered replay: publishing await 2 cannot skip the earlier
receipt/resume events. The successor-await witness first projects receipt 1,
then publishes await 2, then replays receipt 1.

Two candidate slots model concurrent prepared operations under one current
lease. `PrepareProjection` captures the lease fence, expected row revision,
event source and merged status/occurrence. A candidate can remain outstanding
while another operation advances the projection or publishes terminal repair.
`CommitProjection` checks both the current lease fence and expected revision.
A failed revision check is rejected; rebuilding rereads current lifecycle state
and compares source position. Merely assigning an old candidate a fresh revision
is unsafe. Fresh preparation preserves terminal state and ignores lifecycle
sources older than the latest already projected source.

These guards solve different races. CAS prevents a prepared snapshot from
blindly overwriting intervening work. Logical-source ordering prevents an old
event freshly prepared against the current row from regressing it. Terminal
state remains absorbing even when a delayed receipt event has a higher log
position than the evidence used for completion repair.

### Invariants, faults and witnesses

`ComponentSafety` checks the applicable original admission and clarification
invariants in the integrated state, including execution uniqueness, suppression,
immutable receipts, occurrence ownership, accepted-only history/resume,
projection completeness and once-only bookkeeping. `LifecycleCoupled` checks
that engine termination and the input-close fence map to the same facts in both
instances. `WorkHasExecution` requires actual accepted execution before runtime
command decisions/receipts.

`PublicLifecycleMatches` reconstructs the expected live public status and
occurrence from the latest consumed await, resume or accepted-receipt event;
terminal evidence takes precedence. `NewerAwaitPreserved` specifically checks
that a latest consumed await remains visible with its own occurrence.
`TerminalAbsorbing` compares public status to the first terminal evidence.
`PublicTerminalTruth` checks terminal outcome against engine, suppression and
sealed-rejection truth.
`CursorComplete` checks that every consumed remote receipt has its canonical
outcome projected. `CommitAuthorized` records pre-commit validity of the captured
lease fence and expected revision in a monotone bad-commit flag. It catches
unauthorized commits even when their public result happens to be correct; it
does not compare historical commits with a later lease or revision. State and candidate type invariants bound all snapshots.

| Integrated fault suffix | Expected invariant | Causal defect |
| --- | --- | --- |
| `UnsafeReceiptReopensTerminal` | `TerminalAbsorbing` | Freshly prepared accepted-receipt status ignores the existing terminal state |
| `UnsafeReceiptHidesAwait` | `NewerAwaitPreserved` | Old accepted-receipt replay ignores its older occurrence/source and replaces await 2 |
| `UnsafeRebaseTerminal` | `TerminalAbsorbing` | A current-holder stale candidate bypasses expected-revision CAS after terminal repair |
| `UnsafeRebaseAwait` | `NewerAwaitPreserved` | A current-holder stale candidate bypasses CAS after a newer await projection |
| `UnsafeWorkBeforeStart` | `WorkHasExecution` | Runtime command work is allowed without accepted start evidence |
| `UnsafeStaleLeaseCommit` | `CommitAuthorized` | Prepared candidate commits after lease replacement without any row-revision change |

The unconditional-receipt mutation applies only when the receipt is obsolete
against terminal state or a newer occurrence. It does **not** treat ordinary
accepted-before-resume `WORKING` as an error. The two rebase controls use current
lease holders, so lease fencing cannot hide the missing revision check. The
stale-lease control instead keeps the expected revision valid and invalidates
only the captured lease; its prefix reaches the same stale candidate in the
safe model, where commit is disabled and rejection remains enabled.

Six safe witnesses check `LateAfterCompleted`, `LateAfterCanceled`,
`ReplayAfterNewAwait`, `StaleCandidateRebuilt`, `DelayedStartThenAnswer` and
`FreshReceiptWorking`. The late-history flags require the **first** public
projection of an accepted receipt after the named terminal state, not a repeat
of an already published answer. The rebuild witness requires rejection of a
current-holder older-occurrence candidate while await 2 is visible, followed by
a newly prepared old-receipt replay that preserves await 2. The fresh-receipt
witness requires accepted public history and `WORKING` while the occurrence's
resume count is still zero. Every witness retains all safe invariants and must
fail only its negated reachability assertion.

### Bounds, evidence and remaining obligations

The successor-await configuration uses one replica/lease generation, one remote
command, one body, two occurrences and two prepared-candidate slots. The
lease-replacement configuration uses two replicas/two generations, one remote
command/body, one occurrence and two candidate slots. `Combined` exercises
two replicas, two lease generations and two occurrences together, with the
same one remote command/body and two candidate slots. All three expose two start
attempt slots under zero-horizon admission. Body/host competition remains in
the broader clarification configurations; these integrated bounds target the
projection seam rather than the full arbitrary Cartesian product.

The canonical event sequence is bounded by the one-shot receipt identities and
occurrence count. Row revisions increase only for a new consumed event or the
single terminal-repair publication; idempotent replay cannot create an
unbounded revision loop. Process crashes in this instance remove adapter-local
lease state while durable engine/receipt state survives; workflow crash replay
is separately covered by the clarification configurations. No new fairness or
conditional-liveness property is asserted: the concrete gap is a safety failure
at projection commit, and uncertain starts still may remain pending forever.

The final integrated checks used TLA+ tools 1.7.4 and Homebrew OpenJDK
27.0.1.0.3 (`aarch64`) on 2026-10-27. All three safe searches completed
with empty queues:

| Safe configuration | Replicas / generations | Occurrences | Generated states | Distinct states | Reported depth | Runner floor |
| --- | --- | --- | --- | --- | --- | --- |
| `A2AProtocol` | 1 / 1 | 2 | 2,136,410 | 388,926 | 32 | 388,000 |
| `A2AProtocolLeaseReplacement` | 2 / 2 | 1 | 22,696,343 | 3,713,558 | 29 | 3,713,000 |
| `A2AProtocolCombined` | 2 / 2 | 2 | 80,796,209 | 12,950,870 | 34 | 12,950,000 |

The strengthened combined configuration completed in 8 min 18 s with a 1 GB
heap and four workers; lease replacement completed in 1 min 15 s. An earlier
contended combined run took 21 min 44 s. These are measured local
runtimes, not performance guarantees. Earlier
resource-contended searches were stopped before completion and are not counted
as verification evidence. The completed run uses the full configured graph,
without symmetry reduction. TLC uses fingerprints to identify visited states;
for the combined search it reported collision estimates of `4.8E-5`
(optimistic calculation) and `4.1E-6` (actual fingerprints), so this remains
model-checking evidence rather than a machine-checked deductive proof.

All six integrated fault controls produced their exact expected violations
with exit code 12; all six witnesses produced their intended negated-invariant
violation while retaining every safe invariant. The stale-lease trace captures
fence 1 and base revision 0, acquires generation 2 without changing revision 0,
then demonstrates the forbidden commit. The added authorization observer is
always false in safe executions and does not add reachable safe states.

The strengthened combined command completed with exit code **zero** on 2026-10-27:
**seven safe configurations, thirty-five fault controls and twenty-six
reachability witnesses** across all three modules. Every safe search exhausted
its queue; every fault and witness returned exit code 12 for its exact intended
invariant. Both new controls failed `RejectionSealed`. All safe state counts
remain unchanged by the historical observer and match the tables above. The
existing state floors were preserved; no model or configuration changed after
the run. Tables and the rerunnable command record the evidence; diagnostic
traces are not committed. The combined command can take tens of minutes.
Shell syntax and diff checks pass. The original
clarification evidence above is unchanged.

Independent review found no remaining concrete blockers in the modeled
projection boundary.

This is executable **bounded integration** through reused component actions,
not an unbounded compositional theorem, checked refinement to runtime code,
backend atomicity verification or engine certification. Canonical event order,
transactional protocol commits, durable identity/receipt stores and the mapping
of engine terminal facts remain implementation obligations.
