# Test Suite Audit — loom-mcp

Audited: 2026-07-12 (refresh + ongoing remediation) · Baseline commit: `4f2c262` (main) · Auditor: test-suite-audit skill
Baseline: prior audit at `679fb76` earlier the same day; this refresh and the subsequent remediation commits re-measure each finding at the current tree.
Recent remediation continues through the current tree, including generated-output compilation, runtime boundaries, provider failures, real Mongo contracts, integration artifact ownership, provider conformance, and deterministic ordering. See the defect ledger for the individual regression commits.

Scope update (2026-07-30): the MCP Go SDK v1.7.0 upgrade adopts the
`2026-07-28` protocol surface. Sampling and roots are deprecated there and have
been removed from Loom's runtime client-feature API and generated fixture
contracts rather than retained through the compatibility window. Current
official-client coverage for this boundary is elicitation through multi
round-trip `inputRequests`/`inputResponses` plus request-scoped progress. The
DSL now rejects protocol versions newer than the always-generated native
transport implements; `2026-07-28` negotiation remains confined to the
stateless SDK server runtime. Multi-round-trip state is AES-GCM protected,
request-bound, size-bounded, and portable across replicas sharing a stable
server key. The unit target disables Testcontainers' shared cross-package Ryuk
session because every Docker-backed owner performs explicit cleanup; this
prevents a completed package from reaping another package's active container.

Remediation progress: **Complete at the current tree. All findings and exposed defects through C-73 are resolved, every target-state requirement is evidenced, and hosted runs 29202009227 (at `7120e44`) and 29208887958 (at HEAD `4ce0e09`) are green.** Validation covers 49/59 DSL error sites and more than 37/46 `ToolExpr.Validate` conditions. Five materially different agent designs are freshly rendered and compiled. The compile matrix exposed and now prevents invalid cross-package payload conversion, required injected-pointer assignment, and result-less method invocation (C-16–C-18). The completeness passes raised the in-memory workflow engine from 37.8 % to 89.8 %, hook transport from 33.1 % to 87.6 %, SDK client-feature bridge from 4.5 % to 84.1 %, hint rendering from 38.4 % to 73.6 %, the in-memory session store from 50.7 % to 84.9 %, stream translation from 51.6 % to 79.3 %, the Temporal engine from 53.1 % to 75.2 %, durable transcript replay from 55.6 % to 78.7 %, the model wire/error boundary from 50.0 % to 91.7 %, telemetry from 45.7 % to 95.6 %, the registry tool executor from 50.0 % to 84.3 %, model middleware from 58.2 % to 83.2 %, durable memory event codecs from 62.6 % to 77.2 %, the MCP runtime from 64.5 % to 81.5 %, the Mongo runlog client from 59.8 % to 82.4 %, canonical tool-registry wire helpers from 0 % to 90.8 %, structured tool errors from 0 % to 100 %, engine context helpers from 0 % to 100 %, the stream bridge from 0 % to 100 %, the Mongo runlog store façade from 0 % to 100 %, the concrete Pulse client from 0 % to 87.5 %, the core runtime from 68.2 % to 70.2 %, and agent codegen from 67.1 % to 70.3 %, exposing thirty-six runtime defects (C-38–C-73). The final runlog, C-12 façade/client, and codegen-helper slices found no additional product defect; their implementations held under expanded direct and real-service contracts.

## 1. Executive summary

The suite now contains 372 test files and 1,711 top-level test functions (plus one benchmark). Regression discipline remains strong: every audited fix has landed with a test, and remediation exposed fifty-nine additional product defects (C-15–C-73). Shuffle is enforced, design-validation targets are met, a five-design compile matrix rejects invalid generated package graphs, high-fan-in runtime and ordering boundaries are synchronized, all five provider adapters execute one behavioral conformance matrix, real Mongo and Redis contracts run through containers, and elicitation plus progress have direct official-SDK adapter contracts and real client-vs-generated-framework coverage. Hosted Actions exposed C-36's host-dependent Markdown MIME classification and C-37's asynchronous replicated-map visibility, then passed both jobs after their fixes. The completeness work exposed and fixed in-memory workflow identity, cancellation, child-handle, and completed-signal defects; dropped hook display metadata and nil-input codec panics; optional-field, typed-count, and Unicode hint-rendering failures; cross-backend session ownership violations; incomplete/mutable client stream payloads; Temporal cancellation error drift; transcript replay corruption across message/part forms; asymmetric model-part serialization and token estimation; incorrect GenAI pointer-part export; nil/aliasing failures in registry-backed tool execution; clustered model-limiter policy drift; mutable durable-memory payload aliases; missing Temporal child run identity; canonical MCP decode panics/rejections; lost structured tool-error context; and nondeterministic, mutable runtime catalogs. The final runlog, zero-owner, and codegen-helper expansions found no further defect. The warm non-Docker unit critical path is 27 % faster, duplication is resolved or reclassified, and protocol-safe scenario-file parallelism keeps local integration below 90 seconds. Integration fixture artifacts have process-level owners and are removed after every test binary; a measured run left zero prepared clones and zero cached server binaries after an earlier session had accumulated 93 clones (125 MB) and 218 binaries (8.2 GB).

## 2. Inventory (cross-repo baseline metrics)

| Metric | Value (change vs `679fb76`) |
|---|---|
| Languages / frameworks | Go 1.26 (arm64); Goa/Loom DSL+codegen; MCP go-sdk v1.7.0; loom fork v1.7.1 (remote mode) |
| Test runner(s) | `go test` via `make test` (`-shuffle=on`) / `make itest` / `make verify-mcp-local`; testify, gopter, testcontainers (Redis, Mongo) |
| Test files / test cases | 372 files, 1,711 `func Test` plus 1 benchmark; latest full gate counts are refreshed after each remediation phase |
| Layer split (unit / integration / e2e) | 1,523 unit funcs; 188 integration-cluster funcs (framework 30, tests 7, assistant 142, agent_features 9); 1 e2e quickstart in the integration target |
| Total wall time (local) / (CI pipeline) | Non-Docker unit (`-short -race -covermode=atomic`): **21.84 s fully warm** (59.05 s CPU), down from 30.1 s. The strict Colima gate is currently dominated by the 70.6 s Redis registry package. Latest local scenarios: 74.4 s. Hosted runs green: **29202009227** (`7120e44`: integration 2m14s, build 7m42s) and **29208887958** (HEAD `4ce0e09`: integration 2m38s, build 4m15s warm-cached). |
| Parallelism today | Unit packages use package/test parallelism; **2.7 of 10 cores** warm (59.05 s CPU / 21.84 s wall). The integration framework honors its safe `t.Parallel()` declarations; six independent scenario-file groups run concurrently while stateful scenarios remain serial within each YAML file. |
| Coverage (line / branch) | **69.3 %** statements in the latest strict local gate with Docker-backed packages through Colima; branch N/A (Go). Every remaining zero-coverage entry is generated, mock, design, or fixture-only attribution rather than a live product owner (C-12) |
| Skipped/disabled tests | Docker-backed tests may skip only outside strict CI mode; 0 permanent legacy skips; the CLI scenario is canonical in make itest |
| Snapshot tests (count / size on disk) | 53 `.golden` / 276 KB / 18 > 100 lines (net zero new goldens in delta; 2 modified) |
| Sleeps in tests (count / purpose) | 4 sites: subprocess signal fail-safe, real cache TTL, Ollama response-body lifetime, and configurable hook delay. No blind ordering sleeps; integration tests: 0 sleeps |
| Bug-fix commits with tests (sampled ratio) | **52/52** in the remediation delta (measured, not sampled); cumulative 67/67 |
| Sampling performed | 5 cluster subagents re-verified every prior finding + read the full delta; every mandatory grep re-run repo-wide at HEAD; measured unit runs plus final-tree fixed seeds `20260712` and `7302026`, random shuffle, and the integration ladder executed; ≥1 citation per subagent spot-checked in main context |

## 3. Findings

Finding IDs continue the `679fb76` numbering; each carries a status vs that baseline.

### C-1: CI executes the full contract locally and hosted
- **Severity:** blocker · **Status:** resolved to the user-approved local-parity criterion; hosted manual execution proven
- **Confidence:** measured
- **Evidence:** `.github/workflows/ci.yml` installs Go `1.26.5`, uses the repository's Loom `v1.7.1` module pin, runs repository make targets, caches every module, fails closed on missing Docker coverage, enforces 62.0 % aggregate coverage, preserves `cover.out`, uses read-only permissions, cancels superseded runs, and bounds jobs at 30 minutes. Its declared triggers are manual dispatch, pushes to `main`, and pull requests targeting `main`; the remote workflow is active with Actions enabled. `make itest` selects all 188 integration functions plus the quickstart with uncached execution. Manual hosted run [29202009227](https://github.com/CaliLuke/loom-mcp/actions/runs/29202009227) passed both jobs on `7120e44` (integration 2m14s, strict build/lint/unit/coverage 7m42s), and run [29208887958](https://github.com/CaliLuke/loom-mcp/actions/runs/29208887958) passed both jobs at HEAD `4ce0e09` (integration 2m38s, build 4m15s). The missing push-event runs were diagnosed via the Events API: pushes were delivered (26 `PushEvent`s recorded) but suppressed by the fork-workflows gate — the repo began as a fork (parent linkage since detached, so the API reports `fork: false`), and that gate is separate from the Actions permissions toggle and only clearable in the Actions tab UI. The owner cleared it on 2026-07-12; the first ordinary push since then confirms automatic triggering.
- **Impact/Recommendation:** The test-suite blocker is complete and HEAD is hosted-green. Preserve manual dispatch and the declared push/PR triggers; verify the next ordinary push produces an automatic run now that the fork-workflows gate is cleared.

