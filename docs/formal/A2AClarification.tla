-------------------------- MODULE A2AClarification ---------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Commands, Remote, Bodies, MaxOccurrences, Fault, AllowCrash
Occurrences == 1..MaxOccurrences
Outcomes == {"accepted", "stale", "conflict", "terminal"}
NoRequest == <<0, "none">>
(* Every runtime occurrence deliberately reuses the SAME planner-facing ID.
   Only the monotonically increasing occurrence number distinguishes waits. *)
PlannerLabel(o) == "reused-await-label"

VARIABLE s
vars == <<s>>
Init == s = [
    current |-> 1, terminal |-> FALSE, closed |-> FALSE,
    up |-> TRUE, crashUsed |-> FALSE, crashCommand |-> "none", crashStage |-> "none",
    binding |-> [c \in Commands |-> NoRequest],
    firstBinding |-> [c \in Commands |-> NoRequest],
    reservation |-> "none",
    decision |-> [c \in Commands |-> "none"],
    decisionOccurrence |-> [c \in Commands |-> 0],
    decisionPriorOwner |-> [c \in Commands |-> "none"],
    owner |-> [o \in Occurrences |-> "none"],
    acceptedDecisions |-> [o \in Occurrences |-> {}],
    receipt |-> [c \in Commands |-> "none"],
    firstReceipt |-> [c \in Commands |-> "none"],
    receiptBinding |-> [c \in Commands |-> NoRequest],
    acceptedAtClose |-> {},
    projected |-> [c \in Commands |-> "none"],
    historyCount |-> [c \in Commands |-> 0],
    resumeCount |-> [o \in Occurrences |-> 0], resumeEvidence |-> {},
    retryReplies |-> {}, retryClosed |-> FALSE, retryOccurrence |-> 0,
    replayCleanup |-> FALSE, blocked |-> {}]

Bound(c) == s.binding[c] # NoRequest
AcceptedReceipts == {c \in Commands : s.receipt[c] = "accepted"}

(* Remote admission is serialized by one provisional task reservation. Host
   commands have immutable IDs too, but bypass this PROTOCOL reservation. *)
Submit(c, o, b) ==
    /\ s.up /\ ~Bound(c) /\ o <= s.current
    /\ c \notin Remote \/ s.reservation = "none"
    /\ s' = [s EXCEPT !.binding[c] = <<o, b>>, !.firstBinding[c] = <<o, b>>,
                     !.reservation = IF c \in Remote THEN c ELSE @]
AnswerPending(c) ==
    /\ s.up /\ c \in Remote /\ ~Bound(c) /\ s.reservation # "none"
    /\ c \notin s.blocked
    /\ s' = [s EXCEPT !.blocked = @ \cup {c}]

DecisionFor(c) ==
    IF s.binding[c][1] # s.current /\ Fault # "planner_label_only" THEN IF Fault = "stale_as_conflict" THEN "conflict" ELSE "stale"
    ELSE IF s.owner[s.current] # "none" /\ Fault # "competing_acceptance" THEN "conflict"
    ELSE "accepted"
Decide(c) ==
    /\ s.up /\ ~s.terminal /\ Bound(c) /\ s.decision[c] = "none"
    /\ LET result == DecisionFor(c)
       IN s' = [s EXCEPT
           !.decision[c] = result,
           !.decisionOccurrence[c] = s.current,
           !.decisionPriorOwner[c] = s.owner[s.current],
           !.owner[s.current] = IF result = "accepted" THEN c ELSE @,
           !.acceptedDecisions[s.current] = IF result = "accepted" THEN @ \cup {c} ELSE @]

Terminate ==
    /\ ~s.terminal
    /\ s' = [s EXCEPT !.terminal = TRUE]
CloseInput ==
    /\ s.up /\ s.terminal /\ ~s.closed
    /\ s' = [s EXCEPT !.closed = TRUE, !.acceptedAtClose = AcceptedReceipts]

