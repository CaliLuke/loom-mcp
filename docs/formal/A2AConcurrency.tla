--------------------------- MODULE A2AConcurrency ---------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Replicas, MaxLeases, MaxAttempts, PositiveHorizon, LateExclusion, Fault

(* One fixed immutable task/start/command identity. Network requests survive
   caller crashes, deadlines and lease changes. The stores are separate. *)
VARIABLE s
vars == <<s>>
Terminal == {"completed", "canceled"}
(* Engine failures fold into completed; the only public failure is a sealed
   start rejection, which never has an execution. *)
PublicTerminal == Terminal \cup {"failed"}
Attempts == 1..MaxAttempts

Init == s = [
    lease |-> 0, owner |-> "none",
    localFence |-> [r \in Replicas |-> 0],
    prepared |-> {}, crashed |-> FALSE,
    suppressionCrash |-> FALSE, receiptCrash |-> FALSE, badAdmission |-> FALSE,
    staleClaim |-> FALSE, lateFirstAcceptance |-> FALSE,
    admission |-> "absent", admissionFence |-> 0,
    suppressedEver |-> FALSE, rejectedEver |-> FALSE, suppressionRead |-> FALSE,
    cancel |-> FALSE, claims |-> 0,
    packets |-> [a \in Attempts |-> "unused"],
    timedOut |-> {}, horizonOpen |-> TRUE,
    engine |-> "absent", engineRetained |-> FALSE, executions |-> 0,
    delayedAcceptance |-> FALSE,
    command |-> FALSE, receiptPending |-> FALSE,
    receipt |-> "none", acceptedEver |-> FALSE, receiptWrites |-> 0,
    inputClosed |-> FALSE, acceptedAtClose |-> FALSE, answer |-> "none", resumed |-> FALSE,
    public |-> "submitted", terminalSeen |-> "none",
    badProjection |-> FALSE]

Current(r) == s.owner = r /\ s.localFence[r] = s.lease
AdmissionFenceOK(r) == s.localFence[r] > 0 /\ s.localFence[r] >= s.admissionFence

(* Acquisition abstracts expiry plus replacement; old processes retain their
   local fence and an already prepared request. There is no cross-store lock. *)
Acquire(r) ==
    /\ s.lease < MaxLeases
    /\ s' = [s EXCEPT !.lease = @ + 1, !.owner = r,
                     !.localFence[r] = s.lease + 1,
                     !.prepared = @ \ {r}]

Crash(r) ==
    /\ s.localFence[r] > 0
    /\ s' = [s EXCEPT !.localFence[r] = 0, !.prepared = @ \ {r},
                     !.crashed = TRUE,
                     !.suppressionCrash = @ \/ (Current(r) /\ s.suppressedEver /\ s.public = "submitted"),
                     !.receiptCrash = @ \/ (Current(r) /\ s.acceptedEver /\ s.answer = "none")]

Prepare(r) ==
    /\ Current(r) /\ s.localFence[r] > 0
    /\ ~s.cancel /\ s.admission \notin {"suppressed", "rejected"}
    /\ r \notin s.prepared
    /\ s.claims < MaxAttempts
    /\ s.claims = 0 \/ ((PositiveHorizon \/ Fault = "zero_horizon_retry") /\ s.horizonOpen)
    /\ s' = [s EXCEPT !.prepared = @ \cup {r}]

Initialize(r) ==
    /\ AdmissionFenceOK(r)
    /\ s.admission = "absent"
       \/ (Fault = "reset_suppression" /\ s.admission = "suppressed")
       \/ (Fault = "reset_rejection" /\ s.admission = "rejected")
    /\ s' = [s EXCEPT !.admission = "open",
                     !.admissionFence = s.localFence[r]]

(* Claim consumes a prepared request, possibly from a stale process. Runtime
   fencing compares the last runtime commit, not an atomic read of task lease.
   Whichever runtime CAS wins determines claim versus suppression. *)
Claim(r) ==
    /\ r \in s.prepared /\ (AdmissionFenceOK(r) \/ Fault = "stale_admission")
    /\ s.admission \in {"absent", "open", "claimed"}
    /\ s.claims < MaxAttempts
    /\ s.claims = 0 \/ ((PositiveHorizon \/ Fault = "zero_horizon_retry") /\ s.horizonOpen)
    /\ s' = [s EXCEPT !.admission = "claimed",
                     !.admissionFence = s.localFence[r],
                     !.claims = @ + 1,
                     !.badAdmission = @ \/ (s.localFence[r] < s.admissionFence),
                     !.staleClaim = @ \/ ~Current(r),
                     !.packets[s.claims + 1] = "claimed",
                     !.prepared = @ \ {r}]

