----------------------------- MODULE StepPayload -----------------------------
(***************************************************************************)
(* The bound on planner activity inputs (P2) and the index segments,       *)
(* docs/wips/context_compaction.md (revision 9), sections 4.1, 4.5, 4.6    *)
(* and 4.7.                                                                *)
(*                                                                         *)
(* The run start activity stores the caller's history as the first index  *)
(* segment. Each planner activity's loader stores the entries pending      *)
(* since the last segment as a new segment linked to the previous one,     *)
(* and the workflow adopts the returned head. A planner turn adds an       *)
(* assistant entry and its tool results; an await round adds a typed      *)
(* input. Activity inputs carry the ViewRef, the pending entries and one   *)
(* rate per model the run has called; histories stay in the index. An     *)
(* attempt can crash after storing its segment; the retry stores the same  *)
(* segment, which content addressing deduplicates.                         *)
(*                                                                         *)
(* Reconstruction walks the stored segments back from the head; it is     *)
(* compared with the entries the workflow has committed.                  *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    History,    \* entries in the caller's history at run start
    MaxCalls,   \* tool calls in one planner turn
    Models,     \* models the planner may call
    MaxSteps,   \* planner steps checked
    Fixed,      \* the fixed part of an activity input, in entry units
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "seed_in_pending", "pending_not_flushed",
           "accumulated_history", "head_before_store", "segment_prev_wrong"}

ASSUME Fault \in Faults

\* One step's entries: an assistant message, its results, one typed input.
MaxStepEntries == 1 + MaxCalls + 1

NoSeg == [from |-> 0, to |-> 0, prev |-> 0]

VARIABLES
    n,          \* entries in the workflow's index
    segs,       \* the store: segments [from, to, prev], prev = the previous segment's to
    head,       \* the adopted head: the last entry its segment covers
    committed,  \* ground truth: entries the workflow treats as durable
    typed,      \* typed inputs received
    outputs,    \* tool outputs received
    models,     \* models the run has called
    awaited,    \* a typed input arrived since the last planner step
    steps,
    input,      \* size of the latest planner activity input, in entry units
    phase,      \* "start", "idle" or "stored" (a segment stored, output not adopted)
    pendingSeg, \* the segment the current attempt stored
    crashes     \* attempts that crashed after storing

vars == <<n, segs, head, committed, typed, outputs, models, awaited, steps, input,
          phase, pendingSeg, crashes>>

Init ==
    /\ n = 0 /\ segs = {} /\ head = 0 /\ committed = 0 /\ typed = 0 /\ outputs = 0
    /\ models = {} /\ awaited = FALSE /\ steps = 0 /\ input = 0
    /\ phase = "start" /\ pendingSeg = NoSeg /\ crashes = 0

\* The run start activity stores the seeded history as the first segment.
\* The broken configuration leaves it pending for the first planner step.
RunStart ==
    /\ phase = "start"
    /\ n' = History
    /\ IF Fault = "seed_in_pending"
          THEN UNCHANGED <<segs, head, committed>>
          ELSE /\ segs' = {[from |-> 1, to |-> History, prev |-> 0]}
               /\ head' = History
               /\ committed' = History
    /\ phase' = "idle"
    /\ UNCHANGED <<typed, outputs, models, awaited, steps, input, pendingSeg, crashes>>

\* The segment for entries head+1..n. The broken configuration links it to
\* the segment before the previous one.
NewSeg ==
    [from |-> head + 1, to |-> n,
     prev |-> IF Fault = "segment_prev_wrong" /\ head > 0
                 THEN (CHOOSE s \in segs \cup {NoSeg} : s.to = head).prev
                 ELSE head]

\* The planner activity starts: its input carries the pending entries, one
\* rate per model called, and nothing accumulated; its loader stores the
\* pending entries as a segment. The broken configurations carry the
\* typed-input and tool-output histories, or skip storing the segment.
LoadAndStore ==
    /\ phase = "idle"
    /\ steps < MaxSteps
    /\ input' = Fixed + (n - head) + Cardinality(models)
                + (IF Fault = "accumulated_history" THEN typed + outputs ELSE 0)
    /\ IF Fault = "pending_not_flushed" \/ n = head
          THEN /\ pendingSeg' = NoSeg
               /\ UNCHANGED segs
          ELSE /\ pendingSeg' = NewSeg
               /\ segs' = segs \cup {NewSeg}
    /\ phase' = "stored"
    /\ UNCHANGED <<n, head, committed, typed, outputs, models, awaited, steps, crashes>>

\* The attempt crashes after storing; the retry stores the same segment,
\* because a segment is built from workflow state.
Crash ==
    /\ phase = "stored"
    /\ crashes < 1
    /\ crashes' = crashes + 1
    /\ phase' = "idle"
    /\ UNCHANGED <<n, segs, head, committed, typed, outputs, models, awaited, steps,
                   input, pendingSeg>>

\* The workflow adopts the output: the returned head, then the turn's
\* entries. The broken configuration treats the turn's new entries as
\* durable at once.
Adopt ==
    /\ phase = "stored"
    /\ \E c \in 1..MaxCalls, m \in Models :
          /\ head' = IF pendingSeg = NoSeg THEN head ELSE pendingSeg.to
          /\ committed' = IF Fault = "head_before_store" THEN n + 1 + c
                          ELSE IF pendingSeg = NoSeg THEN head ELSE pendingSeg.to
          /\ n' = n + 1 + c
          /\ outputs' = outputs + c
          /\ models' = models \cup {m}
    /\ steps' = steps + 1
    /\ awaited' = FALSE
    /\ phase' = "idle"
    /\ UNCHANGED <<segs, typed, input, pendingSeg, crashes>>

\* An await round: a typed input arrives through Provide* and is indexed.
TypedInput ==
    /\ phase = "idle"
    /\ ~awaited
    /\ n' = n + 1
    /\ typed' = typed + 1
    /\ awaited' = TRUE
    /\ UNCHANGED <<segs, head, committed, outputs, models, steps, input, phase, pendingSeg, crashes>>

Done == phase = "idle" /\ steps = MaxSteps /\ UNCHANGED vars

Next == RunStart \/ LoadAndStore \/ Crash \/ Adopt \/ TypedInput \/ Done

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Reconstruction: walk the stored segments back from the head.            *)

RECURSIVE Walk(_, _)
Walk(to, fuel) ==
    IF to = 0 \/ fuel = 0 THEN {}
    ELSE IF \E s \in segs : s.to = to
       THEN LET s == CHOOSE x \in segs : x.to = to
            IN (s.from..s.to) \cup Walk(s.prev, fuel - 1)
       ELSE {}

Reconstructed == Walk(head, MaxSteps + 2)

-----------------------------------------------------------------------------
(* Invariants.                                                              *)

\* P2: an activity input depends on neither run length nor the history at
\* run start.
InputBounded == input <= Fixed + MaxStepEntries + Cardinality(Models)

\* Reconstruction from the head finds exactly the entries the workflow has
\* committed, with no gap.
Reconstructs == Reconstructed = 1..committed

\* Witness: a run whose attempt crashed after storing completes all its
\* steps. Checked as an invariant that must be violated.
WitnessCrashRetry == ~(crashes > 0 /\ steps = MaxSteps /\ Reconstructs)
=============================================================================