### C-2: Docker-gated registry tests fail closed and execute in hosted CI
- **Severity:** high · **Status:** resolved locally and hosted
- **Confidence:** measured
- **Evidence:** The same 29 functions still use the shared Redis testcontainer. `registry.TestMain` separates setup/cleanup, reports cleanup failures, and honors explicit Docker selection. `make test` now forces the container-free path, while `make test-docker` sets both `LOOM_MCP_RUN_DOCKER_TESTS=1` and `LOOM_MCP_REQUIRE_DOCKER_TESTS=1`, turning Docker/container startup failure into exit 1 instead of a skip. The selector has a focused table test, a no-socket strict probe failed closed with the expected message, and the dedicated gate runs registry, Mongo, and Pulse contracts once under `-race -shuffle=on -count=1` with owner-specific coverage floors.
- **Impact/Recommendation:** Complete. Preserve the single fail-closed Docker gate in `make ci`; do not move container-backed contracts back into the fast unit gate.

### C-3: DSL/expr validation — planned coverage targets achieved
- **Severity:** high · **Status:** resolved to target (DSL error sites 6/59 → 49/59)
- **Confidence:** measured
- **Evidence:** 59 `eval.ReportError`/`InvalidArgError` sites exist in `dsl/*.go`; **49 are tested message-by-message**, exceeding the 48-site target. The added toolset, ServerData/audience, and workflow JSON tables cover 23 more stable diagnostics, and DSL statement coverage rose from 67.5 % to 73.3 %. `expr/agent/tool.go` remains above the planned 37/46 direct-condition threshold: `Validate` is 93.9 % statement-covered; Inject, MCP unsupported features, paging, tool-return bounds, and method-result bounds helpers are 96–100 %; ServerData and shape validation are above 94 %. Binding resolution and unsupported data shapes have exact error tests.
- **Impact:** The central expression validator and the high-risk DSL authoring boundary are no longer major blind spots. Ten lower-priority DSL error sites remain and should be covered when their owning features change.
- **Recommendation:** Preserve the matrices and exact-message assertions; fold the remaining ten sites into related feature work rather than blocking the higher-yield compile and runtime phases.

### C-4: Generated-output compilation matrix reaches five agent designs
- **Severity:** high · **Status:** resolved to target
- **Confidence:** measured
- **Evidence:** `TestGeneratedAgentDesignsCompile` now renders Loom service files plus agent-plugin files into isolated modules and runs `go build ./...` for five materially different designs: FromMCP, method-backed MCP projection, registry-backed discovery/specs, injected payload separation, and a payload-only bound method. Registry local-ref/freeze generation and injected-field hiding retain focused source assertions, but compilation is now the decisive contract. The first expanded run failed on three distinct current generator defects (C-16–C-18), demonstrating that the prior string-only tests could pass on invalid output.
- **Impact:** The highest-risk agent generator shapes can no longer ship uncompilable merely because their source snippets look correct. Prompt/resource-only MCP generation remains a useful protocol-phase addition, but is not needed to satisfy this agent compile target.
- **Recommendation:** Preserve the matrix and add a case whenever a new generated package boundary, provider kind, or method signature shape is introduced.

### C-5: MCP elicitation and progress have real client coverage
- **Severity:** medium · **Status:** resolved
- **Confidence:** measured
- **Evidence:** `TestGeneratedSDKServerElicitsDuringToolCalls` covers accept, decline, and cancel through a generated tool. `TestGeneratedSDKServerSupportsMultiStepElicitation` proves two sequential input requests cause three service invocations, while `TestGeneratedSDKServerElicitsDuringPromptAndResourceCalls` exercises the generated `GetPromptResult` and `ReadResourceResult` input-required branches. `TestGeneratedSDKServerMultiStepElicitationOverStreamableHTTP` proves three modern stateless HTTP POSTs complete the same flow, and `TestGeneratedSDKServerRejectsFutureInputResponseOverStreamableHTTP` proves generated transport rejects a response for an unissued future input; `TestGeneratedSDKServerLegacyClientCompletesSingleStepElicitation` pins the SDK's legacy server-middleware fallback. Focused `runtime/mcp/sdkclient` tests cover encrypted, request-bound, portable bounded/versioned state, missing and wrong keys, tampering, cross-request replay, future and wrong response IDs, changed pending contracts, unsolicited and wrong response types, invalid actions, malformed/unsupported/oversized state, and the 64-response limit. `TestGeneratedServerSDKToolReportsProgressToClient` supplies a string progress token on `tools/call` and receives three monotonic `notifications/progress` messages with the exact token, values, total, and messages before the tool result.
- **Impact/Recommendation:** Complete for the supported client-feature surface. Preserve the shared `runtime/mcp/sdkclient` conversion boundary and do not restore the deprecated sampling or roots compatibility APIs.

### C-6: High-fan-in runtime gaps — all live owners covered
- **Severity:** medium · **Status:** resolved for live code
- **Confidence:** measured
- **Evidence at HEAD:** runtime/agent/tools has 96.4 % direct coverage; planner.ConsumeStream lifts planner to 85.7 %; runtime/agent/interrupt is 94.1 %. Clue metrics and tracing execute against real in-memory OpenTelemetry providers; context rehydration, complete GenAI input/output part schemas, reasoning redaction, usage attributes, MIME mapping, and malformed payloads are exercised directly, raising telemetry from 8.5 % to 95.6 % and exposing C-61. Registry executor dependency/spec validation, stream event handling, result codecs, retry classification, and deep-copy contracts raised that owner from 50.0 % to 84.3 % and exposed C-62–C-64. The in-memory workflow engine rose from 37.8 % to 89.8 % through lifecycle, cancellation, activity, signal, timer, and context contracts; those tests exposed C-38–C-41. A 26-variant payload-equivalence matrix and invalid-envelope cases raised hooks from 33.1 % to 87.6 % and exposed C-42/C-43. Hint registry, compile, failure, optional-field, typed-count, and Unicode contracts raised runtime/hints from 38.4 % to 73.6 % and exposed C-44–C-46. Cross-backend session lifecycle, ownership, filtering, copy-isolation, context, and real Mongo transaction contracts raised session/inmem from 50.7 % to 84.9 % and exposed C-47–C-50. A complete await/authorization/usage translation matrix, failure cases, profile contracts, and buffer-isolation checks raised stream from 51.6 % to 79.3 % and exposed C-51/C-52. Temporal handle/query/completion delegation and complete output-wire tests raised the production engine from 53.1 % to 61.4 % and exposed C-53. Transcript boundary, pointer-part, full JSON, legacy, malformed, and emptiness contracts raised durable replay from 55.6 % to 78.7 % and exposed C-54–C-56. Model part round trips, malformed inputs, legacy shapes, structured provider errors, and token-estimation parity raised model from 50.0 % to 91.7 % and exposed C-57–C-60. Runtime catalog ordering, deep tool-spec isolation, schemas/models, and agent-tool template validation raised the core runtime from 68.2 % to 70.2 % and exposed C-73. Direct generator decision/transform contracts raised codegen/agent from 67.1 % to 70.3 % without exposing another defect. The earlier planner stream tests exposed and fixed C-19–C-21.
- **Second-pass evidence:** model middleware stream/counter parity, cancellation, shared parsing/probing, effective defaults, and configured cluster bounds raised middleware from 58.2 % to 83.2 % and exposed C-65. Durable user/assistant/tool/thinking matrices, malformed fields, and recursive copy isolation raised memory event codecs from 62.6 % to 77.2 % and exposed C-66–C-68. Temporal's workflow test environment now exercises metadata, signals, deterministic timers, timeout/cancellation, validation, and child execution identity; coverage rose from 61.4 % to 75.2 % and exposed C-69. Canonical JSON decoding, named map keys, byte slices, overflow/null/unmarshaler cases, query coercion, caller delegation, and retry prompts raised runtime/mcp from 64.5 % to 81.5 % and retry from 0 % to 100 %, exposing C-70/C-71. Mongo runlog validation, BSON detachment, exact index definitions, constructor/list filters, duplicate resolution, cancellation, and ordered two-page pagination raised its client package from 59.8 % to 82.4 %. The same contracts passed against Mongo 7; no runlog defect was exposed.
- **Impact/Recommendation:** Complete. Keep bridge behavior thin and delegated; move it only if a public API cleanup intentionally changes the documented example surface.

### C-7: Model-adapter error/streaming matrix holes
- **Severity:** medium · **Status:** resolved; 5/5 adapters execute the shared matrix
- **Confidence:** measured
- **Evidence:** `testutil.RunProviderConformance` requires ordinary and rate-limit errors, malformed tool calls, cancellation, structured-output/tool-choice compatibility, usage accounting, and either the complete stream setup/receive/terminal lifecycle or one explicit unsupported-streaming contract. Gemini exposed C-28; Ollama C-29/C-30; OpenAI C-31; Anthropic C-32–C-34. Bedrock now passes the same matrix through Smithy errors, document decoding, captured Converse requests, and an AWS `ConverseStreamEventStream` mock reader. Its private stream seam returns the existing `StreamOutput` contract while production continues to call the real AWS runtime.
- **Impact/Recommendation:** Complete. Every supported adapter must keep this matrix green; SDK-specific fixtures remain in their owning packages and only observable `model.Client` behavior is shared.

