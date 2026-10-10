---------------------------- MODULE RecallDiscovery ----------------------------
(***************************************************************************)
(* Discovery and recall of evicted evidence, docs/wips/context_compaction.md *)
(* (revision 8), sections 4.6, 4.9 and 4.18.                               *)
(*                                                                         *)
(* A continued run (run 2) evicts its history one message at a time       *)
(* behind a checkpoint whose chain reaches run 1's record through From.    *)
(* The checkpoint renders its newest lines and collapses the rest into one *)
(* handle per part: the current run's older lines and the earlier runs'.   *)
(* An agent lists handles and reads messages under separate discovery and  *)
(* read allowances, which each compaction refills. A summary may cite a    *)
(* user message, possibly with forged text. The store also holds a record  *)
(* of another session and server-only data. Every message-kind sequence    *)
(* and run split within the constants is an initial state.                 *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    N,          \* messages that can be evicted, oldest first
    Shown,      \* receipt lines the checkpoint renders before collapsing
    Allow,      \* initial discovery and read allowances
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "assistant_text_omitted", "collapse_without_handle",
           "resolve_any_record", "shared_allowance", "cite_text_from_summary",
           "recall_reads_record", "pair_by_previous", "refill_rounds_down",
           "resolve_current_only"}

ASSUME Fault \in Faults

Ids == 1..N
Kinds == {"user", "text", "call", "result"}
\* Checkpoint records in the store, each with its From link: run 2's record
\* (the current checkpoint) links to run 1's; record 9 belongs to another
\* session. Lines under record 9 name message 99.
Current == 2
Foreign == 9
Tokens == {1, 2, Foreign}
From == [r \in Tokens |-> CASE r = 2 -> 1 [] OTHER -> 0]
ForeignMsg == 99

RECURSIVE ChainFrom(_)
ChainFrom(r) == IF r = 0 THEN {} ELSE {r} \cup ChainFrom(From[r])
MaxAllow == 3 * Allow

VARIABLES
    kind,       \* message -> kind
    pair,       \* message -> the call a result answers, 0 for other kinds
    split,      \* messages 1..split were written in run 1
    cite,       \* the summary's citation: [id, text], text "record" or "forged"
    evicted,    \* messages 1..evicted are behind the checkpoint
    known,      \* messages the agent has listed
    disc,       \* discovery allowance left
    rd,         \* read allowance left
    resolved,   \* checkpoint records that handles resolved to
    returned,   \* content recall returned: [id, part], part "eligible" or "server"
    last,       \* the last action, for the allowance property
    exhausted,  \* the read allowance has hit zero at least once
    readAfter   \* a read succeeded after that

vars == <<kind, pair, split, cite, evicted, known, disc, rd, resolved, returned,
          last, exhausted, readAfter>>

\* Each result answers one earlier call, and each call has at most one
\* result; parallel calls (call, call, result, result) are allowed.
WellPaired(k, p) ==
    /\ \A i \in Ids : k[i] = "result" <=> p[i] # 0
    /\ \A i \in Ids : p[i] # 0 => (p[i] < i /\ k[p[i]] = "call")
    /\ \A i, j \in Ids : i # j /\ p[i] # 0 => p[i] # p[j]

Init ==
    /\ kind \in [Ids -> Kinds]
    /\ pair \in [Ids -> 0..N]
    /\ WellPaired(kind, pair)
    /\ split \in 0..N
    /\ cite \in [id : {i \in Ids : kind[i] = "user"} \cup {0}, text : {"record", "forged"}]
    /\ evicted = 0
    /\ known = {}
    /\ disc = Allow
    /\ rd = Allow
    /\ resolved = {}
    /\ returned = {}
    /\ last = "init"
    /\ exhausted = FALSE
    /\ readAfter = FALSE

-----------------------------------------------------------------------------
(* What the checkpoint renders.                                             *)

Behind == 1..evicted

\* Messages that get a line: user messages (user index), assistant text
\* (first line) and tool calls (receipt). A result is reached from its call.
HasLine(i) ==
    \/ kind[i] \in {"user", "call"}
    \/ kind[i] = "text" /\ Fault # "assistant_text_omitted"

Lines == {i \in Behind : HasLine(i)}
RunLines(r) == {i \in Lines : IF r = 1 THEN i <= split ELSE i > split}

\* The newest Shown lines of the current run render; the rest collapse.
RECURSIVE Newest(_, _)
Newest(S, n) ==
    IF n = 0 \/ S = {} THEN {}
    ELSE LET m == CHOOSE x \in S : \A y \in S : y <= x
         IN {m} \cup Newest(S \ {m}, n - 1)

ShownLines == Newest(RunLines(2), Shown)

\* Handles on collapsed lines, and the chain record each one names.
Handles ==
    (IF RunLines(2) \ ShownLines # {} THEN {2} ELSE {})
    \cup (IF RunLines(1) # {} /\ Fault # "collapse_without_handle" THEN {1} ELSE {})

HandleLines(h) == IF h = 2 THEN RunLines(2) \ ShownLines ELSE RunLines(1)

\* The call a reader takes a result to answer. The broken configuration
\* takes the message just before it, which mis-pairs parallel calls.
CallOf(i) == IF Fault = "pair_by_previous" THEN i - 1 ELSE pair[i]

\* Lines a token lists once resolved: a chain record's lines, or the
\* foreign session's message.
TokenLines(t) == IF t = Foreign THEN {ForeignMsg} ELSE HandleLines(t)

\* list(token) resolves a token only by walking the current checkpoint's From
\* chain. The broken configurations resolve any record in the store, or
\* never walk past the current record.
Resolves(t) ==
    CASE Fault = "resolve_any_record" -> t \in Tokens
      [] Fault = "resolve_current_only" -> t = Current
      [] OTHER -> t \in ChainFrom(Current)

\* Everything reachable with unlimited allowances, through the handler: the
\* lines the checkpoint shows, and the lines each rendered handle lists once
\* the handler resolves it.
LineReach == ShownLines \cup UNION {TokenLines(h) : h \in {x \in Handles : Resolves(x)}}
Reach == LineReach \cup {i \in Behind : kind[i] = "result" /\ CallOf(i) \in LineReach}

-----------------------------------------------------------------------------
(* Actions.                                                                 *)

\* A compaction adds half the initial allowance, rounded up, so even an
\* allowance of one is refilled. The broken configuration rounds down.
Gain == IF Fault = "refill_rounds_down" THEN Allow \div 2 ELSE (Allow + 1) \div 2
Refill(a) == IF a + Gain > MaxAllow THEN MaxAllow ELSE a + Gain

\* A compaction evicts the next message and refills both allowances.
Compact ==
    /\ evicted < N
    /\ evicted' = evicted + 1
    /\ disc' = Refill(disc)
    /\ rd' = Refill(rd)
    /\ last' = "compact"
    /\ UNCHANGED <<kind, pair, split, cite, known, resolved, returned, exhausted, readAfter>>

\* list(token): the agent may present any token, including one it was never
\* shown (a forged or injected one); the handler rejects what it cannot
\* resolve. The broken configuration charges listings to the read allowance
\* as well.
List(t) ==
    /\ disc > 0
    /\ disc' = disc - 1
    /\ rd' = IF Fault = "shared_allowance" /\ rd > 0 THEN rd - 1 ELSE rd
    /\ IF Resolves(t)
          THEN /\ resolved' = resolved \cup {t}
               /\ known' = known \cup TokenLines(t)
          ELSE UNCHANGED <<resolved, known>>
    /\ last' = "list"
    /\ exhausted' = (exhausted \/ rd' = 0)
    /\ UNCHANGED <<kind, pair, split, cite, evicted, returned, readAfter>>

\* read(id): returns the message's eligible content. The broken
\* configuration also resolves its server-only part.
Read(i) ==
    /\ i \in Behind
    /\ i \in ShownLines \cup known \/ (kind[i] = "result" /\ CallOf(i) \in ShownLines \cup known)
    /\ rd > 0
    /\ returned' = returned \cup {[id |-> i, part |-> "eligible"]}
                   \cup (IF Fault = "recall_reads_record" /\ kind[i] = "result"
                            THEN {[id |-> i, part |-> "server"]} ELSE {})
    /\ rd' = rd - 1
    /\ last' = "read"
    /\ exhausted' = (exhausted \/ rd' = 0)
    /\ readAfter' = (readAfter \/ exhausted)
    /\ UNCHANGED <<kind, pair, split, cite, evicted, known, disc, resolved>>

\* Every message is behind the checkpoint: the run has nothing left to do.
Done == evicted = N /\ UNCHANGED vars

Next ==
    \/ Compact
    \/ \E t \in Tokens : List(t)
    \/ \E i \in Ids : Read(i)
    \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(Compact)

-----------------------------------------------------------------------------
(* Invariants and properties.                                               *)

\* P5: every model-visible message behind the checkpoint is reachable from
\* what it renders, given enough allowance.
Discoverable == Behind \subseteq Reach

\* A token resolves only to a record of the current checkpoint chain, and the
\* agent never learns a message of another session.
HandlesConfined == resolved \subseteq ChainFrom(Current) /\ known \subseteq Ids

\* Recall returns eligible evidence only.
RecallNeverExposesServerData == \A x \in returned : x.part = "eligible"

\* User text in the checkpoint comes from the record, never from the
\* summary, so a citation cannot put words in the user's mouth. The broken
\* configuration renders the summary's text for a cited message.
RenderedUserText == IF Fault = "cite_text_from_summary" THEN cite.text ELSE "record"
UserTextFromRecord == cite.id # 0 => RenderedUserText = "record"

AllowancesBounded == disc <= MaxAllow /\ rd <= MaxAllow

\* Listing never spends the read allowance.
ListingKeepsReads == [][last' = "list" => rd' = rd]_vars

\* A spent read allowance is refilled by the next compaction.
RefillRestoresReads == (rd = 0 /\ evicted < N) ~> rd > 0

\* Witness: the agent reads again after exhausting its read allowance.
\* Checked as an invariant that must be violated.
WitnessReadAfterRefill == ~readAfter
=============================================================================
