# Agent experiment support plan

This plan sequences the contracts in [the design](agent_experiment_support_design.md). It supplies framework support for an application-defined improvement process. It does not implement Harness² or modify Luke/AutoK. New API and package names below are proposed additions until their milestones are implemented.

## Status

- 2026-10-18 — Initial source-backed draft reviewed by a fresh agent that independently reread Google Harness², its paper, Loom-MCP and consumer code. Five contract gaps identified.
- 2026-10-18 — Revised around a root prepared-attempt seam, explicit unassessed probes, executed helper source, complete evidence workspace and tool-only create/copy operations. Focused independent re-check found no remaining blockers. Technically ready for implementation; current task remains documentation-only. See the [review record](agent_experiment_support_review.md).
- 2026-10-18 — A second fresh reviewer found two remaining integration blockers: writer binding and probe persistence/quality projections. Both were corrected; its focused re-check found no remaining blockers and judged the updated plan ready to execute. This confirms documentation readiness only; implementation remains outside the current task.
- 2026-10-18 — User authorized implementation of this plan using Sol agents. Work started on `luca/agent-experiment-support`; milestone contracts, failing tests, mandatory gates and independent review precede each atomic commit. Pushes, releases, live experiments and production consumer changes remain outside scope.
- 2026-10-18 — Milestone 1 committed as `1a3a751f`. Independent review found directory anchoring and frozen-draft integrity defects; both received failing regression tests and fixes, then a clean re-review. `make lint`, `make test`, `make ci-integration` and the commit hook passed. Milestone 2 is in progress with separate Sol authors for lifecycle, persistence/projections and the executable-source fixture.

## Scope and current inventory

Required: editable surface discovery; immutable candidates; one prepared configuration/input/environment binding; operational probes; retained sources/outputs and bounded evidence; optional agent control; explicit component reuse. Application-owned: proposal/composition/selection/reflection policy, real environment provisioning, domain checks and production adoption. Unified process accounting and pricing are deferred.

Reuse `eval/{attempt_builder,attempt_factory,runner,execute,scenario,selection,record,verdict,report_groups,show,show_json,quality_check}.go`, `eval/store*.go`, `eval/compare*.go`, `agenttest/harness.go`, `agenttest/evidence.go`, `trajectory/{build,document,render}.go`, `runtime/agent/prompt`, `runtime/mcp/skills`, and `runtime/agent/runtime/{skill_toolset,interceptors}.go`. Preserve immutable registration, canonical call records, default eval quality requirements and runtime-owned authorization. Do not modify generated files manually.

The critical current split is concrete: `AttemptHarnessFunc` returns only a harness and receives item data before defaults; `Scenario.BuildInputs` receives a separately resolved item; `runAttempt` owns execution/close. A wrapper cannot supply the proposed shared preparation and finalization. The integration belongs in root `eval`, with content/inspection/control as optional supporting packages. No separate trial runner or workflow engine is planned.

The design fixes persistence ownership: detached `Plan`/`Store.Begin` first, then attach `Config.PreparedWriter`, then `Run`, then caller-owned `Writer.Finish`. `Store.BeginProbe` admits one operational attempt through the same storage, with `ProbeConfig.PreparedWriter` required before execution. Root eval publishes prepared attempts; `OnAttempt` is an observer in this mode. Existing `Writer.CaptureHarness` and built-in CLI run remain legacy paths. The new CLI adapter uses the existing extension facility from a leaf `eval/controlcli` package. Fixture helpers importing root eval/control require external test packages.

Consumer proof shapes come from inspected sources: Luke `/Users/luca/code/luke/backend-go/internal/localapi/chatv2/{agent_runtime,agent_planner,service}.go` and `chatv2_wire.go`; AutoK `/Users/luca/code/autok-deploy/autok-app/autok-server/internal/chat/{eval_suite,eval_model,prompts}.go` and `internal/service/prompts`. Proofs live in Loom-MCP fixtures. Production adapters require separate scoped tickets.

Each milestone is one focused ticket and one atomic commit. Before edits, freeze its contract and behavioral matrix. Add failing executable scenarios before implementation; introduce minimal type shells before running tests importing new symbols. Use targeted checks during edits. Freeze the diff, run each required gate once, obtain independent review, correct blockers/regressions and repeat affected checks/review. Never bypass hooks. Implementation and local milestone commits are authorized; pushes are not.

## Milestones

### Milestone 1: Discoverable edit surfaces and immutable candidates

Goal: A controller discovers supported component behavior and creates a valid, frozen candidate from an authorized baseline.

Acceptance Criteria

