# Test Harness Hardening Plan

## Goal

Make the repository's verification ladder prove the contracts most likely to
fail in production: MCP wire behavior, generated-code correctness, workflow
replay and retry behavior, concurrent lifecycle handling, provider adaptation,
and persistence across process replacement.

The harness must fail closed. A named verification target must not claim to run
a surface that is absent, skipped, stale, or outside the Go module being tested.

## Progress (2026-09-01)

The original phases 1 and 3 through 6 are implemented. A current-tree audit
reprioritized the remaining work around observed defect yield and production
risk. The unit and integration ladders are green, but the largest confidence
gaps are real Temporal execution, cross-layer runtime transitions, provider
stream state machines, persistence upgrades, and malformed dynamic inputs.

## Reprioritized Backlog

### Priority 1: Isolate Fast Unit And Docker Gates

Status: implemented and locally verified.

1. Make `make test` explicitly container-free so targeted and full unit runs do
   not start Redis or Mongo.
2. Add one fail-closed `make test-docker` owner for Mongo, Pulse, and registry
   contracts, and run it exactly once from `make ci`.
3. Preserve `docker-cover.out` and enforce separate Mongo, Pulse, and registry
   floors rather than printing only the aggregate.
4. Keep scheduled stress capable of selecting Docker tests explicitly while
   requiring them only when `LOOM_MCP_REQUIRE_DOCKER_TESTS=1` is set.

Exit criteria:

- ordinary unit and targeted package tests start no containers;
- `make test-docker` fails instead of skipping when Docker is unavailable;
- local and hosted `make ci` run every Docker contract exactly once;
- each Docker owner must satisfy its own coverage floor.

Implementation evidence:

- targeted registry, Mongo infrastructure, and Pulse unit tests pass with an
  intentionally invalid Docker endpoint because no container setup is reached;
- `make test` completes in about 24 seconds on the audit host instead of waiting
  for the 72-second Redis-backed registry suite;
- `make test-docker` runs Mongo, Pulse, and registry with `-race`, shuffle, and
  fail-closed selection, then enforces 80%, 80%, and 75% owner floors;
- a required Mongo contract fails immediately rather than skipping when given
  an invalid Docker endpoint;
- hosted CI delegates Docker execution to the single `make ci` call and still
  preserves `docker-cover.out`.

### Priority 2: Run Generated Acceptance Against Real Temporal

Status: foundation implemented and locally verified. Priority 3 owns the
remaining generated child/signal boundary matrix, and Priority 5 owns restart
proof against real persistent stores.

1. Extract an engine-neutral generated acceptance fixture.
2. Run the same coordinator scenarios against the in-memory engine and a pinned
   real Temporal development server.
3. Prove late activity retry, signal/timeout races, cancellation, child-run
   identity, active-time budget pauses, replay, and worker replacement.
4. Prove repeated planner effects are observable while application-owned
   idempotency protection commits a durable effect once.

Implementation evidence:

- the generated coordinator await/resume scenario now shares one assertion
  helper between the in-memory engine and Temporal CLI `v1.6.1`;
- a one-second active-work budget still completes after a 1.2-second external
  typed-input wait, proving the wait pauses rather than consumes the budget;
- a real Temporal planner activity fails after its application effect, retries,
  exposes two attempts, and commits the effect once through a filesystem-backed
  application idempotency ledger rather than planner-local state;
- a generated workflow waiting for typed input survives worker shutdown,
  replays on a replacement worker, resumes through a second runtime, and
  preserves deduplicated runlog events plus terminal session state through a
  shared store backend; Priority 5 replaces this backend with real persistence;
- cancellation during the generated typed-input wait returns a workflow error
  and converges the durable run status to `canceled`.

Remaining dependent evidence:

- Priority 3 adds child-run identity and clarification, confirmation, external
  tool, pause/resume, timeout-boundary, and child cancellation scenarios to the
  generated design and executes them on this Temporal lane.
- Priority 5 repeats worker/process replacement with real persistence so the
  test does not retain session or runlog state through shared in-process stores.

### Priority 3: Expand Cross-Layer Generated Runtime Acceptance

Extend `integration_tests/fixtures/agent_features/design/design.go` with a child
agent and generated scenarios for agent-as-tool, confirmation, clarification,
external tool results, pause/resume, cancellation, and `TimeBudget`. Assert
generated registration, runtime output, runlog, session, hook, and stream
contracts together.