### C-8: Prompt and memory Mongo contracts execute against real Mongo
- **Severity:** medium · **Status:** resolved
- **Confidence:** measured
- **Evidence:** The Mongo 7 replica-set integration package now executes four real driver-v2 contracts: runlog append/dedup, session ownership plus child-link transactions, prompt override precedence (session, label subset, global fallback, metadata/history), and memory append/load with nested BSON normalization. The session contract rejects orphan and post-terminal run insertion while permitting terminal updates to existing runs. CI uses the existing LOOM_MCP_REQUIRE_DOCKER_TESTS switch so container startup failure is fatal rather than a skip. All four pass against Colima under race/shuffle. The first memory run exposed C-23; the expanded session transaction exposed C-47/C-48.
- **Impact/Recommendation:** Complete. Keep fast fake tests for branch detail and the real Mongo suite for query/update validity; any new Mongo-backed store must add at least one real round trip.

### C-9: Generated registry gRPC server/client adapter contract
- **Severity:** medium · **Status:** resolved
- **Confidence:** measured
- **Evidence:** TestGRPCClientAdapterGeneratedServerContract runs the generated registry endpoints and generated gRPC server on bufconn, connects the generated protobuf client, wraps it in GRPCClientAdapter, and proves ListToolsets, GetToolset, and Search request/response conversion including payload, result, and sidecar schemas. The test is race/shuffle green and requires no Docker service.
- **Impact/Recommendation:** Complete. Keep the generated server in the path so protocol drift cannot be hidden by the older handwritten client mock tests.

### C-10: Audited concurrency and repeated-finalization contracts are deterministic
- **Severity:** medium · **Status:** resolved for the audited contracts
- **Confidence:** measured
- **Evidence:** Both ToolExpr and MCPExpr repeated-finalization contracts preserve identity and derived state. Concurrent registry StartSync/StopSync remains race-hammered, while restart now proves StopSync observes cancellation, waits for an in-flight generation, and starts a fresh generation through owned channels. Tool completion order waits for the fast future to be consumed before releasing the slow future; pause/resume signals are prequeued on their buffered channels; cluster backoff waits for the exact map mutation; heartbeat absence uses the no-worker return path. All are race/shuffle green without scheduler sleeps.
- **Impact/Recommendation:** Complete for the concrete audited gaps. New concurrent components should expose owned readiness/completion signals and add race-enabled tests at introduction.

### C-11 (positive): Regression discipline is excellent — now measured at 52/52 on the delta
- **Severity:** low (positive) · **Status:** reconfirmed, stronger
- **Confidence:** measured
- **Evidence:** Every remediation fix commit, including C-28's executable Gemini conformance case and the renewed runtime matrices through C-73, touches tests. Quality spot-reads: `63f3a22` asserts cap size, FIFO eviction order, and concurrent-writer safety; `91028a1` asserts timeout bounding with elapsed-time and detached-context assertions; `eb3911e` race-hardened 1000-iteration loop. Not echoes.
- **Recommendation:** Preserve the norm; none.

### C-12: Zero-coverage inventory distinguishes attribution artifacts from live owners
- **Severity:** low · **Status:** resolved after completion-audit reopening
- **Confidence:** measured
- **Evidence:** `testutil` is 16.7 % because the executable provider harness is directly tested. Generated registry packages, registry/design, generated mocks, `codegen/testhelpers`, and codegen test scenarios are test-time/generated attribution artifacts. The earlier audit incorrectly omitted four live owners: direct contracts now raise engine context helpers to 100 %, the stream bridge to 100 %, the Mongo runlog store façade to 100 %, and the concrete Pulse client to 87.5 % through a fail-closed Redis publish/subscribe/ack/destroy round trip. Canonical tool-registry wire helpers and structured tool errors are likewise now 90.8 % and 100 % and exposed C-72.
- **Recommendation:** Complete. Keep generated/mock attribution entries out of product-gap counts and require a real service contract for any new concrete persistence or streaming client.

### C-13 (new): Residual untested branches introduced by the delta's own fixes
- **Severity:** low
- **Status:** resolved
- **Confidence:** measured
- **Evidence:** providerServeCause now covers both a signaled root error and the context-error fallback. ConsumeDeferredSkillDirectory covers missing entries, middle removal, final deletion, and order preservation. validateConfig explicitly documents zero health values as accepted programmatic defaults while rejecting negatives. Anthropic's existing checkpoint-only message case already proves that a checkpoint with no cacheable preceding block is dropped safely. The MCP request-size global mutation remains non-parallel and cleanup-restored.
- **Impact/Recommendation:** Complete. Preserve these neighboring-branch tests and keep the global-mutating metadata test serialized until the configuration becomes instance-owned.

### C-14 (found and fixed): ServerData validation panicked on a bound method with no result
- **Severity:** medium · **Status:** resolved in this change
- **Confidence:** reproduced by regression test
- **Evidence:** `ToolExpr.Validate()` resolved the bound method and `validateServerDataShapes` called `t.Method.Result.Find(...)` without checking whether the method declared a result. `TestToolExprValidateServerDataRejectsNilBoundMethodResult` reproduced the nil-pointer panic. Validation now treats a nil result like a missing source field and returns the existing stable `FromMethodResultField(...) does not exist on method result` error.
- **Impact:** A malformed or partially evaluated design could crash validation/code generation instead of producing a design error.
- **Recommendation:** Complete; retain the non-panic regression and exact error assertion.

### C-15 (found and fixed): Parallel assistant SDK clients shared the global HTTP transport
- **Severity:** medium · **Status:** resolved in this change
- **Confidence:** failure observed once; root cause inferred from lifecycle and stress-verified
- **Evidence:** The full gate failed TestGeneratedSDKServerCompletesPromptArguments during notifications/initialized with "http: CloseIdleConnections called". The test passed 50 times alone and the full fixture passed 10 times, pointing to cross-test lifecycle rather than deterministic protocol behavior. connectSDKSessionToServer and the generated JSON-RPC transport helper both wrapped the global default transport while many fixture tests run in parallel. They now receive a per-test cloned transport whose idle connections close through test cleanup. After the change, the entire assistant fixture passed 20 consecutive runs and the original completion test passed 50 race-enabled runs.
- **Impact:** A parallel test could close or disrupt a shared connection pool during another SDK client's initialize handshake, making a correct server appear flaky.
- **Recommendation:** Complete; keep parallel clients transport-isolated and treat any recurrence as a separate lifecycle bug.

### V-1: Assistant-fixture consolidation — apparent duplication is intentional contract layering
- **Severity:** medium · **Status:** resolved by contract inventory; no consolidation warranted
- **Confidence:** code-inspected
- **Evidence:** The tool-search files exercise three different owners: adapter ranking/configuration and proxy semantics, raw generated JSON-RPC encoding/error behavior, and SDK transport behavior. OAuth likewise separates audience-verifier policy, protected-resource discovery/cache headers, and HTTP challenge/forwarded-host construction. Similar scenario names across those files are deliberate cross-layer conformance checks, not repeated setup around one implementation seam.
- **Recommendation:** Keep the layer-specific tests independently selectable. Share only transport/session construction helpers; do not table-drive across adapter, JSON-RPC, and SDK boundaries because a combined case would obscure which public layer regressed.

### V-2: codegen/mcp re-runs full `Generate` redundantly
- **Severity:** medium · **Status:** resolved for the audited redundancies
- **Confidence:** measured
- **Evidence:** File-order determinism now performs three full generations (baseline plus two repeats) instead of thirteen. The seven transport conformance contracts are named subtests over one immutable generated fixture instead of seven independent generations. Their focused command completes in 0.9 s of test time; the complete race/coverage package remains the unit suite's 12.7 s critical path, down from 20.6 s.
- **Recommendation:** Complete for V-2. Keep generation inputs isolated; do not add process-global fixture caching merely to optimize unrelated designs.

### V-3: Parametrization candidates across four clusters
- **Severity:** low · **Status:** resolved
- **Confidence:** code-inspected and targeted tests
- **Evidence:** The byte-identical Ollama/OpenAI stream drain is now the provider-neutral `testutil.CollectStreamChunks` helper. The other candidates are intentionally separate behavioral contracts: registry security covers different Goa security-holder types and multiplicity; FromRegistry covers derived naming, explicit naming, version overlay, and shared-expression immutability; subscriber toggles construct different hook event types and profile fields; cache properties use different generators and prove fallback identity, full-field preservation, expiry, and repeatability. Their shared outer setup is small, while combining them would require untyped callbacks or branch-heavy assertions.
- **Recommendation:** Complete. Parameterize data variants within one contract, but keep independently named tests for different DSL evaluation, hook-event, and property invariants.

### V-4: Tautological/echo tests removed
- **Severity:** low · **Status:** resolved
- **Confidence:** measured
- **Evidence:** The three ToolsetExpr provider-resolution subtests only constructed structs and asserted the same assigned fields; they are deleted. The nearby MCP ToolExpr validation and EvalName cases invoke behavior and remain. Repo-wide greps still show no assert-true equivalents, zero-assertion tests, or commented-out tests.
- **Recommendation:** Complete; prefer DSL evaluation, validation, generated output, or runtime behavior over struct field echoes.

