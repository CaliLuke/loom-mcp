----------------------------- MODULE A2AProtocol -----------------------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS ReplicaSet, LeaseLimit, CommandSet, RemoteSet, OccurrenceLimit, Fault
VARIABLES a, q, p, events, candidates
vars == <<a, q, p, events, candidates>>

(* Reuse the actual component actions and invariants. The original C receipt
   submachine is disabled; Q supplies the composed receipt/answer protocol. *)
C == INSTANCE A2AConcurrency WITH s <- a, Replicas <- ReplicaSet,
    MaxLeases <- LeaseLimit, MaxAttempts <- 2, PositiveHorizon <- FALSE,
    LateExclusion <- FALSE, Fault <- "none"
Q == INSTANCE A2AClarification WITH s <- q, Commands <- CommandSet,
    Remote <- RemoteSet, Bodies <- {"body"}, MaxOccurrences <- OccurrenceLimit,
    AllowCrash <- FALSE, Fault <- "none"
Slots == {1, 2}
Terminal == {"completed", "canceled"}
PublicTerminal == Terminal \cup {"failed"}
Event(kind, cmd, occurrence) == [kind |-> kind, command |-> cmd, occurrence |-> occurrence]
EmptyCandidate == [ready |-> FALSE, replica |-> "none", fence |-> 0,
    base |-> 0, source |-> 0, status |-> "submitted", occurrence |-> 0, statusSource |-> 0, rebuilt |-> FALSE]

Init ==
    /\ C!Init /\ Q!Init
    /\ events = <<>>
    /\ p = [status |-> "submitted", occurrence |-> 0, statusSource |-> 0,
            cursor |-> 0, revision |-> 0, terminalSeen |-> "none",
            badCommit |-> FALSE, rejectedStale |-> FALSE, rejectedAtAwait2 |-> FALSE, lateCompleted |-> FALSE, lateCanceled |-> FALSE,
            replayAfterAwait |-> FALSE, rebuiltAfterReject |-> FALSE]
    /\ candidates = [slot \in Slots |-> EmptyCandidate]

Admission ==
    /\ (\E r \in ReplicaSet : C!Acquire(r) \/ C!Crash(r) \/ C!Prepare(r)
            \/ C!Initialize(r) \/ C!Claim(r) \/ C!Suppress(r) \/ C!SealRejected(r))
       \/ (\E attempt \in 1..2 : C!Send(attempt) \/ C!DropBeforeSend(attempt)
            \/ C!Timeout(attempt) \/ C!Reject(attempt))
       \/ C!CancelIntent \/ C!ExpireHorizon
    /\ UNCHANGED <<q, p, events, candidates>>
Accept(attempt) ==
    /\ C!Accept(attempt)
    /\ events' = IF a.executions = 0 /\ a'.executions = 1
                     THEN Append(events, Event("await", "none", 1)) ELSE events
    /\ UNCHANGED <<q, p, candidates>>
Finish(outcome) ==
    /\ C!Finish(outcome) /\ Q!Terminate
    /\ UNCHANGED <<p, events, candidates>>
CloseInput ==
    /\ C!CloseInput /\ Q!CloseInput
    /\ UNCHANGED <<p, events, candidates>>

Submit(cmd, occurrence) ==
    /\ a.executions = 1 \/ Fault = "work_before_start"
    /\ cmd \notin RemoteSet \/
        (p.status = "input_required" /\ p.occurrence = occurrence) \/ Fault = "work_before_start"
    /\ Q!Submit(cmd, occurrence, "body")
    /\ UNCHANGED <<a, p, events, candidates>>
Decide(cmd) ==
    /\ a.engine = "running" \/ Fault = "work_before_start"
    /\ Q!Decide(cmd)
    /\ UNCHANGED <<a, p, events, candidates>>
Persist(cmd) ==
    /\ a.executions = 1 /\ Q!Persist(cmd)
    /\ events' = Append(events, Event("receipt", cmd, q.binding[cmd][1]))
    /\ UNCHANGED <<a, p, candidates>>