- The design review has no unresolved blockers before implementation begins.
- Surface discovery includes baseline identity, editable slots, activation mode, limits, supported bindings and component dependencies.
- Create/fork/edit/freeze rejects stale revisions, unknown slots, escaping paths, missing dependencies and invalid host content; freezing retains bytes independent of later directory edits.
- Existing eval behavior and stored experiments remain unchanged without opt-in.

Checklist

- [x] Read this plan, its design, `AGENTS.md` and `.agents/skills/loom-mcp/SKILL.md`; recite milestone order, exit criteria, proof commands, test-first sequence, review/commit gates and generation rules before code edits.
- [x] Reconcile `rg -n 'Variant|AttemptHarness|Fingerprint|Parent|CaptureHarness|Verdict|CheckQualityStrict|RerunSelection|validateConfig' eval agenttest` and `rg -n 'Resolve|RenderResolved|SkillToolsetConfig|Interceptor|Seal' runtime/agent/prompt runtime/agent/runtime runtime/mcp/skills` into the ticket's affected-contract inventory; map matches to the milestone that changes them or an explicit unchanged-contract disposition.
- [x] Add proposed `eval/candidate/{candidate,surface,store}_test.go` scenarios with minimal compiling declarations, then implement those contracts in the corresponding files. Keep execution callbacks out of the candidate store.
- [x] Run `go test ./eval/candidate/...`; update `docs/agent_testing.md`, `docs/runtime.md` and applicable repo skill references with supported/unsupported surfaces and activation semantics.
- [x] Freeze the diff; run `make lint`, `make test`, `make ci-integration`; obtain independent review before the ticket's atomic commit.

### Milestone 2: Prepared attempts, operational probes and the first executable proof

Goal: A newly authored helper changes a real trial's behavior through one frozen configuration/input/environment binding.

Acceptance Criteria

- New `Config.PrepareAttempt` is exclusive with legacy factories and scenario-owned drivers. Existing factory contracts stay unchanged. Prepared registration and inputs share one immutable snapshot after task defaults resolve.
- `Config.PreparedWriter`/`ProbeConfig.PreparedWriter` reject missing, wrong-plan, finished and errored writers before acquisition. Receipt-write failure prevents registration/dispatch; runner capture options preserve base/configure/runner ordering and reject an application evidence directory.
- Concurrent candidates on the same item retain distinct model requests and tools even when the source content directory changes after preparation.
- Phase tests cover preparation/construction/input/cancellation/quiescence/collection/release failures; acquired leases release once in-process. Positive host callback timeouts are mandatory; existing context-free `Harness.Close` cleanup behavior is not redefined.
- `eval/probe_store_test.go` proves no-rubric probe → existing store → finish → reopen → summary/show/compare/strict check. Kind/status axes survive; quality comparison is not_comparable; strict check rejects with quality_not_assessed; ordinary rerun rejects probes.
- Version-2 prepared/probe records coexist with readable version-1 records. Legacy evaluation store/report golden files are byte-identical; successful probes are neither quality passes nor failing attempts. Failed probes remain visible without entering quality denominators.
- The fixture compiles submitted Go helper source, invokes the resulting executable, and uses its result in a deliverable. Two revisions produce different behavior, including a new behavior unavailable as a precompiled branch. Invalid source fails before model dispatch.
- This milestone already proves edit → validate → install → execute → close/quiesce → retain output → release. Later milestones improve browsing and agent access; they do not postpone execution proof.

Checklist

