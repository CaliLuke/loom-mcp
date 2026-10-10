--------------------------- MODULE ContinuationCarry ---------------------------
(***************************************************************************)
(* The continuation's carried forms, docs/wips/context_compaction.md       *)
(* (revision 8), section 4.16.                                             *)
(*                                                                         *)
(* A run ends with a tail of units. SealRecord carries it into the next    *)
(* run within a cap: it drops reasoning blocks, evicts the oldest units    *)
(* into the receipt, clears the newest unit's results (protected ones      *)
(* too), then shrinks its other parts to minimal previews. Only what each  *)
(* entry renders as is copied. The next run renders the carried entries,   *)
(* may clear results itself, and seals again for a third run. Every tail   *)
(* shape within the constants is an initial state.                         *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
    Cap,        \* the continuation cap, in size units
    MinPreview, \* size of a minimal preview
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "copy_original", "carry_reasoning", "drop_on_evict",
           "inherit_watermark", "no_forced_clear", "chain_restarts",
           "copy_truncates_full", "recarry_restores_full"}

ASSUME Fault \in Faults

Kinds == {"result", "text", "args", "reasoning"}
Sizes == {1, 3}

\* A part of a unit as the final view rendered it.
Parts(u, i) ==
    {[id |-> <<u, i>>, kind |-> k, size |-> s, keep |-> kp, form |-> f, bytes |-> 0] :
        k \in Kinds, s \in Sizes, kp \in BOOLEAN,
        f \in {"full", "preview", "placeholder"}}

\* Rendering rules of the final view: only results are cleared or
\* previewed; a protected result is never cleared by tier 1; only results
\* are protected.
Valid(p) ==
    /\ p.kind # "result" => (p.form = "full" /\ ~p.keep)
    /\ p.keep => p.form # "placeholder"

VP(u, i) == {q \in Parts(u, i) : Valid(q)}

\* An older unit has one or two parts; the newest has one to three, so its
\* minimal form can exceed the cap.
Units(u) == {<<p>> : p \in VP(u, 1)} \cup {<<p, q>> : p \in VP(u, 1), q \in VP(u, 2)}
\* The third part of a three-part unit is large text or arguments: enough
\* to push the minimal form over the cap.
Third(u) == {[id |-> <<u, 3>>, kind |-> k, size |-> 3, keep |-> FALSE, form |-> "full", bytes |-> 0] : k \in {"text", "args"}}
NewestUnits(u) == Units(u) \cup {<<p, q, r>> : p \in VP(u, 1), q \in VP(u, 2), r \in Third(u)}

\* Run 2's own turn: a small or large assistant text part. A small one lets
\* run 1's entries be carried again into run 3.
Run2Unit(sz) == <<[id |-> <<3, 1>>, kind |-> "text", size |-> sz, keep |-> FALSE, form |-> "full", bytes |-> 0]>>

VARIABLES
    tail1,      \* run 1's final tail: a sequence of units
    carried1,   \* the continuation run 1 published: [units, receipt, copied]
    view2,      \* run 2's view of the carried entries, after its own clearing
    carried2,   \* the continuation run 2 published
    phase

vars == <<tail1, carried1, view2, carried2, phase>>

-----------------------------------------------------------------------------
(* Sizes and the seal steps.                                                *)

Rendered(p) ==
    CASE p.form = "full" -> p.size
      [] p.form = "preview" -> MinPreview
      [] OTHER -> 0

RECURSIVE SumParts(_)
SumParts(u) == IF u = <<>> THEN 0 ELSE Rendered(Head(u)) + SumParts(Tail(u))

RECURSIVE Size(_)
Size(t) == IF t = <<>> THEN 0 ELSE SumParts(Head(t)) + Size(Tail(t))

RECURSIVE MapUnit(_, _)
MapUnit(F(_), u) == IF u = <<>> THEN <<>> ELSE <<F(Head(u))>> \o MapUnit(F, Tail(u))

RECURSIVE FilterUnit(_, _)
FilterUnit(P(_), u) ==
    IF u = <<>> THEN <<>>
    ELSE (IF P(Head(u)) THEN <<Head(u)>> ELSE <<>>) \o FilterUnit(P, Tail(u))

RECURSIVE MapTail(_, _)
MapTail(F(_), t) == IF t = <<>> THEN <<>> ELSE <<F(Head(t))>> \o MapTail(F, Tail(t))

NotReasoning(p) == p.kind # "reasoning"

\* Step 0: carried entries never include reasoning blocks.
DropReasoning(t) ==
    IF Fault = "carry_reasoning" THEN t
    ELSE LET Drop(u) == FilterUnit(NotReasoning, u) IN MapTail(Drop, t)

\* Step 1: evict the oldest units while over the cap, keeping the newest.
RECURSIVE Evict(_)
Evict(t) == IF Size(t) > Cap /\ Len(t) > 1 THEN Evict(Tail(t)) ELSE t

\* Step 2: clear every result of the newest unit, protected ones included.
ClearPart(p) == IF p.kind = "result" THEN [p EXCEPT !.form = "placeholder"] ELSE p
ClearNewest(t) ==
    IF Size(t) <= Cap \/ Fault = "no_forced_clear" THEN t
    ELSE [t EXCEPT ![Len(t)] = MapUnit(ClearPart, t[Len(t)])]

\* Step 3: shrink the newest unit's other parts to minimal previews.
PreviewPart(p) == IF p.kind # "result" /\ p.form = "full" THEN [p EXCEPT !.form = "preview"] ELSE p
PreviewNewest(t) ==
    IF Size(t) <= Cap THEN t ELSE [t EXCEPT ![Len(t)] = MapUnit(PreviewPart, t[Len(t)])]

IdsOf(t) == UNION {{t[i][j].id : j \in 1..Len(t[i])} : i \in 1..Len(t)}

\* Bytes copied into the next run's scope for an entry: what it rendered as.
\* The broken configurations copy the original behind a preview or
\* placeholder, or nothing of an entry that renders whole.
Copy(p) ==
    CASE Fault = "copy_original" -> p.size
      [] Fault = "copy_truncates_full" /\ p.form = "full" -> 0
      [] OTHER -> Rendered(p)

RECURSIVE CopyParts(_)
CopyParts(u) == IF u = <<>> THEN 0 ELSE Copy(Head(u)) + CopyParts(Tail(u))
RECURSIVE CopyTail(_)
CopyTail(t) == IF t = <<>> THEN 0 ELSE CopyParts(Head(t)) + CopyTail(Tail(t))

\* Seal builds the carried form and the receipt. The receipt is a chain: it
\* extends the predecessor's. The broken configuration restarts it.
\* Each carried entry records the bytes the copy actually took for it.
WithBytes(p) == [p EXCEPT !.bytes = Copy(p)]

Seal(t, prior, again) ==
    LET t0 == DropReasoning(t)
        t1 == Evict(t0)
        kept == IF Fault = "drop_on_evict" THEN IdsOf(t1) ELSE IdsOf(t0)
        t2 == PreviewNewest(ClearNewest(t1))
        \* The broken configuration restores an earlier run's carried result to
        \* whole when it is carried again.
        Restore(p) == IF Fault = "recarry_restores_full" /\ p.id[1] # 3 /\ p.kind = "result"
                         THEN [p EXCEPT !.form = "full"] ELSE p
        R2(u) == MapUnit(Restore, u)
        t3 == IF again /\ Fault = "recarry_restores_full" THEN MapTail(R2, t2) ELSE t2
        chain == IF Fault = "chain_restarts" THEN {} ELSE prior
        B(u) == MapUnit(WithBytes, u)
    IN [units |-> MapTail(B, t3), receipt |-> chain \cup (kept \ IdsOf(t1)), copied |-> CopyTail(t3)]

\* The next run renders carried entries as carried: a placeholder is a
\* structural entry and stays one. The broken configuration inherits a
\* cleared watermark instead, which exempts protected results.
RenderNext(p) ==
    IF Fault = "inherit_watermark" /\ p.kind = "result" /\ p.form = "placeholder" /\ p.keep
       THEN [p EXCEPT !.form = "full"]
       ELSE p

\* Tier 1 in the next run may clear unprotected results it shows.
Tier1(p) == IF p.kind = "result" /\ ~p.keep THEN [p EXCEPT !.form = "placeholder"] ELSE p

-----------------------------------------------------------------------------

Init ==
    /\ \/ \E b \in NewestUnits(2) : tail1 = <<b>>
       \/ \E a \in Units(1), b \in NewestUnits(2) : tail1 = <<a, b>>
    /\ carried1 = [units |-> <<>>, receipt |-> {}, copied |-> 0]
    /\ view2 = <<>>
    /\ carried2 = [units |-> <<>>, receipt |-> {}, copied |-> 0]
    /\ phase = "run1"

SealRun1 ==
    /\ phase = "run1"
    /\ carried1' = Seal(tail1, {}, FALSE)
    /\ phase' = "run2"
    /\ UNCHANGED <<tail1, view2, carried2>>

\* Run 2 renders the carried entries, may clear results itself, and adds a
\* turn of its own after them.
StartRun2 ==
    /\ phase = "run2"
    /\ \E clear \in BOOLEAN, sz \in Sizes :
          LET R(u) == MapUnit(RenderNext, u)
              C(u) == MapUnit(Tier1, u)
              v == MapTail(R, carried1.units)
          IN view2' = (IF clear THEN MapTail(C, v) ELSE v) \o <<Run2Unit(sz)>>
    /\ phase' = "run2sealing"
    /\ UNCHANGED <<tail1, carried1, carried2>>

SealRun2 ==
    /\ phase = "run2sealing"
    /\ carried2' = Seal(view2, carried1.receipt, TRUE)
    /\ phase' = "done"
    /\ UNCHANGED <<tail1, carried1, view2>>

Next == SealRun1 \/ StartRun2 \/ SealRun2 \/ (phase = "done" /\ UNCHANGED vars)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Invariants.                                                              *)