(* A claimed request may be lost before I/O, or sent despite caller death.
   Loss never releases the durable claim. *)
Send(a) ==
    /\ s.packets[a] = "claimed"
    /\ s' = [s EXCEPT !.packets[a] = "inflight"]
DropBeforeSend(a) ==
    /\ s.packets[a] = "claimed"
    /\ s' = [s EXCEPT !.packets[a] = "lost"]
Timeout(a) ==
    /\ s.packets[a] \in {"claimed", "inflight"} /\ a \notin s.timedOut
    /\ s' = [s EXCEPT !.timedOut = @ \cup {a}]
(* The engine definitively refuses this attempt (ErrWorkflowStartRejected):
   no execution exists for it and none can. Duplicates and transport errors
   never take this transition. *)
Reject(a) ==
    /\ s.packets[a] = "inflight"
    /\ s' = [s EXCEPT !.packets[a] = "rejected"]
(* Zero horizon only: the sole authorized attempt was rejected, so the
   admission is sealed against any further claim and published as failed.
   Under a positive horizon a rejection proves nothing about other attempts. *)
SealRejected(r) ==
    /\ (~PositiveHorizon \/ Fault = "reject_closes_retry")
    /\ AdmissionFenceOK(r) /\ s.admission = "claimed"
    /\ \E a \in Attempts : s.packets[a] = "rejected"
    /\ s' = [s EXCEPT !.admission = "rejected", !.rejectedEver = TRUE,
                     !.admissionFence = s.localFence[r]]
ExpireHorizon ==
    /\ s.horizonOpen
    /\ s' = [s EXCEPT !.horizonOpen = FALSE]
ForgetEngine ==
    /\ ~s.horizonOpen /\ s.engine \in Terminal /\ s.engineRetained
    /\ s' = [s EXCEPT !.engineRetained = FALSE]

(* LateExclusion abstracts ONLY the duplicate-exclusion consequence of a
   certified engine guarantee, potentially implemented by a server acceptance
   bound. It does not model that bound or disable delayed FIRST acceptance.
   Thus this model overapproximates even a certificate that actually rules out
   all late first acceptance. V1 cancellation deliberately does not consume
   that stronger timing evidence. Client deadlines alone imply neither fact. *)
Accept(a) ==
    /\ s.packets[a] = "inflight"
    /\ LET duplicate == s.engineRetained /\ Fault # "no_engine_dedup"
           excluded == LateExclusion /\ s.executions > 0 /\ ~s.engineRetained
       IN s' = [s EXCEPT
           !.packets[a] = "delivered",
           !.executions = IF duplicate \/ excluded THEN @ ELSE @ + 1,
           !.engine = IF duplicate \/ excluded THEN @ ELSE "running",
           !.engineRetained = IF excluded THEN @ ELSE TRUE,
           !.delayedAcceptance = @ \/ (a \in s.timedOut /\ ~duplicate /\ ~excluded),
           !.lateFirstAcceptance = @ \/ (s.executions = 0 /\ ~s.horizonOpen /\
               s.cancel /\ a \in s.timedOut /\ ~duplicate /\ ~excluded)]

CancelIntent ==
    /\ ~s.cancel
    /\ s' = [s EXCEPT !.cancel = TRUE]
(* Deliberately more permissive than the adapter: a non-current holder may
   suppress if its fence still passes the runtime record CAS. This explores a
   superset of intended caller behavior. *)
Suppress(r) ==
    /\ s.cancel /\ AdmissionFenceOK(r)
    /\ s.admission \in {"absent", "open"}
    /\ s' = [s EXCEPT !.admission = "suppressed",
                     !.admissionFence = s.localFence[r],
                     !.suppressedEver = TRUE]

(* Mutation of the old read-in-runtime/write-in-protocol algorithm. The stale
   read may precede a competing claim; it cannot authorize suppression. *)
ReadSuppression ==
    /\ Fault = "cross_store_suppression" /\ s.cancel
    /\ s.admission \in {"absent", "open"} /\ ~s.suppressionRead
    /\ s' = [s EXCEPT !.suppressionRead = TRUE]
UnsafeSuppress(r) ==
    /\ Fault = "cross_store_suppression" /\ s.suppressionRead
    /\ Current(r) /\ s.claims > 0 /\ s.admission # "suppressed"
    /\ s' = [s EXCEPT !.admission = "suppressed", !.suppressedEver = TRUE]

(* canceled abstracts cancellation request plus actual engine termination;
   cancel intent alone cannot take this transition. Canonical failures of an
   accepted execution fold into completed because their terminal races coincide. *)
