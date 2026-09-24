---------------------------- MODULE CallAdmission ----------------------------
(***************************************************************************)
(* One routed tool call through the registry's Redis call-admission        *)
(* protocol. Every Lua script in registry/call_admission.go,               *)
(* registry/call_decision.go and registry/bounded_publish.go is one atomic *)
(* action. Pulse consumer-group delivery is at-least-once: any             *)
(* unacknowledged request event may be (re)delivered to any live provider  *)
(* incarnation, including after an overload retry or retention expiry.    *)
(*                                                                         *)
(* Check with:                                                             *)
(*   java -cp tla2tools.jar tlc2.TLC -deadlock -config CallAdmission.cfg \  *)
(*     CallAdmission.tla                                                   *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Leases,          \* provider incarnations (provider_id + incarnation)
          MaxIds,          \* bound on stream ids handed out
          MaxDeliveries,   \* bound on Pulse (re)deliveries
          MaxGenerations   \* bound on admissions created for the same tool_use_id

None == "none"

VARIABLES
    h,          \* the call hash (registry:<name>:call:<sha(tool_use_id)>)
    nextId,     \* Redis stream id allocator (monotonic across both streams)
    unacked,    \* toolset request events not yet acknowledged by the group
    reqGen,     \* request event id -> admission generation that published it
    inflight,   \* set of [l, e, ph] provider-local work items
    lease,      \* Leases -> {"valid","expired","released"}
    alive,      \* Leases -> BOOLEAN (provider process running)
    idx,        \* settlement indexes: zset member, membership hash, lease index
    execs,      \* generation -> number of handler invocations
    gen,        \* current admission generation (0 = never admitted)
    gw,         \* overload event id snapshotted by a pending RetryTool (0 = none)
    fatal,      \* errors returned to a provider while its lease was valid
    deliveries

vars == <<h, nextId, unacked, reqGen, inflight, lease, alive, idx, execs,
          gen, gw, fatal, deliveries>>

NoHash == [exists |-> FALSE]

Fresh(g) == [exists |-> TRUE, g |-> g, published |-> FALSE, pub |-> 0,
             claims |-> {}, ovField |-> 0, ovEv |-> 0, ovReq |-> {},
             terminal |-> FALSE, cause |-> None,
             dLease |-> None, dReq |-> 0, execExpired |-> FALSE]

NoIdx == [settle |-> FALSE, member |-> FALSE, leaseIdx |-> FALSE]
AllIdx == [settle |-> TRUE, member |-> TRUE, leaseIdx |-> TRUE]

Init ==
    /\ h = NoHash
    /\ nextId = 1
    /\ unacked = {}
    /\ reqGen = [e \in {} |-> 0]
    /\ inflight = {}
    /\ lease = [l \in Leases |-> "valid"]
    /\ alive = [l \in Leases |-> TRUE]
    /\ idx = NoIdx
    /\ execs = [g \in 1..MaxGenerations |-> 0]
    /\ gen = 0
    /\ gw = 0
    /\ fatal = {}
    /\ deliveries = 0

Routable == \E l \in Leases : lease[l] = "valid"

\* A provider-visible error. It is a defect only when the provider still
\* holds a valid lease: every such error stops Serve and leaves the event
\* unacknowledged for redelivery.
Fail(l, err) ==
    /\ fatal' = IF lease[l] = "valid" THEN fatal \cup {err} ELSE fatal
    /\ alive' = [alive EXCEPT ![l] = FALSE]
    /\ inflight' = {w \in inflight : w.l # l}

-----------------------------------------------------------------------------
(* Gateway: ensureCallAdmissionScript, then boundedPublishScript.          *)

Ensure ==
    /\ ~h.exists
    /\ gen < MaxGenerations
    /\ Routable
    /\ h' = Fresh(gen + 1)
    /\ gen' = gen + 1
    /\ gw' = 0
    /\ UNCHANGED <<nextId, unacked, reqGen, inflight, lease, alive, idx, execs,
                   fatal, deliveries>>

\* boundedPublishScript with admission digest and overload id `ov`.
Publish(ov) ==
    /\ h.exists
    /\ ~h.terminal
    /\ ~((ov = 0 /\ h.published) \/ (ov # 0 /\ h.ovField = ov))
    /\ h.dLease = None
    /\ ~h.execExpired
    /\ Routable
    /\ nextId <= MaxIds
    /\ h' = [h EXCEPT !.published = TRUE, !.ovField = ov, !.pub = nextId,
                      !.claims = @ \cup {nextId}]
    /\ unacked' = unacked \cup {nextId}
    /\ reqGen' = [e \in DOMAIN reqGen \cup {nextId} |->
                    IF e = nextId THEN h.g ELSE reqGen[e]]
    /\ nextId' = nextId + 1

PublishInitial ==
    /\ Publish(0)
    /\ UNCHANGED <<inflight, lease, alive, idx, execs, gen, gw, fatal, deliveries>>

\* RetryTool: Attach snapshots overload_event_id while nonterminal.
RetryAttach ==
    /\ h.exists /\ ~h.terminal /\ h.ovEv # 0 /\ gw = 0
    /\ gw' = h.ovEv
    /\ UNCHANGED <<h, nextId, unacked, reqGen, inflight, lease, alive, idx,
                   execs, gen, fatal, deliveries>>

RetryPublish ==
    /\ gw # 0
    /\ \/ /\ Publish(gw)
       \/ /\ ~ENABLED Publish(gw)
          /\ UNCHANGED <<h, nextId, unacked, reqGen>>
    /\ gw' = 0
    /\ UNCHANGED <<inflight, lease, alive, idx, execs, gen, fatal, deliveries>>

-----------------------------------------------------------------------------
(* Provider: Pulse delivery, claim, overload, complete, ack.               *)

Deliver(l, e) ==
    /\ alive[l] /\ lease[l] # "released"
    /\ e \in unacked
    /\ deliveries < MaxDeliveries
    /\ ~\E w \in inflight : w.e = e
    /\ inflight' = inflight \cup {[l |-> l, e |-> e, ph |-> "recv"]}
    /\ deliveries' = deliveries + 1
    /\ UNCHANGED <<h, nextId, unacked, reqGen, lease, alive, idx, execs, gen,
                   gw, fatal>>

SetPhase(w, ph) == inflight' = (inflight \ {w}) \cup {[w EXCEPT !.ph = ph]}

\* claimCallAdmissionScript
Claim(w) ==
    LET l == w.l  e == w.e IN
    /\ w \in inflight /\ w.ph = "recv"
    /\ CASE lease[l] # "valid" ->
                Fail(l, "PROVIDERLEASECHANGED") /\ UNCHANGED <<h, idx, execs>>
         [] ~h.exists ->
                SetPhase(w, "ack") /\ UNCHANGED <<h, idx, execs, alive, fatal>>
         [] e \notin h.claims ->
                \* Published by an expired admission of this tool_use_id.
                SetPhase(w, "ack") /\ UNCHANGED <<h, idx, execs, alive, fatal>>
         [] h.terminal \/ h.execExpired \/ h.dLease # None \/ e # h.pub ->
                SetPhase(w, "ack") /\ UNCHANGED <<h, idx, execs, alive, fatal>>
         [] OTHER ->
                /\ h' = [h EXCEPT !.dLease = l, !.dReq = e]
                /\ idx' = AllIdx
                /\ execs' = [execs EXCEPT ![h.g] = @ + 1]
                /\ SetPhase(w, "exec")
                /\ UNCHANGED <<alive, fatal>>
    /\ UNCHANGED <<nextId, unacked, reqGen, lease, gen, gw, deliveries>>

\* reportOverloadCallScript (worker queue full, before any claim)
Overload(w) ==
    LET l == w.l  e == w.e IN
    /\ w \in inflight /\ w.ph = "recv"
    /\ CASE lease[l] # "valid" ->
                Fail(l, "PROVIDERLEASECHANGED") /\ UNCHANGED <<h, nextId>>
         [] ~h.exists ->
                SetPhase(w, "ack") /\ UNCHANGED <<h, nextId, alive, fatal>>
         [] e \notin h.claims \/ e \in h.ovReq \/ h.execExpired \/ h.terminal \/ h.dLease # None ->
                SetPhase(w, "ack") /\ UNCHANGED <<h, nextId, alive, fatal>>
         [] OTHER ->
                /\ nextId <= MaxIds
                /\ h' = [h EXCEPT !.ovEv = nextId, !.ovReq = @ \cup {e}]
                /\ nextId' = nextId + 1
                /\ SetPhase(w, "ack")
                /\ UNCHANGED <<alive, fatal>>
    /\ UNCHANGED <<unacked, reqGen, lease, idx, execs, gen, gw, deliveries>>

\* completeCallAdmissionScript
Complete(w) ==
    LET l == w.l  e == w.e IN
    /\ w \in inflight /\ w.ph = "exec"
    /\ CASE ~h.exists \/ h.g # reqGen[e] ->
                Fail(l, "CALLADMISSIONCHANGED") /\ UNCHANGED <<h, idx>>
         [] lease[l] # "valid" ->
                Fail(l, "PROVIDERLEASECHANGED") /\ UNCHANGED <<h, idx>>
         [] e \notin h.claims ->
                Fail(l, "CALLCLAIMCHANGED") /\ UNCHANGED <<h, idx>>
         [] h.dLease # l \/ h.dReq # e ->
                Fail(l, "DISPATCHCLAIMCHANGED") /\ UNCHANGED <<h, idx>>
         [] h.terminal /\ h.cause = "execution_deadline" ->
                SetPhase(w, "ack") /\ UNCHANGED <<h, idx, alive, fatal>>
         [] h.terminal ->
                \* The provider's digest differs from the settled outcome_unknown.
                Fail(l, "TERMINALCONFLICT") /\ UNCHANGED <<h, idx>>
         [] OTHER ->
                /\ h' = [h EXCEPT !.terminal = TRUE,
                                  !.cause = IF h.execExpired THEN "execution_deadline"
                                                             ELSE "provider"]
                /\ idx' = NoIdx
                /\ SetPhase(w, "ack")
                /\ UNCHANGED <<alive, fatal>>
    /\ UNCHANGED <<nextId, unacked, reqGen, lease, execs, gen, gw, deliveries>>

Ack(w) ==
    /\ w \in inflight /\ w.ph = "ack"
    /\ unacked' = unacked \ {w.e}
    /\ inflight' = inflight \ {w}
    /\ UNCHANGED <<h, nextId, reqGen, lease, alive, idx, execs, gen, gw, fatal,
                   deliveries>>

Crash(l) ==
    /\ alive[l]
    /\ alive' = [alive EXCEPT ![l] = FALSE]
    /\ inflight' = {w \in inflight : w.l # l}
    /\ UNCHANGED <<h, nextId, unacked, reqGen, lease, idx, execs, gen, gw,
                   fatal, deliveries>>

-----------------------------------------------------------------------------
(* Catalog leases and time.                                                *)

\* superviseRegistration stops Serve at deadline - shutdownMargin, before the
\* registry lease expires, so a live provider never continues on (or revives)
\* a lease the registry already expired.
LeaseExpire(l) ==
    /\ lease[l] = "valid"
    /\ lease' = [lease EXCEPT ![l] = "expired"]
    /\ alive' = [alive EXCEPT ![l] = FALSE]
    /\ inflight' = {w \in inflight : w.l # l}
    /\ UNCHANGED <<h, nextId, unacked, reqGen, idx, execs,
                   gen, gw, fatal, deliveries>>

ExecDeadline ==
    /\ h.exists /\ ~h.execExpired
    /\ h' = [h EXCEPT !.execExpired = TRUE]
    /\ UNCHANGED <<nextId, unacked, reqGen, inflight, lease, alive, idx, execs,
                   gen, gw, fatal, deliveries>>

\* PEXPIREAT on the call hash (expires_at > execution deadline). Settlement
\* indexes carry no TTL.
Retention ==
    /\ h.exists /\ h.execExpired
    \* Handlers are cancelled at the execution deadline and the retention TTL
    \* covers MaxToolCallWait plus the result transport budget.
    /\ ~\E w \in inflight : w.ph = "exec" /\ reqGen[w.e] = h.g
    /\ h' = NoHash
    /\ gw' = 0
    /\ UNCHANGED <<nextId, unacked, reqGen, inflight, lease, alive, idx, execs,
                   gen, fatal, deliveries>>

\* settleLostClaimScript, run by the settlement ticker. The Go caller first
\* reads the membership hash and returns an error when it is missing.
Settle ==
    /\ idx.settle
    /\ idx.member
    /\ CASE ~h.exists \/ h.terminal \/ h.dLease = None ->
                idx' = NoIdx /\ UNCHANGED h
         [] ~h.execExpired /\ lease[h.dLease] = "valid" ->
                UNCHANGED <<h, idx>>
         [] OTHER ->
                /\ h' = [h EXCEPT !.terminal = TRUE,
                                  !.cause = IF h.execExpired THEN "execution_deadline"
                                                             ELSE "provider_lease_lost"]
                /\ idx' = NoIdx
    /\ UNCHANGED <<nextId, unacked, reqGen, inflight, lease, alive, execs, gen,
                   gw, fatal, deliveries>>

