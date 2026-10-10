----------------------------- MODULE CallCommit -----------------------------
(***************************************************************************)
(* Planner steps under the commit protocol of                              *)
(* docs/wips/context_compaction.md (revision 9), sections 4.5 and 4.13.    *)
(*                                                                         *)
(* A run has Steps planner steps. For each step the workflow schedules     *)
(* activity attempts; each attempt makes a producing model call and an     *)
(* auxiliary one, in order. The innermost wrapper takes a sequence number  *)
(* from NextSeq, writes a started call record, sends, receives, and writes *)
(* the completed or failed record. A worker can crash between any two      *)
(* steps; an attempt can time out and keep running as a zombie, even into  *)
(* the next planner step. A finished attempt stores its output, commits    *)
(* its presentation (unless it reports a context overflow) and returns;    *)
(* the return can be lost. The workflow adopts the first attempt it hears  *)
(* from, indexes that attempt's output, and records its call keys in the   *)
(* step's event; when every attempt has timed out, the step fails.         *)
(*                                                                         *)
(* Reconstruction (Classify) reads only call records, presentation events, *)
(* step events and the index. The invariants compare it with ground truth  *)
(* the actions record separately (adopted, doneWritten, pres.attempt).     *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Steps,          \* planner steps in the run
    MaxAttempts,    \* activity attempts the retry policy allows per step
    Fault           \* "none", or one rule deliberately broken

Faults == {"none", "send_before_started", "adopt_by_entry",
           "presentation_without_key", "overflow_commits_presentation",
           "incomplete_as_abandoned", "seq_per_attempt", "index_first_stored",
           "retry_same_attempt", "intercepted_record_event"}

ASSUME Fault \in Faults
ASSUME Steps \in Nat /\ Steps >= 1 /\ MaxAttempts \in Nat /\ MaxAttempts >= 1

StepIds == 1..Steps
Attempts == 1..MaxAttempts

\* Call c of attempt a of step s: c = 1 produces the assistant message, c = 2
\* is an auxiliary call the planner makes for itself.
Keys == StepIds \X Attempts \X {1, 2}
AttemptsOf == StepIds \X Attempts

None == "none"
NoEvent == [keys |-> {}]
NoKey == <<0, 0, 0>>
NoOutput == <<0, 0>>

VARIABLES
    rec,        \* store: call key -> "none", "started", "completed", "failed"
    seqOf,      \* store: call key -> its sequence number, 0 before started
    nextSeq,    \* NextSeq's counter for the run
    sent,       \* calls sent to the provider
    got,        \* calls whose response or error the wrapper holds in memory
    outputs,    \* store: attempts whose output is stored
    pres,       \* run log: presentation events [attempt, key]; attempt is ground truth
    event,      \* run log: step -> [keys] of the adopted attempt, or NoEvent
    index,      \* workflow: step -> the attempt whose output the index references
    cur,        \* step being planned
    att,        \* attempts scheduled in the current step
    astate,     \* attempt -> "idle", "running", "returned", "crashed"
    timedOut,   \* attempt -> the workflow gave up on it
    outcome,    \* attempt -> "ok" or "overflow", once decided
    adopted,    \* ground truth: step -> the attempt the workflow adopted, 0 if none
    doneWritten,\* ground truth: calls whose completion write happened
    failed      \* the step failed with a typed error after its last attempt timed out

vars == <<rec, seqOf, nextSeq, sent, got, outputs, pres, event, index, cur, att,
          astate, timedOut, outcome, adopted, doneWritten, failed>>

Init ==
    /\ rec = [k \in Keys |-> "none"]
    /\ seqOf = [k \in Keys |-> 0]
    /\ nextSeq = 1
    /\ sent = {}
    /\ got = {}
    /\ outputs = {}
    /\ pres = {}
    /\ event = [s \in StepIds |-> NoEvent]
    /\ index = [s \in StepIds |-> 0]
    /\ cur = 1
    /\ att = 1
    /\ astate = [a \in AttemptsOf |-> IF a = <<1, 1>> THEN "running" ELSE "idle"]
    /\ timedOut = [a \in AttemptsOf |-> FALSE]
    /\ outcome = [a \in AttemptsOf |-> None]
    /\ adopted = [s \in StepIds |-> 0]
    /\ doneWritten = {}
    /\ failed = FALSE

AttOf(k) == <<k[1], k[2]>>
Live(a) == astate[a] = "running"
CallsOf(a) == {<<a[1], a[2], c>> : c \in {1, 2}}

\* Calls run in order: the auxiliary call starts after the producing one is
\* recorded as done.
MayStart(k) == k[3] = 1 \/ rec[<<k[1], k[2], 1>>] \in {"completed", "failed"}

Done(a) == \A k \in CallsOf(a) : rec[k] \in {"completed", "failed"}

-----------------------------------------------------------------------------
(* The innermost model wrapper.                                             *)

\* NextSeq, then PutCall(started) with the request, before sending. The
\* broken configurations send first, or number calls per attempt.
WriteStarted(k) ==
    /\ Live(AttOf(k))
    /\ rec[k] = "none"
    /\ MayStart(k)
    /\ Fault = "send_before_started" => k \in sent
    /\ rec' = [rec EXCEPT ![k] = "started"]
    /\ seqOf' = [seqOf EXCEPT ![k] = IF Fault = "seq_per_attempt" THEN k[3] ELSE nextSeq]
    /\ nextSeq' = nextSeq + 1
    /\ UNCHANGED <<sent, got, outputs, pres, event, index, cur, att, astate, timedOut,
                   outcome, adopted, doneWritten, failed>>

Send(k) ==
    /\ Live(AttOf(k))
    /\ k \notin sent
    /\ MayStart(k)
    /\ IF Fault = "send_before_started" THEN rec[k] = "none" ELSE rec[k] = "started"
    /\ sent' = sent \cup {k}
    /\ UNCHANGED <<rec, seqOf, nextSeq, got, outputs, pres, event, index, cur, att,
                   astate, timedOut, outcome, adopted, doneWritten, failed>>

\* The provider answers (a response or an error); the wrapper holds it.
Receive(k) ==
    /\ Live(AttOf(k))
    /\ k \in sent \ got
    /\ got' = got \cup {k}
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, outputs, pres, event, index, cur, att,
                   astate, timedOut, outcome, adopted, doneWritten, failed>>

\* PutCall(completed) or PutCall(failed).
WriteDone(k) ==
    /\ Live(AttOf(k))
    /\ k \in got
    /\ rec[k] = "started"
    /\ \E r \in {"completed", "failed"} : rec' = [rec EXCEPT ![k] = r]
    /\ doneWritten' = doneWritten \cup {k}
    /\ UNCHANGED <<seqOf, nextSeq, sent, got, outputs, pres, event, index, cur, att,
                   astate, timedOut, outcome, adopted, failed>>

-----------------------------------------------------------------------------
(* The planner activity and the workflow.                                   *)

Crash(a) ==
    /\ Live(a)
    /\ astate' = [astate EXCEPT ![a] = "crashed"]
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, outputs, pres, event, index, cur,
                   att, timedOut, outcome, adopted, doneWritten, failed>>

\* The attempt finishes: it stores its output and, on success, commits its
\* presentation carrying the producing call's key; on a context overflow it
\* commits none. Then it returns; the return may never arrive.
Return(a) ==
    /\ Live(a)
    /\ Done(a)
    /\ outputs' = outputs \cup {a}
    /\ \E o \in {"ok", "overflow"} :
          /\ outcome' = [outcome EXCEPT ![a] = o]
          /\ pres' = IF o = "ok" \/ Fault = "overflow_commits_presentation"
                        THEN pres \cup {[attempt |-> a,
                                         key |-> IF Fault = "presentation_without_key"
                                                    THEN NoKey ELSE <<a[1], a[2], 1>>]}
                        ELSE pres
    /\ astate' = [astate EXCEPT ![a] = "returned"]
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, event, index, cur, att, timedOut,
                   adopted, doneWritten, failed>>

\* The workflow stops waiting for the current attempt; a zombie keeps running.
Timeout ==
    /\ ~failed
    /\ adopted[cur] = 0
    /\ ~timedOut[<<cur, att>>]
    /\ timedOut' = [timedOut EXCEPT ![<<cur, att>>] = TRUE]
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, outputs, pres, event, index, cur,
                   att, astate, outcome, adopted, doneWritten, failed>>

\* The workflow schedules the next attempt after a timeout. The broken
\* configuration reschedules the same attempt without counting it, so the
\* retry policy never runs out.
Retry ==
    /\ ~failed
    /\ adopted[cur] = 0
    /\ timedOut[<<cur, att>>]
    /\ IF Fault = "retry_same_attempt"
          THEN /\ astate' = [astate EXCEPT ![<<cur, att>>] = "running"]
               /\ timedOut' = [timedOut EXCEPT ![<<cur, att>>] = FALSE]
               /\ UNCHANGED att
          ELSE /\ att < MaxAttempts
               /\ att' = att + 1
               /\ astate' = [astate EXCEPT ![<<cur, att + 1>>] = "running"]
               /\ UNCHANGED timedOut
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, outputs, pres, event, index, cur,
                   outcome, adopted, doneWritten, failed>>

\* The last attempt timed out: the step fails with a typed error.
StepFail ==
    /\ Fault # "retry_same_attempt"
    /\ ~failed
    /\ adopted[cur] = 0
    /\ att = MaxAttempts
    /\ timedOut[<<cur, att>>]
    /\ failed' = TRUE
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, outputs, pres, event, index, cur,
                   att, astate, timedOut, outcome, adopted, doneWritten>>

\* The workflow receives an attempt's output, adopts it, indexes its output,
\* records the call keys it returned in the step's event, and moves to the
\* next step. The step event is a record event, so it skips BeforeEvent
\* interceptors and always lands as written. The broken configurations record
\* only the call a transcript entry names, index the first output stored in
\* the step, or let an interceptor drop the step event.
Adopt(a) ==
    /\ ~failed
    /\ a[1] = cur
    /\ astate[a] = "returned"
    /\ ~timedOut[a]
    /\ adopted[cur] = 0
    /\ adopted' = [adopted EXCEPT ![cur] = a[2]]
    /\ index' = [index EXCEPT ![cur] =
                    IF Fault = "index_first_stored"
                       THEN CHOOSE b \in Attempts : <<cur, b>> \in outputs /\ \A c \in Attempts : <<cur, c>> \in outputs => b <= c
                       ELSE a[2]]
    /\ LET written == [keys |-> IF Fault = "adopt_by_entry" THEN {<<a[1], a[2], 1>>}
                                ELSE {k \in CallsOf(a) : rec[k] # "none"}]
       IN \E e \in IF Fault = "intercepted_record_event" THEN {written, NoEvent} ELSE {written} :
             event' = [event EXCEPT ![cur] = e]
    /\ IF cur < Steps
          THEN /\ cur' = cur + 1
               /\ att' = 1
               /\ astate' = [astate EXCEPT ![<<cur + 1, 1>>] = "running"]
          ELSE UNCHANGED <<cur, att, astate>>
    /\ UNCHANGED <<rec, seqOf, nextSeq, sent, got, outputs, pres, timedOut, outcome,
                   doneWritten, failed>>

Finished == failed \/ adopted[Steps] # 0

\* The run has ended and no attempt can still act.
Idle == Finished /\ \A a \in AttemptsOf : ~Live(a)
Stop == Idle /\ UNCHANGED vars

Progress ==
    \/ \E k \in Keys : WriteStarted(k) \/ Send(k) \/ Receive(k) \/ WriteDone(k)
    \/ \E a \in AttemptsOf : Return(a) \/ Adopt(a)
    \/ Timeout \/ Retry \/ StepFail

Next ==
    \/ Progress
    \/ \E a \in AttemptsOf : Crash(a)
    \/ Stop

Spec == Init /\ [][Next]_vars /\ WF_vars(Progress)

-----------------------------------------------------------------------------
(* Reconstruction: RunRecord's classification, from the store and the log. *)

Adopted(k) == k \in event[k[1]].keys

Classify(k) ==
    CASE rec[k] = "none" -> "none"
      [] Adopted(k) -> "adopted"
      [] rec[k] = "started" /\ Fault # "incomplete_as_abandoned" -> "incomplete"
      [] OTHER -> "abandoned"

\* What happened, from the ground-truth variables.
Truth(k) ==
    CASE rec[k] = "none" -> "none"
      [] adopted[k[1]] = k[2] -> "adopted"
      [] k \notin doneWritten -> "incomplete"
      [] OTHER -> "abandoned"

\* The attempt reconstruction takes as adopted for a step: the one whose
\* producing call the step's event names.
AdoptedAttempt(s) ==
    IF \E a \in Attempts : <<s, a, 1>> \in event[s].keys
       THEN CHOOSE a \in Attempts : <<s, a, 1>> \in event[s].keys
       ELSE 0

PresentationAdopted(p) ==
    IF p.key = NoKey THEN event[p.attempt[1]] # NoEvent ELSE Adopted(p.key)

-----------------------------------------------------------------------------
(* Invariants and properties.                                               *)

\* No request reaches a provider without a call record.
SentImpliesRecorded == \A k \in sent : rec[k] # "none"

\* Every started call has its own sequence number.
KeysUnique ==
    \A k1, k2 \in Keys : k1 # k2 /\ rec[k1] # "none" /\ rec[k2] # "none" => seqOf[k1] # seqOf[k2]

\* Reconstruction classifies every call as what happened to it: adopted
\* (auxiliary calls included), abandoned, or incomplete.
ClassificationCorrect == \A k \in Keys : Classify(k) = Truth(k)

\* The index references the output of the attempt reconstruction reports as
\* adopted.
IndexMatchesAdoption == \A s \in StepIds : index[s] = AdoptedAttempt(s)

\* Every committed presentation is classified correctly as adopted or
\* "presented, not adopted".
PresentationsClassified ==
    \A p \in pres : PresentationAdopted(p) <=> p.attempt[2] = adopted[p.attempt[1]]

\* An attempt that reports a context overflow shows the user nothing.
NoPresentationFromOverflow == \A p \in pres : outcome[p.attempt] = "ok"

\* State constraint for the two-step configuration: at most one timeout in
\* the run, enough for a zombie that writes into the next step.
OneTimeout == Cardinality({a \in AttemptsOf : timedOut[a]}) <= 1

\* Every step is eventually adopted or fails with a typed error.
StepsEnd == <>Finished

\* Witness: a zombie attempt writes a completed record after its step has
\* adopted another attempt. Checked as an invariant that must be violated.
WitnessZombieAfterAdoption ==
    ~(\E k \in Keys : adopted[k[1]] # 0 /\ adopted[k[1]] # k[2] /\ k \in doneWritten
         /\ timedOut[AttOf(k)] /\ cur > k[1])
=============================================================================
