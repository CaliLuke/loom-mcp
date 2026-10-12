------------------------- MODULE SessionContinuation -------------------------
(***************************************************************************)
(* One session of several runs under the context-compaction design in     *)
(* docs/wips/context_compaction.md (revision 9), sections 4.9 and 4.16.    *)
(*                                                                         *)
(* Root runs start from the session's head continuation, add user          *)
(* messages, may write a summary that cites one user message, seal, and    *)
(* publish their continuation with AdvanceContinuation: one atomic,        *)
(* idempotent step in the session store. An acknowledgement can be lost,   *)
(* so SealRecord retries; a workflow can be terminated at any point. Child *)
(* runs share their root's session and never publish.                      *)
(*                                                                         *)
(* Message IDs are local to a run and are reused across runs. An entry     *)
(* carried into a later run keeps its origin: the position of the run      *)
(* that wrote it in the continuation chain, and its ID there. A summary    *)
(* records the run it was written in, so a bare citation keeps meaning a   *)
(* message of that run after the summary is copied forward.               *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
    Roots,      \* root runs of the session
    Children,   \* child runs (agent-as-tool) inside root runs
    MaxMsgs,    \* user messages a run may add
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "non_idempotent_publish", "publish_before_seal",
           "unconditional_head", "child_publishes", "order_by_local_id",
           "bare_cites_current_run", "raw_previous_summary",
           "already_without_compare", "advance_two_writes", "publish_failed"}

ASSUME Roots \cap Children = {}
ASSUME Fault \in Faults
ASSUME MaxMsgs \in Nat

Runs == Roots \cup Children

None == "none"

\* No summary yet; a record, so it compares with summaries.
NoSummary == [run |-> None, src |-> None]

\* No body presented yet.
NoBody == [id |-> None]


VARIABLES
    head,       \* the session's head continuation (a run id), or None
    pubs,       \* published continuations: [id, prev, pos, ver]
    phase,      \* run -> "idle" | "running" | "sealed" | "done" | "terminated"
    prev,       \* run -> the head it read at start, or None
    pos,        \* run -> its position in the continuation chain
    completed,  \* run -> completed with a final response
    sealed,     \* run -> run_sealed was appended
    outcome,    \* run -> the AdvanceContinuation result it received
    msgs,       \* run -> user messages in its view: [origin, t]
    added,      \* run -> user messages it wrote
    summ,       \* run -> its checkpoint's summary: [run, cite, intended], or NoSummary
    sums,       \* run -> summaries it has written
    presented,  \* run -> the continuation body its latest publish presented
    now         \* global time, to state chronological order

vars == <<head, pubs, phase, prev, pos, sealed, outcome, msgs, added, summ,
          sums, presented, now, completed>>

PubIds == {b.id : b \in pubs}

PosOf(id) == IF id = None THEN 0 ELSE (CHOOSE b \in pubs : b.id = id).pos

\* The continuation body a publish presents. SealRecord builds it from the
\* sealed index, so a retry rebuilds the same body (ver 1); a body that
\* differs (ver 2) is what a nondeterministic rebuild would produce, and the
\* store must refuse it.
Body(r, v) == [id |-> r, prev |-> prev[r], pos |-> pos[r], ver |-> v]

Init ==
    /\ head = None
    /\ pubs = {}
    /\ phase = [r \in Runs |-> "idle"]
    /\ prev = [r \in Runs |-> None]
    /\ pos = [r \in Runs |-> 0]
    /\ completed = [r \in Runs |-> FALSE]
    /\ sealed = [r \in Runs |-> FALSE]
    /\ outcome = [r \in Runs |-> "none"]
    /\ msgs = [r \in Runs |-> <<>>]
    /\ added = [r \in Runs |-> 0]
    /\ summ = [r \in Runs |-> NoSummary]
    /\ sums = [r \in Runs |-> 0]
    /\ presented = [r \in Runs |-> NoBody]
    /\ now = 0

-----------------------------------------------------------------------------
(* Runs.                                                                    *)

\* The run start activity reads the head and records it as the run's Prev,
\* whether the run continues from it or starts fresh. A continuing run
\* seeds the predecessor's carried user messages, keeping their origins, and
\* its copied summary, keeping the run it was written in.
Start(r) ==
    /\ phase[r] = "idle"
    /\ \E continue \in BOOLEAN :
          /\ continue => (head # None /\ r \in Roots)
          /\ phase' = [phase EXCEPT ![r] = "running"]
          /\ prev' = [prev EXCEPT ![r] = head]
          /\ pos' = [pos EXCEPT ![r] = PosOf(head) + 1]
          /\ msgs' = [msgs EXCEPT ![r] = IF continue THEN msgs[head] ELSE <<>>]
          /\ summ' = [summ EXCEPT ![r] = IF continue THEN summ[head] ELSE NoSummary]
    /\ UNCHANGED <<head, pubs, sealed, outcome, added, sums, presented, now, completed>>

\* The user sends a message. Its ID is local to the run.
AddUser(r) ==
    /\ phase[r] = "running"
    /\ added[r] < MaxMsgs
    /\ msgs' = [msgs EXCEPT ![r] = Append(@, [origin |-> <<pos[r], added[r] + 1>>, t |-> now])]
    /\ added' = [added EXCEPT ![r] = @ + 1]
    /\ now' = now + 1
    /\ UNCHANGED <<head, pubs, phase, prev, pos, sealed, outcome, summ, sums, presented, completed>>

\* How the view labels a message of origin o in run r.
Label(o, r) ==
    IF o[1] = pos[r] THEN [bare |-> TRUE, id |-> o[2]]
    ELSE [bare |-> FALSE, pos |-> o[1], id |-> o[2]]

\* The previous summary's citation as SummarizeContext's input shows it: a
\* bare citation of another run is rendered qualified by the run that wrote
\* the summary. The broken configuration passes the stored text through.
PreviousCite(r) ==
    IF summ[r].cite.bare /\ summ[r].run # r /\ Fault # "raw_previous_summary"
       THEN [bare |-> FALSE, pos |-> pos[summ[r].run], id |-> summ[r].cite.id]
       ELSE summ[r].cite

\* SummarizeContext writes a summary citing one user message: one it sees in
\* the view, labelled as the view labels it, or one it repeats from the
\* previous summary, written as its input showed it.
Summarize(r) ==
    /\ phase[r] = "running"
    /\ sums[r] < 2
    /\ \/ \E i \in 1..Len(msgs[r]) :
             summ' = [summ EXCEPT ![r] = [run |-> r, cite |-> Label(msgs[r][i].origin, r),
                                          intended |-> msgs[r][i].origin, src |-> "view"]]
       \/ /\ summ[r] # NoSummary
          /\ summ' = [summ EXCEPT ![r] = [run |-> r, cite |-> PreviousCite(r),
                                          intended |-> summ[r].intended,
                                          src |-> IF summ[r].run # r THEN "copy" ELSE "own"]]
    /\ sums' = [sums EXCEPT ![r] = @ + 1]
    /\ UNCHANGED <<head, pubs, phase, prev, pos, sealed, outcome, msgs, added, presented, now, completed>>

\* SealRecord appends run_sealed first. A child run's SealRecord publishes
\* nothing, so a child is done once sealed.
Seal(r) ==
    /\ phase[r] = "running"
    /\ \E success \in BOOLEAN :
          /\ completed' = [completed EXCEPT ![r] = success]
          /\ phase' = [phase EXCEPT ![r] = IF (success \/ Fault = "publish_failed") /\ (r \in Roots \/ Fault = "child_publishes") THEN "sealed" ELSE "done"]
          /\ outcome' = [outcome EXCEPT ![r] = IF success THEN @ ELSE "skipped"]
    /\ sealed' = [sealed EXCEPT ![r] = TRUE]
    /\ UNCHANGED <<head, pubs, prev, pos, msgs, added, summ, sums, presented, now>>

\* AdvanceContinuation(session, expected = Prev, next): one atomic store step
\* (one transaction in Mongo) that checks the ID before the head and returns
\* published, already published, conflict or superseded. The broken
\* configurations drop the idempotency check, ignore the expected head, or
\* publish before sealing.
StoredBody(r) == CHOOSE b \in pubs : b.id = r

AdvanceResult(r, b) ==
    CASE Fault = "unconditional_head" -> "published"
      [] Fault # "non_idempotent_publish" /\ r \in PubIds ->
            IF Fault = "already_without_compare" \/ StoredBody(r) = b THEN "already" ELSE "conflict"
      [] head = prev[r] -> "published"
      [] OTHER -> "superseded"

\* SealRecord publishes body b. The acknowledgement may be lost (ack =
\* FALSE), leaving the activity to retry; the store's effect stands either
\* way. A conflict fails the activity loudly.
PublishWith(r, ack) ==
    /\ \/ phase[r] = "sealed"
       \/ Fault = "publish_before_seal" /\ phase[r] = "running" /\ r \in Roots
    /\ \E v \in {1, 2} :
          LET b == Body(r, v)
              res == AdvanceResult(r, b)
          IN /\ IF res = "published"
                   THEN /\ pubs' = pubs \cup {b}
                        /\ head' = r
                   ELSE UNCHANGED <<pubs, head>>
             /\ presented' = [presented EXCEPT ![r] = b]
             /\ IF ack
                   THEN /\ outcome' = [outcome EXCEPT ![r] = res]
                        /\ phase' = [phase EXCEPT ![r] = "done"]
                   ELSE UNCHANGED <<outcome, phase>>
    /\ UNCHANGED <<prev, pos, sealed, msgs, added, summ, sums, now, completed>>

Publish(r) == PublishWith(r, TRUE)
PublishLost(r) == PublishWith(r, FALSE)

\* The broken configuration implements AdvanceContinuation as two writes:
\* it inserts the continuation, then the worker crashes before the
\* conditional head update. The retry finds the ID and gets "already".
PublishInsertOnly(r) ==
    /\ Fault = "advance_two_writes"
    /\ phase[r] = "sealed"
    /\ r \notin PubIds
    /\ head = prev[r]
    /\ pubs' = pubs \cup {Body(r, 1)}
    /\ presented' = [presented EXCEPT ![r] = Body(r, 1)]
    /\ UNCHANGED <<head, phase, prev, pos, sealed, outcome, msgs, added, summ, sums, now, completed>>

\* The workflow is terminated outside workflow code.
Terminate(r) ==
    /\ phase[r] \in {"running", "sealed"}
    /\ phase' = [phase EXCEPT ![r] = "terminated"]
    /\ UNCHANGED <<head, pubs, prev, pos, sealed, outcome, msgs, added, summ, sums, presented, now, completed>>

\* Every run has finished: the only legitimate end state.
Finished == \A r \in Runs : phase[r] \in {"done", "terminated"}

Done == Finished /\ UNCHANGED vars

Next ==
    \/ \E r \in Runs :
          \/ Start(r) \/ AddUser(r) \/ Summarize(r) \/ Seal(r)
          \/ Publish(r) \/ PublishLost(r) \/ PublishInsertOnly(r) \/ Terminate(r)
    \/ Done

\* A delivered acknowledgement is eventually possible for every sealed root.
Spec == Init /\ [][Next]_vars /\ \A r \in Roots : WF_vars(Publish(r))

-----------------------------------------------------------------------------
(* Invariants.                                                              *)

\* A run told "superseded" really did not publish: a retry after a lost
\* acknowledgement recognises its own earlier success.
NoFalseSupersede == \A r \in Runs : outcome[r] = "superseded" => r \notin PubIds

\* A published continuation always comes from a sealed run.
PublishedImpliesSealed == \A r \in PubIds : sealed[r] /\ completed[r]

\* Continuations form one chain: no continuation has two successors.
OneSuccessor == \A a, b \in pubs : a.prev = b.prev => a = b

\* Positions follow the chain, so chain labels (rN) are unambiguous.
PositionsFollowChain ==
    \A b \in pubs :
        IF b.prev = None THEN b.pos = 1
        ELSE \E a \in pubs : a.id = b.prev /\ b.pos = a.pos + 1

\* Only root runs publish.
ChildNeverPublishes == PubIds \cap Children = {}

\* Ordering by origin (chain position, then ID in that run) is the order in
\* which the user wrote the messages. The broken configuration compares the
\* local IDs alone.
OriginBefore(a, b) ==
    IF Fault = "order_by_local_id"
       THEN a.origin[2] < b.origin[2]
       ELSE \/ a.origin[1] < b.origin[1]
            \/ a.origin[1] = b.origin[1] /\ a.origin[2] < b.origin[2]

OrderIsChronological ==
    \A r \in Runs : \A i, j \in 1..Len(msgs[r]) :
        OriginBefore(msgs[r][i], msgs[r][j]) <=> msgs[r][i].t < msgs[r][j].t

\* A citation resolves to the message the summariser meant, in every run the
\* summary has been copied into. A bare citation means the run that wrote
\* the summary; the broken configuration resolves it in the current run.
Resolve(s, r) ==
    IF ~s.cite.bare THEN <<s.cite.pos, s.cite.id>>
    ELSE IF Fault = "bare_cites_current_run" THEN <<pos[r], s.cite.id>>
    ELSE <<pos[s.run], s.cite.id>>

CitationsResolve ==
    \A r \in Runs : summ[r] # NoSummary => Resolve(summ[r], r) = summ[r].intended

\* A retry that gets "already published" presented the body the store holds;
\* a different body fails loudly instead.
NoSilentBodyMismatch ==
    \A r \in Runs : outcome[r] = "already" => StoredBody(r) = presented[r]

\* A sealed root that is not terminated eventually learns its outcome.
SealedRootsResolve ==
    \A r \in Roots : phase[r] = "sealed" ~> phase[r] \in {"done", "terminated"}

\* Witness: a retry recognises its own publication after a later run has
\* advanced the head. Checked as an invariant that must be violated.
WitnessAlreadyAfterAdvance == ~(\E r \in Roots : outcome[r] = "already" /\ head # r)

\* Witness: a summary repeats a citation from a summary copied from an
\* earlier run.
WitnessResummarizedCopy ==
    ~(\E r \in Roots : summ[r].src = "copy" /\ summ[r].intended[1] < pos[r])
\* Witness: an unsuccessful root seals and records an outcome without publishing.
WitnessUnsuccessful == ~(\E r \in Roots : sealed[r] /\ outcome[r] = "skipped" /\ r \notin PubIds)
=============================================================================