Finish(outcome) ==
    /\ s.engine = "running"
    /\ outcome = "completed" \/ (outcome = "canceled" /\ s.cancel)
    /\ s' = [s EXCEPT !.engine = outcome]

(* The command remains durable even if no replica is alive. Receipt activities
   can arrive after engine completion. Only the receipt-store CAS determines
   whether a pre-terminal decision commits before the input-close fence. *)
ScheduleReceipt ==
    /\ s.engine = "running" /\ ~s.command
    /\ s' = [s EXCEPT !.command = TRUE, !.receiptPending = TRUE]
AppendReceipt ==
    /\ s.receiptPending
    /\ ~s.inputClosed \/ Fault = "append_after_close"
    /\ s.receipt = "none" \/ (Fault = "duplicate_receipt" /\ s.receiptWrites < 2)
    /\ s' = [s EXCEPT !.receipt = "accepted", !.acceptedEver = TRUE,
                     !.receiptWrites = @ + 1]
CloseInput ==
    /\ s.engine \in Terminal /\ ~s.inputClosed
    /\ s' = [s EXCEPT !.inputClosed = TRUE, !.acceptedAtClose = s.acceptedEver,
                     !.receipt = IF Fault = "close_loses_receipt" THEN "none" ELSE @]
ResolveReceipt(r) ==
    /\ Current(r) /\ s.command /\ s.answer = "none"
    /\ s.receipt # "none" \/ s.inputClosed
    /\ s' = [s EXCEPT !.answer = IF s.receipt # "none" THEN s.receipt ELSE "terminal"]
Resume ==
    /\ s.engine = "running" /\ s.receipt = "accepted" /\ ~s.resumed
    /\ s' = [s EXCEPT !.resumed = TRUE]

Desired ==
    CASE s.admission = "suppressed" -> "canceled"
      [] s.admission = "rejected" -> "failed"
      [] s.engine \in Terminal -> s.engine
      [] OTHER -> "submitted"
Project(r) ==
    /\ Current(r)
    /\ s.public \notin PublicTerminal
    /\ Desired \in PublicTerminal
    /\ s' = [s EXCEPT !.public = Desired, !.terminalSeen = Desired]
UnsafeTimeoutCancel(r) ==
    /\ Fault = "timeout_notfound" /\ Current(r)
    /\ s.cancel /\ s.claims > 0 /\ s.engine = "absent"
    /\ s.timedOut # {} /\ s.public \notin PublicTerminal
    /\ s' = [s EXCEPT !.public = "canceled", !.terminalSeen = "canceled"]
UnsafeStaleProjection(r) ==
    /\ Fault = "stale_projection"
    /\ s.localFence[r] > 0 /\ ~Current(r)
    /\ s.public \in PublicTerminal
    /\ s' = [s EXCEPT !.public = "submitted", !.badProjection = TRUE]

Next ==
    \/ \E r \in Replicas : Acquire(r) \/ Crash(r) \/ Prepare(r) \/ Initialize(r)
        \/ Claim(r) \/ Suppress(r) \/ UnsafeSuppress(r) \/ ResolveReceipt(r)
        \/ SealRejected(r)
        \/ Project(r) \/ UnsafeTimeoutCancel(r) \/ UnsafeStaleProjection(r)
    \/ \E a \in Attempts : Send(a) \/ DropBeforeSend(a) \/ Timeout(a) \/ Accept(a) \/ Reject(a)
    \/ \E outcome \in Terminal : Finish(outcome)
    \/ CancelIntent \/ ReadSuppression \/ ExpireHorizon \/ ForgetEngine
    \/ ScheduleReceipt \/ AppendReceipt \/ CloseInput \/ Resume
    (* Waiting forever is permitted: unknown starts have no liveness claim. *)
    \/ UNCHANGED vars

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ s.lease \in 0..MaxLeases /\ s.owner \in Replicas \cup {"none"}
    /\ s.localFence \in [Replicas -> 0..MaxLeases]
    /\ s.prepared \subseteq Replicas /\ s.admissionFence \in 0..MaxLeases
    /\ s.admission \in {"absent", "open", "claimed", "suppressed", "rejected"}
    /\ s.claims \in 0..MaxAttempts /\ s.executions \in 0..MaxAttempts
    /\ s.packets \in [Attempts -> {"unused", "claimed", "inflight", "lost", "delivered", "rejected"}]
    /\ s.timedOut \subseteq Attempts
    /\ s.engine \in {"absent", "running", "completed", "canceled"}
    /\ s.receipt \in {"none", "accepted"} /\ s.receiptWrites \in 0..2
    /\ s.answer \in {"none", "accepted", "terminal"}
    /\ s.public \in {"submitted"} \cup PublicTerminal
    /\ s.terminalSeen \in {"none"} \cup PublicTerminal
    /\ \A b \in {s.crashed, s.suppressedEver, s.rejectedEver, s.suppressionRead, s.cancel,
                  s.horizonOpen, s.engineRetained, s.delayedAcceptance, s.command,
                  s.receiptPending, s.acceptedEver, s.inputClosed, s.resumed,
                  s.acceptedAtClose, s.badProjection, s.badAdmission, s.staleClaim, s.lateFirstAcceptance, s.suppressionCrash, s.receiptCrash} : b \in BOOLEAN