### V-5: Dead-weight inventory resolved
- **Severity:** low · **Status:** resolved
- **Confidence:** measured
- **Evidence:** The two permanently skipped legacy codegen test files are deleted. make itest now sets MCP_CLI_TESTS=true; the CLI prompt scenario passes locally in 12.3 s. DeleteMeta, SetDescription, and SetTitle are not dead: they satisfy Goa metadata/description/title holder interfaces consumed dynamically by standard DSL helpers. runtime/agent/stream/bridge is a documented public façade used by runnable examples rather than internal production wiring.
- **Recommendation:** Complete. Keep the CLI scenario canonical and do not classify interface implementations as dead solely from static call counts.

### S-1: Top 5 % of tests = 95 % of summed test time; warm gate now meets the evidence-based target
- **Severity:** high (as dev-loop finding, reframed) · **Status:** resolved
- **Confidence:** measured
- **Evidence:** `make test` now uses short mode and `make itest` explicitly owns the race-enabled quickstart, so no e2e contract disappeared. FileOrder is 13→3, the transport fixture generates once, and the TTL property keeps 100 cases while moving deadlines deterministically; `TestMemoryCacheTTLExpiration` retains real clock-passage coverage. Focused times are 0.9 s for the codegen pair and 0.48 s for the property. The fully warm canonical unit gate is **21.84 s wall / 59.05 s CPU**, down 27 % from 30.1 s / 69.7 s. Repeated warm runs vary from 21.24–24.05 s; the slowest test body is 1.53 s, while race/coverage compilation and the 12.4 s global-root codegen package set the critical path.
- **Impact:** The three avoidable test-time multipliers no longer dominate local feedback. Compile and the remaining codegen package chain now set the critical path.
- **Recommendation:** Complete against a ≤25 s warm guardrail. The original ≤20 s aspiration is below observed scheduler variance and would require weakening race/coverage or making shared expression-root generation concurrent. Revisit only after expression state becomes instance-owned.

### S-2: Order coupling in `dsl` — resolved and shuffle enforced
- **Severity:** high · **Status:** resolved
- **Confidence:** measured
- **Evidence:** `resetDSLRoots` now recreates and registers `mcpexpr.Root`; the three `expr/agent` tests that replace Goa/MCP roots call `preserveGlobalRoots`, which restores both roots with `t.Cleanup`. The complete non-integration package set passes with `-shuffle=101` and `-shuffle=202`, and the race-enabled `make test` passes with randomized order. `Makefile` now includes `-shuffle=on` in the normal unit gate.
- **Recommendation:** Complete. Keep shuffle in the canonical test target; the next parallelism blocker is S-3's process-global environment channel.

### S-3: Process-global scenario environment channel removed
- **Severity:** medium · **Status:** resolved
- **Confidence:** measured
- **Evidence:** The `MCP_*` special case mutated the parent test environment when applying request headers, after the managed child server was already running, so it could neither configure that server nor restore the parent safely. Scenario `headers` now have one contract: they are HTTP headers. A regression test proves an `MCP_*` header reaches the request while the same-named process variable remains unchanged. The canonical integration target no longer forces `-parallel 1`; the race-enabled framework package is green and fell from 38.9 s to 18.1 s, while the complete framework/scenario command remained green in 178.2 s.
- **Recommendation:** Complete. Keep child-process configuration explicit on `exec.Cmd.Env` if a future scenario needs it; never overload request headers as a process-global environment channel.

### S-4: Per-scenario isolation retained with parallel file groups; artifact leak resolved
- **Severity:** medium · **Status:** resolved
- **Confidence:** measured
- **Evidence:** The audit session had accumulated **93 prepared clones / 125 MB** under `integration_tests/.tmp` and **218 cached server binaries / 8.2 GB** under the system temp directory. `CleanupTestArtifacts` now atomically drains both process caches and removes their artifacts; both integration test binaries call it from `TestMain`, make cleanup failure fail an otherwise-green process, and a focused contract proves the maps reset and files disappear. A real CLI scenario process left both artifact counts at zero. The suite still boots one server per scenario because the generated server retains single-client initialization state beyond the MCP session header. The six independent YAML file groups now run in parallel, while scenarios within each file remain serial and independently booted; the race-enabled package fell from **180.6 s to 72.1 s**.
- **Recommendation:** Complete. Keep per-scenario runner, port, clone, process, and session ownership; file-group concurrency is the safe throughput boundary until the server itself supports independent client initialization state.

### S-5: Core utilization 2.7/10; global expr roots serialize the expensive packages
- **Severity:** medium · **Status:** resolved by readiness inventory; no safe high-yield parallelism remains
- **Confidence:** measured and code-inspected
- **Evidence:** Go already schedules packages concurrently. Within the 12.4 s critical codegen/mcp package, only four small pure helper tests are parallel; the generation tests share and reset Goa/Loom/MCP expression roots. The slowest individual body is 1.53 s and the remaining independently parallelizable bodies are too small to move the 21–24 s wall range. Mechanical `t.Parallel` would introduce exactly the global-root races the suite now detects.
- **Recommendation:** Complete for the current architecture. Do not optimize the aggregate CPU/wall ratio; make expression roots instance-owned first, then reassess generation-test concurrency.

### S-6: Build-cache invalidation dominates perceived runtime
- **Severity:** low (but the largest perceived-time factor) · **Status:** resolved by measurement discipline
- **Confidence:** measured
- **Evidence:** The original large delta needed ≈3 min before the first test event; the smaller test-only delta took 41.8 s on its first canonical run and now varies from 21.24–24.05 s fully warm. The 679fb76 audit's 98–111 s "warm" runs were partially cold subprocess caches.
- **Recommendation:** Complete. Use one canonical flag set and never compare timings across flag changes.

### S-7: Blind ordering sleeps eliminated
- **Severity:** low · **Status:** resolved
- **Confidence:** measured
- **Evidence:** Test sleeps fell from 13 to 4. Registry shutdown waits for a real Pulse node stream; sync restart owns cancellation/release channels; activity completion, pause/resume, cluster backoff, and heartbeat tests use exact signals; prompt history uses a fixed clock with monotonic store timestamps. The four retained sleeps are the behavior under test or a fail-safe: SIGTERM helper guard, real cache expiration, Ollama response-body lifetime beyond header timeout, and configurable hook activity delay. Integration tests contain none.
- **Recommendation:** Complete. Treat any new bare test sleep as a review finding unless elapsed time is the explicit contract.

## 4. Checklist coverage

Phase 1 — Value (`value.md`):
- 1.1 Tautological tests — **found, minor** (V-4: echo tests now 3+1 subtests; assert-true equivalents 0 hits; zero-assertion tests 0; commented-out 0 — all greps re-run at HEAD).
- 1.2 Implementation-coupled — **found, contained**: delta reproduced the string-assertion pattern at the codegen layer (C-4) while fixture/runtime tests stayed behavioral; adapter echo ratios unchanged (C-7).
- 1.3 Duplicates/near-duplicates — **resolved** (the only byte-identical provider helper is shared; superficially similar fixture, DSL, subscriber, and cache tests were inventoried as distinct contracts).
- 1.4 Snapshot/golden audit — **found, moderate**: 53 / 276 KB / 18 > 100 lines; delta added 0, modified 2 goldens alongside their generator change (disciplined).
- 1.5 Testing someone else's code — **not found** (no new SDK-behavior tests in delta).
- 1.6a Test roots & selection mapping — **resolved locally and hosted**: canonical targets select all 188 integration-cluster functions plus the quickstart.
- 1.6 Dead weight — **remediated**: permanent skips removed, dynamic DSL interface methods reclassified, bridge ownership documented, CLI scenario canonical.

Phase 2 — Completeness (`completeness.md`):
- 2.1 Criticality ranking — re-done at HEAD (top churn: `codegen/mcp/generate.go` 40, `runtime.go` 38, `workflow.go` 34, `bedrock/client.go` 34 …).
- 2.2 Coverage — re-measured via repo's own strict flags (69.7 %); attribution artifacts separated from and all four live owners closed (C-12); codegen/agent is 70.3 % and runtime/agent/runtime is 70.2 %; note `runtime/agent/runtime/runtime.go` and `planner/planner.go` legitimately contain few executable statements (facade files) — not attribution failures.
- 2.3 Untested public surface — refreshed top-churn file evidence: hooks/events.go 88.8 %, expr/agent/tool.go 82.5 %, stream/subscriber.go 79.1 %, and codegen/agent/generate.go 82.8 %. The core runtime catalog/template boundary is directly covered and C-6 sweeps are complete.
- 2.4 Error paths — **found**: C-3 remediated to target (49/59 DSL sites), C-7 (provider shapes), C-13 (delta residuals).
- 2.5 Boundaries/edge inputs — **substantially remediated** (C-3 target achieved; provider matrix and audited runtime concurrency complete).
- 2.6 Structural gaps — real Mongo contracts added (C-8 resolved); audited concurrency resolved (C-10); migrations N/A; CLI flag canonical; property-based present; protocol-layer sampling/roots/progress resolved (C-5).
- 2.7 Regression discipline — **measured 52/52 on the full remediation delta** (C-11).
- 2.8 Assertion strength — spot-read 5 delta fix tests (behavioral); no mutation tooling (none configured).