Resume(cmd) ==
    /\ a.engine = "running" /\ Q!Resume(cmd)
    /\ events' = Append(events, Event("resume", cmd, q.current))
    /\ UNCHANGED <<a, p, candidates>>
Advance ==
    /\ a.engine = "running" /\ Q!Advance
    /\ events' = Append(events, Event("await", "none", q'.current))
    /\ UNCHANGED <<a, p, candidates>>

(* Public status evidence and the consumed run-log cursor are distinct.
   Completion repair may publish terminal before earlier receipt bookkeeping,
   but does not skip those receipts in the cursor. *)
RepairTerminal(r) ==
    /\ C!Project(r)
    /\ p' = [p EXCEPT !.status = a'.public, !.terminalSeen = a'.public,
                     !.revision = @ + 1]
    /\ UNCHANGED <<q, events, candidates>>

(* A current lease holder can have two concurrent prepared operations. A fresh
   candidate merges against the current row AND the event's logical source.
   Fresh accepted receipts consume the public await and mean working even
   before resume; old receipt replay cannot overwrite newer lifecycle state. *)
PrepareProjection(slot, r, source) ==
    /\ C!Current(r) /\ ~candidates[slot].ready
    /\ source \in 1..Len(events) /\ source <= p.cursor + 1
    /\ LET e == events[source]
           bogusReceiptStatus == e.kind = "receipt" /\ q.receipt[e.command] = "accepted"
                                 /\ Fault = "unconditional_receipt_status"
                                 /\ (p.status \in Terminal \/ p.occurrence > e.occurrence)
           change == p.status \notin PublicTerminal /\ source > p.statusSource
                     /\ (e.kind \in {"await", "resume"} \/
                         (e.kind = "receipt" /\ q.receipt[e.command] = "accepted"))
           status == IF bogusReceiptStatus THEN "working"
                     ELSE IF change THEN IF e.kind = "await" THEN "input_required" ELSE "working"
                     ELSE p.status
           occurrence == IF bogusReceiptStatus \/ change THEN e.occurrence ELSE p.occurrence
           statusSource == IF bogusReceiptStatus \/ change THEN source ELSE p.statusSource
       IN candidates' = [candidates EXCEPT ![slot] =
           [ready |-> TRUE, replica |-> r, fence |-> a.localFence[r],
            base |-> p.revision, source |-> source, status |-> status,
            occurrence |-> occurrence, statusSource |-> statusSource,
            rebuilt |-> p.rejectedAtAwait2]]
    /\ UNCHANGED <<a, q, p, events>>

CandidateCurrent(slot) ==
    /\ candidates[slot].ready
    /\ candidates[slot].replica \in ReplicaSet
    /\ C!Current(candidates[slot].replica)
    /\ candidates[slot].fence = a.localFence[candidates[slot].replica]
CommitProjection(slot) ==
    /\ CandidateCurrent(slot) \/ (Fault = "stale_lease_commit" /\ candidates[slot].ready
       /\ candidates[slot].fence < a.lease)
    /\ candidates[slot].base = p.revision \/ Fault = "rebase_stale_candidate"
    /\ LET candidate == candidates[slot]
           e == events[candidate.source]
           remoteReceipt == e.kind = "receipt" /\ e.command \in RemoteSet
           firstReceiptProjection == remoteReceipt /\ q.projected[e.command] = "none"
           advanceCursor == candidate.source = p.cursor + 1
       IN /\ IF remoteReceipt THEN Q!Project(e.command) ELSE UNCHANGED q
          /\ p' = [p EXCEPT
               !.badCommit = @ \/ ~CandidateCurrent(slot) \/ candidate.base # p.revision,
               !.status = candidate.status, !.occurrence = candidate.occurrence,
               !.statusSource = candidate.statusSource,
               !.cursor = IF advanceCursor THEN candidate.source ELSE @,
               !.revision = IF advanceCursor THEN @ + 1 ELSE @,
               !.lateCompleted = @ \/ (firstReceiptProjection /\ p.status = "completed"
                                      /\ q.receipt[e.command] = "accepted"),
               !.lateCanceled = @ \/ (firstReceiptProjection /\ p.status = "canceled"
                                     /\ q.receipt[e.command] = "accepted"),
               !.replayAfterAwait = @ \/ (remoteReceipt /\ ~firstReceiptProjection
                   /\ q.receipt[e.command] = "accepted"
                   /\ p.status = "input_required" /\ p.occurrence > e.occurrence),
               !.rebuiltAfterReject = @ \/ (candidate.rebuilt /\ candidate.base = p.revision /\
                   remoteReceipt /\ e.occurrence < p.occurrence)]
    /\ candidates' = [candidates EXCEPT ![slot] = EmptyCandidate]
    /\ UNCHANGED <<a, events>>
