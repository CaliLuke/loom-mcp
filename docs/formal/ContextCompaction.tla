-------------------------- MODULE ContextCompaction --------------------------
(***************************************************************************)
(* One agent run under the context-compaction design in                   *)
(* docs/wips/context_compaction.md (revision 9).                           *)
(*                                                                         *)
(* Content ids stand in for content hashes and are named after what       *)
(* produced them, so equal histories are equal states. The run store only *)
(* grows; the workflow keeps a transcript index of entries that refer to   *)
(* stored content or are structural (ref = None, rendered from templates). *)
(* The model's view is the index from tailFrom onwards plus a checkpoint   *)
(* whose summary and receipt carry every position before the view. What a  *)
(* checkpoint carries is derived from what it references (Carried).        *)
(*                                                                         *)
(* Compaction actions are enabled whenever their preconditions hold, not   *)
(* only after a triggering decision: a superset of the triggered          *)
(* behaviours, so the safety properties also hold for the trigger.         *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
    ActivityCalls,  \* tool calls answered by tool activities
    ExternalCalls,  \* tool calls answered by Provide* signals
    MaxPlans,       \* bound on planner turns
    MaxDecisions,   \* bound on recorded trigger decisions
    Limits,         \* context limits an adapter may report over time
    ReceiptCap,     \* receipt lines the checkpoint renders before collapsing
    MaxLimitChanges,\* how often the adapter's limit may change (a model switch)
    LargeCalls,     \* activity calls whose result the view renders as an omission
    ServerCalls,    \* activity calls whose result also has server-only data
    Fault           \* "none", or one rule deliberately broken

Calls == ActivityCalls \cup ExternalCalls

Faults == {"none", "reference_before_record", "cut_inside_unit",
           "unrecorded_limit", "recovery_without_new_view",
           "summary_at_other_cut", "evict_drops_summary",
           "unbounded_receipt", "render_stored_summary",
           "access_by_rendering", "read_server_only", "view_renders_server",
           "unrecorded_size", "recovery_keeps_budget", "recovery_reads_adapter",
           "unbounded_recovery", "recovery_ignores_target", "compact_during_recovery"}

ASSUME ActivityCalls \cap ExternalCalls = {}
ASSUME Fault \in Faults
ASSUME Limits \subseteq Nat /\ Limits # {}
ASSUME ReceiptCap \in Nat
ASSUME LargeCalls \cup ServerCalls \subseteq ActivityCalls

VARIABLES
    store,      \* content ids written to the run store
    idx,        \* transcript index (sequence of entries)
    planned,    \* tool calls issued so far
    resRef,     \* call -> content id of its result, or None
    tailFrom,   \* first index position in the model's view
    ckpt,       \* [through, ref, receipt]: positions 1..through are behind the
                \* checkpoint; ref is the latest summary (None if only receipts);
                \* receipt is the positions evicted since that summary
    cleared,    \* calls whose results render as placeholders
    summary,    \* validated summary written but not applied, or None
    limit,      \* the limit the adapter would report right now
    changed,    \* how many times the adapter's limit has changed
    facts,      \* recorded planner facts: the limit reported at each turn
    decisions,  \* recorded trigger decisions
    overflows,  \* overflow recoveries spent in the current planner step
    stuck,      \* an overflow recovery left the view no smaller
    plans,      \* planner turns completed
    failed,     \* the run failed with a typed overflow error
    exposed,    \* content ids that model-facing paths have read
    budget,     \* the context limit the view's RenderBudget is computed from
    recovered,  \* an overflow recovery has run since the last planner turn
    failedLimit,\* the limit of the model whose request failed in that recovery
    pending,    \* an overflow reported but not yet applied: the failed request's limit, 0 if none
    failedSize, \* the size of that failed request, recorded with its facts
    final       \* the final planner turn, without tool calls, has run

vars == <<store, idx, planned, resRef, tailFrom, ckpt, cleared, summary,
          limit, changed, facts, decisions, overflows, stuck, plans, failed,
          exposed, budget, recovered, failedLimit, pending, failedSize, final>>

None == [t |-> "none"]

Entry(kind, calls, ref) == [kind |-> kind, calls |-> calls, ref |-> ref]

\* Content ids, named by producer.
UserCid == [t |-> "user"]
AsstCid(n) == [t |-> "asst", n |-> n]
ResCid(c) == [t |-> "res", c |-> c]
\* Server-only data of a result (ServerData): stored, never model-facing.
ServerCid(c) == [t |-> "server", c |-> c]
\* A summary of the previous checkpoint plus positions from..to-1; basis is
\* the set of positions its text carries; made is the context limit of the
\* budget it was written under.
SumCid(from, to, basis, made) ==
    [t |-> "sum", from |-> from, to |-> to, basis |-> basis, made |-> made]

Init ==
    /\ store = {UserCid}
    /\ idx = <<Entry("user", {}, UserCid)>>
    /\ planned = {}
    /\ resRef = [c \in Calls |-> None]
    /\ tailFrom = 1
    /\ ckpt = [through |-> 0, ref |-> None, receipt |-> {}]
    /\ cleared = {}
    /\ summary = None
    /\ limit \in Limits
    /\ changed = 0
    /\ facts = <<>>
    /\ decisions = <<>>
    /\ overflows = 0
    /\ stuck = FALSE
    /\ plans = 0
    /\ failed = FALSE
    /\ exposed = {}
    /\ budget = limit
    /\ recovered = FALSE
    /\ failedLimit = limit
    /\ pending = 0
    /\ failedSize = 0
    /\ final = FALSE

-----------------------------------------------------------------------------
(* Derived state.                                                           *)

Positions == 1..Len(idx)

ResultAt(c) == {i \in Positions : idx[i].kind = "result" /\ c \in idx[i].calls}

Resolved(c) == ResultAt(c) # {}

Open == {c \in planned : ~Resolved(c)}

\* Weight of one entry in the view: an uncleared result is large.
Weight(e, cl) ==
    IF e.kind = "result" /\ ~(e.calls \subseteq cl) THEN 3 ELSE 1

RECURSIVE SizeFrom(_, _)
SizeFrom(i, cl) == IF i > Len(idx) THEN 0 ELSE Weight(idx[i], cl) + SizeFrom(i + 1, cl)

ViewSizeOf(t, cl) == SizeFrom(t, cl)

ViewSize == ViewSizeOf(tailFrom, cleared)

\* Tool calls a checkpoint's receipt lists: one line per evicted call.
ReceiptCalls(ck) == UNION {idx[i].calls : i \in {j \in ck.receipt : idx[j].kind = "asst"}}

\* Rendered receipt: the newest ReceiptCap lines and one line for the
\* collapsed rest. The broken configuration renders every line.
ReceiptSize(ck) ==
    LET n == Cardinality(ReceiptCalls(ck))
    IN IF Fault = "unbounded_receipt" \/ n <= ReceiptCap THEN n ELSE ReceiptCap + 1

\* The current budget comes from the latest recorded facts: a planner turn's,
\* or the failed request's in an overflow recovery, so a smaller model lowers
\* it for every later rendering. MaxSummaryTokens scales with it.
CurLimit == budget

SummaryCap(l) == l \div 4

Min(a, b) == IF a <= b THEN a ELSE b

\* A summary renders whole within the current cap, else as a bounded summary.
\* The broken configuration renders the stored summary at the size it was
\* written under.
SummarySizeAt(ck, l) ==
    IF ck.ref = None THEN 0
    ELSE IF Fault = "render_stored_summary" THEN SummaryCap(ck.ref.made)
    ELSE Min(SummaryCap(ck.ref.made), SummaryCap(l))

SummarySize(ck) == SummarySizeAt(ck, CurLimit)

\* Rendered checkpoint: the rendered summary plus the rendered receipt.
CheckpointSizeAt(ck, l) == SummarySizeAt(ck, l) + ReceiptSize(ck)
CheckpointSizeOf(ck) == CheckpointSizeAt(ck, CurLimit)

\* What the model receives from the view: checkpoint and tail.
RequestSizeAt(t, cl, ck, l) == CheckpointSizeAt(ck, l) + ViewSizeOf(t, cl)
RequestSizeOf(t, cl, ck) == RequestSizeAt(t, cl, ck, CurLimit)

\* The view size over positions t..len, as it was when the index had len
\* entries: the index is append-only, so that prefix never changes.
RECURSIVE SizeRange(_, _, _)
SizeRange(i, len, cl) == IF i > len THEN 0 ELSE Weight(idx[i], cl) + SizeRange(i + 1, len, cl)

\* What a planner request renders: the view's entries (a cleared result
\* renders as a placeholder, so its content is not read) and the
\* checkpoint's summary. The broken configuration also renders server-only
\* data of the results in the view.
ViewReads ==
    {idx[i].ref : i \in {j \in tailFrom..Len(idx) :
                          idx[j].ref # None /\ ~(idx[j].kind = "result" /\ idx[j].calls \subseteq cleared)}}
    \cup (IF ckpt.ref = None THEN {} ELSE {ckpt.ref})
    \cup (IF Fault = "view_renders_server"
          THEN {ServerCid(c) : c \in UNION {idx[j].calls : j \in {i \in tailFrom..Len(idx) : idx[i].kind = "result"}}} \cap store
          ELSE {})

RequestSize == RequestSizeOf(tailFrom, cleared, ckpt)

LastAsst == LET A == {i \in Positions : idx[i].kind = "asst"}
            IN IF A = {} THEN 0 ELSE CHOOSE i \in A : \A j \in A : j <= i

\* A cut at p starts the view at an assistant message, keeps the newest
\* unit, and separates no tool call from its result: every call issued
\* before p already has its result before p.
ValidCut(p) ==
    /\ p > tailFrom
    /\ p <= Len(idx)
    /\ IF Fault = "cut_inside_unit"
          THEN TRUE
          ELSE /\ idx[p].kind = "asst"
               /\ p <= LastAsst
               /\ \A j \in 1..(p - 1) :
                      idx[j].kind = "asst" =>
                          \A c \in idx[j].calls :
                              Resolved(c) /\ \A r \in ResultAt(c) : r < p

\* Measured at the retry: a request sent after a recovery must be smaller
\* than the failed request (its size recorded with its facts). The flag is
\* sticky, so RecoveryShrinks fails once any retry is not smaller.
RetryStuck == stuck \/ (recovered /\ RequestSize >= failedSize)

\* Results tier 1 may clear: in the view, not in the newest unit.
Eligible == {c \in Calls :
                /\ Resolved(c)
                /\ c \notin cleared
                /\ \A r \in ResultAt(c) : r >= tailFrom /\ r < LastAsst}

\* Positions the checkpoint carries: those its summary text covers plus those
\* its receipt lists.
Carried == (IF ckpt.ref = None THEN {} ELSE ckpt.ref.basis) \cup ckpt.receipt

\* The checkpoint after receipt-only eviction at p: it keeps the previous
\* summary and receipt and adds receipt lines for tailFrom..p-1. The broken
\* configuration replaces the checkpoint with the new receipt alone.
EvictCheckpoint(p) ==
    IF Fault = "evict_drops_summary"
       THEN [through |-> p - 1, ref |-> None, receipt |-> tailFrom..(p - 1)]
       ELSE [through |-> p - 1, ref |-> ckpt.ref,
             receipt |-> ckpt.receipt \cup (tailFrom..(p - 1))]

-----------------------------------------------------------------------------
(* Producers.                                                               *)

\* The planner activity writes the assistant message, then completes and
\* returns its request facts, including the limit the adapter reported.
Plan(C) ==
    /\ ~failed
    /\ ~final
    /\ pending = 0
    /\ plans < MaxPlans
    /\ Open = {}
    /\ C # {} /\ C \subseteq Calls \ planned /\ Cardinality(C) <= 2
    /\ store' = store \cup {AsstCid(plans + 1)}
    /\ idx' = Append(idx, Entry("asst", C, AsstCid(plans + 1)))
    /\ planned' = planned \cup C
    /\ plans' = plans + 1
    /\ facts' = Append(facts, limit)
    /\ budget' = limit
    /\ recovered' = FALSE
    /\ exposed' = exposed \cup ViewReads
    /\ overflows' = 0
    /\ stuck' = RetryStuck
    /\ UNCHANGED <<resRef, tailFrom, ckpt, cleared, summary, limit, changed,
                   decisions, failed, failedLimit, pending, failedSize, final>>

\* A tool activity stores the full eligible result and, separately, its
\* server-only data before returning.
ToolPut(c) ==
    /\ c \in Open \cap ActivityCalls
    /\ resRef[c] = None
    /\ store' = store \cup {ResCid(c)} \cup (IF c \in ServerCalls THEN {ServerCid(c)} ELSE {})
    /\ resRef' = [resRef EXCEPT ![c] = ResCid(c)]
    /\ UNCHANGED <<idx, planned, tailFrom, ckpt, cleared, summary, limit, changed,
                   facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* The workflow indexes the completed activity result.
ToolComplete(c) ==
    /\ c \in Open \cap ActivityCalls
    /\ resRef[c] # None
    /\ idx' = Append(idx, Entry("result", {c}, resRef[c]))
    /\ UNCHANGED <<store, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* Runtime.Provide* stores the external result.
ExternalPut(c) ==
    /\ c \in Open \cap ExternalCalls
    /\ ResCid(c) \notin store
    /\ store' = store \cup {ResCid(c)}
    /\ resRef' = [resRef EXCEPT ![c] = ResCid(c)]
    /\ UNCHANGED <<idx, planned, tailFrom, ckpt, cleared, summary, limit, changed,
                   facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* Runtime.Provide* signals the hash; the workflow indexes it. The broken
\* configuration signals a hash before the content is stored.
ExternalSignal(c) ==
    /\ c \in Open \cap ExternalCalls
    /\ \/ resRef[c] # None
       \/ Fault = "reference_before_record"
    /\ idx' = Append(idx, Entry("result", {c}, ResCid(c)))
    /\ UNCHANGED <<store, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* A structural reminder after tool results, rendered from a template.
\* One per run is enough to cover structural entries.
Reminder ==
    /\ pending = 0
    /\ ~recovered
    /\ idx[Len(idx)].kind = "result"
    /\ \A i \in Positions : idx[i].kind # "reminder"
    /\ idx' = Append(idx, Entry("reminder", {}, None))
    /\ UNCHANGED <<store, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

-----------------------------------------------------------------------------
(* Compaction.                                                              *)

\* The workflow's trigger decision before the next planner step. It uses
\* the recorded facts of the latest turn and the view, which it records as
\* the ViewRef (tailFrom, cleared, index length). The broken configurations
\* read the adapter's live limit, or a size replay cannot reproduce.
Decide ==
    /\ pending = 0
    /\ plans > 0
    /\ Len(decisions) < MaxDecisions
    /\ \E jitter \in (IF Fault = "unrecorded_size" THEN {0, 1} ELSE {0}) :
          LET used == IF Fault = "unrecorded_limit" THEN limit ELSE facts[plans]
              size == ViewSize + jitter
          IN decisions' = Append(decisions,
                            [plan |-> plans, t |-> tailFrom, len |-> Len(idx), cl |-> cleared,
                             trig |-> size > used])
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* Tier 1: clear old tool results, in bulk (one prompt-cache break).
Clear ==
    /\ pending = 0
    /\ (~recovered \/ Fault = "compact_during_recovery")
    /\ Eligible # {}
    /\ cleared' = cleared \cup Eligible
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, summary, limit,
                   changed, facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* Positions holding a result the view renders as an omission.
LargeAt(R) == {i \in R : idx[i].kind = "result" /\ idx[i].calls \subseteq LargeCalls}

\* What SummarizeContext reads for positions R: the eligible content at full
\* size. The broken configurations read the view's rendering instead (an
\* omission carries none of a large result) or resolve server-only data.
SummaryReads(R) ==
    LET eligible == {idx[i].ref : i \in R} \ {None}
        server == {ServerCid(c) : c \in UNION {idx[i].calls : i \in {j \in R : idx[j].kind = "result"}}} \cap store
    IN CASE Fault = "access_by_rendering" -> eligible \ {idx[i].ref : i \in LargeAt(R)}
         [] Fault = "read_server_only" -> eligible \cup server
         [] OTHER -> eligible

\* The positions a summary's text can carry: those whose content it read.
SummaryBasis(R) ==
    Carried \cup {i \in R : idx[i].ref = None \/ idx[i].ref \in SummaryReads(R)}

\* SummarizeContext summarises the previous checkpoint and the units from
\* the view start to one cut, under the current budget, validates its
\* output, and stores it.
SummaryPut ==
    /\ pending = 0
    /\ summary = None
    /\ \E p \in Positions :
          /\ ValidCut(p)
          /\ LET R == tailFrom..(p - 1)
                 s == SumCid(tailFrom, p, SummaryBasis(R), CurLimit)
             IN /\ store' = store \cup {s}
                /\ summary' = s
                /\ exposed' = exposed \cup SummaryReads(R)
    /\ UNCHANGED <<idx, planned, resRef, tailFrom, ckpt, cleared, limit, changed,
                   facts, decisions, overflows, stuck, plans, failed, budget, recovered, failedLimit, pending, failedSize, final>>

\* The workflow applies the summary at the cut it was made for. The broken
\* configuration applies it at any valid cut.
SummaryApply(p) ==
    /\ pending = 0
    /\ (~recovered \/ Fault = "compact_during_recovery")
    /\ summary # None
    /\ IF Fault = "summary_at_other_cut" THEN TRUE ELSE p = summary.to
    /\ ValidCut(p)
    /\ ckpt' = [through |-> p - 1, ref |-> summary, receipt |-> {}]
    /\ tailFrom' = p
    /\ summary' = None
    /\ UNCHANGED <<store, idx, planned, resRef, cleared, limit, changed, facts,
                   decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* A summary whose cut is no longer ahead of the view is stale: discarded.
SummaryDiscard ==
    /\ pending = 0
    /\ summary # None
    /\ summary.to <= tailFrom
    /\ summary' = None
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, cleared, limit,
                   changed, facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* Receipt-only eviction, no model call.
Evict(p) ==
    /\ pending = 0
    /\ (~recovered \/ Fault = "compact_during_recovery")
    /\ ValidCut(p)
    /\ ckpt' = EvictCheckpoint(p)
    /\ tailFrom' = p
    /\ UNCHANGED <<store, idx, planned, resRef, cleared, summary, limit, changed,
                   facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* The views recovery may choose: clear everything eligible; clear and evict
\* at a valid cut; or evict at a valid cut.
Evictions(cl) == {[t |-> p, cl |-> cl, ck |-> EvictCheckpoint(p)] : p \in {q \in Positions : ValidCut(q)}}
Options ==
    (IF Eligible # {} THEN {[t |-> tailFrom, cl |-> cleared \cup Eligible, ck |-> ckpt]} ELSE {})
    \cup (IF Eligible # {} THEN Evictions(cleared \cup Eligible) ELSE {})
    \cup Evictions(cleared)

\* Recovery evicts "until enough" (design 4.15): a view is enough when the
\* whole request, rendered under the budget the retry will use, is smaller
\* than the failed request. That accounts for a checkpoint that renders
\* larger because the failed model's budget is larger. The broken
\* configuration takes any view.
Enough(o, used) ==
    \/ Fault = "recovery_ignores_target"
    \/ RequestSizeAt(o.t, o.cl, o.ck, used) < failedSize

CanShrink(used) == \E o \in Options : Enough(o, used)

\* The broken configuration reschedules with the same view.
RecomputeView(used) ==
    IF Fault = "recovery_without_new_view"
       THEN UNCHANGED <<tailFrom, cleared, ckpt>>
       ELSE \E o \in Options :
               /\ Enough(o, used)
               /\ tailFrom' = o.t
               /\ cleared' = o.cl
               /\ ckpt' = o.ck

\* The planner activity reports a context overflow. The rejected request
\* was sent, so it is model-facing; the activity's output records the failed
\* request's facts, including its model's limit, in workflow history.
OverflowReport ==
    /\ ~failed
    /\ ~final
    /\ plans > 0
    /\ Open = {}
    /\ pending = 0
    /\ pending' = limit
    /\ failedSize' = RequestSize
    /\ exposed' = exposed \cup ViewReads
    /\ stuck' = RetryStuck
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, decisions, overflows, plans, failed,
                   budget, recovered, failedLimit, final>>

\* The workflow applies recovery from the recorded facts: it takes the budget
\* from the failed request's model, recomputes the view and reschedules, at
\* most twice per step; when it cannot shrink the view or the budget is
\* spent, the run fails with a typed error. The adapter's limit may have
\* changed since the report. The broken configurations keep the previous
\* budget, read the adapter's live limit, or recover without bound.
OverflowApply ==
    /\ pending # 0
    /\ LET used == IF Fault = "recovery_reads_adapter" THEN limit ELSE pending
       IN IF Fault = "unbounded_recovery"
             THEN /\ recovered' = TRUE
                  /\ failedLimit' = pending
                  /\ budget' = used
                  /\ UNCHANGED <<tailFrom, cleared, ckpt, overflows, stuck, failed>>
          ELSE IF overflows < 2 /\ (CanShrink(used) \/ Fault = "recovery_without_new_view")
             THEN /\ overflows' = overflows + 1
                  /\ budget' = IF Fault = "recovery_keeps_budget" THEN budget ELSE used
                  /\ recovered' = TRUE
                  /\ failedLimit' = pending
                  /\ RecomputeView(used)
                  /\ UNCHANGED <<stuck, failed>>
             ELSE /\ failed' = TRUE
                  /\ UNCHANGED <<tailFrom, cleared, ckpt, overflows, stuck, budget, recovered, failedLimit>>
    /\ pending' = 0
    /\ UNCHANGED <<store, idx, planned, resRef, summary, limit, changed, facts,
                   decisions, plans, exposed, failedSize, final>>

\* The final planner turn answers without tool calls; its request reads the
\* view like any other.
FinalTurn ==
    /\ ~failed
    /\ ~final
    /\ plans > 0
    /\ Open = {}
    /\ pending = 0
    /\ final' = TRUE
    /\ exposed' = exposed \cup ViewReads
    /\ recovered' = FALSE
    /\ stuck' = RetryStuck
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, cleared, summary,
                   limit, changed, facts, decisions, overflows, plans, failed,
                   budget, failedLimit, pending, failedSize>>

\* The adapter's reported limit changes (a deploy, a config override).
ChangeLimit ==
    /\ changed < MaxLimitChanges
    /\ \E l \in Limits \ {limit} : limit' = l
    /\ changed' = changed + 1
    /\ UNCHANGED <<store, idx, planned, resRef, tailFrom, ckpt, cleared, summary,
                   facts, decisions, overflows, stuck, plans, failed, exposed, budget, recovered, failedLimit, pending, failedSize, final>>

\* The run has nothing left to do: it failed, or its final turn ran.
Finished == failed \/ final

Done == Finished /\ pending = 0 /\ UNCHANGED vars

\* Fairness covers what the environment and the workflow eventually do: tool
\* results arrive, a reported overflow is applied, and a planner attempt has
\* some outcome (a turn, the final turn, or an overflow). Which outcome is
\* not fair: the provider may keep rejecting.
Attempt == (\E C \in SUBSET Calls : Plan(C)) \/ FinalTurn \/ OverflowReport

Progress ==
    \/ Attempt
    \/ OverflowApply
    \/ \E c \in Calls : ToolPut(c) \/ ToolComplete(c) \/ ExternalPut(c) \/ ExternalSignal(c)

Next ==
    \/ Done
    \/ \E C \in SUBSET Calls : Plan(C)
    \/ \E c \in Calls : ToolPut(c) \/ ToolComplete(c) \/ ExternalPut(c) \/ ExternalSignal(c)
    \/ Reminder
    \/ Decide
    \/ Clear
    \/ SummaryPut
    \/ SummaryDiscard
    \/ \E p \in Positions : SummaryApply(p) \/ Evict(p)
    \/ OverflowReport
    \/ OverflowApply
    \/ FinalTurn
    \/ ChangeLimit

Spec == Init /\ [][Next]_vars /\ WF_vars(Progress)

-----------------------------------------------------------------------------
(* Invariants.                                                              *)

\* Nothing the workflow refers to is missing from the run store, so the
\* full run is always consultable (evals, recall_context).
RecordBeforeReference ==
    /\ \A i \in Positions : idx[i].ref # None => idx[i].ref \in store
    /\ ckpt.ref # None => ckpt.ref \in store

\* The view starts at an assistant message (or the run start) and every
\* tool result in it has its call in it.
ViewWellFormed ==
    /\ tailFrom = 1 \/ idx[tailFrom].kind = "asst"
    /\ \A i \in tailFrom..Len(idx) :
           idx[i].kind = "result" =>
               \E j \in tailFrom..(i - 1) :
                   idx[j].kind = "asst" /\ idx[i].calls \subseteq idx[j].calls

\* Everything before the view is behind the checkpoint.
Coverage == ckpt.through = tailFrom - 1

\* Nothing leaves the view silently: the checkpoint's summary and receipt
\* carry every position before the view.
NothingSilentlyDropped == 1..(tailFrom - 1) \subseteq Carried

\* Placeholders only stand for results that exist.
ClearedSound == \A c \in cleared : Resolved(c)

\* Replaying the recorded facts reproduces every trigger decision.
ReplayDeterministic ==
    \A k \in 1..Len(decisions) :
        LET d == decisions[k]
        IN d.trig = (SizeRange(d.t, d.len, d.cl) > facts[d.plan])

\* Every overflow recovery makes the request strictly smaller, counting the
\* rendered checkpoint as well as the tail.
RecoveryShrinks == ~stuck

\* The rendered checkpoint has a bound independent of run length, under the
\* current budget, including a summary written under a larger one.
CheckpointBounded ==
    /\ ReceiptSize(ckpt) <= ReceiptCap + 1
    /\ SummarySize(ckpt) <= SummaryCap(CurLimit)

\* No model-facing path ever reads server-only data: planner requests and
\* summariser input.
ServerOnlyNeverExposed == \A c \in ServerCalls : ServerCid(c) \notin exposed

\* After a recovery, the retried request's checkpoint fits the budget of the
\* model whose request failed.
RetryFitsFailedModel == recovered => SummarySizeAt(ckpt, budget) <= SummaryCap(failedLimit)

\* A recovery ends: a later planner turn succeeds, or the step fails, even
\* with the provider free to keep rejecting. Strict shrinking ends it within
\* the bounds; the budget of two is the rule invariant OverflowBounded.
RecoveryEnds == recovered ~> (~recovered \/ failed)

\* Sanity: the run ends. It follows from the bounds and fairness.
Terminates == <>Finished

\* Each recovery applies the failed request's recorded limit.
RecoveryReplays == recovered => budget = failedLimit

\* Witness: a summary written under the larger budget renders under the
\* smaller one after a recovery that evicted. Checked as an invariant that
\* must be violated.
WitnessShrinkThenRecover ==
    ~(ckpt.ref # None /\ ckpt.ref.made = 8 /\ budget = 4 /\ recovered /\ ckpt.receipt # {})

OverflowBounded == overflows <= 2

-----------------------------------------------------------------------------
(* Action properties: the record only grows.                                *)

StoreGrows == [][store \subseteq store']_store

IndexAppendOnly == [][Len(idx') >= Len(idx) /\ SubSeq(idx', 1, Len(idx)) = idx]_idx

ViewOnlyAdvances == [][tailFrom' >= tailFrom /\ cleared \subseteq cleared']_<<tailFrom, cleared>>
=============================================================================
