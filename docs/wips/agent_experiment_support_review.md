# Harness² source review and plan critique

Date: 2026-10-18. This records the independent review behind [the design](agent_experiment_support_design.md) and [the plan](agent_experiment_support_plan.md).

## Review procedure and limits

The reviewer started with a fresh agent context, without the primary agent's earlier conclusions. It was explicitly asked to reread the Google repository and paper first, then inspect Loom-MCP and current Luke/AutoK consumers, and finally critique the draft. It made no edits and delegated no work. This is a review of source and proposed contracts, not evidence of live benchmark performance or completed application integration.

Google baseline: commit `1cfe254e8d25b15f37048c3730134686abe60335`, cloned to `/tmp/harness2-review-20261018`. Paper: [Harness², v1](https://arxiv.org/html/2610.14224v1). The primary agent also traced the execution path and ran `python3 -m unittest discover -s tests -v` in the Google checkout: all five included tests passed in 16.625 seconds. These local tests do not reproduce its live model experiments.

Current consumer sources were inspected read-only. Luke: `/Users/luca/code/luke/backend-go/internal/localapi/chatv2` and `chatv2_wire.go`. AutoK: `/Users/luca/code/autok-deploy/autok-app/autok-server/internal/chat`, `internal/service/prompts`, and its evaluation/runtime-content technical documents. No information was posted to Google or another external project.

## What the reread changed

1. **Executable components come first.** Google harvests the files the editor changed, then installs and runs them. The editor's final prose is not the candidate. The [demo](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/demo.py#L86-L104) writes a helper and [executes it](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/demo.py#L156-L160). A framework proof that only changes metadata or selects a precompiled callback misses that mechanism.
2. **Component form changes execution cost and reach.** Always-on instructions, on-demand skills/scripts, delegated context and inline middleware are distinct capabilities. The [editor prompt](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/prompts.py#L98-L145) makes the choice explicit. The substrate validates and activates supported forms; a framework should advertise those semantics rather than promise universal Google-compatible files.
3. **The editor gets a workspace of evidence.** It can inspect task sources, trajectories, outputs, current/proposed trees, diffs and decisions through [context construction](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/context.py#L105-L188). Captured model calls alone cannot supply a source file the solver never opened.
4. **Probes and selection do not require a supplied answer key.** Probe execution produces evidence; application selection and separate grading have different roles. [Recursion](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/recursion.py#L271-L296) does not justify labeling operational completion as quality success.
5. **Reuse stores executable material.** The [library](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/library.py#L106-L152) retains the selected tree plus experience information. Later tasks begin from the baseline and the editor deliberately retrieves/copies components. This is different from automatically replacing the next task's instructions with the previous winner.

## Initial independent findings and disposition

| Finding | Source evidence | Revision |
| --- | --- | --- |
| P1: factory wrapper cannot own the proposed lifecycle or freeze input preparation | `eval/attempt_builder.go`, `attempt_factory.go`, `runner.go`, `scenario.go`; AutoK independently calls `request()` during registration and input construction | Add an optional root `PrepareAttempt` seam binding resolved inputs, registration and environment. Close/flush, quiesce, collect and release belong to that execution path. Concurrent same-item/source-mutation proof is mandatory. |
| P1: current eval rejects operational probes without quality checks | `eval/scenario.go` validates assertions; `eval/execute.go` rejects health-only quality outcomes; `eval/verdict.go` defines passing quality | Add an explicit probe path with quality unassessed while preserving normal eval defaults. Do not insert a synthetic completion assertion. |
| P1: fixture could select precompiled behavior instead of executing edited bytes | Initial fixture acceptance asked for a helper/interceptor but did not require source execution; Google's demo does | Require compilation/execution of submitted Go helper source, source/build identity and observable output. Separately verify candidate-authored instructions in the child request. |
| P2: evidence contract omitted unobserved task sources and application decisions | `harness2/core/context.py` retains source/candidate/diff context; `selection.py` supplies adopted-tree and decision context | Include authorized immutable task attachments, candidate contents/diffs and opaque decision notes in the same manifest/read contract. |
| P2: tool-only journey had no candidate creation or public reuse operation | Initial operations began with read/edit, and copy existed only as a lower-level proposal | Discovery exposes an authorized baseline; create/fork begins the draft; public copy includes dependency closure, compatibility and replacement rules. |

Additional review recommendations were adopted: remove a separate trial wrapper/package, keep package choices subordinate to required behavior, move the first executable proof to milestone 2, explicitly test durable host-executor crashes, and defer unified process accounting. Native tool/Go/CLI support is the V1 projection; a generated MCP service remains application follow-up.

The initial reviewer judged the draft **not ready**. After the corrections, the same independent reviewer performed a focused re-check against the changed text and relevant code. It found **no remaining blockers**, judged the design ready for execution planning and the plan technically ready for implementation. It confirmed the revised lifecycle agrees with current evidence ownership, probes preserve eval semantics, and the source-execution/evidence/tool-journey proofs address all five findings. The re-check certifies design/plan readiness only, not unimplemented runtime behavior. Implementation is outside the current documentation-only task.

## Second fresh review

The user requested another fresh agent on 2026-10-18. This reviewer received no prior agent conversation and was instructed not to read this record until it had formed findings. It independently inspected the updated design/plan, Google mechanisms and current framework/consumer code, then read the earlier review. It confirmed the Harness² interpretation and application/framework boundary, but judged the plan **not ready** because two integration contracts were still undefined.

| Finding | Current-code evidence | Applied correction |
| --- | --- | --- |
| P1: mandatory prepared receipt/capture has no writer binding | `eval.Config` has no writer; `Writer.CaptureHarness` only wraps `AttemptHarnessFunc`; `selection.go` admits two factories; constructor options have established ordering | Design names `Config.PreparedWriter` and `ProbeConfig.PreparedWriter`. Detached plan/store admission precedes attachment; runner reserves capture, persists receipt before construction, appends only after finalization; caller owns `Begin`/`Finish`. Missing/wrong/closed writer, conflicting capture and receipt-write failures get explicit proofs. |
| P1: probes have no admission/storage/projection contract | `Store.Begin` calls quality-requiring `Plan`; decoder admits five verdicts; groups/comparison use execution counts as quality denominators; summary treats every nonpass as failing; strict-check/rerun have independent switches | Design names `Store.BeginProbe`, `ExecutionKind`, `ProbeStatus`, `VerdictUnassessed`, v2 opt-in storage and v1 compatibility. A projection table defines summary/show/comparison/strict-check/rerun semantics. Milestone 2 names all affected code and proves store → finish → reopen → projections end to end. |

Nonblocking precision fixes were also applied: reuse the existing CLI `Extension` API through a leaf adapter; state import direction and external fixture test packages; define terminal cancellation and success-race precedence; separate host callback deadlines from context-free `Harness.Close`; add targeted eval/control race/shuffle/repetition alongside the inherited stress gate. Because milestone 2 changes quality denominator/comparison handling, its plan also includes the qualification gate.

The second reviewer completed its focused re-check and found **no remaining blockers**, judging the corrected plan **ready to execute**. It confirmed writer ownership, pre-dispatch persistence, probe admission/versioned storage, quality projections, cancellation semantics, package boundaries and concrete acceptance proofs. The initial two findings remain recorded because the earlier clean review did not cover these previously unspecified integration decisions. No source implementation or runtime tests were performed as part of either document review.

## Boundaries preserved

- No framework-owned general/domain proposal split, search width, composition strategy, winner selection, reflection policy or domain taxonomy.
- No automatic lesson ingestion, production promotion or change to Opik's application-owned role.
- No hot modification of sealed runtimes, arbitrary native plugin loading, generic shell executor or new workflow engine.
- Environment provisioning, process recovery/reclamation and live application integration stay host-owned. Framework fixtures exercise those contracts without claiming to have integrated Luke or AutoK.
- Existing eval assertions/verdicts, runtime authorization, old store readability and `Parent` rerun semantics remain compatibility requirements.

## Verification for this documentation task

Only the three linked draft Markdown files are added. No framework or consumer code is changed; no implementation tests are claimed. Repository-wide Go gates are specified for future implementation milestones, not run as a substitute for reviewing the documents.
