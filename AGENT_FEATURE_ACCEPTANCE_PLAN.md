# Agent Feature Acceptance Plan

Refactor confidence around the existing generated `integration_tests/fixtures/agent_features` fixture. The fixture already defines `features.coordinator`, runtime-backed toolsets `features.artifacts`, `features.memory`, `features.skills`, executor-backed toolset `features.workflow`, generated helpers `RegisterCoordinatorAgent`, `RegisterUsedToolsets`, and `WithWorkflowExecutor`, plus an initial untracked `agent_features_test.go`; the work is to extend that acceptance layer and make small fixture adjustments that prove the public DSL-to-generated-code-to-runtime path without overfitting implementation internals.

## Status

- 2026-07-06 — Plan created for review.
- 2026-07-06 — External review found the first draft stale because `integration_tests/fixtures/agent_features` already exists as untracked work, `go.work` already includes it, `TestArtifactRefsRejectMismatchedToolCallID` already exists, and the current graph has no distinct `revise` branch.
- 2026-07-06 — Plan reconciled to preserve the existing fixture, use current generated owner names, add a real `revise` branch, and drive typed-input acceptance through generated registration plus `coordinator.NewClient(rt).Start(...)`.
- 2026-07-06 — Focused review found the reconciled draft still said `Run(...)` where typed input needs `Start(...)`, risked importing `workflow.Revise` before regeneration, and missed existing `agent_features_test.go`; plan updated to extend that file, use `Start(...)`, and assert pre-regeneration `tools.Ident("workflow.revise")`.
- 2026-07-06 — Milestone 1 red test added. `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1` fails in `TestGeneratedFeatureRunBranchesToReviseWhenApprovalFalse` because observed tools are `workflow.review`, `workflow.draft`, `workflow.retry`, `workflow.retry`, `workflow.publish`, with no `workflow.revise`.
- 2026-07-06 — Milestone 2 reconciled the fixture DSL with `workflow.revise`, regenerated `integration_tests/fixtures/agent_features`, and added `regen-agent-feature-fixture` plus `verify-agent-feature-fixture`. `make loom-status` initially reported remote mode for root, fixture, and quickstart; `make loom-local` required restoring the canonical `/Users/luca/code/loom-mono` symlink to the existing local checkout. The focused registration proof passes after returning to remote mode.
- 2026-07-06 — Milestone 3 expanded the acceptance harness into `acceptance_helpers_test.go`, fixed branch target dependency construction so `publish` and `revise` both depend on `route`, fixed current-run memory to use the store even when an indexed searcher is configured, and proved `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1`, `go test ./dsl -run TestWorkflowBranchTargetsShareBranchDependency -count=1`, and `go test ./runtime/agent/runtime -run TestMemoryToolsetCurrentRunUsesStoreWhenSearcherConfigured -count=1`.

## Milestones

### Milestone 1: Red Acceptance Contract

Toc: Red tests

Goal: Add failing acceptance tests against the current generated fixture before changing the fixture behavior.

Acceptance Criteria

- The executing agent has recited the workflow on the record before any code edits.
- `integration_tests/fixtures/agent_features/agent_features_test.go` contains tests that call `coordinator.RegisterCoordinatorAgent`, `coordinator.RegisterUsedToolsets`, `coordinator.WithWorkflowExecutor`, and `coordinator.NewClient(rt).Start(...)` for typed-input runs.
- `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1` runs after the tests are written and fails on missing behavior rather than missing imports.

Checklist

