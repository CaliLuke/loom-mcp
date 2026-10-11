----------------------------- MODULE OverflowGuard -----------------------------
(***************************************************************************)
(* The pre-send guard and overflow recovery for one planner step,          *)
(* docs/wips/context_compaction.md (revision 9), sections 4.8 and 4.15.    *)
(*                                                                         *)
(* The planner sends requests built from the framework's view, or its own  *)
(* rewritten messages, or messages unrelated to the view. The guard        *)
(* checks each request against the window when it can trust its figure    *)
(* (an exact count, or a calibrated estimate with known limits); the       *)
(* estimate may be off by one either way. A provider overflow may report   *)
(* usage, which calibrates the estimate for the rest of the step. The      *)
(* provider is authoritative. Recovery shrinks the view; a retried request *)
(* must be smaller than the smallest request that model's provider        *)
(* rejected in this step (whole input plus reserve, never call ordinal).   *)
(* This one-model abstraction has strictly shrinking sends, so lastProv    *)
(* is also that minimum. Every                                            *)
(* combination of view size, planner, calibration, exactness, known limits *)
(* and estimate error is an initial state. Recovery availability abstracts  *)
(* a B-derived checkpoint shortfall: provider failure, no retry consumed;   *)
(* a guard only disables itself.                                           *)
(*                                                                         *)
(* The invariants read the history of requests sent (sent) and the        *)
(* recorded failure, not the variables the actions branch on.              *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
    Window,     \* the provider's real limit, in size units
    MaxView,    \* largest initial view
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "guard_fails_step", "compare_view_only",
           "guard_spends_overflow_budget", "compare_last_rejection",
           "guard_never_off"}

ASSUME Fault \in Faults

MinView == 1

VARIABLES
    view,       \* the framework's view size
    initView,   \* the view at step start: what a rewriting planner keeps sending
    planner,    \* "passes" the view, "rewrites" it, or sends "unrelated" messages
    calibrated, \* a request to this model has reported usage
    exact,      \* the adapter's TokenCounter returns Exact counts
    recoveryAvailable, \* the B-derived checkpoint can render (decision 5)
    known,      \* the model's limits are known
    err,        \* estimate error when the count is not exact (1 is exact)
    guardRej,   \* guard rejections in this step (budget 1)
    guardOff,   \* the guard is off for the rest of the step
    provLeft,   \* provider overflow recoveries left (budget 2)
    lastProv,   \* size of the last request the provider rejected, 0 if none
    lastRej,    \* size of the last rejected request, by guard or provider
    sent,       \* history: <<[size, overflow]>> of every request sent
    status,     \* "planning", "ok" or "failed"
    failKind,   \* why the step failed
    failReq     \* the request the step would have sent when it failed

vars == <<view, initView, planner, calibrated, exact, known, err, guardRej,
          guardOff, provLeft, lastProv, lastRej, sent, status, failKind, failReq, recoveryAvailable>>

Init ==
    /\ view \in MinView..MaxView
    /\ initView = view
    /\ planner \in {"passes", "rewrites", "unrelated"}
    /\ calibrated \in BOOLEAN
    /\ exact \in BOOLEAN
    /\ known \in BOOLEAN
    /\ recoveryAvailable \in BOOLEAN
    /\ err \in {0, 1, 2}
    /\ guardRej = 0
    /\ guardOff = FALSE
    /\ provLeft = 2
    /\ lastProv = 0
    /\ lastRej = 0
    /\ sent = <<>>
    /\ status = "planning"
    /\ failKind = "none"
    /\ failReq = 0

Req == IF planner = "passes" THEN view ELSE initView
ViewBearing == planner # "unrelated"
Est == IF exact THEN Req ELSE Req + err - 1

GuardOn == known /\ (exact \/ calibrated) /\ ~guardOff /\ (guardRej < 1 \/ Fault = "guard_never_off")

Fixed == <<initView, planner, exact, known, err, recoveryAvailable>>

\* The size the no-progress check compares against: the last request the
\* provider rejected (also its minimum under RetriesShrink). The broken
\* configurations compare the framework's
\* view (which a rewriting planner ignores), or the last rejection of any
\* kind, so a guard rejection resets the comparison.
Baseline ==
    CASE Fault = "compare_view_only" -> 0
      [] Fault = "compare_last_rejection" -> lastRej
      [] OTHER -> lastProv

Fail(kind) ==
    /\ status' = "failed"
    /\ failKind' = kind
    /\ failReq' = Req

\* The guard rejects a request it estimates over the window. Recovery
\* shrinks the view; when it cannot, or the request is not view-bearing,
\* the guard turns off and the request goes to the provider.
GuardReject ==
    /\ status = "planning"
    /\ GuardOn
    /\ Est > Window
    /\ guardRej' = IF Fault = "guard_never_off" THEN guardRej ELSE guardRej + 1
    /\ lastRej' = Req
    /\ provLeft' = IF Fault = "guard_spends_overflow_budget" /\ provLeft > 0 THEN provLeft - 1 ELSE provLeft
    /\ IF ViewBearing /\ view > MinView /\ recoveryAvailable
          THEN /\ view' = view - 1
               /\ UNCHANGED <<guardOff, status, failKind, failReq>>
       ELSE IF Fault = "guard_fails_step"
          THEN /\ Fail("guard")
               /\ UNCHANGED <<view, guardOff>>
       ELSE /\ guardOff' = (Fault # "guard_never_off")
            /\ UNCHANGED <<view, status, failKind, failReq>>
    /\ UNCHANGED <<Fixed, calibrated, lastProv, sent>>

\* The wrapper fails the step without sending when the request is no smaller
\* than one the provider already rejected.
NoProgress ==
    /\ status = "planning"
    /\ ~(GuardOn /\ Est > Window)
    /\ Baseline > 0
    /\ Req >= Baseline
    /\ Fail("no_progress")
    /\ UNCHANGED <<Fixed, view, calibrated, guardRej, guardOff, provLeft, lastProv, lastRej, sent>>

\* The request goes to the provider. An overflow may report usage, which
\* calibrates the estimate for the rest of the step.
Send ==
    /\ status = "planning"
    /\ ~(GuardOn /\ Est > Window)
    /\ ~(Baseline > 0 /\ Req >= Baseline)
    /\ IF Req <= Window
          THEN /\ sent' = Append(sent, [size |-> Req, overflow |-> FALSE])
               /\ status' = "ok"
               /\ UNCHANGED <<view, calibrated, provLeft, lastProv, lastRej, failKind, failReq>>
          ELSE /\ sent' = Append(sent, [size |-> Req, overflow |-> TRUE])
               /\ lastProv' = Req
               /\ lastRej' = Req
               /\ calibrated' \in {calibrated, TRUE}
               /\ IF ~recoveryAvailable
                     THEN /\ Fail("budget_too_small") /\ UNCHANGED <<view, provLeft>>
                  ELSE IF provLeft = 0
                     THEN /\ Fail("budget") /\ UNCHANGED <<view, provLeft>>
                  ELSE IF ~ViewBearing
                     THEN /\ Fail("not_view_bearing") /\ UNCHANGED <<view, provLeft>>
                  ELSE IF view = MinView
                     THEN /\ Fail("minimal") /\ UNCHANGED <<view, provLeft>>
                  ELSE /\ view' = view - 1
                       /\ provLeft' = provLeft - 1
                       /\ UNCHANGED <<status, failKind, failReq>>
    /\ UNCHANGED <<Fixed, guardRej, guardOff>>

Done == status # "planning" /\ UNCHANGED vars

Next == GuardReject \/ NoProgress \/ Send \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(GuardReject \/ NoProgress \/ Send)

-----------------------------------------------------------------------------
(* Invariants, over the history of requests sent.                          *)

Overflows == {i \in 1..Len(sent) : sent[i].overflow}

\* Only a provider overflow fails a step: either the last request sent
\* overflowed, or the request the step would send is no smaller than one the
\* provider rejected.
FailsOnlyOnProvider ==
    status = "failed" =>
        \/ failKind \in {"budget", "not_view_bearing", "minimal", "budget_too_small"}
              /\ Len(sent) > 0 /\ sent[Len(sent)].overflow
        \/ failKind = "no_progress"
              /\ \E i \in Overflows : failReq >= sent[i].size

\* No request is sent that is no smaller than an earlier one the provider
\* rejected.
RetriesShrink ==
    \A i, j \in 1..Len(sent) : i < j /\ sent[i].overflow => sent[j].size < sent[i].size

\* A step fails for a spent budget only after its two recoveries were spent
\* on provider overflows.
BudgetFailureHonest == failKind = "budget" => Cardinality(Overflows) = 3

\* A successful step's last request fit the window.
SuccessFits == status = "ok" => (Len(sent) > 0 /\ ~sent[Len(sent)].overflow /\ sent[Len(sent)].size <= Window)

\* Every step ends.
Terminates == <>(status # "planning")

\* Witness: a guard rejection and a provider overflow in one step that still
\* succeeds. Checked as an invariant that must be violated.
WitnessGuardThenProvider ==
    ~(status = "ok" /\ guardRej = 1 /\ Cardinality(Overflows) >= 1)
=============================================================================