### Priority 4: Test Provider Streams As State Machines

Extend `testutil.ProviderStreamingConformance` beyond setup, receive failure,
rate limits, and terminal output. Require fragmented tool calls, text/thinking/
usage ordering, early EOF, partial cancellation, close errors, and interleaved
content indexes. Start with Bedrock, whose streaming branch coverage and event
grammar are the coldest high-churn provider surface.

### Priority 5: Test Persistence Upgrades And Concurrency Against Real Services

Pre-seed Mongo with legacy prompt, memory, runlog, and session records before
client construction. Prove fingerprint migration, mixed legacy/bucket ordering,
duplicate insertion, index conflicts, and session terminal-state races using
real driver semantics. Add Redis disruption and pending-delivery cases after
the Mongo upgrade contracts.

### Priority 6: Add Fuzzing At Dynamic Trust Boundaries

Add seed corpora for canonical JSON, hook envelopes, protected MCP request
state, model message codecs, provider tool fragments, and generated union
codecs. Run corpus seeds on pull requests and bounded fuzz time in the scheduled
stress workflow. Malformed inputs must return typed errors without panic, hang,
or silent coercion.

### Priority 7: Add Risk-Weighted Owner Floors

After behavioral tests land, add exact owner floors for
`runtime/agent/runtime`, Bedrock, Gemini, and each Docker-backed package. Do not
raise the global floor as a substitute for exercising the high-risk state
machines.

Phase 1 is implemented and verified:

- raw HTTP tests now cover initialization negotiation, unsupported and missing
  protocol versions, malformed JSON, unknown methods, pre-initialization calls,
  duplicate initialization, notification responses, and tool validation;
- `make itest` delegates to the same fixture/framework ladder as
  `make verify-mcp-local`, and every nested fixture module runs with `-race`;
- the integration README now describes only executable Go tests and supported
  settings;
- managed servers inherit an already-bound listener, readiness validates the
  mounted MCP transport, `/rpc` is appended exactly once, and generation/build
  processes inherit the test context;
- raw resource-policy denial and terminal SSE tool-error behavior are pinned;
- roots left by interrupted test processes are removed after 24 hours without
  touching fresh, unrelated, or non-directory entries.

Phase 3 now compiles generated nested types, unions, projections, no-payload
methods, reused toolsets, multi-toolset services, and each MCP-only surface.
This matrix found and fixed pointer conversion for top-level primitive payloads.

Phase 4 adds `make test-stress`. The target repeats race-enabled tests with
shuffle for the runtime, registry, Pulse, and generated MCP lifecycle owners.
A weekly CI workflow runs five repetitions and requires Docker-backed tests.

Phase 5 makes each provider declare and prove multimodal input, typed thinking,
exact token counting, and tool-name round trips. Streaming providers also prove
setup, receive, terminal, and receive-time rate-limit behavior. This matrix
found and fixed Anthropic receive-time rate-limit mapping and Bedrock
non-streaming reasoning translation. OpenAI now requests and returns typed
reasoning summaries.

Phase 6 is complete. `make test` enforces global and package-group coverage
floors. Generated, mock, and design packages do not affect the group floors.
CI produces a separate Docker-required report. The workflow uses the complete
`make ci` target, pinned protobuf tools, and the canonical Loom release pin.
Local and hosted CI now share `make verify-generated`, which snapshots the
current diff before regenerating every tracked surface. Loom mode changes also
preserve quickstart generator-only checksums, closing the ordering gap that let
local release verification miss stale `quickstart/go.sum` state.
The coverage-group parser is also verified with GNU Awk so Linux does not
discover dialect-specific failures after macOS passes. The registry
unregister/re-register failover test now waits for the peer to detach its old
ticker before creating the new registration epoch, while retaining the same
post-failover ping assertion.

Generated server middleware now converts untyped adapter errors into typed
JSON-RPC errors. Duplicate initialization and generated resource-policy errors
return `-32602`. Unknown handler errors return `-32603`. Already typed SDK errors
pass through. Unit tests cover every mapping branch. Raw HTTP tests cover the
generated wire behavior.

