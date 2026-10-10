---------------------------- MODULE RequestRecord ----------------------------
(***************************************************************************)
(* The call record's message sequence, docs/wips/context_compaction.md     *)
(* (revision 8), section 4.13.                                             *)
(*                                                                         *)
(* The rendered view at call time is a sequence of messages. A planner may *)
(* send any sequence of messages: view messages in any order, subset or    *)
(* repetition, and distinct messages of its own. The wrapper encodes the   *)
(* request as segments, each a span of consecutive view positions or one   *)
(* message hash, against the ViewRef it records. Later the view moves on   *)
(* (a compaction); decoding must use the recorded ViewRef and rebuild the  *)
(* request exactly. Every request up to MaxLen messages is an initial      *)
(* state, so TLC checks all of them.                                       *)
(***************************************************************************)
EXTENDS Naturals, Sequences

CONSTANTS
    MaxLen,     \* longest request checked
    Fault       \* "none", or one rule deliberately broken

Faults == {"none", "viewref_plus_added", "span_ignores_order",
           "hash_first_own", "decode_current_view"}

ASSUME Fault \in Faults

\* The view when the call was made, and the view after a later compaction
\* evicted its first message.
CallView == <<"m1", "m2", "m3">>
LaterView == <<"m2", "m3", "m4">>

ViewMsgs == {CallView[i] : i \in 1..Len(CallView)}
OwnMsgs == {"o1", "o2"}
Msgs == ViewMsgs \cup OwnMsgs

Requests == UNION {[1..n -> Msgs] : n \in 0..MaxLen}

VARIABLE req

PosIn(m, V) == CHOOSE i \in 1..Len(V) : V[i] = m

\* Encode against the recorded view: extend the current span when the next
\* message is the next view position, else start a new segment.
RECURSIVE EncodeFrom(_, _)
EncodeFrom(r, segs) ==
    IF r = <<>> THEN segs
    ELSE LET m == Head(r)
             last == IF segs = <<>> THEN [kind |-> "none"] ELSE segs[Len(segs)]
         IN IF m \in OwnMsgs
               THEN EncodeFrom(Tail(r), Append(segs, [kind |-> "hash", msg |-> m]))
            ELSE LET p == PosIn(m, CallView)
                 IN IF last.kind = "span" /\ last.to + 1 = p
                       THEN EncodeFrom(Tail(r), [segs EXCEPT ![Len(segs)] = [kind |-> "span", from |-> last.from, to |-> p]])
                       ELSE EncodeFrom(Tail(r), Append(segs, [kind |-> "span", from |-> p, to |-> p]))

SetOf(r) == {r[i] : i \in 1..Len(r)}

RECURSIVE OwnOf(_)
OwnOf(r) == IF r = <<>> THEN <<>>
            ELSE (IF Head(r) \in OwnMsgs THEN <<[kind |-> "hash", msg |-> Head(r)]>> ELSE <<>>) \o OwnOf(Tail(r))

RECURSIVE SpansOf(_, _)
SpansOf(i, S) ==
    IF i > Len(CallView) THEN <<>>
    ELSE (IF CallView[i] \in S THEN <<[kind |-> "span", from |-> i, to |-> i]>> ELSE <<>>) \o SpansOf(i + 1, S)

RECURSIVE FirstOwnEverywhere(_, _)
FirstOwnEverywhere(segs, o) ==
    IF segs = <<>> THEN <<>>
    ELSE (IF Head(segs).kind = "hash" THEN <<[kind |-> "hash", msg |-> o]>> ELSE <<Head(segs)>>)
         \o FirstOwnEverywhere(Tail(segs), o)

\* The broken configurations: revision 7's encoding (the whole view if any
\* view message is present, then the planner's messages); spans that record
\* which positions appear but not their order; and a hash taken from the
\* first own message for every own message.
Encode(r) ==
    CASE Fault = "viewref_plus_added" ->
            (IF SetOf(r) \cap ViewMsgs = {} THEN <<>> ELSE <<[kind |-> "span", from |-> 1, to |-> Len(CallView)]>>)
            \o OwnOf(r)
      [] Fault = "span_ignores_order" -> SpansOf(1, SetOf(r) \cap ViewMsgs) \o OwnOf(r)
      [] Fault = "hash_first_own" /\ OwnOf(r) # <<>> -> FirstOwnEverywhere(EncodeFrom(r, <<>>), OwnOf(r)[1].msg)
      [] OTHER -> EncodeFrom(r, <<>>)

RECURSIVE Range(_, _, _)
Range(V, a, b) == IF a > b THEN <<>> ELSE <<V[a]>> \o Range(V, a + 1, b)

\* Decode against the recorded ViewRef. The broken configuration decodes
\* against the view as it is when the record is read.
RECURSIVE Decode(_, _)
Decode(segs, V) ==
    IF segs = <<>> THEN <<>>
    ELSE LET s == Head(segs)
         IN (IF s.kind = "hash" THEN <<s.msg>> ELSE Range(V, s.from, s.to)) \o Decode(Tail(segs), V)

DecodeView == IF Fault = "decode_current_view" THEN LaterView ELSE CallView

Init == req \in Requests

Next == UNCHANGED req

Spec == Init /\ [][Next]_req

\* Replaying the segments rebuilds the exact request: subsets, reordering,
\* duplicates, interleaving and distinct own messages included, after the
\* view has moved on.
RoundTrip == Decode(Encode(req), DecodeView) = req

\* The common case stays cheap: the view sent in place, followed by the
\* planner's additions, costs one span plus one segment per addition.
InPlaceIsOneSpan ==
    (Len(req) >= Len(CallView) /\ SubSeq(req, 1, Len(CallView)) = CallView
        /\ \A i \in (Len(CallView) + 1)..Len(req) : req[i] \in OwnMsgs)
    => Len(Encode(req)) = 1 + (Len(req) - Len(CallView))
=============================================================================