RejectCandidate(slot) ==
    /\ candidates[slot].ready
    /\ ~CandidateCurrent(slot) \/ candidates[slot].base # p.revision
    /\ candidates' = [candidates EXCEPT ![slot] = EmptyCandidate]
    /\ p' = [p EXCEPT !.rejectedStale = @ \/ CandidateCurrent(slot),
                     !.rejectedAtAwait2 = @ \/ (CandidateCurrent(slot) /\
                       p.status = "input_required" /\ p.occurrence = 2 /\
                       candidates[slot].occurrence < 2)]
    /\ UNCHANGED <<a, q, events>>

Next ==
    \/ Admission
    \/ \E attempt \in 1..2 : Accept(attempt)
    \/ \E outcome \in Terminal : Finish(outcome)
    \/ CloseInput
    \/ \E cmd \in CommandSet, occurrence \in 1..OccurrenceLimit : Submit(cmd, occurrence)
    \/ \E cmd \in CommandSet : Decide(cmd) \/ Persist(cmd) \/ Resume(cmd)
    \/ Advance
    \/ \E r \in ReplicaSet : RepairTerminal(r)
    \/ \E slot \in Slots, r \in ReplicaSet, source \in 1..Len(events) : PrepareProjection(slot, r, source)
    \/ \E slot \in Slots : CommitProjection(slot) \/ RejectCandidate(slot)
    \/ UNCHANGED vars
Spec == Init /\ [][Next]_vars

ComponentSafety ==
    /\ C!TypeOK /\ C!AtMostOneExecution /\ C!ZeroHorizonSingleClaim
    /\ C!SuppressionIrreversible /\ C!SuppressionExcludesDispatch
    /\ C!NoFalseCanceled /\ C!NoFalseCompleted /\ C!NoFalseFailed /\ C!RejectionSealed
    /\ C!TerminalPublicImmutable /\ C!AdmissionFenced
    /\ Q!TypeOK /\ Q!BindingImmutable /\ Q!OneAcceptedOwner /\ Q!OwnershipPreserved
    /\ Q!AcceptedMatchesOccurrence /\ Q!DecisionSound /\ Q!ReceiptImmutable
    /\ Q!ReceiptMatchesRequest /\ Q!InputCloseFenced /\ Q!ReceiptAuthority
    /\ Q!ReservationAccounted /\ Q!ProjectionComplete /\ Q!PublicFromReceipt
    /\ Q!PublicAnswerOnce /\ Q!ResumeFromReceipt /\ Q!ResumeOnce

TypeOK ==
    /\ p.status \in {"submitted", "input_required", "working"} \cup PublicTerminal
    /\ p.occurrence \in 0..OccurrenceLimit
    /\ p.cursor \in 0..Len(events) /\ p.statusSource \in 0..Len(events)
    /\ p.revision \in 0..(Len(events) + 1)
    /\ p.terminalSeen \in PublicTerminal \cup {"none"}
    /\ Len(events) <= Cardinality(CommandSet) + 2 * OccurrenceLimit
    /\ \A i \in 1..Len(events) :
          /\ events[i].kind \in {"await", "receipt", "resume"}
          /\ events[i].command \in CommandSet \cup {"none"}
          /\ events[i].occurrence \in 1..OccurrenceLimit
    /\ \A b \in {p.badCommit, p.rejectedStale, p.rejectedAtAwait2, p.lateCompleted, p.lateCanceled,
                  p.replayAfterAwait, p.rebuiltAfterReject} : b \in BOOLEAN