- [x] Read this plan end-to-end, read the linked skill at `/Users/luca/code/skills/skills/design-docs-execution-plans/SKILL.md`, then recite the workflow you will follow — milestone order, exit criteria, named commands in execution order, test-first rule, peer-review gate, commit/push handoff, and any inherited repo constraints. Do not edit code before this recital is on the record.
- [x] Read `.agents/skills/loom-mcp/SKILL.md`, `AGENTS.md`, `Makefile`, `go.work`, `integration_tests/fixtures/agent_features/design/design.go`, `integration_tests/fixtures/agent_features/gen/features/agents/coordinator/registry.go`, `integration_tests/fixtures/agent_features/gen/features/agents/coordinator/agent.go`, `integration_tests/fixtures/agent_features/gen/features/toolsets/workflow/specs.go`, `runtime/agent/runtime/artifact_test.go`, `runtime/agent/runtime/memory_toolset_test.go`, `runtime/agent/runtime/skill_toolset_test.go`, `runtime/agent/runtime/runtime_interceptors_test.go`, `runtime/agent/runtime/runtime_interceptors_m3_test.go`, `runtime/agent/runtime/typed_input_test.go`, `runtime/agent/planner/workflow_graph_test.go`, and `runtime/agent/debug/server_test.go`.
- [x] Run `git status --short --branch` and record in the conversation that `integration_tests/fixtures/agent_features` and `go.work` are existing local work to preserve.
- [x] Extend existing file `integration_tests/fixtures/agent_features/agent_features_test.go` with `TestGeneratedFeatureFixtureRegistersRuntimeSurface`, `TestGeneratedFeatureRunPublishesAwaitAndResumesWithTypedInput`, `TestGeneratedFeatureRunPersistsArtifactsMemorySkillsAndDebugState`, `TestGeneratedFeatureRunAppliesNamedInterceptorsAndRetryReflect`, and `TestGeneratedFeatureRunBranchesToReviseWhenApprovalFalse`.
- [x] In `TestGeneratedFeatureFixtureRegistersRuntimeSurface`, assert registration through `coordinator.RegisterCoordinatorAgent` and `coordinator.RegisterUsedToolsets` exposes toolsets `features.artifacts`, `features.memory`, `features.skills`, `features.workflow` and tool names `workflow.draft`, `workflow.review`, `workflow.retry`, `workflow.publish`.
- [x] In `TestGeneratedFeatureRunPublishesAwaitAndResumesWithTypedInput`, start a session with `coordinator.NewClient(rt).Start(...)`, observe `hooks.AwaitTypedInput`, call `rt.ProvideTypedInput(...)`, wait for the handle, and assert the final message comes from the generated graph.
- [ ] In `TestGeneratedFeatureRunPersistsArtifactsMemorySkillsAndDebugState`, use the same generated runtime setup to execute a run whose `workflow.publish` returns an artifact, then assert artifact list/load, memory current-run/indexed lookup, skill list/load, and debug endpoints read emitted state.
- [ ] In `TestGeneratedFeatureRunAppliesNamedInterceptorsAndRetryReflect`, register runtime-level interceptors with `agentsruntime.WithInterceptors`, register named `audit` with `agentsruntime.WithNamedInterceptors`, make `workflow.retry` fail once, and assert interceptor order plus planner-visible `RetryHint`.
- [x] In `TestGeneratedFeatureRunBranchesToReviseWhenApprovalFalse`, provide typed input `{"approved":false}` and assert the workflow executor receives `tools.Ident("workflow.revise")` instead of `workflow.Publish`; do not import or reference `workflow.Revise` before Milestone 2 regenerates that constant.
- [x] Run `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1` and append the concrete red failure to this plan status.

### Milestone 2: Fixture Contract Reconciliation

Toc: Fixture

Goal: Adjust the existing fixture design and generation targets so the red tests compile and exercise a meaningful graph.

Acceptance Criteria

- `integration_tests/fixtures/agent_features/design/design.go` preserves API `agentfeatures`, service `features`, agent `coordinator`, and toolsets `ArtifactTools`, `MemoryTools`, `SkillTools`, and `workflow`.
- Generated files under `integration_tests/fixtures/agent_features/gen/features/agents/coordinator` show `NamedInterceptors: []string{"audit"}`, `PreloadMemory`, `NewRetryAndReflectInterceptor`, runtime-backed registrations for artifact, memory, and skill toolsets, and graph nodes for parallel draft/review, join, typed input, loop, branch, publish, and revise.
- `Makefile` has `.PHONY` entries plus `regen-agent-feature-fixture` and `verify-agent-feature-fixture`, and `verify-mcp-local` runs assistant fixture tests, agent feature fixture tests, and `./integration_tests/framework`.

Checklist