(* A workflow decision is NOT the canonical receipt. Closure can win before
   append, yielding terminal even after a provisional accepted decision.
   Existing receipts always win; all other late decisions resolve terminal. *)
Persist(c) ==
    /\ s.up /\ Bound(c) /\ s.receipt[c] = "none"
    /\ s.decision[c] # "none" \/ s.closed
    /\ LET result == IF s.closed /\ Fault # "append_after_close" THEN "terminal"
                     ELSE s.decision[c]
       IN /\ result \in Outcomes
          /\ s' = [s EXCEPT !.receipt[c] = result, !.firstReceipt[c] = result,
                            !.receiptBinding[c] = s.binding[c]]

(* Equal retry reads the canonical outcome, even after current await changes.
   Reusing a command ID with different occurrence/body returns conflict without
   replacing the binding or its original canonical receipt. *)
Retry(c, o, b) ==
    /\ s.up /\ Bound(c)
    /\ <<o, b>> # s.binding[c] \/ s.receipt[c] # "none"
    /\ LET equal == <<o, b>> = s.binding[c]
           reply == IF ~equal THEN "conflict"
                    ELSE IF Fault = "retry_redecides"
                         THEN IF s.closed THEN "terminal" ELSE DecisionFor(c)
                         ELSE s.receipt[c]
       IN s' = [s EXCEPT
           !.retryReplies = {<<c, o, b, reply>>},
           !.retryClosed = s.closed, !.retryOccurrence = s.current,
           !.receipt[c] = IF equal /\ Fault = "retry_redecides" THEN reply ELSE @,
           !.binding[c] = IF ~equal /\ Fault = "changed_body_rebinds" THEN <<o, b>> ELSE @]

(* A retry of projection/cleanup is enabled after its first commit. The slot
   may now belong to a later command: release is a command-ID compare-and-swap.
   Host receipts can own/resume a wait, but never publish host content here. *)
Project(c) ==
    /\ s.up /\ c \in Remote /\ Bound(c)
    /\ s.receipt[c] # "none" \/ (Fault = "project_decision" /\ s.decision[c] # "none")
    /\ s.historyCount[c] < 2
    /\ LET result == IF s.receipt[c] # "none" THEN s.receipt[c] ELSE s.decision[c]
           rejected == result # "accepted"
       IN s' = [s EXCEPT
           !.projected[c] = result,
           !.replayCleanup = @ \/ (rejected /\ s.projected[c] # "none" /\
               s.reservation # "none" /\ s.reservation # c),
           !.historyCount[c] = IF result = "accepted" /\
               (s.historyCount[c] = 0 \/ Fault = "duplicate_public_answer") /\
               Fault # "omit_public_answer" THEN @ + 1 ELSE @,
           !.reservation = IF (s.reservation = c /\ Fault # "retain_reservation") \/ (rejected /\ Fault = "release_other")
                            THEN "none" ELSE @,
           !.owner[s.binding[c][1]] = IF rejected /\ Fault = "rejection_erases_owner"
                                      THEN "none" ELSE @]

Resume(c) ==
    /\ s.up /\ ~s.terminal /\ Bound(c)
    /\ s.receipt[c] = "accepted"
       \/ (Fault = "resume_decision" /\ s.decision[c] = "accepted")
       \/ (Fault = "resume_rejection" /\ s.receipt[c] \in Outcomes)
    /\ s.decisionOccurrence[c] = s.current \/ Fault = "resume_rejection"
    /\ s.resumeCount[s.current] = 0
       \/ (Fault = "duplicate_resume" /\ s.resumeCount[s.current] < 2)
    /\ s' = [s EXCEPT !.resumeCount[s.current] = @ + 1,
                     !.resumeEvidence = @ \cup {<<c, s.current>>}]
Advance ==
    /\ s.up /\ ~s.terminal /\ s.current < MaxOccurrences
    /\ s.resumeCount[s.current] = 1
    /\ s' = [s EXCEPT !.current = @ + 1]

