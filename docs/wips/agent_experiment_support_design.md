# Framework support for agent directed experiments

## Summary

Draft for discussion, 2026-10-18. Loom-MCP should let an application expose its agent's editable components, turn a candidate into an isolated runnable configuration, and return inspectable execution evidence. Luke and AutoK own the improvement process, domain knowledge, environment provisioning, and deployment decisions. The framework supplies the contracts and reusable tooling that make that process possible.

The central capability is **edit → validate → materialize → execute → inspect**. Candidate hashes and token totals support that path; they do not replace it. A useful acceptance demonstration must compile or execute newly authored helper source and show its output affecting the deliverable. A second proof must show candidate-authored child instructions reaching a real child request. Choosing precompiled behavior from metadata does not meet this contract.

This document proposes contracts. The companion [plan](agent_experiment_support_plan.md) sequences the proposed work. Neither authorizes live experiments, application changes, releases, or production promotion.

## Review contract

Required behavior: identify concrete gaps relative to Harness², map all eight component classes to actual Loom-MCP capabilities, define application/framework ownership, and describe executable proof with two consumer shapes.

Acceptance proof: a fresh reviewer reads the Google code and paper independently, inspects the named framework and consumer sources, and critiques the draft. Findings and their disposition belong in the companion review record.

Affected surfaces: the initial review changed documentation only. The user subsequently authorized the companion implementation plan on 2026-10-18. Implementation owners are `eval`, `agenttest`, `trajectory`, optional packages beneath `eval`, fixtures and public documentation. New package and API names below are proposals until implemented by their milestones.

Exclusions: no dependency changes, optimizer, mandatory improvement stages, lesson extraction policy, benchmark task content, arbitrary in-process code loading, or changes to live Luke/AutoK state. The authorized plan includes implementation and local atomic milestone commits; it excludes pushes and releases.

## What Harness² actually does

The code baseline is Google commit `1cfe254e8d25b15f37048c3730134686abe60335`. These mechanics, rather than the headline scores, drive this proposal:

| Mechanism | Actual behavior and implication |
| --- | --- |
| Editable executable components | `core/tree.py` and the substrate modules declare permissible files, loading rules and validation. The editor changes a copied tree; `core/proposer.py` harvests its files. The final reply is not an executable candidate. A framework needs discoverable edit surfaces and a materializer. |
| Activation validation | Skills need discoverable triggers; referenced helpers must travel with them; unsupported subagents and malformed hooks fail validation. A syntactically valid file that never activates is not a working candidate. |
| Substrate-specific installation | OpenCode has three installation modes; Codex installs a different native layout. Capability support also differs by benchmark. Eight conceptual components do not imply eight universally supported portable file formats. |
| Contrastive execution | `core/recursion.py` runs references, independent general/domain proposals, probes, then freshly executed compositions. Sequential mode carries the current harness and accumulated evidence; parallel mode composes from a shared pool. This is not response voting or prompt rewriting alone. |
| Inspectable evidence | `core/context.py` creates navigable task, source, trajectory, deliverable, harness, diff and decision files. Whole trajectories are retained, but per-observation rendering can truncate. Missing evidence is explicit. |
| Selection and measurement | `core/selection.py` anonymizes final deliverables; `k=1` bypasses selection. Its final pool excludes reference outputs. Grading is separate from proposal evidence. Keep these choices application-owned. |
| Cross-task reuse | `core/library.py` retains an adopted executable tree with an experience record only when an entry exists. Subsequent tasks start from the common baseline, inspect earlier library entries, copy selected components and recheck task facts. The improvement library is not automatically injected solver memory. |
| Resource and lifecycle handling | Benchmark adapters own fresh workspaces/containers, installation, artifact extraction and completion checks. Resume depends on recorded configuration and intact outputs. A new Go runtime alone does not isolate databases, files, remote tools or memory. |