- [x] Update existing file `integration_tests/fixtures/agent_features/design/design.go` to add `workflow.revise` with `Args(EmptyPayload)` and `Return(StatusResult)`.
- [x] Update existing file `integration_tests/fixtures/agent_features/design/design.go` so `Branch("route", "approval", Case("$.approved", "true", "publish"), BranchDefault("revise"))` routes false approvals to `revise`.
- [x] Preserve existing `Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))`, `Toolset("memory", FromMemory(MemoryMaxResults(20)))`, `Toolset("skills", FromSkills(".agents/skills", SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall)))`, `Interceptors("audit")`, `RetryAndReflect(MaxRetries(1), ErrorIfRetryExceeded(true))`, and `PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))`.
- [x] Preserve existing `go.work` use entry `./integration_tests/fixtures/agent_features` and do not remove unrelated local runtime changes.
- [x] Add `regen-agent-feature-fixture` to `Makefile` with `cd ./integration_tests/fixtures/agent_features && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/agentfeatures/design`.
- [x] Add `verify-agent-feature-fixture` to `Makefile` with `go test -C ./integration_tests/fixtures/agent_features ./... -count=1`.
- [x] Update `verify-mcp-local` in `Makefile` so it runs `go test -C ./integration_tests/fixtures/assistant ./... -count=1`, `go test -C ./integration_tests/fixtures/agent_features ./... -count=1`, and `go test ./integration_tests/framework -count=1`.
- [x] Run `make loom-status` and record whether local or remote Loom mode is active.
- [x] Run `make loom-local`.
- [x] Run `make regen-agent-feature-fixture`.
- [x] Run `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGeneratedFeatureFixtureRegistersRuntimeSurface -count=1`.

### Milestone 3: Runtime Acceptance Harness

Toc: Harness

Goal: Make the generated fixture tests pass through the generated client and public runtime stores.

Acceptance Criteria

- `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1` passes.
- The tests prove a real generated run by using `engine/inmem.New`, `agentsruntime.New`, `coordinator.RegisterCoordinatorAgent`, `coordinator.RegisterUsedToolsets`, `coordinator.WithWorkflowExecutor`, `coordinator.NewClient(rt).Start(...)`, `rt.ProvideTypedInput(...)`, and `handle.Wait(...)`.
- Debug assertions read `/runs/{run_id}/await`, `/runs/{run_id}/memory`, `/runs/{run_id}/artifacts`, and `/runs/{run_id}/workflow` from `runtime/agent/debug.NewServer` against the runtime that executed the generated run.

Checklist

- [x] Create new file `integration_tests/fixtures/agent_features/acceptance_helpers_test.go` by moving reusable helper types from existing `agent_features_test.go` and adding helpers that construct `engineinmem.New`, in-memory session store, runlog store, memory store, memory searcher, artifact store, hook recorder, stream recorder, runtime interceptors, named `audit` interceptors, and the generated coordinator registrations.
- [x] Implement a `features.workflow` executor in `acceptance_helpers_test.go` that records tool call order and returns deterministic results for `workflow.draft`, `workflow.review`, `workflow.retry`, `workflow.publish`, and `workflow.revise`.
- [x] Make the `workflow.retry` executor fail on its first call with a structured tool error and succeed on the second call so retry-and-reflect behavior is observable.
- [x] Make the `workflow.publish` executor return an artifact body through `planner.ToolResult.Artifacts` so artifact materialization is observed through the generated run.
- [x] Seed current-run memory without session labels before `PlanStartActivity` runs and assert generated memory preload reaches the planner through the registered agent path.
- [x] Seed `.agents/skills/release-check/SKILL.md` frontmatter in the fixture and assert generated `features.skills` tools expose `allowed_tools`, `preload`, `reload`, `preloaded`, and `reloaded`.
- [x] Assert artifact list output omits artifact body text and artifact load output returns bounded content for the artifact produced by the generated run.
- [x] Assert memory current-run lookup ignores `SessionID` for run-scoped events and indexed lookup calls the configured `MemorySearcher` with `AgentID`, `RunID`, `SessionID`, labels, event types, and limit.
- [x] Assert runtime-level interceptors run before the named `audit` interceptor resolved from `RunPolicy.NamedInterceptors`.
- [x] Assert `TestGeneratedFeatureRunBranchesToReviseWhenApprovalFalse` observes `workflow.revise` and does not observe `workflow.publish`.
- [x] Run `go test -C ./integration_tests/fixtures/agent_features ./... -run TestGenerated -count=1`.

### Milestone 4: Layer Boundaries Stay Sharp

Toc: Layers

Goal: Keep existing unit and golden tests as local-contract tests while the fixture owns cross-layer confidence.

Acceptance Criteria

