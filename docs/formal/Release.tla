------------------------------- MODULE Release -------------------------------
EXTENDS Naturals

(* Commits form the linear main history 0 -> 1 -> 2. Two invocations may
   compete for one version. Git ref updates and GitHub API mutations are
   atomic; a crash can follow any completed mutation, including a lost reply.
   CI is bound to the candidate commit, never to the moving branch name.
   Shell tests cover dirty trees, divergent histories, tag object identity,
   API failures, metadata, and timeouts outside this finite abstraction. *)
CONSTANTS RequirePrePushCI, ResumeTags, AllowFailure, MaxCrashes
Clients == 1..2
Commits == 0..2
Phases == {"start", "push", "ci", "tag", "pushTag", "publish", "done", "stopped"}

VARIABLES main, ci, localTag, tag, release, phase, candidate, checked, crashes
vars == <<main, ci, localTag, tag, release, phase, candidate, checked, crashes>>

Init ==
    /\ main = 0
    /\ ci = [c \in Clients |-> "missing"]
    /\ localTag = [p \in Clients |-> 0]
    /\ tag = 0
    /\ release = 0
    /\ phase = [p \in Clients |-> "start"]
    /\ candidate = [p \in Clients |-> p]
    /\ checked = [p \in Clients |-> FALSE]
    /\ crashes = [p \in Clients |-> 0]

Start(p) ==
    /\ phase[p] = "start"
    /\ IF ~ResumeTags /\ (localTag[p] # 0 \/ tag # 0)
       THEN /\ phase' = [phase EXCEPT ![p] = "stopped"]
            /\ UNCHANGED <<candidate, localTag>>
       ELSE IF localTag[p] # 0 /\ tag # 0 /\ localTag[p] # tag
       THEN /\ phase' = [phase EXCEPT ![p] = "stopped"]
            /\ UNCHANGED <<candidate, localTag>>
       ELSE /\ candidate' = [candidate EXCEPT ![p] =
                  IF tag # 0 THEN tag ELSE IF localTag[p] # 0 THEN localTag[p] ELSE p]
            /\ localTag' = [localTag EXCEPT ![p] = IF tag # 0 THEN tag ELSE @]
            /\ phase' = [phase EXCEPT ![p] =
                  IF tag # 0 \/ localTag[p] # 0 THEN "ci" ELSE "push"]
    /\ UNCHANGED <<main, ci, tag, release, checked, crashes>>

PushMain(p) ==
    /\ phase[p] = "push"
    /\ ~RequirePrePushCI \/ ci[candidate[p]] = "passed"
    /\ IF main <= candidate[p]
       THEN /\ main' = candidate[p]
            /\ phase' = [phase EXCEPT ![p] = "ci"]
       ELSE /\ UNCHANGED main
            /\ phase' = [phase EXCEPT ![p] = "stopped"]
    /\ UNCHANGED <<ci, localTag, tag, release, candidate, checked, crashes>>

DiscoverCI(c) ==
    /\ c <= main /\ ci[c] = "missing"
    /\ ci' = [ci EXCEPT ![c] = "pending"]
    /\ UNCHANGED <<main, localTag, tag, release, phase, candidate, checked, crashes>>

CompleteCI(c) ==
    /\ ci[c] = "pending"
    /\ ci' \in {[ci EXCEPT ![c] = "passed"]} \cup
         (IF AllowFailure THEN {[ci EXCEPT ![c] = "failed"]} ELSE {})
    /\ UNCHANGED <<main, localTag, tag, release, phase, candidate, checked, crashes>>

CheckCI(p) ==
    /\ phase[p] = "ci"
    /\ ci[candidate[p]] \in {"passed", "failed"}
    /\ checked' = [checked EXCEPT ![p] = ci[candidate[p]] = "passed"]
    /\ phase' = [phase EXCEPT ![p] = IF ci[candidate[p]] = "passed" THEN "tag" ELSE "stopped"]
    /\ UNCHANGED <<main, ci, localTag, tag, release, candidate, crashes>>

CreateTag(p) ==
    /\ phase[p] = "tag" /\ checked[p] /\ candidate[p] <= main
    /\ localTag' = [localTag EXCEPT ![p] = candidate[p]]
    /\ phase' = [phase EXCEPT ![p] = "pushTag"]
    /\ UNCHANGED <<main, ci, tag, release, candidate, checked, crashes>>

PushTag(p) ==
    /\ phase[p] = "pushTag"
    /\ IF tag = 0 \/ tag = localTag[p]
       THEN /\ tag' = localTag[p]
            /\ phase' = [phase EXCEPT ![p] = "publish"]
       ELSE /\ UNCHANGED tag
            /\ phase' = [phase EXCEPT ![p] = "stopped"]
    /\ UNCHANGED <<main, ci, localTag, release, candidate, checked, crashes>>

Publish(p) ==
    /\ phase[p] = "publish" /\ tag = candidate[p]
    /\ release' = tag
    /\ phase' = [phase EXCEPT ![p] = "done"]
    /\ UNCHANGED <<main, ci, localTag, tag, candidate, checked, crashes>>

Crash(p) ==
    /\ phase[p] \notin {"start", "stopped"} /\ crashes[p] < MaxCrashes
    /\ phase' = [phase EXCEPT ![p] = "start"]
    /\ checked' = [checked EXCEPT ![p] = FALSE]
    /\ crashes' = [crashes EXCEPT ![p] = @ + 1]
    /\ UNCHANGED <<main, ci, localTag, tag, release, candidate>>

Work(p) == Start(p) \/ PushMain(p) \/ CheckCI(p) \/ CreateTag(p) \/ PushTag(p) \/ Publish(p)
Next == \E p \in Clients : Work(p) \/ Crash(p) \/ DiscoverCI(p) \/ CompleteCI(p)
Spec == Init /\ [][Next]_vars /\
        (\A p \in Clients : WF_vars(Work(p)) /\ WF_vars(DiscoverCI(p)) /\ WF_vars(CompleteCI(p)))

TypeOK ==
    /\ main \in Commits /\ tag \in Commits /\ release \in Commits
    /\ ci \in [Clients -> {"missing", "pending", "passed", "failed"}]
    /\ localTag \in [Clients -> Commits] /\ phase \in [Clients -> Phases]
    /\ candidate \in [Clients -> Clients] /\ checked \in [Clients -> BOOLEAN]
    /\ crashes \in [Clients -> 0..MaxCrashes]
TagVerified == tag = 0 \/ (tag <= main /\ ci[tag] = "passed")
ReleaseVerified == release = 0 \/ (release = tag /\ ci[release] = "passed")
MainNeverRewinds == [][main' >= main]_vars
TagNeverMoves == [][tag = 0 \/ tag' = tag]_vars
ReleaseNeverMoves == [][release = 0 \/ release' = release]_vars
EventuallyPublished == <>(release # 0)
=============================================================================