Phase 3 — Speed (`performance.md`):
- 3.1 Baseline — post-remediation canonical runs measured at 41.8 s after recompilation and 21.24–24.05 s fully warm, with the same race/shuffle/coverage flags plus short mode; hosted CI is green.
- 3.2 Usual suspects — fixtures: V-2 generation duplication and S-4 cleanup/concurrency resolved; blind ordering sleeps eliminated (S-7); real I/O: quickstart is explicitly integration-only; retries: none; lint-in-suite: none.
- 3.2b Flake hunt — baseline: 2 unit runs + 2 shuffle seeds reproduced deterministic S-2 coupling, not flakes. Remediation: both fixed seeds and a race-enabled random-seed unit run are now green.
- 3.2c Fixture architecture — **keep**; process-scoped cache ownership and cleanup are explicit and verified, and isolated scenario-file concurrency meets the integration target.
- 3.3 Parallelization readiness — package concurrency is active; global expression roots correctly keep generation tests serial. Framework tests and six isolated scenario-file groups run in parallel while scenarios retain fresh servers. No safe high-yield unit candidate remains (S-5 resolved).
- 3.4 CI-level structure — C-1 resolved by a full green hosted run; hooks remain lint-only on staged files (read, not timed — no test latency to measure).
- 3.5 Achievable target — warm unit wall improved 30.1→21.24–24.05 s and meets the revised ≤25 s guardrail; integration improved 180.6→72.1–73.8 s and meets ≤90 s.

## 5. Top opportunities (ranked)

Rank positions are preserved for traceability. Every opportunity is complete or an evidence-based reclassification.

| Rank | Opportunity | Findings | Type | Effort | Expected payoff |
|---|---|---|---|---|---|
| 1 | **Completed:** repaired CI selection/toolchain and Docker fail-closed behavior; hosted run green | C-1, C-2 | complete | done | 188 integration funcs + quickstart + 29 registry tests are a real gate |
| 2 | **Completed:** fix `resetDSLRoots`, restore roots with `t.Cleanup`, enforce `-shuffle=on` | S-2 | complete | done | Order-coupling class eliminated; unlocks parallelism work |
| 3 | **Completed:** quickstart integration-only, FileOrder 13→3, one conformance generation, deterministic 100-case TTL property | S-1, V-2 | complete | done | Warm wall 30.1→21.24–24.05 s; ≤25 s guardrail met |
| 4 | **Completed:** DSL/expr validation tables (49/59 DSL; ToolExpr target achieved) | C-3, C-13, C-14 | complete | done | Both coverage targets met; one current panic fixed |
| 5 | **Completed:** compile-the-output matrix with five generated designs | C-4, C-16, C-17, C-18 | complete | done | Three current generator defects found and fixed |
| 6 | **Completed:** `TestMain` cleanup stops leaks, process-global env is gone, and isolated scenario groups run concurrently | S-3, S-4 | complete | done | 8.2 GB observed leak removed; framework 38.9 s → 18.1 s; scenarios 180.6 s → 72.1 s |
| 7 | **Completed:** shared adapter conformance matrix plus real Mongo prompt/memory integration | C-7, C-8 | complete | done | Five adapters uniformly gated; seven provider/persistence defects exposed |
| 8 | **Completed:** echo tests and stale skips removed; DSL setters/bridge and layered fixture contracts reclassified | V-1, V-4, V-5 | cleanup | days | Dead weight removed without deleting public contracts |
| 9 | **Completed:** tools codec/ident, interrupts, ConsumeStream, Clue telemetry, bufconn registry, double-Finalize | C-6, C-9, C-10 | complete | done | Highest-fan-in plumbing has direct behavioral proof |

### Completed correctness plan after C-64

The original ranked opportunities, the risk-weighted second pass, and the requirement-by-requirement completion audit are complete:

1. **Completed — model middleware parity:** `Stream`, optional `CountTokens` delegation/failure, cancellation, effective clustered defaults, configured-bound preservation, and shared-map parse/update behavior are covered. Package coverage is 83.2 %, and C-65 prevents stale cluster state from overriding local limiter policy.
2. **Completed — durable memory event codecs:** exported user/assistant/tool/thinking decoding, malformed optional fields, typed pointer/value forms, and recursive copy isolation are covered. Package coverage is 77.2 %, and C-66–C-68 prevent persisted or decoded state from being rewritten through retained pointers/maps.
3. **Completed — Temporal workflow-context adapters:** Temporal's workflow test environment covers metadata, all six signal receivers, deterministic timers, timeout and cancellation normalization, validation, child execution, and child run identity; option/retry merging remains directly table-tested. Package coverage is 75.2 %, and C-69 aligns child handles with their documented identity contract.
4. **Completed — MCP canonical input boundaries:** `UnmarshalCanonicalJSON`, named map keys, byte slices, custom unmarshalers, numeric overflow/null behavior, field/index diagnostics, query coercion, caller-function delegation, and retry prompt construction are covered. runtime/mcp is 81.5 % and retry is 100 %; C-70/C-71 close the exposed panic and standard-byte-decoding failures.
5. **Completed — Mongo runlog production path:** constructor and exact index contracts, append validation, duplicate resolution, cancellation, payload isolation, list filtering, and ordered pagination are covered by fast fakes and a fail-closed Mongo 7 contract. Package coverage is 82.4 %; the production implementation held without a new defect.

Each slice first ran targeted race/shuffle stress and then `make lint`, Colima-backed `make test`, `make itest`, and `make verify-mcp-local`. Every exposed defect landed with its regression test and an audit-ledger entry in the same commit. The five selected second-pass boundaries are behaviorally complete. The later requirement-by-requirement completion audit disproved the blanket classification of every remaining zero, then closed the four live owners under C-12; only generated, mock, design, and fixture-only attribution entries remain at zero.

## 6. Suggested refactoring sequence

The execution order below is intentionally different from the original opportunity ranking. The goal is not maximum line coverage or minimum runtime; it is to expose existing defects at contract boundaries and then make those checks unavoidable for every future change. Each phase is a separately verified commit on `main`. If a new test exposes a product bug, the regression test and root-cause fix land together so the branch remains green.

### Target state

Status: **achieved and verified at the current tree.**

The testing setup is reassuring enough to resume normal feature work when all of the following are true:

- one canonical CI workflow runs the race-enabled shuffled unit suite, all three integration clusters (`framework/tests`, `fixtures/assistant`, and `fixtures/agent_features`), and the Docker-backed registry tests without silently skipping them;
- at least 80 % of the DSL error sites and `ToolExpr.Validate` branches identified by C-3 have direct message-level assertions;
- at least four materially different generated designs are rendered and compiled, not merely checked as strings;
- the highest-fan-in untested runtime boundaries (`planner.ConsumeStream`, interrupt waits, tool identifiers/idempotency, registry gRPC adaptation, and repeated finalization) have deterministic behavioral and error-path tests;
- provider adapters share a minimum behavioral matrix for rate limits, malformed tool calls, stream errors, cancellation, structured output, usage, and terminal events;
- the MCP scenario layer covers sampling, roots, and progress or explicitly records the missing framework/spec implementation found by the required red test;
- no critical, non-generated package remains below 60 % statement coverage, and the high-churn DSL/expr/runtime/codegen owners are at or above 70 %;
- two fixed shuffle seeds, `make lint`, `make test`, `make itest`, and `make verify-mcp-local` are green before the testing campaign is considered complete.

Coverage percentages are guardrails, not the objective. Defect yield is tracked separately: every test that fails against current production code records the finding ID, failing contract, root cause, and fixing commit in this audit. If the DSL + compile + runtime phases find fewer than three distinct defects, perform deliberate fault-seeding against one validation, one generator, and one runtime error path; surviving faults mean the corresponding tests are too implementation-coupled and must be strengthened before moving on.

### Phase 1 — make every existing test unavoidable

**Findings:** C-1, C-2, V-5. **Expected defect yield:** immediate configuration failures plus failures formerly hidden in 158 dark integration functions and 29 Docker-gated registry tests; local and hosted selection/execution are proven.

Current contract inventory after the local repair:

- both CI jobs use Go `1.26.5`, matching the root, assistant, and agent-feature modules;
- CI uses Loom `v1.7.1`, matching `scripts/loom_core_mode.sh`;
- `make itest` selects assistant, agent_features, framework, and scenario tests and forces uncached execution;
- `registry.TestMain` preserves local no-Docker skips but CI requires the 29 Docker-backed tests to be available;
- hosted execution is verified by manual green runs 29202009227 (`7120e44`) and 29208887958 (HEAD `4ce0e09`); the prior two hosted runs exposed C-36 and C-37 before the first green rerun. The push trigger's four-month silence was the fork-workflows gate (push events were delivered but suppressed); the owner cleared it in the Actions tab on 2026-07-12.

Commit-sized work:

1. **Implemented locally:** CI uses Go `1.26.5`, Loom `v1.7.1`, and repository make targets.
2. **Implemented locally:** `make itest` owns all three integration clusters, including the assistant fixture.
3. **Implemented locally:** registry Docker coverage fails closed in CI and remains skippable without Docker locally.
4. **Implemented:** make itest enables MCP_CLI_TESTS and the hermetic CLI prompt scenario passes.
5. **Completed to current scope:** hosted manual run 29202009227 passes both jobs with strict Docker execution and retained coverage; final-tree parity is proven locally without manufacturing an unrelated trigger commit.