- Existing package tests still own DSL validation, expression validation, generated-shape assertions, planner graph scheduling, runtime toolsets, interceptors, typed input helpers, MCP skills, and debug endpoint units.
- `TestArtifactRefsRejectMismatchedToolCallID` remains in `runtime/agent/runtime/artifact_test.go` and passes.
- The acceptance fixture tests do not duplicate generated string assertions already owned by `codegen/agent/tests/golden_*`.

Checklist

- [ ] Confirm `runtime/agent/runtime/artifact_test.go` contains existing `TestArtifactRefsRejectMismatchedToolCallID` and keep it as the artifact-ref scope regression.
- [ ] Keep `dsl/artifact_test.go`, `dsl/memory_test.go`, `dsl/interceptors_test.go`, `dsl/workflow_graph_test.go`, `expr/agent/artifact_test.go`, `expr/agent/memory_test.go`, `expr/agent/interceptors_test.go`, and `expr/agent/workflow_graph_test.go` focused on DSL and validation behavior.
- [ ] Keep `codegen/agent/tests/golden_artifacts_toolset_test.go`, `codegen/agent/tests/golden_memory_toolset_test.go`, `codegen/agent/tests/golden_skills_toolset_test.go`, `codegen/agent/tests/golden_workflow_graph_test.go`, and `codegen/agent/tests/golden_run_policy_test.go` focused on generated-shape assertions.
- [ ] Keep `runtime/agent/planner/workflow_graph_test.go`, `runtime/agent/runtime/memory_toolset_test.go`, `runtime/agent/runtime/skill_toolset_test.go`, `runtime/agent/runtime/runtime_interceptors_test.go`, `runtime/agent/runtime/runtime_interceptors_m3_test.go`, `runtime/agent/runtime/typed_input_test.go`, and `runtime/agent/debug/server_test.go` as package-level behavior tests.
- [ ] Run `go test ./dsl ./expr/agent ./codegen/agent/tests ./runtime/agent/planner ./runtime/agent/runtime ./runtime/mcp/skills ./runtime/agent/debug -count=1`.

### Milestone 5: Documentation And Skill Contract

Toc: Docs

Goal: Document `agent_features` as the cross-layer acceptance fixture for model-facing agent runtime features.

Acceptance Criteria

- `docs/runtime.md` names `integration_tests/fixtures/agent_features` as the acceptance proof for artifact, memory, model-facing skills, interceptors, retry-and-reflect, workflow graph, typed input, and debug behavior.
- `.agents/skills/loom-mcp/SKILL.md` tells future agents to extend `integration_tests/fixtures/agent_features` when a feature crosses DSL, codegen, generated registration, and runtime behavior.
- This plan status records external review findings as applied or intentionally rejected with a reason.

Checklist

- [ ] Update existing file `docs/runtime.md` near the runtime feature sections to point new cross-layer agent feature changes at `integration_tests/fixtures/agent_features`.
- [ ] Update existing file `.agents/skills/loom-mcp/SKILL.md` so future DSL, codegen, runtime, model-facing skill, artifact, memory, interceptor, workflow, and typed-input work includes the generated acceptance fixture when behavior crosses layers.
- [ ] Append a dated `AGENT_FEATURE_ACCEPTANCE_PLAN.md` status entry listing the external review findings as applied.
- [ ] Run `python3 /Users/luca/code/skills/skills/design-docs-execution-plans/render_plan.py AGENT_FEATURE_ACCEPTANCE_PLAN.md`.

### Milestone 6: Full Verification And Handoff

Toc: Handoff

Goal: Prove the new confidence layer passes in the repo verification ladder and leave a clean delivery state.

Acceptance Criteria

- `make verify-mcp-local`, `make lint`, `make test`, and `make itest` pass from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- `make loom-remote` has restored CI-facing Loom mode before final verification.
- `git status --short --branch` shows only intentional plan, fixture, generated, docs, skill, Makefile, go.work, and existing runtime artifact-ref changes before handoff.

Checklist

- [ ] Run `make loom-remote`.
- [ ] Run `make verify-mcp-local`.
- [ ] Run `make lint`.
- [ ] Run `make test`.
- [ ] Run `make itest`.
- [ ] Run `git status --short --branch`.
- [ ] Report the final changed-file set, verification results, existing local runtime artifact-ref changes, and commit boundary to the user.
- [ ] Leave commit and push for an explicit user request, with all intended files ready for scoped staging.