Crash(c, stage) ==
    /\ AllowCrash /\ s.up /\ ~s.crashUsed /\ c \in Remote
    /\ IF stage = "decision"
          THEN s.decision[c] # "none" /\ s.receipt[c] = "none"
          ELSE s.receipt[c] # "none" /\ s.projected[c] = "none"
    /\ s' = [s EXCEPT !.up = FALSE, !.crashUsed = TRUE,
                     !.crashCommand = c, !.crashStage = stage]
Restart ==
    /\ ~s.up
    /\ s' = [s EXCEPT !.up = TRUE]

Next ==
    \/ \E c \in Commands, o \in Occurrences, b \in Bodies : Submit(c, o, b) \/ Retry(c, o, b)
    \/ \E c \in Commands : AnswerPending(c) \/ Decide(c) \/ Persist(c) \/ Project(c) \/ Resume(c)
    \/ \E c \in Remote, stage \in {"decision", "receipt"} : Crash(c, stage)
    \/ Terminate \/ CloseInput \/ Advance \/ Restart
    (* Safety only: unavailable workers/stores can leave any stage pending. *)
    \/ UNCHANGED vars
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ s.current \in Occurrences
    /\ s.binding \in [Commands -> ({NoRequest} \cup (Occurrences \X Bodies))]
    /\ s.firstBinding \in [Commands -> ({NoRequest} \cup (Occurrences \X Bodies))]
    /\ s.reservation \in Remote \cup {"none"}
    /\ s.decision \in [Commands -> Outcomes \cup {"none"}]
    /\ s.receipt \in [Commands -> Outcomes \cup {"none"}]
    /\ s.firstReceipt \in [Commands -> Outcomes \cup {"none"}]
    /\ s.projected \in [Commands -> Outcomes \cup {"none"}]
    /\ s.receiptBinding \in [Commands -> ({NoRequest} \cup (Occurrences \X Bodies))]
    /\ s.owner \in [Occurrences -> Commands \cup {"none"}]
    /\ s.acceptedDecisions \in [Occurrences -> SUBSET Commands]
    /\ s.decisionPriorOwner \in [Commands -> Commands \cup {"none"}]
    /\ s.retryOccurrence \in 0..MaxOccurrences
    /\ s.decisionOccurrence \in [Commands -> 0..MaxOccurrences]
    /\ s.historyCount \in [Commands -> 0..2]
    /\ s.resumeCount \in [Occurrences -> 0..2]
    /\ s.resumeEvidence \subseteq Commands \X Occurrences
    /\ s.retryReplies \subseteq Commands \X Occurrences \X Bodies \X Outcomes
    /\ s.acceptedAtClose \subseteq Commands /\ s.blocked \subseteq Remote
    /\ s.crashCommand \in Remote \cup {"none"}
    /\ s.crashStage \in {"none", "decision", "receipt"}
    /\ \A b \in {s.up, s.crashUsed, s.terminal, s.closed, s.retryClosed, s.replayCleanup} : b \in BOOLEAN

BindingImmutable == s.binding = s.firstBinding
OneAcceptedOwner == \A o \in Occurrences : Cardinality(s.acceptedDecisions[o]) <= 1
OwnershipPreserved == \A o \in Occurrences : \A c \in s.acceptedDecisions[o] : s.owner[o] = c
AcceptedMatchesOccurrence == \A o \in Occurrences : \A c \in s.acceptedDecisions[o] : s.binding[c][1] = o
DecisionSound == \A c \in Commands : s.decision[c] # "none" =>
    s.decision[c] = IF s.firstBinding[c][1] # s.decisionOccurrence[c] THEN "stale"
                   ELSE IF s.decisionPriorOwner[c] # "none" THEN "conflict"
                   ELSE "accepted"