Proof complete: `make build`, `make lint`, `make test`, fixed-seed full race suites, uncached `make itest`, `make verify-mcp-local`, the strict Colima-backed unit suite, and hosted manual run 29202009227 are green.

### Phase 2 — attack design validation first

**Findings:** C-3, C-10, C-13. **Expected defect yield:** high; these are executable product contracts with roughly 53 historical error branches lacking assertions.

Owners and tests:

- add `dsl/history_test.go` using `runDSLExpectError` for duplicate `History`, non-positive values, `KeepRecentTurns`/compression conflicts, and invalid placement;
- extend DSL tables for `dsl/bounds.go`, registry duration parsing, and `Passthrough` resolution;
- add direct `ToolExpr.Validate()` tables in `expr/agent/tool_validation_test.go` for `Inject`, `ServerData`, paging, and bounded-result shapes;
- extend `expr/mcp/mcp_test.go` and agent expression tests so `Finalize()` is called twice and produces the same evaluated contract without duplication or panic;
- cover C-13 neighboring branches while the relevant tables are open.

Work red-green by validation cluster. Assert the exact stable message and the resulting expression state; do not merely assert `err != nil`. Proof after each cluster: targeted `go test -shuffle=on ./dsl ./expr/agent ./expr/mcp`, followed by all four repository gates because these packages are design/codegen inputs.

Progress: **Phase 2 exit criteria achieved.** 49/59 DSL error sites and more than 37/46 `ToolExpr.Validate` conditions have direct tests; DSL statement coverage is 73.3 %. Repeated `ToolExpr` and MCP finalization are covered. This phase found and fixed the C-14 nil-result panic; the ensuing full-gate run also exposed and fixed the C-15 assistant transport isolation defect. Move next to the generated-code compile matrix.

### Phase 3 — compile generated contracts

**Finding:** C-4. **Expected defect yield:** high; current string assertions can pass while emitted Go is uncompilable.

Extend `codegen/agent/mcp_executor_compile_test.go` so `writeGeneratedModule` drives a table of evaluated designs and runs `go build ./...` for each:

1. existing `FromMCP` toolset baseline;
2. local method-backed toolset projected through `Expose(AgentRuntime, MCPSurface)` and `MCPPlacement`;
3. registry-backed toolset with refresh/freeze specs and local JSON Pointer schema references;
4. prompt/resource-only MCP service with no declared tools;
5. injected-field payload shape, ensuring generated public codecs retain the field while advertised schemas hide it.

The design packages own these contracts; never patch emitted `gen/` files. Any generator fix must update the relevant golden and regenerate affected fixtures intentionally. Proof: targeted `go test ./codegen/agent ./codegen/mcp`, compile matrix green, `make regen-assistant-fixture` or `make regen-agent-feature-fixture` when their designs change, then the four repository gates.

Exit criterion: at least four distinct designs compile from freshly rendered output, and each high-risk string-only assertion identified by C-4 is paired with compile or behavioral proof.

Progress: **Phase 3 exit criteria achieved.** Five agent designs compile from freshly rendered service and plugin output. The new cases found and fixed three independent compile failures: named-type conversion bypass, invalid injected-field pointer assignment, and incorrect result-less service invocation. The prompt/resource-only MCP shape is deferred to the protocol phase, where it can be paired with real transport behavior rather than a vacuous agent-only build.

### Phase 4 — cover high-fan-in runtime and concurrency boundaries

**Findings:** C-6, C-9, C-10. **Expected defect yield:** medium to high because these paths fan into most agent executions but currently have little or no direct isolation.

Add deterministic tests at their owning packages:

- `runtime/agent/planner/stream_test.go`: `ConsumeStream` text/thinking/tool-call deltas, usage aggregation, metadata usage, EOF, receive errors, cancellation, and nil inputs using a raw model streamer only;
- `runtime/agent/interrupt/controller_test.go`: every wait operation, matching signal delivery, timeout/cancellation, and cross-signal isolation using an in-memory workflow context;
- `runtime/agent/tools/ident_test.go` and `idempotency_test.go`: malformed/empty identifiers, namespace splits, tag conflicts, and stable scope selection;
- `runtime/registry/grpc_client_adapter_test.go`: add a Docker-free `bufconn` generated-server contract instead of testing only a handwritten client mock;
- concurrency/reentrancy cases for repeated finalization and shared stream/session state, always under `-race`.

Proof: targeted race tests for each owner, then `make lint`, `make test`, `make itest`, and `make verify-mcp-local`.

Progress: **Phase 4 complete.** runtime/agent/tools is 96.4 %, runtime/agent/planner is 85.7 %, runtime/agent/interrupt is 94.1 %, and telemetry rose from 8.5 % to 95.6 % under race/shuffle. ConsumeStream preserves all token usage and error paths. The generated registry gRPC server/client/adapter chain is covered through bufconn. The stream bridge remains a documented public façade, and audited runtime ordering now uses owned signals rather than sleeps (C-10/S-7 resolved).

### Phase 5 — provider conformance and persistence reality

**Findings:** C-7, C-8, V-3. **Expected defect yield:** medium; Gemini and persistence encode/decode boundaries are the least uniformly covered.

Define one provider-neutral conformance case model, while keeping SDK-specific stubs in each adapter package. Every supported adapter must prove: rate-limit classification; ordinary provider errors; malformed tool input; stream setup and receive errors; context cancellation; structured-output/tool-choice compatibility; usage accounting; and exactly one terminal event. Fill Gemini first, then Bedrock keyword normalization, then Anthropic/Ollama/OpenAI gaps. Reuse helpers only for observable model contracts, never SDK internals.

Add testcontainer round trips for Mongo prompt and memory stores covering encode + persist + retrieve + decode, with a CI fail-fast switch matching Redis. Keep fast codec unit tests for local feedback.

Exit criterion: every provider passes the common matrix or documents an intentional unsupported capability with a tested error; both Mongo stores have at least one real round trip in CI.

Progress: **complete locally.** All five adapters pass `RunProviderConformance`; Gemini explicitly proves streaming unsupported while Anthropic, Bedrock, Ollama, and OpenAI prove setup, receive, usage, stop, and EOF behavior. Prompt and memory Mongo round trips pass under the strict Docker gate. Hosted execution remains C-1/C-2 rather than a Phase 5 selection gap.

### Phase 6 — protocol scenarios, then suite throughput

**Findings:** C-5, S-1, S-3, S-4, S-5, S-7, V-1, V-2. **Expected defect yield:** protocol work medium; speed work is confidence-enabling rather than directly bug-finding.

Start sampling, roots, and progress as real client-vs-framework validation tests in `integration_tests/framework` or scenario YAML. If a test proves the protocol surface is absent, treat it as MCP feature work and follow the repo's `new-mcp-feature-development` red-green workflow rather than weakening the test.

Only after the correctness phases are green:

- preserve the completed request-header/process-environment separation and uncapped framework parallelism;
- retain the completed `TestMain` cleanup and design a server-reuse boundary that preserves fresh initialization state per scenario;
- preserve the completed 3-run file-order contract, single conformance generation, deterministic TTL property, and explicit integration-only quickstart;
- preserve event/readiness synchronization; no blind ordering sleeps remain;
- preserve tool-search/OAuth layer isolation; share only transport/session mechanics when identical.

Exit criterion: warm unit wall at or below 25 seconds, integration wall at or below 90 seconds, no leaked prepared fixture directories after a run, and no reduction in the contract matrix or assertion strength. The unit target was revised from 20 seconds after test-level profiling showed the residual cost is race/coverage compilation and globally owned expression state rather than slow test bodies.

### Commit and review discipline

- Work directly on `main` as requested, one coherent phase slice per commit.
- Before every commit run targeted red-green checks and the proportionate repository gates; framework-scale changes require all four gates.
- Never commit a red tree, bypass hooks, hand-edit generated files, or mix an exposed bug with unrelated cleanup.
- Update this audit in the same commit that changes a finding's status. Record newly exposed bugs in a remediation ledger directly below this plan.
- After Phases 2, 3, and 4, pause for a defect-yield review. Reorder remaining work using actual failures found, not coverage percentage alone.

### Remediation ledger