AtMostOneExecution == s.executions <= 1
ZeroHorizonSingleClaim == ~PositiveHorizon => s.claims <= 1
SuppressionIrreversible == s.suppressedEver => s.admission = "suppressed"
SuppressionExcludesDispatch == s.suppressedEver => s.claims = 0 /\ s.executions = 0
NoFalseCanceled == s.public = "canceled" =>
    (s.admission = "suppressed" /\ s.claims = 0 /\ s.executions = 0)
    \/ (s.engine = "canceled" /\ s.executions = 1)
NoFalseCompleted == s.public = "completed" => s.engine = "completed" /\ s.executions = 1
(* A failed task is exactly a sealed zero-horizon rejection of the sole
   attempt: no execution exists, none can, and nothing else was ever claimed. *)
NoFalseFailed == s.public = "failed" =>
    s.admission = "rejected" /\ s.executions = 0 /\ s.engine = "absent"
    /\ s.claims = 1 /\ s.packets[1] = "rejected"
RejectionSealed ==
    /\ s.rejectedEver => s.admission = "rejected"
    /\ s.admission = "rejected" =>
        ~PositiveHorizon /\ s.rejectedEver /\ s.claims = 1
        /\ s.packets[1] = "rejected" /\ s.executions = 0 /\ s.engine = "absent"
TerminalPublicImmutable == s.terminalSeen # "none" => s.public = s.terminalSeen
ProjectionFenced == ~s.badProjection
(* Committed-record fence monotonicity, NOT current-global-lease ownership.
   Acquire does not atomically advance this separate runtime record. *)
AdmissionFenced == ~s.badAdmission
(* Durable recoverability, NOT eventual acknowledgement or execution progress.
   A closed fence is a durable terminal outcome for an unreceipted command. *)
NoLostReceipt ==
    /\ s.acceptedEver => s.receipt = "accepted"
    /\ s.answer = "accepted" => s.receipt = "accepted"
    /\ s.answer = "terminal" => s.inputClosed /\ ~s.acceptedEver
    /\ s.resumed => s.receipt = "accepted"
InputCloseFenced == s.inputClosed => s.acceptedEver = s.acceptedAtClose
ReceiptUnique == s.receiptWrites <= 1

(* Negated reachability assertions: each must produce a counterexample with
   Fault="none" and all safety invariants enabled. *)
WitnessDelayedAcceptance == ~s.lateFirstAcceptance
WitnessStaleClaim == ~s.staleClaim
WitnessStaleClaimLostBeforeIO == ~(s.staleClaim /\ s.claims = 1 /\
    s.packets[1] = "lost" /\ s.cancel /\ s.engine = "absent" /\ s.public = "submitted")
WitnessPositiveHorizonLateFirstAcceptance == ~(PositiveHorizon /\ s.lateFirstAcceptance)
(* Reachability-only constraint: select paths where horizon expiry follows a
   claimed dispatch. Safe configurations retain the broader expiry ordering. *)
WitnessClaimBeforeExpiry == s.horizonOpen \/ s.claims > 0
WitnessSuppressionRecovery == ~(s.suppressionCrash /\ s.lease = MaxLeases /\
    s.suppressedEver /\ s.public = "canceled")
WitnessReceiptRecovery == ~(s.receiptCrash /\ s.lease = MaxLeases /\
    s.acceptedEver /\ s.inputClosed /\ s.answer = "accepted")
WitnessRetryDedup == ~(s.claims = 2 /\ s.executions = 1 /\
    \A a \in Attempts : s.packets[a] = "delivered")
WitnessCompletionWins == ~(s.cancel /\ s.public = "completed")
WitnessStartRejected == ~(s.public = "failed" /\ s.cancel)
WitnessLostBeforeIO == ~(s.cancel /\ s.engine = "absent" /\ s.claims > 0 /\
    s.public = "submitted" /\ \E a \in Attempts : s.packets[a] = "lost")
=============================================================================
