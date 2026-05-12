# Runtime Package Boundary Refactor

The `runtime/agent/runtime` package (107 Go files) concentrates too many concerns in three broad files: `runtime_options.go` (632 lines), `activities.go` (640 lines), and `workflow_support.go` (646 lines). The chosen design is to land three behavior-preserving file splits (`CR-001a/b/c`) in order, then three follow-up milestones that tighten real contracts: explicit hook sink criticality, a single policy-application boundary, and explicit dispatch modes on `ToolsetRegistration`. No package re-org, no API changes, no cross-cutting abstractions introduced ahead of the file splits. All verification runs from `/Users/luca/code/loom-mono/loom-mcp` using `make lint`, `make test`, and `make itest`; any red build blocks milestone exit.

## Milestones

### Milestone 1: CR-001a — Split `runtime_options.go`

Goal: `runtime/agent/runtime/runtime_options.go` no longer mixes run-option APIs, runtime construction, dependency defaults, subscriber installation, and model registration; each concern lives in its own file with no behavior change.

Acceptance Criteria

- `runtime/agent/runtime/runtime_options.go` is either deleted or reduced to only `RunOption` setters (`WithRunID`, `WithLabels`, `WithTurnID`, `WithMetadata`, `WithTaskQueue`, `WithMemo`, `WithSearchAttributes`, `WithWorkflowOptions`, `WithTiming`, `WithPerTurnMaxToolCalls`, `WithRunMaxToolCalls`, `WithRunMaxConsecutiveFailedToolCalls`, `WithRunTimeBudget`, `WithRunFinalizerGrace`, `WithRunInterruptsAllowed`, `WithRestrictToTool`, `WithAllowedTags`, `WithDeniedTags`) and the `MissingFieldsAction` type / `defaultRetriedActivityPolicy` helper.
- `newFromOptions`, the `resolveRuntime*` / `resolveRunEventStore` / `resolveSessionStore` helpers, and every `RuntimeOption` constructor and runtime-scope factory (`New`, `WithEngine`, `WithHookActivityTimeout`, `WithMemoryStore`, `WithPromptStore`, `WithSessionStore`, `WithRunEventStore`, `WithPolicy`, `WithStream`, `WithHooks`, `WithLogger`, `WithMetrics`, `WithTracer`, `WithToolConfirmation`, `WithHintOverrides`, `WithWorker`, `WithQueue`) live in `runtime_bootstrap.go`.
- `installRuntimeSubscribers`, `registerSessionSubscriber`, `registerMemorySubscriber`, and the session-event helpers (`sessionRunMetaFromEvent`, `sessionRunCompletedStatus`, `sessionRunCompletedMetadata`) live in `runtime_subscribers.go`.
- `RegisterModel`, `ModelClient`, the `BedrockConfig` type, and `NewBedrockModelClient` all live in `runtime_models.go` (the Bedrock type and its constructor stay together).
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Add a seam test in `runtime/agent/runtime/` covering `newFromOptions` + `installRuntimeSubscribers` wiring (session and memory subscribers register on the returned bus) before moving code.
- [ ] Create `runtime/agent/runtime/run_options.go` and move the `RunOption` setters and `defaultRetriedActivityPolicy` into it.
- [ ] Create `runtime/agent/runtime/runtime_bootstrap.go` and move `newFromOptions`, the `resolveRuntime*` / `resolveRunEventStore` / `resolveSessionStore` helpers, and every `RuntimeOption` constructor listed in the acceptance criteria (`New`, `WithEngine`, `WithHookActivityTimeout`, `WithMemoryStore`, `WithPromptStore`, `WithSessionStore`, `WithRunEventStore`, `WithPolicy`, `WithStream`, `WithHooks`, `WithLogger`, `WithMetrics`, `WithTracer`, `WithToolConfirmation`, `WithHintOverrides`, `WithWorker`, `WithQueue`) into it.
- [ ] Create `runtime/agent/runtime/runtime_subscribers.go` and move `installRuntimeSubscribers`, `registerSessionSubscriber`, `registerMemorySubscriber`, and the `sessionRunMetaFromEvent` / `sessionRunCompletedStatus` / `sessionRunCompletedMetadata` helpers into it.
- [ ] Create `runtime/agent/runtime/runtime_models.go` and move `RegisterModel`, `ModelClient`, the `BedrockConfig` type, and `NewBedrockModelClient` into it.
- [ ] Reduce or delete `runtime_options.go` so only run-input option setters remain.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.
- [ ] Commit as one behavior-preserving change titled `refactor(runtime): split runtime_options.go (CR-001a)`.

### Milestone 2: CR-001b — Split `activities.go`

Goal: Planner activities, tool activity execution, retry-hint synthesis, and codec helpers live in separate files in `runtime/agent/runtime/`, with no behavior change to activity registration or error shapes.

Acceptance Criteria