ReceiptImmutable == s.receipt = s.firstReceipt
ReceiptMatchesRequest == \A c \in Commands : s.receipt[c] # "none" => s.receiptBinding[c] = s.firstBinding[c]
InputCloseFenced == s.closed => AcceptedReceipts = s.acceptedAtClose
ReceiptAuthority == \A c \in Commands :
    /\ s.receipt[c] = "accepted" => s.decision[c] = "accepted" /\ c \in s.acceptedDecisions[s.binding[c][1]]
    /\ s.receipt[c] = "terminal" => s.closed
    /\ s.receipt[c] \in {"stale", "conflict"} => s.receipt[c] = s.decision[c]
RetryOutcomeStable == \A reply \in s.retryReplies :
    IF <<reply[2], reply[3]>> = s.firstBinding[reply[1]]
       THEN reply[4] = s.firstReceipt[reply[1]]
       ELSE reply[4] = "conflict"
ReservationAccounted == \A c \in Remote : Bound(c) /\ s.projected[c] = "none" => s.reservation = c
ProjectionComplete == \A c \in Remote : s.projected[c] # "none" =>
    /\ s.reservation # c
    /\ s.historyCount[c] = IF s.projected[c] = "accepted" THEN 1 ELSE 0
PublicFromReceipt == \A c \in Commands :
    /\ s.projected[c] # "none" => c \in Remote /\ s.projected[c] = s.receipt[c]
    /\ s.historyCount[c] > 0 => c \in Remote /\ s.receipt[c] = "accepted"
PublicAnswerOnce == \A c \in Commands : s.historyCount[c] <= 1
ResumeFromReceipt == \A e \in s.resumeEvidence :
    s.receipt[e[1]] = "accepted" /\ s.binding[e[1]][1] = e[2]
ResumeOnce == \A o \in Occurrences : s.resumeCount[o] <= 1

WitnessTwoOccurrences == ~(s.current = 2 /\ PlannerLabel(1) = PlannerLabel(2) /\ \A o \in Occurrences :
    \E c \in Remote : s.receipt[c] = "accepted" /\ s.binding[c][1] = o /\ s.resumeCount[o] = 1)
WitnessHostConflictCleanup == ~(\E r \in Remote, h \in Commands \ Remote :
    s.projected[r] = "conflict" /\ s.receipt[h] = "accepted" /\
    s.binding[r][1] = s.binding[h][1] /\ s.owner[s.binding[h][1]] = h /\ s.reservation # r)
WitnessStaleRetry == ~(s.retryClosed /\ \E reply \in s.retryReplies :
    s.receipt[reply[1]] = "stale" /\ <<reply[2], reply[3]>> = s.firstBinding[reply[1]] /\ reply[4] = "stale")
WitnessConflictRetry == ~(\E reply \in s.retryReplies :
    s.retryOccurrence > s.firstBinding[reply[1]][1] /\
    s.receipt[reply[1]] = "conflict" /\ <<reply[2], reply[3]>> = s.firstBinding[reply[1]] /\ reply[4] = "conflict")
WitnessCleanupAfterNewReservation == ~s.replayCleanup
WitnessChangedBodyRetry == ~(\E reply \in s.retryReplies :
    reply[3] # s.firstBinding[reply[1]][2] /\ reply[4] = "conflict" /\ s.receipt[reply[1]] = "accepted")
WitnessDecisionCrashRecovery == ~(s.crashStage = "decision" /\ s.up /\
    s.projected[s.crashCommand] = "accepted")
WitnessReceiptCrashRecovery == ~(s.crashStage = "receipt" /\ s.up /\
    s.projected[s.crashCommand] = "accepted")
WitnessCloseWins == ~(\E c \in Remote : s.decision[c] = "accepted" /\
    s.projected[c] = "terminal" /\ s.historyCount[c] = 0 /\ s.resumeCount[s.binding[c][1]] = 0)
WitnessAnswerPending == ~(s.blocked # {})
=============================================================================