The MCP Go SDK v1.7.0 still serializes a call made before initialization with
JSON-RPC error code `0`. That gate runs before the SDK invokes server receiving
middleware, so correcting it locally would require duplicating transport logic
or rewriting response bodies. The raw harness accepts only the known `0` or the
expected future `-32602` on that upstream-owned path.

## Current Contract Inventory

### Verification entry points

- `Makefile` owns `make test`, `make test-docker`, `make itest`,
  `make verify-mcp-local`, fixture regeneration, linting, and the current global
  and owner coverage floors.
- `.github/workflows/ci.yml` owns the pull-request unit and integration jobs,
  Docker-required coverage, and generated-fixture drift detection.
- `.githooks/pre-commit` owns staged Go formatting and changed-file linting.
- `integration_tests/README.md` is the documented catalog of integration
  surfaces and commands.

Invariants:

1. Every documented test group must resolve to an executable package and test.
2. `make itest` must cross nested fixture module boundaries intentionally.
3. Generated fixtures must be regenerated from their design packages before
   freshness or behavior is claimed.
4. Required gates must fail when Docker-backed contracts cannot run in CI.

### MCP generated-server surface

- `integration_tests/fixtures/assistant/design/design.go` and
  `integration_tests/fixtures/assistant/progressive_discovery/design/design.go`
  are the source of truth.
- `codegen/mcp` owns generated `MCPAdapter`, `ToolsetRegistration`, and
  `SDKServer` behavior.
- `integration_tests/fixtures/assistant/gen/mcp_assistant` and
  `integration_tests/fixtures/assistant/progressive_discovery/gen/mcp_catalog`
  are generated outputs and must never be edited directly.
- `integration_tests/framework.Runner` owns managed server preparation,
  compilation, readiness, process cleanup, and official SDK connections.
- `integration_tests/framework/sdk_streamable_http_test.go` and assistant
  fixture tests own current valid-client and generated-server acceptance.

Invariants:

1. The official MCP Go SDK is the sole transport implementation.
2. Valid behavior is tested through the official SDK.
3. Invalid JSON-RPC and Streamable HTTP inputs are tested at the raw HTTP
   boundary because a conforming SDK cannot construct all invalid inputs.
4. Protocol errors, session rules, version negotiation, notification behavior,
   and terminal stream behavior are asserted explicitly.

### Agent runtime and workflow surface

- `integration_tests/fixtures/agent_features/design/design.go` is the generated
  acceptance design.
- Generated coordinator registration and workflow code under
  `integration_tests/fixtures/agent_features/gen` is generator-owned.
- `runtime/agent/runtime.Runtime` owns registration, plan/execute/resume,
  waits, events, sessions, child runs, and tool execution.
- `runtime/agent/engine/inmem` and `runtime/agent/engine/temporal.Engine` are the
  execution engines.
- `integration_tests/fixtures/agent_features/acceptance_helpers_test.go`
  currently constructs the acceptance runtime with the in-memory engine.

Invariants:

1. Engine-neutral runtime contracts produce the same observable result under
   in-memory and Temporal execution.
2. Temporal replay and activity retry can repeat planner calls and direct side
   effects; tests must prove stable idempotency boundaries.
3. Signals, cancellation, child workflow links, time-budget pauses, runlog
   events, and session ownership survive their documented lifecycle edges.

### Code generation surface

- `dsl` and `expr` own validation and evaluated design contracts.
- `codegen/agent` and `codegen/mcp` own generated source sections.
- `codegen/testhelpers.BuildAndGenerate` exercises production preparation and
  generation without compiling the resulting package graph.
- `codegen/agent.TestGeneratedAgentDesignsCompile` compiles a focused generated
  design matrix.
- Golden and structural assertions describe exact output but are not a
  substitute for compiling and executing generated code.

Invariants:

1. Every materially different generated type or ownership shape must compile.
2. Generated codecs and dispatchers must execute round trips for the shapes
   whose correctness cannot be established by compilation alone.
3. Repeated generation from the same evaluated design must be byte-stable.

### Provider and persistence surfaces

- `testutil.ProviderConformanceSuite` owns the provider-neutral model contract.
- Provider packages under `features/model` own SDK-specific fixtures.
- Mongo client packages own storage semantics; Docker-backed tests in
  `features/mongo/clientinfra` prove real-driver transactions and round trips.
- Pulse and registry integration tests own Redis-backed delivery, health, and
  replicated-catalog behavior.