- `runtime/agent/runtime/activities.go` is either deleted or reduced to activity registration wiring only.
- `PlanStartActivity`, `PlanResumeActivity`, `planStart`, `planResume`, `plannerContext`, `normalizeTranscriptRawJSON`, and `normalizeAnyRawMessage` live in `planner_activities.go`.
- `ExecuteToolActivity`, `applyToolActivityTelemetry`, `newToolActivityOutput`, `prepareToolActivity`, `validateToolActivityRequest`, `resolveToolsetRegistration`, and `adaptAndValidateToolPayload` live in `tool_activity.go`.
- Retry-hint construction (`toolDecodeErrorOutput`, `buildRetryHintFromValidation`, `validationIssues`, `validationDescriptions`, `collectValidationFields`, `buildValidationRetryQuestion`, `validationQuestionLabel`, `buildRetryHintFromDecodeError`, `buildRetryHintFromAgentToolRequestError`) lives in `tool_retry_hints.go` and keeps `runtime/agent/runtime/activities_retryhint_test.go` green unchanged.
- Codec helpers (`marshalToolValue`, `unmarshalToolValue`, `toolCodec`) live in `tool_codecs.go`.
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Extend `runtime/agent/runtime/activities_retryhint_test.go` with two new top-level tests before moving code: `TestBuildRetryHintFromDecodeError` (asserting a JSON syntax error produces a `planner.RetryHint` whose reason is the decode-failure path) and `TestBuildRetryHintFromAgentToolRequestError` (asserting an agent-tool request-validation error produces a `planner.RetryHint` with the expected fields). The existing `buildRetryHintFromValidation` coverage stays as-is.
- [ ] Create `runtime/agent/runtime/planner_activities.go` and move the planner activity entrypoints and `plannerContext` / transcript normalization helpers.
- [ ] Create `runtime/agent/runtime/tool_activity.go` and move `ExecuteToolActivity` and its preparation / validation / resolution / payload adaptation helpers.
- [ ] Create `runtime/agent/runtime/tool_retry_hints.go` and move all validation/decode/agent-tool retry-hint helpers.
- [ ] Create `runtime/agent/runtime/tool_codecs.go` and move `marshalToolValue`, `unmarshalToolValue`, and `toolCodec`.
- [ ] Reduce or delete `activities.go`; keep activity registration shape identical.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.
- [ ] Commit as `refactor(runtime): split activities.go (CR-001b)`.

### Milestone 3: CR-001c — Split `workflow_support.go`

Goal: The workflow loop reads as phase orchestration by splitting `workflow_support.go` into one file per lifecycle concern (finalize, interrupts, clarification, plan-activity support), with no behavior change.

Acceptance Criteria

- `runtime/agent/runtime/workflow_support.go` is either deleted or reduced to a thin phase dispatcher.
- `finalizeWithPlanner`, `runFinalizationPlan`, `validateFinalizePlanOutput`, `publishFinalizeOutput`, `buildFinalizedRunOutput`, `prepareFinalizePlan`, `buildFinalizePlanRequest`, `finalPlannerMessage`, `clonePlannerNotes`, `publishFinalizingPhase`, `finalizationHint`, `prepareFinalizationMessages`, `appendSyntheticToolResultsForFinalize`, `publishFinalizeTransition`, `finalizationReasonText`, and `publishFinalizationAssistantMessage` live in `workflow_finalize.go`.
- `handleInterrupts`, `awaitInterruptResume`, `handleResumeWaitError`, `applyResumeMessages`, `resolveResumeActivityOptions`, `publishPauseEvent`, `publishResumeReason`, and `publishResumed` live in `workflow_interrupts.go`.
- `handleMissingFieldsPolicy`, `firstMissingFieldsHint`, `awaitMissingFieldClarification`, `publishMissingFieldAwaitClarification`, `waitForClarificationAnswer`, `publishClarificationError`, `appendClarificationAnswer`, and `publishClarificationResumed` live in `workflow_clarification.go`.
- `runPlanActivity`, `capPlanActivityOptions`, `validatePlanActivityOutput`, `logPlanActivityResult`, and `publishPlannerNotes` live in `workflow_plan_activity.go`.
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Add a new test `TestHandleInterruptsAppliesResumeMessages` in `runtime/agent/runtime/pause_resume_timeout_test.go` that exercises `handleInterrupts` → `awaitInterruptResume` → `applyResumeMessages` end-to-end with a resume request carrying messages, asserting the resulting `PlanInput.Messages` contain the appended resume content. Land this before moving code.
- [ ] Create `workflow_finalize.go` and move finalization and finalize-publication helpers.
- [ ] Create `workflow_interrupts.go` and move interrupt/resume/pause helpers.
- [ ] Create `workflow_clarification.go` and move missing-field clarification flow helpers.
- [ ] Create `workflow_plan_activity.go` and move plan-activity execution + validation + logging helpers.
- [ ] Reduce or delete `workflow_support.go`.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.
- [ ] Commit as `refactor(runtime): split workflow_support.go (CR-001c)`.