-----------------------------------------------------------------------------

Next ==
    \/ Ensure \/ PublishInitial \/ RetryAttach \/ RetryPublish
    \/ \E l \in Leases, e \in unacked : Deliver(l, e)
    \/ \E w \in inflight : Claim(w) \/ Overload(w) \/ Complete(w) \/ Ack(w)
    \/ \E l \in Leases : Crash(l) \/ LeaseExpire(l)
    \/ ExecDeadline \/ Retention \/ Settle

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Invariants.                                                             *)

\* The attach script's admitted_state_error must accept every live record;
\* otherwise CallTool, RetryTool and replay fail with CALLDECISIONINVALID.
AttachAccepts ==
    h.exists =>
        /\ h.terminal => h.published
        /\ h.dLease # None => h.dReq = h.pub

\* A handler runs at most once per admission.
AtMostOnceExecution == \A g \in 1..MaxGenerations : execs[g] <= 1

\* Benign races never return a fatal error to a provider with a valid lease.
NoFatalWithValidLease == fatal = {}

\* The settlement ticker aborts its whole batch when membership is missing.
SettlerCanProceed == idx.settle => idx.member

\* Every live dispatch stays indexed for settlement.
DispatchIndexed ==
    (h.exists /\ h.dLease # None /\ ~h.terminal) => idx.settle
=============================================================================