Invariants:

1. Provider capabilities are reported truthfully and all applicable complete,
   streaming, tool, thinking, token, cancellation, error, and name-mapping
   contracts are tested.
2. Persistent runlog and session state, transcript memory, and live streams
   retain their distinct delivery guarantees.
3. CI must not silently skip required Redis or Mongo contracts.

## Phase 1: Restore Truthful MCP Integration Coverage

Priority: immediate.

1. Add raw Streamable HTTP contract tests under `integration_tests/framework`
   for the high-value negative cases removed during the official-SDK migration:
   initialization and version negotiation, calls before initialization,
   duplicate initialization, malformed JSON-RPC envelopes, missing/unknown
   methods, notifications without responses, invalid tool arguments, resource
   policy denial, and stream error termination.
2. Keep valid catalog, tool, resource, prompt, progress, elicitation, session,
   and cancellation flows in official-SDK tests.
3. Replace the obsolete YAML-scenario documentation with the actual Go test
   catalog. Remove empty `scenarios` and `tests` descriptions and the unused
   `MCP_CLI_TESTS` setting.
4. Run nested assistant and agent-feature fixture modules with `-race`.
5. Make `make verify-mcp-local` and `make itest` share one explicit set of
   fixture/framework commands so their coverage cannot drift.
6. Harden `framework.Runner`: validate an expected MCP readiness response,
   eliminate the release-before-bind port race, normalize external endpoints,
   bound generation/build commands, and clean stale temporary roots.

Proof:

```bash
go test -race -count=1 ./integration_tests/framework
go test -race -C ./integration_tests/fixtures/assistant ./... -count=1
go test -race -C ./integration_tests/fixtures/agent_features ./... -count=1
make verify-mcp-local
make itest
```

Exit criteria:

- The integration README names only executable tests and supported settings.
- Negative MCP wire contracts fail red when intentionally broken.
- Every nested fixture module runs under the race detector.
- No test claims readiness from an arbitrary HTTP response.

## Priority 2 Detail: Run Generated Acceptance Against Real Temporal

Priority: highest remaining after unit/Docker gate isolation.

1. Extract an engine fixture contract from
   `integration_tests/fixtures/agent_features/acceptance_helpers_test.go`.
2. Run the core generated coordinator scenarios against both
   `engine/inmem.Engine` and `engine/temporal.Engine` backed by a real Temporal
   development server in CI.
3. Add focused scenarios for:
   - late activity failure followed by retry without duplicate durable effects;
   - pause, resume, clarification, confirmation, typed input, and external tool
     signals arriving near timeout and cancellation boundaries;
   - child workflow identity and `ChildRunLinked`/`RunLink` preservation;
   - active-time `TimeBudget` pauses during external waits;
   - worker restart and replay using shared persistent `runlog.Store` and
     `session.Store` implementations;
   - cancellation during planner, tool, and child-workflow execution.
4. Keep Temporal SDK unit-environment tests for fast deterministic edge checks;
   treat the real-server lane as an additional adapter/system contract.

Proof:

```bash
go test -race ./runtime/agent/engine/temporal ./runtime/agent/runtime
go test -race -C ./integration_tests/fixtures/agent_features ./... -count=1
make itest
```

Exit criteria:

- The same generated behavior suite passes under both engines.
- At least one test proves replay/retry idempotency and one proves restart
  persistence against a real Temporal server.

## Phase 3: Expand Executable Generated-Code Contracts

Priority: high.

1. Extend `codegen/agent.TestGeneratedAgentDesignsCompile` and add the equivalent
   MCP compile matrix for:
   - nested and located user types;
   - explicit-tag and custom-envelope unions;
   - optional/defaulted pointer and value injection;
   - server data and bounded results;
   - method-backed projected tools;
   - no-payload and no-result methods;
   - streaming methods collected into unary MCP results;
   - reused and multi-service toolsets;
   - prompt-only, resource-only, and `SkillDirectory`-only MCP servers.
2. Add generated runtime tests for codecs, injection, dispatch, schema examples,
   and projected runtime/MCP parity where compilation is insufficient.
3. Keep goldens for reviewable source contracts, but require a compile or
   behavior case for every material generator branch.
4. Enforce byte-stable repeated generation for combined agent/MCP pipelines.