### Milestone 4: Explicit Hook Sink Criticality

Goal: Non-critical hook subscribers (stream, session projection, memory projection) cannot fail an agent run, while audit/runlog sinks remain fail-fast.

Acceptance Criteria

- `runtime/agent/runtime/runtime_hook_helpers.go` exposes two registration modes — `critical` and `best_effort` — and the session/memory projection subscribers in `runtime/agent/runtime/runtime_subscribers.go` (post-CR-001a) register as `best_effort`.
- A test in `runtime/agent/runtime/` proves that a best-effort subscriber returning an error does not propagate out of the workflow run, and a separate test proves a critical subscriber's error still fails the run fast.
- Every current caller of `publishHookErr` (`planner_events.go:125`, `run_completion.go:118`, `run_completion.go:223`, `run_completion.go:250`, `one_shot_run.go:74`, `one_shot_run.go:85`, `workflow.go:137`, and the in-package callers at `runtime_hook_helpers.go:90` and `runtime_hook_helpers.go:114`) is reviewed and either keeps the critical path or is switched to best-effort, with the classification recorded in the commit body. The authoritative list is whatever `rg -n "publishHookErr\(" runtime/agent/runtime/ --glob '!*_test.go'` returns at the time the commit lands.
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Add a failing test that registers a best-effort subscriber returning an error and asserts the run completes successfully.
- [ ] Add a failing test asserting a critical subscriber error aborts the run.
- [ ] Introduce `critical` / `best_effort` modes in `runtime_hook_helpers.go` and route best-effort failures through the runtime logger + tracer instead of `publishHookErr`.
- [ ] Switch session-projection and memory-projection subscriber registration to `best_effort`; leave audit/runlog paths critical.
- [ ] Audit every call site of `publishHookErr` listed in the acceptance criteria above and record the chosen mode for each in the commit body.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.

### Milestone 5: Single Policy-Application Boundary

Goal: The workflow loop consumes one `policyApplicationResult` value from `runtime/agent/runtime/workflow_policy.go` instead of coordinating `applyRuntimePolicy`, `capAllowedCalls`, label plumbing, and policy-event publication separately.

Acceptance Criteria

- `workflow_policy.go` exports a single entrypoint that returns `{allowedCalls, caps, labels, events}` and no other `runtime/agent/runtime/*.go` file calls `applyRuntimePolicy` or `capAllowedCalls` directly.
- Existing retry-hint, cap, and label behavior is unchanged: the pre-existing policy tests in `runtime/agent/runtime/` pass without modification to their assertions (only call-site updates allowed).
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Run `rg -n "applyRuntimePolicy\(|capAllowedCalls\(" runtime/agent/runtime/ --glob '!*_test.go'` and paste the full output under a `Policy Boundary Audit` heading in the commit body; the migration must remove every listed non-test call site.
- [ ] Introduce the `policyApplicationResult` return type in `workflow_policy.go` and a single entrypoint that composes the current helpers.
- [ ] Migrate every listed caller to the new entrypoint; keep the old helpers unexported or delete them if unused.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.

### Milestone 6: Explicit Dispatch Modes on `ToolsetRegistration`

Goal: Dispatch code switches on one explicit runtime-owned mode (`activity`, `inline`, `agent_child`) instead of probing `ToolsetRegistration.Inline` and `ToolsetRegistration.AgentTool`.

Acceptance Criteria

- `ToolsetRegistration` (in `runtime/agent/runtime/runtime.go`) exposes a single dispatch-mode field; `runtime/agent/runtime/tool_calls_dispatch.go` branches only on that field.
- `runtime/agent/runtime/runtime_registration.go` sets the mode at registration time for each of: activity-executed toolsets, inline-executed toolsets, and agent-as-tool registrations.
- The agent-as-tool ordering tests (`execute_tool_calls_agent_child_order_test.go`, `execute_tool_calls_activity_order_test.go`, `execute_tool_calls_mixed_batch_test.go`) stay green unchanged.
- `make lint`, `make test`, and `make itest` are green from `/Users/luca/code/loom-mono/loom-mcp`.

Checklist

- [ ] Add the dispatch-mode field/type to `ToolsetRegistration` in `runtime.go`.
- [ ] Update `runtime_registration.go` to set the mode on every registration path.
- [ ] Rewrite `tool_calls_dispatch.go` to switch on the new mode and stop reading `Inline` / `AgentTool`.
- [ ] Remove the now-unused `Inline` / `AgentTool` fields (or mark them private) once every non-test reader is migrated.
- [ ] Run `cd /Users/luca/code/loom-mono/loom-mcp && make lint && make test && make itest`.