| Finding | Exposing test | Root cause | Fix commit | Status |
|---|---|---|---|---|
| C-73 | TestRuntimeCatalogListsAreDeterministic / TestRuntimeCatalogDetachesRegisteredAndReturnedToolSpecs | public catalog lists inherited randomized map iteration, while registration and lookup copied ToolSpec values shallowly so callers could rewrite nested schemas, examples, bounds, confirmation, metadata, and server-data contracts | this change | fixed; catalogs sort deterministically and tool specs are recursively detached on registration and lookup |
| C-72 | TestFromErrorPreservesEveryWrappingLayer / TestConstructorsProduceUsefulMessages | `FromError` used `errors.As`, which skipped outer wrappers to the first nested ToolError and discarded execution context; empty `NewWithCause` with no cause also emitted a blank error | this change | fixed; only direct ToolErrors are reused, wrappers become explicit serializable cause nodes, and empty construction has a useful default |
| C-71 | TestUnmarshalCanonicalJSONDecodesCompleteShape | canonical slice decoding asserted a JSON array before its []byte special case, rejecting the standard base64 JSON string representation for every byte field | this change | fixed; byte slices delegate to encoding/json before generic array validation |
| C-70 | TestUnmarshalCanonicalJSONDecodesCompleteShape / TestUnmarshalCanonicalJSONReportsMapAndIndexFields | map decoding accepted named string key types but inserted a plain string reflect.Value, panicking for map[NamedString]T | this change | fixed; canonical keys convert to the destination's named string type before insertion |
| C-69 | TestTemporalWorkflowContextLifecycleAndChildIdentity | Temporal child handles carried a runID field but never resolved the SDK child-execution future, so RunID always returned empty | this change | fixed; RunID deterministically resolves and caches the started child execution identity |
| C-68 | TestMessageEventDataRoundTripsAndDetachesStructuredPayloads | user/assistant event maps and typed decoders retained mutable structured payload maps and byte slices directly | this change | fixed; JSON-compatible structured values are recursively detached on storage and decode |
| C-67 | TestToolResultDataDeepCopyIsolation | telemetry Extra and retry ExampleInput/PriorInput used shallow map copies, so nested consumer mutations rewrote stored event metadata | this change | fixed; nested JSON-compatible maps, lists, and bytes clone recursively |
| C-66 | TestToolResultDataDeepCopyIsolation | memory's Bounds clone copied Total and NextCursor pointers directly, allowing later mutations to rewrite durable bounds | this change | fixed; optional scalar pointers are copied into event-owned storage |
| C-65 | TestClusterLimiterUsesConfiguredBoundsAndEffectiveDefaults | clustered construction rebuilt min/max/recovery policy from the replicated current TPM and seeded raw zero values, so stale shared capacity could override the configured maximum and defaulted processes published an invalid zero budget | this change | fixed; configured policy is constructed once, effective defaults are seeded, and shared current TPM is clamped through replaceTPM |
| C-64 | TestDecodeToolResultBuildsDetachedRetryHint | issue-derived retry hints returned the tool spec's nested ExampleInput map directly, allowing a result consumer to mutate shared compiled tool metadata | this change | fixed; retry examples are recursively detached before leaving the executor |
| C-63 | TestHandleResultStreamEventRejectsNilEvent | a nil event delivered by a closing or malformed Pulse subscription was dereferenced before validation | this change | fixed; nil events terminate fail-closed with a traced error |
| C-62 | TestPrepareExecutionRejectsInvalidDependenciesAndSpecs/nil_spec | SpecLookup's pointer/bool contract allowed `(nil, true)`, but prepareExecution dereferenced that invalid lookup result | this change | fixed; inconsistent lookup results become structured tool failures |
| C-61 | TestGenAIMessagesAttrTreatsPointerPartsLikeValues | every model part's value-receiver marker makes pointer forms valid, but GenAI telemetry translated only values and mislabeled pointers as unknown parts | this change | fixed; pointer and value forms share normalization, including reasoning redaction and typed-nil omission |
| C-60 | TestTokenEstimatorCountsPointerAndValuePartsEqually | token estimation handled only value-form message parts even though the public Part interface also admits pointers, so pointer-backed text, images, documents, citations, and tools were omitted from preflight counts | this change | fixed; pointer and value representations share normalization and produce identical estimates |
| C-59 | TestMessageUnmarshalRejectsMalformedParts/null | the legacy raw-string fallback unmarshaled JSON null into an empty Go string and silently materialized an empty TextPart | this change | fixed; the fallback accepts actual JSON strings only |
| C-58 | TestMessageMarshalRejectsInvalidParts | the encoder emitted image, document, tool-use, and tool-result payloads that the same codec rejected during decoding | this change | fixed; shared validation makes the wire boundary symmetric and fail-fast |
| C-57 | TestMessageMarshalAcceptsPointersToEveryPartType | all part marker methods use value receivers, so pointers satisfy the public Part interface, but the encoder rejected every pointer representation as unknown | this change | fixed; all non-nil pointer and value forms normalize through one codec path |
| C-56 | TestMessageJSONSupportsLegacyStringParts | the decoder recognized a legacy string part but then passed the raw string to the TextPart object decoder, so documented history compatibility always failed | this change | fixed; legacy JSON strings decode directly to TextPart |
| C-55 | TestFromModelMessagesAcceptsPointerPartsAndDetachesReasoning | model part interfaces accept pointer implementations, but transcript import handled only value forms and silently dropped valid reasoning/text/tool-use parts | this change | fixed; value and non-nil pointer forms share the same conversion path |
| C-54 | TestFromModelMessagesPreservesAssistantMessageBoundaries | transcript import accumulated every assistant message into one open ledger message and never flushed between durable turns | this change | fixed; each source assistant message retains its provider-visible boundary |
| C-53 | TestWorkflowHandleDelegatesWaitSignalAndCancel | handle-based Temporal cancellation returned raw FailedPrecondition/NotFound errors while direct cancellation and signaling normalized the same backend outcomes to engine contract errors | this change | fixed; every Temporal handle/direct signal and cancellation path shares mapSignalError |
| C-52 | TestStreamSubscriberAwaitAuthorizationAndUsageMatrix/questions | await_questions hooks carried canonical tool arguments, but the independently selectable stream event omitted them and could not be self-contained when ToolStart was disabled | this change | fixed; AwaitQuestionsPayload includes a copied canonical payload |
| C-51 | TestStreamSubscriberDetachesMutablePayloadBuffers | stream translation retained hook-owned JSON and redacted-reasoning byte slices, allowing later producer mutation to change already delivered events | this change | fixed; every translated raw payload/result/reasoning buffer is detached at the stream boundary |
| C-50 | TestUpsertRunRejectsSessionReassignment / TestUpsertRunRequiresExistingSessionAndStableAssociation | both stores allowed an existing durable run ID to move between sessions during an update | this change | fixed; run SessionID is immutable in both backends |
| C-49 | TestStoreHonorsCanceledContexts | every in-memory session-store method ignored caller cancellation and could mutate state after the operation was canceled | this change | fixed; all public store operations fail before reading or mutating on canceled contexts |
| C-48 | TestSessionLifecycleAndRunQueries / TestMongoDriverV2SessionLinkChildRunTransaction | both stores allowed new runs and child links to materialize after their session became terminal | this change | fixed; new run/link ownership is checked atomically and existing terminal run updates remain allowed |
| C-47 | TestUpsertRunRequiresExistingSessionAndStableAssociation / TestMongoDriverV2SessionLinkChildRunTransaction | both stores allowed orphan runs whose SessionID had no corresponding session record | this change | fixed; new run insertion requires an existing active session in-memory and transactionally in Mongo |
| C-46 | TestCompileHintTemplatesSupportsOptionalFieldsTypedCountsAndUnicode | truncate sliced UTF-8 strings by byte offset, producing invalid user-visible text when the limit split a multi-byte code point | this change | fixed; truncation operates on Unicode code points |
| C-45 | TestCompileHintTemplatesSupportsOptionalFieldsTypedCountsAndUnicode | count recognized only `[]any`, returning zero for ordinary typed slices used by generated payloads | this change | fixed; count supports arrays, slices, maps, strings, channels, and pointer-wrapped collections |
| C-44 | TestCompileHintTemplatesSupportsOptionalFieldsTypedCountsAndUnicode | templates documented optional missing-field support but compiled with missingkey=error, causing the entire hint to disappear instead of taking with/if fallbacks | this change | fixed; missing fields evaluate to zero values as documented |
| C-43 | TestHookCodecRejectsInvalidEnvelopes | exported hook codec entry points dereferenced nil envelopes/events instead of returning validation errors | this change | fixed; nil and typed-nil events plus nil inputs fail closed |
| C-42 | TestHookCodecRoundTripsEveryEventType/tool_call_scheduled | ToolCallScheduled decoding reconstructed the event through a constructor that did not accept DisplayHint, silently discarding the transported display metadata | this change | fixed; decoded events restore DisplayHint and all 26 variants are payload-equivalent after a round trip |
| C-41 | TestWorkflowContextSignals | signaling a completed workflow selected nondeterministically between the closed completion channel and a writable buffered signal channel | this change | fixed; completion is checked before attempting signal delivery |
| C-40 | TestChildWorkflowReportsRunID | the in-memory child-handle adapter discarded the underlying run identifier | this change | fixed; in-memory child handles preserve the started run ID |
| C-39 | TestWorkflowHandleCancelStopsRun / TestCancelByIDStopsRunAndRejectsUnknownID | both cancellation entry points returned success without canceling the workflow context, and runs were not addressable by ID | this change | fixed; live handles and cancel functions are tracked and terminal status becomes canceled |
| C-38 | TestStartWorkflowRejectsDuplicateRunID | StartWorkflow marked status after validation but never reserved the required engine-unique workflow ID | this change | fixed; ID and handle are reserved atomically before execution starts |
| C-37 | hosted `make test` / TestRegisterSeedsHealthForImmediateCallTool | registration and pong writes returned after the Redis commit but before the local replicated-map reader observed that revision, violating immediate post-register health | this change | fixed; catalog and health writes wait for their committed local revision |
| C-36 | hosted `make test` / TestSkillToolsetRegistrationExposesSkillsAsModelTools | skill-resource MIME detection delegated to the host MIME registry, so Markdown was `text/plain` on macOS and `text/markdown` on Ubuntu | this change | fixed; MIME classification is deterministic and directly table-tested |
| C-35 | TestInMemoryStoreHistoryNewestFirst with a fixed clock | two prompt overrides could receive equal wall-clock timestamps, making the documented newest-first tie ambiguous | this change | fixed; store timestamps advance monotonically under lock |
| C-34 | TestClientConformance/stream_terminal (Anthropic) | real Anthropic streams split input/cache usage into `message_start` and output usage into `message_delta`, but the processor discarded the start usage | this change | fixed; full usage is accumulated before the terminal event |
| C-33 | TestClientConformance usage_accounting/stream_terminal (Anthropic) | response and stream translators did not carry resolved model or requested model class into usage | this change | fixed in both modes |
| C-32 | TestClientConformance/malformed_tool_call (Anthropic) | non-streaming tool-use blocks copied provider `json.RawMessage` directly without validating it | this change | fixed; invalid JSON fails before entering workflow payloads |
| C-31 | TestClientConformance usage_accounting/stream_terminal (OpenAI) | the Responses API translator and stream processor received resolved model IDs but not `Request.ModelClass` | this change | fixed in completion and official-SDK stream usage |
| C-30 | TestClientConformance usage_accounting/stream_terminal (Ollama) | completion and stream usage builders received model IDs but not the requested `ModelClass` | this change | fixed in both HTTP response paths |
| C-29 | TestClientConformance/rate_limit (Ollama) | non-200 HTTP handling returned status text without classifying 429 as `model.ErrRateLimited` | this change | fixed through the shared complete/stream status mapper |
| C-28 | TestClientConformance/usage_accounting (Gemini) | Gemini response translation received only the resolved model ID, so it could not preserve `Request.ModelClass` in `TokenUsage` | this change | fixed; high-reasoning class and resolved model are both asserted |
| C-27 | full assistant fixture after adding report_progress | ToolSearch narrow mode's activation threshold excluded strong name/title contains matches, allowing unrelated fuzzy subsequence candidates beside a high-confidence match | this change | fixed; existing one-result SDK/JSON-RPC search contracts green |
| C-26 | TestGeneratedServerSDKToolReportsProgressToClient | generated tool contexts discarded request `_meta.progressToken` and exposed no session-backed progress reporter | this change | fixed; three exact monotonic notifications green over HTTP |
| C-25 | TestGeneratedServerSDKToolCanListClientRoots | generated SDK request contexts exposed elicitation only and had no transport-neutral roots/list bridge | this change | retired; roots are deprecated in MCP 2026-07-28 and the compatibility API was removed |
| C-24 | TestGeneratedServerSDKToolCanSampleThroughClient | generated SDK telemetry wrapper did not expose `Unwrap`, so the official SDK could not flush nested server-to-client messages while a tool call was active | this change | retired; sampling is deprecated in MCP 2026-07-28 and the compatibility API was removed |
| C-23 | TestMongoDriverV2MemoryRoundTrip | memory upsert updated events through both setOnInsert and push, which real Mongo rejects as a conflicting path | this change | fixed; four Mongo 7 contracts race/shuffle green |
| C-22 | TestAnthropicStreamerRejectsEOFBeforeMessageStop | Anthropic stream EOF was treated as success without observing the required message_stop event | this change | fixed; partial streams fail closed |
| C-21 | TestConsumeStreamErrorsAndAlwaysCloses | ConsumeStream discarded Streamer.Close errors on both EOF and receive-error paths | this change | fixed; close and receive errors remain discoverable |
| C-20 | TestConsumeStreamAggregatesChunksAndEvents | planner usage aggregation omitted cache read/write token fields | this change | fixed; all numeric usage fields aggregate |
| C-19 | TestConsumeStreamAggregatesChunksAndEvents | a malformed ChunkTypeToolCall with nil ToolCall was dereferenced | this change | fixed; malformed chunk ignored |
| C-18 | payload-only bound-method compile case | generated wrappers assumed every service method returned `(result, error)`; result-less Goa methods return only `error` | this change | fixed; generated module compiles |
| C-17 | injected-payload compile case | `inject.go` assigned `*string` to required injected fields generated as `string` | this change | fixed; generated module compiles |
| C-16 | method-backed projection compile case | alias detection compared schema/user-type names but ignored that tool and service types are distinct named Go types in different packages | this change | fixed; generated module compiles |
| C-15 | full make itest + repeated assistant fixture | parallel SDK clients shared the global HTTP transport, allowing cross-test connection-pool lifecycle interference | `614d111` | fixed; 20× fixture + 50× race stress green |
| S-2 | shuffled `dsl` suite, seeds `101` and `202` | `resetDSLRoots` omitted `mcpexpr.Root`; three `expr/agent` tests leaked replaced globals | `2752473` | fixed |
| S-8 | make verify-mcp-local / TestGeneratedJSONRPCServerEventsStreamPublishesNotifications | the fixture treated flushed SSE response headers as subscription readiness even though broadcaster registration occurs afterward, allowing a notification to publish into the gap | this change | fixed; a test-owned broadcaster signals after synchronous session subscription; 100 focused and 10 full-fixture repetitions green |
| C-1 | CI contract inventory + uncached `make itest` | workflow used Go `1.25.x`, Loom `@latest`, selected only 6 integration functions, and allowed cached scenario passes | this change | all 188 integration funcs plus quickstart green locally and hosted |
| C-2 | strict no-socket probe + full Colima registry suite | `TestMain` converted every Docker/container failure into skips even in CI | this change | 29 tests green locally and hosted |
| C-14 | nil-result ServerData validation regression | `validateServerDataShapes` dereferenced `t.Method.Result` before checking it | `5dce735` | fixed |