- [x] Add proposed `eval/{prepared_attempt,probe,probe_store}_test.go`; extend `eval/{runner,execute,scenario}_test.go`, `attempt_factory_test.go`, `selection_test.go` and store tests for default compatibility, writer binding, timeout/capture conflicts and failure ordering. Name concurrency/lifecycle cases with `TestPrepared` or `TestProbeLifecycle` prefixes. Introduce minimal type shells before tests compile.
- [x] Implement prepared contracts in proposed `eval/prepared_attempt.go` and probe entry point in `eval/probe.go`; integrate ownership/default resolution in `eval/{runner,execute,attempt_factory,selection}.go`. Add `PreparedWriter` and required callback timeout fields; preserve detached plan validation and legacy factory behavior. Run `go test ./eval -run 'TestPrepared|TestProbe|TestAttemptHarness|TestRunner|TestScenario|TestSelection'`.
- [x] Implement `Store.BeginProbe` plus prepared writer reservation/receipt/final publication in `eval/{store,store_evidence,store_write,store_finish}.go`. Add execution kind, optional prepared receipt/probe status and `VerdictUnassessed` in `eval/{record,verdict,store_schema,store_decode}.go`, following the design's version-2/legacy omission rules. Keep root eval independent of inspect/control.
- [x] Apply the design's projection table in `eval/{record,report_groups,show,show_json,compare,compare_types,compare_deltas,compare_text,quality_check,store_read}.go` and existing CLI show/compare/check output. Extend `verdict_test.go`, `report_fixture_test.go`, `compare_test.go`, `quality_check_test.go`, `store_read_test.go` and `eval/evalcli/{check,compare}_test.go`. Add v2 fixtures alongside unchanged v1 goldens; run `go test ./eval/...`.
- [x] Add proposed `eval/testfixture/component_host.go` and external-package `eval/component_probe_test.go` (`package eval_test`). The fixture writes frozen source into an isolated directory, runs the existing Go compiler, records source/compiler/dependency/executable identities, executes the helper subprocess through a tool and captures output before release. The helper uses only the standard library; no dependency or generic shell runtime is added.
- [x] Use scripted model turns to discover/load the skill, invoke its helper and consume the actual result. Assert invocation and resulting deliverable, not receipt metadata or callback counts. Retain canonical capture outside the released environment; reopen it after close for verification.
- [x] Run `go test ./eval/... ./agenttest/...`; update `docs/agent_testing.md` and repo-local contract references with preparation/finalization ordering and probe semantics.
- [x] Run `go test -race -shuffle=on -count=20 -timeout=15m ./eval -run '^(TestPrepared|TestProbeLifecycle)'` for the new lifecycle contract. The inherited `make test-stress` does not include eval, so this targeted repetition is additional proof.
- [ ] Freeze the diff; run `make lint`, `make test`, `make ci-integration`, `make test-stress` and `make qualification` (this ticket changes quality comparison/denominator semantics); obtain independent review before the ticket's atomic commit.

### Milestone 3: A navigable evidence workspace

Goal: An editor can inspect task sources, candidate changes and their effects after environment teardown and process restart.

Acceptance Criteria

- Stable locators and bounded reads cover task-source attachments, baseline/candidate components, diffs, canonical run views, deliverables and authorized opaque application notes.
- A source file the solver never opened is still available; two candidate diffs link to their respective runs/outputs. Raw deliverables and rendered views retain source digests and renderer versions.
- Editor/selector/evaluator projections enforce server-owned scope. Selector view omits candidate lineage and decision notes by default; evaluator references and server-only raw data never leak into editor/selector views.
- Reads report missing, withheld, provisional and truncated content explicitly and remain stable after restart. Existing full local exports remain unchanged.

Checklist

- [ ] Inventory `eval/{store_evidence,show_json,store_message}.go`, `trajectory/{trajectory,document,render}.go`, `runtime/agent/artifact/artifact.go` and `runtime/agent/runtime/artifact_toolset.go`; do not widen the current-run artifact tools globally.
- [ ] Add proposed `eval/inspect/{views,read,artifacts}_test.go`, then implement the corresponding bounded manifest/read/projection contracts using canonical captures plus retained host attachments and candidate store content.
- [ ] Extend `eval/component_probe_test.go` to retain an unopened source, candidate diffs and an application decision note; verify byte identity and access rules after teardown and reopening stores in a new process.
- [ ] Include raw server-only data, hidden judge references and instruction-bearing task/tool content in projection tests. Assert structural authorization and exact inclusion/exclusion, not heuristic string filtering.
- [ ] Run `go test ./eval/inspect/... ./eval/... ./trajectory/...`; document locator lifetime, read limits, incomplete evidence and host redaction ownership.
- [ ] Freeze the diff; run `make lint`, `make test`, `make ci-integration`; obtain independent review before the ticket's atomic commit.

### Milestone 4: A complete agent tool journey with durable host execution

Goal: A controller can create, edit, submit and inspect a candidate using public tools, including recovery across host restart.

Acceptance Criteria

- Optional `eval/control` Go service, native tools and `eval/controlcli` adapter share validation, errors, authorization and evidence views. The adapter returns existing `evalcli.Extension` values; no new dispatcher or default tool installation is added.
- A scripted controller discovers an authorized baseline, creates its own draft, fixes an invalid edit, freezes, submits a probe, polls/cancels and reads its evidence without hidden fixture-side candidate setup.
- An explicit disk-backed host-executor fixture persists request/job identity before acknowledgement. Crashes around acceptance and completion do not duplicate execution; uncertain effects become interrupted rather than silently replayed.
- Cancellation remains requested until execution/finalization confirms canceled, failed or interrupted. Terminal-success races follow the design's serialized commit rule. Credentials, arbitrary filesystem roots and live-model permission cannot be enabled through tool inputs.