CandidateTypes ==
    /\ DOMAIN candidates = Slots
    /\ \A slot \in Slots :
        /\ candidates[slot].ready \in BOOLEAN /\ candidates[slot].rebuilt \in BOOLEAN
        /\ candidates[slot].replica \in ReplicaSet \cup {"none"}
        /\ candidates[slot].fence \in 0..LeaseLimit
        /\ candidates[slot].base \in 0..(Len(events) + 1)
        /\ candidates[slot].source \in 0..Len(events)
        /\ candidates[slot].statusSource \in 0..Len(events)
        /\ candidates[slot].occurrence \in 0..OccurrenceLimit
        /\ candidates[slot].status \in {"submitted", "input_required", "working"} \cup PublicTerminal
CommitAuthorized == ~p.badCommit
LifecycleCoupled ==
    /\ q.terminal = (a.engine \in Terminal)
    /\ q.closed = a.inputClosed
    /\ q.closed => a.engine \in Terminal
WorkHasExecution == (\E cmd \in CommandSet : q.decision[cmd] # "none"
    \/ q.receipt[cmd] # "none") => a.executions = 1
TerminalAbsorbing == p.terminalSeen # "none" => p.status = p.terminalSeen
PublicTerminalTruth ==
    /\ p.status = "completed" => a.engine = "completed"
    /\ p.status = "canceled" => a.engine = "canceled" \/ a.suppressedEver
    /\ p.status = "failed" => a.admission = "rejected" /\ a.executions = 0

LifecycleSources == {i \in 1..p.cursor : events[i].kind \in {"await", "resume"}
    \/ (events[i].kind = "receipt" /\ q.receipt[events[i].command] = "accepted")}
LatestLifecycle == IF LifecycleSources = {} THEN 0
    ELSE CHOOSE i \in LifecycleSources : \A j \in LifecycleSources : i >= j
PublicLifecycleMatches ==
    IF p.terminalSeen # "none" THEN p.status = p.terminalSeen
    ELSE IF LatestLifecycle = 0 THEN p.status = "submitted"
    ELSE /\ p.status = IF events[LatestLifecycle].kind = "await" THEN "input_required" ELSE "working"
         /\ p.occurrence = events[LatestLifecycle].occurrence
         /\ p.statusSource = LatestLifecycle
NewerAwaitPreserved ==
    (p.terminalSeen = "none" /\ LatestLifecycle # 0 /\ events[LatestLifecycle].kind = "await")
    => p.status = "input_required" /\ p.occurrence = events[LatestLifecycle].occurrence
CursorComplete == \A i \in 1..p.cursor :
    (events[i].kind = "receipt" /\ events[i].command \in RemoteSet)
    => q.projected[events[i].command] = q.receipt[events[i].command]

WitnessLateAfterCompleted == ~p.lateCompleted
WitnessLateAfterCanceled == ~p.lateCanceled
WitnessReplayAfterNewAwait == ~p.replayAfterAwait
WitnessStaleCandidateRebuilt == ~(p.rejectedAtAwait2 /\ p.rebuiltAfterReject /\
    p.status = "input_required" /\ p.occurrence = 2)
WitnessFreshReceiptWorking == ~(a.engine = "running" /\ p.status = "working" /\
    \E cmd \in RemoteSet : q.historyCount[cmd] = 1 /\ q.receipt[cmd] = "accepted" /\
        q.resumeCount[q.binding[cmd][1]] = 0 /\ p.occurrence = q.binding[cmd][1])
WitnessDelayedStartThenAnswer == ~(a.delayedAcceptance /\ \E cmd \in RemoteSet : q.historyCount[cmd] = 1)
=============================================================================