## 7. Method notes

- **This is a refresh audit.** Baseline `TEST_AUDIT.md` was produced earlier today at `679fb76`; every subsequent remediation slice updates its affected evidence and measurements in place. The original refresh re-ran every mandatory repo-wide grep and used focused cluster reviews across dsl/expr, codegen, features, runtime+registry, and integration tests.
- **Executed:** repeated full unit runs (`-race -covermode=atomic -count=1`), final-tree full-suite race runs with fixed shuffle seeds `20260712` and `7302026`, random shuffle, coverage aggregation, per-fix-commit test-presence checks, repeated integration ladders, strict Redis and Mongo 7 runs through Colima, and focused process-exit artifact checks. Machine: 10-core arm64 mac, Go 1.26, loom mode = remote (v1.7.1).
- **Timing discipline:** the original refresh's run 1 included ~3 min of delta-induced recompilation and subagent load. The throughput slice was re-measured with its canonical flags: 41.8 s after recompilation, then 21.24–24.05 s fully warm. Older 98–111 s figures are not comparable because their subprocess/build caches differed (S-6).
- **Not run:** multi-process shard experiment, mutation tooling (none configured), hook timing (hooks are lint-only). Both Docker-backed Redis and Mongo contracts omitted from the baseline were run during remediation via Colima.
- **Artifact-cleanup probe:** before remediation, exact generated prefixes accounted for 93 prepared fixture roots (125 MB) and 218 temporary server binaries (8.2 GB). After removing that historical debris, a real `MCP_CLI_TESTS=true` scenario process exited green and left 0/0 artifacts. A one-server-per-YAML experiment was rejected after the protocol group correctly failed initialization-isolation cases; per-scenario servers remain the current correctness boundary.
- **Integration parallelism probe:** removing the ineffective `MCP_*` parent-environment mutation and `-parallel 1` cap kept the full race-enabled command green in 178.2 s. The framework package fell from 38.9 s to 18.1 s and now overlaps the 177.3 s stateful scenario package; scenario startup, not framework serialization, is the remaining critical path.
- **Baseline integration:** all green — agent_features 0.6 s, framework 37.6 s, scenario suite (`./integration_tests/...` with itest flags) 172.5 s, assistant fixture 1.1 s. The baseline audit's working-tree WIP (which broke `make itest` at the time) landed as the delta commits and its scenario failures are gone. Scenario wall (172.5 s vs 203.5 s cold at baseline) reflects warm codegen/build caches, consistent with S-4's per-scenario boot cost dominating.
- **CI-remediation verification:** workflow YAML parses; `make build`, `make lint`, shuffled race-enabled `make test`, fixed-seed full race suites (`20260712`, `7302026`), uncached expanded `make itest`, and `make verify-mcp-local` are green. The latest local scenarios ran in 74.4 s. With Colima exported through `DOCKER_HOST` and `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE`, the complete strict unit gate passed at 69.7 % coverage; without a discoverable/mountable socket, it failed closed. Hosted manual runs: 29202009227 passed on `7120e44` (integration 2m14s, build 7m42s) and 29208887958 passed at HEAD `4ce0e09` (integration 2m38s, build 4m15s). The push-event silence was root-caused via the GitHub Events API: all pushes were delivered as `PushEvent`s but no runs were created, dispatch worked on the same commits, and the sibling repo `CaliLuke/loom` (246 runs) triggers on push normally — isolating the fork-workflows gate, which the owner cleared in the Actions tab on 2026-07-12. Workflow actions were bumped to `checkout@v7`/`setup-go@v6`/`upload-artifact@v7` afterward to clear Node 20 deprecation annotations; the bump is not yet exercised by a hosted run.