Sources: [tree](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/tree.py), [proposer](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/proposer.py), [recursion](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/recursion.py), [context](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/context.py), [selection](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/selection.py), [library](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/library.py), [OpenCode](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/substrate/opencode.py), [Codex](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/substrate/codex.py). The [paper's method](https://arxiv.org/html/2610.14224v1#S2) separates task-level refinement from learning how to refine later tasks. This design adopts neither algorithm as framework policy.

The important distinction within the eight components is when and how they affect execution. Google's [editor instructions](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/core/prompts.py#L98-L145) make that distinction available to the editing agent:

| Component | Execution role | Framework implication |
| --- | --- | --- |
| System prompt | Role and procedure, always in context | Frozen prompt binding shared by registration and request construction |
| Guardrails | Standing instructions, always in context | Editable instruction slots are separate from host permissions, which remain fixed |
| Memory | Retained domain facts included in context | Explicit snapshot; application decides what is true and what to retrieve |
| Tool guidance | Usage instructions associated with tools | Bind guidance and the schemas actually read by the planner together |
| Subagents | Bounded delegated work in another context | Expose supported child configuration, context boundary and capability limits |
| Skills | Discoverable trigger, body loaded on demand | Validate discovery plus helper/resource closure; retain on-demand loading |
| Scripts | Deterministic code executed on demand | Host execution binding must consume the submitted source bytes |
| Plugins | Registered behavior inside the tool loop | Use supported inline interception; observer callbacks alone are insufficient |

An editor needs this activation information to choose a suitable component. Adding everything to the system prompt would lose a central property of the Google implementation. Its [small demo](https://github.com/google-research/harness2/blob/1cfe254e8d25b15f37048c3730134686abe60335/harness2/demo.py#L86-L104) provides a useful minimum proof: the editor writes a helper, and the solver executes that file.

## Current state and ownership

| Surface | Current capability | Remaining work |
| --- | --- | --- |
| Prompts, standing guidance, tool guidance | `runtime/agent/prompt/registry.go` supports registration, scoped resolution and versioned rendering; planners construct model requests. | Host-defined editable prompt slots and attempt-local application of a frozen candidate. Do not mutate shared overrides to compare candidates. |
| Domain memory | `runtime/agent/memory/service.go` separates long-term entries from transcripts. | Bind a caller-selected immutable memory snapshot to a trial. Learning/retrieval policy stays with the host. |
| Skills and resources | `runtime/agent/runtime/skill_toolset.go` discovers/loads skills from roots; `runtime/mcp/skills` reads resources. | Candidate-local roots, dependency closure and activation proof. Loading a script resource does not execute it. |
| Child agents | Existing generated registrations and agent-as-tool run real child workflows. | Host adapter can construct approved child configurations before sealing. Arbitrary new tools/capabilities require explicit host support. |
| Deterministic helpers | Application tool executors can perform deterministic work. | A declared host execution binding for candidate helpers. Core does not supply a Python/JS shell or sandbox. |
| Hooks/plugins | `runtime/agent/runtime/interceptors.go` has inline tool/model/run interceptors, separate from observational hooks. | A host compiler/executor can install allowed interceptor behavior in a fresh trial. Event rewriting cannot supply authoritative proof of improvement. |
| Trial execution | `eval.NewAttemptHarnessFunc`, `AttemptHarnessConfig`, `Selection.Keys`, `agenttest.NewHarness`, passthrough/stubs and live-model guards. | Candidate installation and external-environment lifecycle around those APIs. Preserve registration sealing. |
| Evidence and comparison | `eval.Store`, `CaptureHarness`, `ReadEvidence`, `ShowJSON`, `CompareVariants`, `trajectory.Build` and versioned trajectory JSON. | Bounded addressable evidence views, artifact body retention, and separate views for solver/editor/selector/judge. |
| Accounting | Runner and judge totals, child/abandoned call evidence, token dispatch stop. | Report controller/preparation calls separately and combine them without double counting. No claim of a strict provider-spend cap. |

AutoK already uses framework evals in `internal/chat/eval_suite.go` and `eval_model.go`. Registration and input construction independently call `evalModel.request()`, which creates a default prompt registry. Its prompt service accepts an explicit content root, while `newChatPromptRegistry` currently constructs the default service. A candidate must supply one frozen prompt snapshot to both paths; changing process-wide `CONTENT_DIR` is not an isolated binding. Its `tech_docs/0005-agent-evaluation-optimization-pipeline.md` keeps Opik authoritative for shared review and promoted datasets. Preserve that boundary.

Luke's active `backend-go/internal/localapi/chatv2/agent_runtime.go` wires a durable Selvedge runtime, generated assistant and application dispatcher. `agent_planner.go` reads `Deps.OllamaToolSchemas`; message preparation is injected through `chatv2_wire.go`. Runtime registration alone does not control either. A trial needs an isolated service/fixture with candidate-bound schemas, messages and document/data destinations. Luke's older improvement notes and probe plan are not evidence that a portable candidate API exists.

## Proposed design

### 1. A declared editable surface and immutable candidates

Proposed package `eval/candidate` owns a small versioned content contract with no dependency on runtime execution. A host registers a surface ID/version, authorized baseline, named slots, accepted media types, size limits, explicit component dependencies, validation callbacks and installation binding identifiers. Discovery describes each slot's activation (always-on, on-demand, child-context or inline), permitted operations and capability boundaries. Slot names describe application capabilities, not fixed Google filenames. Unsupported categories are absent and edits to them fail. Actual installation callbacks belong to the execution host, not the content store.

A candidate is a complete frozen set of component bytes derived from an identified baseline. Editing uses an expected revision: explicit put/delete operations reject stale revisions and missing dependencies. Freezing computes SHA-256 over a canonical manifest containing the surface version, baseline digest, sorted component names/content digests, dependency edges and host binding versions. Labels and explanations do not substitute for content identity. Paths are confined, relative and normalized; duplicate names, escaping paths, symlinks and missing referenced bytes fail before any execution. Limits are host-declared, finite and included in the surface.

The host validates semantic dependencies and activation, using existing skill validation where applicable. Core does not parse prose to guess script references. No template braces are globally forbidden: template validation follows the actual host renderer. A frozen candidate contains retained bytes or immutable blobs, never references to live mutable directories. Mutation creates a new candidate; it cannot change a running or historical one.

### 2. One prepared attempt, including inputs and environment lifecycle

This needs an additive integration point in `eval` itself. `NewAttemptHarnessFunc` only returns a harness; it cannot own collection/release around `runAttempt`, and `Scenario.BuildInputs` independently receives only an item. Wrapping the current factory is insufficient. A proposed `Config.PrepareAttempt` is mutually exclusive with `Harness` and `AttemptHarness`. Its scenarios cannot also declare `BuildInputs` or `Drive`: the prepared result owns the driver. Legacy factory behavior and the existing `AttemptInfo` contract remain unchanged.

Preparation receives an owned attempt key, variant and item with scenario task/reference defaults already resolved. It returns one lease with `AttemptHarnessConfig`, exactly one prepared input list or prepared driver, environment identity, installation receipt, quiescence, collection and release callbacks. Registration and request construction close over the same frozen resources. Inputs cannot reread a live content root; a custom driver binds its fixed driver version and input snapshot, with subsequent actual messages recorded canonically. References remain host/evaluator data unless the host explicitly exposes them. No shared mutable scenario closures or item-ID-only side tables are involved.

The root eval execution path owns the lifecycle below. All callbacks must cooperate with cancellation. Prepared configuration requires positive `PrepareTimeout` and `CleanupTimeout`; zero or negative values fail validation. Preparation is bounded by the earlier of its timeout and the caller deadline. Scenario execution keeps its existing timeout semantics. Cleanup callbacks get a fresh bounded context so canceled task execution does not skip release. Each of quiescence, collection and release gets its own `CleanupTimeout`, ensuring an exhausted collector does not consume the release allowance. Current `Harness.Close()` has no context argument and keeps its existing internal cleanup behavior; these host callback limits do not create an overall hard deadline or forcibly stop noncooperating code. The host owns actual DB/container/filesystem/sandbox mechanisms.

| Phase | Required behavior and failure handling |
| --- | --- |
| Validate and prepare | Validate the candidate, acquire isolated state, install/compile its resources and freeze input configuration. Preparation returns any acquired lease even on error, allowing release. No evaluated-agent model dispatch occurs here. |
| Record and construct | Persist candidate/input/environment receipt before dispatch; construct and seal the harness with runner-owned options and durable evidence capture. Receipt or construction failure skips execution and still releases the lease. |
| Execute | Run the prepared inputs/driver. Record execution, quality and infrastructure outcomes separately. Input/driver errors and cancellation still enter finalization. |
| Stop and flush | Close the harness, stopping open runs and flushing durable capture; then quiesce host-owned processes/writers while keeping fixture state available. Errors remain explicit. |
| Collect | Reopen durable canonical capture and retain deliverables from the still-existing environment. Failed quiescence makes collected outputs explicitly provisional; missing output is not empty output. |
| Release | Release the acquired lease once, even after setup, driver, capture or collection failure. Record cleanup errors without discarding prior evidence. No terminal result claims complete evidence until finalization finishes. |

Callbacks return phase-specific errors, joined without losing the primary error. Partial preparation must either return its lease or release resources before returning. This is an in-process ownership guarantee; after process death, recovery and lease reclamation belong to the host executor. A new `eval/trial` package is not required to establish this seam.

#### Persistence binding

The additive binding is `Config.PreparedWriter *Writer`; `ProbeConfig` has the same field. It is required when executing prepared attempts, rejected on a legacy factory configuration, and is never created implicitly by `Run` or `Probe`. `Plan` validates a detached prepared configuration without a writer. The caller runs `Store.Begin(ctx, cfg, meta)`, assigns the returned writer to `cfg.PreparedWriter`, calls `Run`, then calls `Writer.Finish(report, runErr)`. `Store.BeginProbe` provides the corresponding probe admission. The caller owns experiment begin/finish; the runner owns prepared-attempt reservation, capture, receipt and final attempt publication. Existing storage-optional factories and `Writer.CaptureHarness` retain their behavior.

Before acquiring resources, the prepared path checks that the writer is open, has no sticky error, matches the execution kind and declared plan (ordered keys, dataset/variant provenance and runner settings), and has not reserved this attempt. It reserves the existing durable evidence binding before calling preparation. After preparation, it writes the immutable receipt before registration or any evaluated-agent model call. A wrong/finished/missing writer, conflicting reservation, or persistence failure prevents dispatch; partial leases still finalize. No application callback needs to implement receipt persistence.

Harness options retain base → configured → variant/runner ordering. The prepared result may provide `BaseOptions`; these precede `AttemptHarnessConfig.Options`. The runner then adds the variant options, default run options and writer-selected evidence directory. An application-supplied evidence directory conflicts and fails before registration; the runner never swaps stores after construction. Receipt and capture live in the experiment store, outside the releasable environment.

After harness close, quiescence, collection and release, the runner appends the finalized attempt through this writer and then invokes `Config.OnAttempt` as an observer. Prepared callers must not also wrap that observer with `Writer.OnAttempt`; the prepared path already owns persistence. The writer's sticky failure stops further dispatch and is returned even when no optional error callback was installed. `Writer.Finish` retains its existing final-report publication role, including later judgment updates. The control CLI extension uses this path directly; V1 does not add prepared configuration to the existing built-in `evalcli run` command.

Receipts bind candidate digest, surface/binding versions, task/source snapshot, prepared input or driver identity, model/configuration identity and environment snapshot ID to an attempt key. For compiled helpers they also retain compiler/dependency identity and executable digest. Receipts describe installation; recorded model/tool behavior proves use. The visible environment mode is `fixture`, `snapshot` or `external_read_only`, with repeatability limited accordingly. A fresh harness cannot make mutable remote services deterministic or isolate production state by itself.

The first concrete host binding compiles submitted Go helper source with the existing Go toolchain into an attempt-local executable and invokes it as a subprocess. Core adds no scripting engine or arbitrary in-process plugin loader. The source must introduce behavior that is not a precompiled fixture branch; invalid source fails before model dispatch. The second proof binds candidate-authored instructions to an approved child and verifies the actual child model request. Host-supported interceptor bindings remain expressible; V1 need not add another compiler just to demonstrate every category.

### 3. Operational probes without an invented quality score

An explicit proposed `eval.Probe` entry point executes one prepared attempt and retains its evidence without requiring a rubric, reference answer, assertion or judge. It shares private driving/capture/lifecycle code with eval; it does not add a scheduler or optimization loop. Probe inputs include task/item, attempt identity and preparation binding. The control service explicitly chooses `probe` or `evaluate`; it defaults to `probe` for exploratory submissions and never silently changes an existing eval scenario into a probe.

Probe results report execution status, delivery status, evidence completeness and `quality: not_assessed`. Failures remain failures, but completion is never `VerdictPassed`. A later application can request evaluation or attach its own comparison decision. Existing `eval.Validate`, `Execute`, quality verdicts and required-assertion semantics retain their defaults. Reports/comparisons show unassessed quality separately and do not count probes as passing evaluations.

#### Admission, stored representation and projections

`Probe(ctx, ProbeConfig)` returns the existing `Report` shape containing exactly one planned attempt. `Store.BeginProbe(ctx, ProbeConfig, ExperimentMeta)` validates that one key/item/variant, the prepared driver contract, timeouts and resource binding declarations without creating an evaluation `Scenario` or calling its quality validator. It reuses the existing experiment header, attempt log, writer and capture implementation. It does not admit evaluation assertions or judges. `BeginProbe` rejects `ExperimentMeta.Parent`; probe provenance uses explicit source references, never evaluation rerun overlays.

Add `ExecutionKind` to `Report`, `AttemptRecord`, `ExperimentHeader` and `StoredAttempt`, serialized as `execution_kind,omitempty` on persisted records. Empty means legacy evaluation; new prepared evaluation uses `evaluation` and probes use `probe`. Header and attempt kinds must agree. New prepared/probe experiments use version 2 of the existing experiment and attempts formats. The reader continues to accept version 1 with evaluation semantics. Legacy writers and legacy JSON output stay version 1 and omit every new optional field. Unknown kinds/versions and inconsistent kind/status combinations fail decoding; older readers may reject version 2 rather than misclassify it.

`AttemptRecord` and `StoredAttempt` add an optional `Probe *ProbeStatus`, serialized as `probe,omitempty`, required for probe attempts and absent for evaluations. Its required fields are `execution` (`not_run`, `succeeded`, `failed`, `canceled`), `delivery` (`not_declared`, `complete`, `incomplete`), `evidence` (`complete`, `incomplete`, `provisional`) and `quality` (only `not_assessed`). Delivery is complete only when all host-declared deliverables were retained; it is not a quality judgment. Evidence status is provisional after failed quiescence, otherwise incomplete after missing/corrupt capture or collection failure. Lifecycle failures remain errors alongside these statuses.

Add `VerdictUnassessed = "unassessed"`. A started probe with successful execution and no infrastructure/finalization error gets this verdict. A probe with execution or infrastructure failure retains `VerdictError`; one that never starts retains `VerdictNotRun`. Started cancellation is an execution error with `probe.execution=canceled`. None of these changes `probe.quality`. A probe cannot contain quality assertions, claims, judgments or a passed/failed/pending quality verdict. Informational runtime usage remains available.

| Projection | Probe behavior | Existing evaluation behavior |
| --- | --- | --- |
| Store/load and capture | Preserve kind and all status axes; receipt/capture is mandatory; `Finish` accepts the single-attempt probe report | Existing writer and quality validation remain unchanged for legacy callers |
| Groups and summary | Count probe attempts separately, including failed/canceled probes; exclude every probe from quality denominators. Successful unassessed probes are not listed as failing attempts; errors remain visible | Existing pass counts, denominators, ordering and golden outputs remain unchanged |
| Show text/JSON | Show execution/delivery/evidence plus quality unassessed and retained evidence | Existing fields and export controls remain unchanged |
| Comparison | If either side is a probe, group `status` is `not_comparable` for quality; no quality check/claim/score deltas or pass-fraction winner are inferred. Tool/usage/duration evidence can still be compared, with artifacts inspectable alongside it | Evaluation-versus-evaluation comparison keeps current semantics |
| Strict quality check | Reject with stable reason `quality_not_assessed`, including an unfinished probe; CLI check returns its existing rejection exit code 3 | Existing strict policy remains unchanged |
| Rerun and parent overlay | `RerunSelection` returns `ErrProbeRerunUnsupported`; compare rejects a parent overlay involving a probe. Retry is an explicit new probe submission/request identity | Existing `Parent` overlay and failed-evaluation rerun semantics remain unchanged |

New probe-specific counts/status fields are omitted from legacy projections. Show/summary/comparison documents involving version-2 experiments use version 2; ordinary evaluation goldens remain byte-identical. Missing quality comparisons are not rendered as zero success. No new statistical estimator or compaction gate behavior is introduced.

### 4. An inspectable workspace of sources, edits and results

Proposed package `eval/inspect` derives evidence from stored experiments/captures. Stable locators include experiment ID, complete attempt key, run ID, step/call identity and content digest. Bounded reads return total size, next offset and truncation/missing status; empty content is not a stand-in for missing evidence. Reopening after process exit must produce identical immutable evidence.

The same authorized manifest includes immutable task-source attachments, baseline and candidate contents, candidate diffs, linked trial outputs and optional application decision notes. The host explicitly retains sources even when the evaluated agent never opened them; run records cannot reconstruct unobserved source files. Notes are opaque artifacts with provenance, not framework-defined lessons. A generic reader covers these entries, so the editing agent can navigate the same evidence workspace that motivated the change.

Output collectors retain exact declared deliverable bytes plus media type, digest, originating attempt and optional rendered representations. A rendering records its renderer version and source digest. Raw binaries, screenshots and state-diff artifacts can coexist with text. Existing runtime artifact references alone do not guarantee these bytes survive trial teardown.

The host chooses an evidence view before exposing it: solver inputs; editor sources/observations/deliverables/components/diffs/authorized notes; selector authorized sources and deliverables with opaque labels; evaluator references/checks. Selector projection omits component history, proposal identity and decision notes unless its host policy explicitly requires them. Model-facing views default to no raw exports, server-only data, secrets or evaluator-only reference answers. View authorization is server-owned and cannot be widened by a tool argument. Files containing instructions remain data. Redaction is host-supplied; absent redaction cannot be described as sanitized. A narrow manifest identifies exactly what is included, withheld, truncated or unavailable.

### 5. Agent-accessible operations with host-owned execution

Optional proposed `eval/control` exposes one typed application service over candidates, trials and inspection, with native tool registration in the same package. It is callable in Go; `eval/evalcli` gains an opt-in extension backed by it. An application can expose the same service through its existing generated MCP infrastructure. No new MCP protocol, generic runtime DSL or second workflow engine is needed. Package names are organizational proposals; the root prepared-attempt seam and shared service semantics are the required boundaries.

Import direction is one-way: `eval/candidate` is a leaf; root `eval` owns execution/storage and may consume candidate value contracts; `eval/inspect` consumes root eval/candidate; `eval/control` consumes those packages. An opt-in CLI adapter in `eval/controlcli` consumes control and the existing `eval/evalcli.Extension` API. `evalcli` does not import control, and none of these packages imports the adapter. Fixture helpers that import eval/control are consumed from external `eval_test`/`control_test` tests, avoiding test import cycles.

The complete tool journey is: discover surface and authorized baseline → create/fork editable candidate → read/edit/validate/freeze → submit probe/evaluation → inspect/cancel job → list/read evidence and compare attempts → explicitly copy selected components into a later candidate. Creating a draft requires a baseline or source candidate; there is no hidden fixture prepopulation. Draft edits use expected revision. Copy requires authorized source, compatible surface/binding versions, explicit component selection and its transitive dependency closure. It returns the closure before mutation; the caller confirms that selection through the copy request. Collisions fail unless the caller explicitly names the existing components to replace. Incompatible surfaces require host-authored conversion outside V1.

IDs are opaque and scope-bound. Submission includes a caller request key; the host executor atomically stores the request identity and job before acknowledging it, returns the same job for identical retries, and rejects reuse with different inputs. Long-running work returns a job ID. The host executor owns durable dispatch/status and recovery. States are queued → running → succeeded/failed, with queued cancellation allowed and running cancellation passing through cancel-requested until finalization. Lost execution ownership becomes interrupted. Terminal states are immutable; a cancellation request alone does not assert that side effects stopped. A durable fixture must recover crashes around acceptance and completion without duplicate execution; uncertain interrupted effects cannot be silently replayed.

`canceled` is a terminal job state. Queued cancellation commits it without preparation. Running cancellation reaches it only after execution has stopped and finalization has completed. Failure to establish stopped execution is `interrupted`; a recorded infrastructure/finalization error is `failed`. When cancellation races success, the host serializes state transitions: an already committed terminal success remains success; cancellation accepted first prevents a later success commit and completes as canceled after safe finalization. Raw execution/evidence statuses remain available independently of that job state.

The framework validates and records contracts; it does not schedule propose/probe/compose, decide candidate counts, select a winner, or update memory. Capabilities are explicitly registered and default to absent. Authorization, live-model permission and executor availability are checked before acceptance. Command and tool inputs cannot enable live models, choose credentials, rewrite permissions or select arbitrary filesystem roots.

### 6. Reuse and provenance without an optimizer

Frozen candidates can be read, diffed and copied into a later candidate. Generic provenance links name source candidate/experiment/attempt plus an optional opaque application decision artifact. This supports an application library that stores executable components and notes without the framework inventing a lesson schema, domain taxonomy or retrieval policy. The caller selects a fixed library snapshot; no implicit ingestion or automatic inheritance occurs.

V1 keeps existing evaluated-agent and judge usage evidence. Controller/preparation calls can be linked as their own existing run records. A unified process ledger, pricing identities, deduplication of external usage and cross-process spend enforcement are follow-up design work; they do not block the executable edit/probe/inspect/reuse journey. Existing runner token-stop semantics, including in-flight completion, remain unchanged.

## Contract matrix

| Capability | Supported and source of truth | Rejected | Default and placement | Routing and equivalence | Proof obligation |
| --- | --- | --- | --- | --- | --- |
| Candidate | Frozen manifest and immutable component bytes | Unknown slots, stale edits, unsupported kinds, missing dependencies, invalid content | Absent unless host declares a surface; `eval/candidate` | Same validator for Go/CLI/tool calls; stable names within one surface | A declared component changes the materialized trial; concurrent candidates cannot alter one another |
| Trial | One prepared lease binds configuration, inputs and environment | Conflicting factories/drivers, unsupported binding, changed receipt | Optional root `eval` seam; old factories unchanged | Shared lifecycle for explicit probe and evaluation | Frozen requests and tools agree; construction/cancellation/collection failures release exactly once |
| Probe | Execution/delivery/evidence with quality unassessed | Fabricated passing quality or implicit evaluation downgrade | Explicit `eval.Probe`; control submission defaults to probe | Shared execution internals, separate quality semantics | No-rubric task runs and yields inspectable evidence; eval still rejects no-assertion scenarios |
| Evidence | Immutable run capture and collected artifact blobs | Unauthorized views/locators; digest mismatch | Bounded model-facing view excludes privileged material; `eval/inspect` | Same reader across projections | Referenced step/artifact is readable after teardown; hidden evaluator data is absent |
| Control | Registered service and host job executor | Conflicting request keys, unauthorized scope, implicit live enabling | No tools/commands registered by default; `eval/control` | Shared Go service; application MCP projects it | Tool-only create/edit/submit/poll/inspect/copy journey; durable restart does not launch twice |
| Reuse | Explicit component closure and source refs | Unauthorized sources, unapproved replacement, incompatible surfaces, dangling local refs | No automatic library or adoption | Versioned additive metadata; old stores remain readable | A later baseline-derived candidate explicitly copies and executes prior components |

Compatibility: existing `eval.Run`, runtime registration, default CLI output, trajectory JSON and stores retain their current semantics when the new optional capability is absent. Existing version-1 experiments remain readable. New optional metadata must not reinterpret `Parent` (which currently drives rerun overlay). `eval/gate` has compaction-specific measures and is not a general optimizer gate. No runtime workflow command changes are proposed; a later need for one is a design change requiring protocol/replay planning.

## Proof obligations

- A controller discovers the baseline, creates a draft, edits a skill and newly authored helper source, fixes missing-dependency and invalid-source failures, freezes and executes it. Two source revisions cause different tool output and deliverables, including behavior unavailable as a precompiled fixture branch. Build identity and invocation evidence agree.
- A second candidate changes an approved child's instructions. The actual child request carries those bytes and child execution appears in canonical evidence. Unsupported interceptor/helper/child bindings fail before execution.
- Two candidates run the same item concurrently. Changing the original content directory after preparation changes neither the installed tools nor either candidate's model requests.
- A task with no quality rubric produces execution status, artifacts and trajectory with quality unassessed. A normal eval scenario without assertions remains invalid.
- Same task snapshot under baseline and candidate produces separate output artifacts. Parallel attempts, retries, cancellation, setup failure and teardown cannot contaminate sibling trials or the production workspace.
- Composition is an application action that produces another candidate and executes it; copying two files is not treated as proof of the combination's behavior.
- A later task starts from the original baseline, explicitly copies a prior skill/helper closure and revalidates task facts through application logic. No prior task state or evaluator reference is inherited implicitly.
- A Luke-shaped document-edit fixture and an AutoK-shaped product-graph fixture execute through public framework APIs. A model-facing controller can inspect artifacts and their provenance without access to protected reference answers.
- Published evidence, including a source file the solver never opened, two candidate diffs and an authorized decision note, survives teardown and restart. A completed job is not launched again under the same request identity; an uncertain interrupted job is reported rather than silently replayed.

## Review status and remaining application work

The first independent review found missing prepared-input/lifecycle integration, no-rubric probe semantics, executable-source proof, source/decision evidence and a complete tool-only create/copy journey. A second fresh reviewer confirmed the Harness² interpretation and found two remaining integration gaps: writer binding and probe admission/projections. This revision defines both using the existing writer/store. The [review record](agent_experiment_support_review.md) tracks the findings and re-checks. Production host bindings, sandbox implementations, environment reset recipes and adoption workflows remain separately owned application work. A framework fixture proves the contract, not that Luke or AutoK is already integrated.