Minimal(t) ==
    /\ Len(t) <= 1
    /\ \A i \in 1..Len(t) : \A j \in 1..Len(t[i]) :
           LET p == t[i][j] IN IF p.kind = "result" THEN p.form = "placeholder" ELSE p.form # "full"

Run1Sealed == phase \in {"run2", "run2sealing", "done"}

\* The carried tail fits the cap, or is the newest unit in its minimal form.
CarryBounded ==
    /\ Run1Sealed => (Size(carried1.units) <= Cap \/ Minimal(carried1.units))
    /\ phase = "done" => (Size(carried2.units) <= Cap \/ Minimal(carried2.units))

\* The copy holds only what the entries render as, never the original behind
\* a preview or placeholder.
CopyIsRendered ==
    /\ carried1.copied <= Size(carried1.units)
    /\ carried2.copied <= Size(carried2.units)

\* No reasoning block crosses into another run.
NoReasoningCarried ==
    \A c \in {carried1, carried2} :
        \A i \in 1..Len(c.units) : \A j \in 1..Len(c.units[i]) : c.units[i][j].kind # "reasoning"

\* Every model-visible part of run 1's tail, and of run 2's view, is carried
\* or listed in the receipt chain; its evidence stays in its run's record.
EveryPartAccounted ==
    /\ Run1Sealed =>
          IdsOf(DropReasoning(tail1)) \subseteq IdsOf(carried1.units) \cup carried1.receipt
    /\ phase = "done" =>
          IdsOf(DropReasoning(tail1)) \cup IdsOf(DropReasoning(view2))
              \subseteq IdsOf(carried2.units) \cup carried2.receipt