Proof:

```bash
go test -race ./codegen/agent ./codegen/agent/tests ./codegen/mcp
make regen-assistant-fixture
make regen-progressive-discovery-fixture
make regen-agent-feature-fixture
git diff --exit-code -- integration_tests/fixtures/assistant integration_tests/fixtures/agent_features
```

Exit criteria:

- Golden-only coverage is no longer the sole proof for any listed shape.
- Repeated generation is deterministic.
- Generated examples validate against their advertised schemas.

## Phase 4: Add Concurrency and Lifecycle Stress Gates

Priority: high.

1. Add a `make test-stress` target for the concurrency-heavy owners:
   `runtime/agent/runtime`, `runtime/agent/engine/temporal`, `runtime/registry`,
   `registry`, `features/stream/pulse`, and the generated SDK session tests.
2. Run with `-race`, `-shuffle=on`, and repeated counts. Preserve the printed
   shuffle seed in failures.
3. Replace remaining ordering sleeps with explicit synchronization or fake
   clocks where possible.
4. Cover concurrent registration/unregistration, shutdown, subscription ack,
   cancellation, session deletion, cache refresh, and partial-failure fan-out.
5. Start as a scheduled CI job; promote stable, fast cases to pull-request CI.

Proof:

```bash
make test-stress
```

Exit criteria:

- Critical lifecycle suites pass repeated race runs without timing-only waits.
- Failures identify a reproducible seed and owning package.

## Phase 5: Complete Provider Conformance

Priority: medium-high.

1. Expand `testutil.ProviderConformanceSuite` into capability-aware sections for
   complete calls, streaming, tools, structured output, typed thinking,
   multimodal input, exact token counting, cancellation, normalized setup and
   receive errors, and canonical/provider tool-name round trips.
2. Require stream-receive rate limits to wrap `model.ErrRateLimited`.
3. Require middleware conformance to prove optional interfaces remain truthful.
4. Keep deterministic SDK fixtures mandatory. Add opt-in or scheduled live
   smoke tests for providers where credentials and cost controls are available.

Proof:

```bash
go test -race ./testutil ./features/model/...
make test
```

Exit criteria:

- Every provider explicitly passes or declares unsupported for each capability.
- The shared suite matches the provider contract documented in the repo skill.

## Phase 6: Make Coverage and Tooling Gates Actionable

Priority: medium.

1. Retain a global coverage floor, but add package-group or changed-code floors
   for runtime, MCP, codegen, Temporal, registry/Pulse, and provider adapters.
2. Exclude generated packages, mocks, and design-only fixtures from the
   actionable denominator while still compiling them.
3. Produce a separate Docker-required coverage report in CI so local skips do
   not disguise Redis and Mongo gaps.
4. Make `make ci` run the complete required ladder or rename it to make its
   unit-only scope explicit.
5. Pin `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` from one source.
6. Derive the Loom version from the repository's canonical remote pin instead
   of maintaining a stale CI-only value.

Proof:

```bash
make lint
make test
make verify-mcp-local
make itest
```

Exit criteria:

- A coverage loss in a critical owner cannot be hidden by unrelated packages.
- Local and CI toolchains resolve the same pinned versions.
- The named CI target represents the full required verification ladder.

## Completion Checklist

- [x] Priority 1: fast unit and fail-closed Docker gate isolation
- [ ] Priority 2: real-Temporal generated acceptance (foundation implemented;
  completed by Priority 3 signal/child coverage and Priority 5 persistence)
- [ ] Priority 3: cross-layer generated runtime transitions
- [ ] Priority 4: provider stream state-machine conformance
- [ ] Priority 5: real persistence upgrade and concurrency contracts
- [ ] Priority 6: fuzz dynamic trust boundaries
- [ ] Priority 7: risk-weighted owner floors
- [x] Phase 1: truthful MCP integration coverage
- [x] Phase 3: executable codegen matrix
- [x] Phase 4: race and stress lane
- [x] Phase 5: complete provider conformance
- [x] Phase 6: actionable coverage and reproducible tooling
- [x] MCP Go SDK checked against authoritative module versions (`v1.7.0` is current)
- [x] `make lint`
- [x] `make test`
- [x] `make verify-mcp-local`
- [x] `make itest`
- [x] `make test-stress STRESS_COUNT=2`