Checklist

- [ ] Read `.agents/skills/mcp-tool-design/SKILL.md` before freezing public tool schemas; specify canonical service input/output/error schemas and host executor ownership from the design.
- [ ] Add proposed `eval/control/{service,submission,toolset}_test.go` and `eval/controlcli/extension_test.go`, with type shells first; implement the service/native registration and proposed `eval/controlcli/extension.go` through the existing `eval/evalcli/extensions.go` API. Keep imports controlcli → control/evalcli, never evalcli → controlcli/control.
- [ ] Add proposed `eval/control/testfixture/durable_executor.go` plus external-package `eval/control/recovery_test.go` (`package control_test`) using a temporary on-disk request/job store and child-process crash injection before/after acceptance and completion persistence. Cover cancellation/terminal races and lost ownership without adding a framework scheduler. Name these cases `TestControlRecovery`/`TestControlCancellation`.
- [ ] Exercise native tool registration through proposed `integration_tests/fixtures/agent_features/experiment_control_test.go`. V1 proves native tools and Go/CLI equivalence; a generated MCP projection is application follow-up, so this milestone does not add a speculative MCP service or alter wire handling.
- [ ] Run `go test ./eval/control/... ./eval/controlcli/... ./eval/evalcli/...` and `make verify-agent-feature-fixture`; run `go test -race -shuffle=on -count=20 -timeout=15m ./eval/control -run '^(TestControlRecovery|TestControlCancellation)'`. Update `docs/agent_testing.md`, `docs/runtime.md` and repo skills; identify any confirmed external publishing source before a future release.
- [ ] Freeze the diff; run `make lint`, `make test`, `make ci-integration` and `make test-stress`; obtain independent review before the ticket's atomic commit.

### Milestone 5: Explicit reuse and two consumer-shaped proofs

Goal: Demonstrate the full contract with document-edit and graph-analysis fixtures, including newly authored child instructions and component reuse on a later task.

Acceptance Criteria

- Public create/copy operations import an explicit component selection plus its dependency closure from an authorized candidate. Collisions need explicit replacement; incompatible surfaces fail.
- Task B starts from its advertised baseline and explicitly imports a task A skill/helper. The tool-only controller performs the copy; fixture code cannot prepopulate it. Task facts remain distinct and application logic revalidates them.
- A child-role candidate places its authored instruction bytes in the actual child model request and changes observable child behavior. Unsupported bindings fail before dispatch.
- Luke-shaped document and AutoK-shaped graph fixtures support both a single revision and separate proposals plus composition without changing framework code. Each composition is executed before claims about its effects.
- The review maps all eight Google component classes to supported host behavior or an explicit unsupported result. No framework-owned proposal/selection/reflection policy is added.

Checklist

- [ ] Add proposed `eval/candidate/reuse_test.go` and `eval/control/copy_test.go`, then implement dependency-aware import/copy with source provenance in the candidate store and shared service/tool contract. Preserve existing `Parent` semantics.
- [ ] Add proposed `integration_tests/fixtures/agent_features/experiment_support_test.go` and fixture-local resources. Reuse the public helper binding and add approved child registration with candidate-bound instructions. Add design declarations only where required and regenerate with `make regen-agent-feature-fixture`; never edit `gen/` manually.
- [ ] Write two scripted controller scenarios: one revision; independent proposals followed by explicit composition. Both execute through public tools, inspect retained outputs and perform task-B reuse.
- [ ] Reconcile fixture bindings with the exact Luke and AutoK source paths in the inventory. Record application-owned prompt/schema binding, environment reset and adoption work in the handoff; do not modify either consumer repository.
- [ ] Run `go test ./eval/candidate/... ./eval/control/...` and `make verify-agent-feature-fixture`; update `docs/agent_testing.md` with runnable wiring and mark controller scripts as examples, not framework policy.
- [ ] Freeze the diff; select pinned dependencies with `make loom-remote` and `make selvedge-remote`, verify status, and run `make ci` once for final generation/build/lint/unit/integration/Docker proof. No qualification behavior changes are in scope, so `make qualification` is not required.
- [ ] Obtain independent review against the frozen contract, make the ticket's atomic commit with mandatory hooks, and hand off proof artifacts and application follow-ups. No push/release/live experiment is implied.

## Deferred work

Unified controller/preparation/trial/judge usage accounting, pricing and cross-process spend enforcement need a separate contract. Existing run usage remains available. Production Luke/AutoK adapters, sandbox backends, library indexing/retrieval, learned-note formats, domain metrics and adoption decisions stay application-owned. These are not acceptance conditions for the framework support above.