\* What the next run renders can be rendered from what was copied: an entry
\* renders whole only if its whole content was copied.
RenderableFromCopy ==
    phase \in {"run2sealing", "done"} =>
        \A i \in 1..Len(carried1.units) : \A j \in 1..Len(carried1.units[i]) :
            Rendered(view2[i][j]) <= carried1.units[i][j].bytes

\* Witness: an entry of run 1 is carried into run 3. Checked as an invariant
\* that must be violated.
WitnessCarriedTwice ==
    ~(phase = "done" /\ \E i \in 1..Len(carried2.units) : \E j \in 1..Len(carried2.units[i]) :
          carried2.units[i][j].id[1] # 3)

\* Witness: a tail where the cap cannot be met and the minimal form is the
\* bound. Checked as an invariant that must be violated.
WitnessMinimalNeeded ==
    ~(Run1Sealed /\ Size(carried1.units) > Cap /\ Minimal(carried1.units))

\* A placeholder stays a placeholder in every later run, whatever its
\* protection.
PlaceholdersStick ==
    phase \in {"run2sealing", "done"} =>
        \A i \in 1..Len(carried1.units) : \A j \in 1..Len(carried1.units[i]) :
            carried1.units[i][j].form = "placeholder" => view2[i][j].form = "placeholder"
=============================================================================
