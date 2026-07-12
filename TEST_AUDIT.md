# Test Suite Audit — loom-mcp

Audited: 2026-07-11 (refresh + first remediation) · Baseline commit: `4f2c262` (main) · Auditor: test-suite-audit skill
Baseline: prior audit at `679fb76` earlier the same day; this refresh re-measured everything at HEAD (30 commits, 116 files, +4,227/−1,438 later) and re-verified every prior finding.
Recent remediation commits: `2752473` (shuffled order hygiene), `3392ddd` (complete CI selection), `fb333e2`/`93dd57c`/`49bfddd` (validation coverage).

Remediation progress: **S-2 is resolved; C-1/C-2 are locally repaired and fully verified, with only an actual hosted Actions run pending; C-3 is now 26/59 at the DSL layer and above its 37/46 `ToolExpr.Validate` target.** CI uses the correct pinned toolchain and complete uncached gates. `ToolExpr.Validate` is 93.9 % statement-covered, all unsupported MCP projection features are asserted, and `ToolExpr.Finalize` is tested for idempotency. The validation campaign exposed and fixed C-14, a nil method-result panic in ServerData validation.

## 1. Executive summary

The suite grew slightly (303 test files, 1,439 top-level test functions, +37 since `679fb76`) and every one of the 30 delta fix commits landed with tests — regression discipline is now 45/45 across both audit passes, and the new runtime/registry tests are genuinely behavior-asserting (bounded-cache eviction, race-hardened error precedence, subprocess signal handling). The first structural remediation is complete: the DSL order-coupling defect is fixed and shuffle is enforced by `make test`. The CI configuration repair is now implemented locally: its toolchain matches the modules, repository make targets own the commands, `make itest` reaches all 164 integration functions, and Docker-backed registry coverage fails closed in CI. **An actual green Actions run remains the immediate blocker** because the repository still reports zero historical runs; code inspection cannot prove repository-level Actions permissions or hosted-run behavior. The other high/medium completeness and value findings remain open. The `.tmp` fixture leak is still 77 dirs / 103 MB, and the bedrock structured-schema fix (`c5f293a`) still has roughly 10 neighboring keyword branches without direct coverage.

## 2. Inventory (cross-repo baseline metrics)

| Metric | Value (change vs `679fb76`) |
|---|---|
| Languages / frameworks | Go 1.26 (arm64); Goa/Loom DSL+codegen; MCP go-sdk v1.6.1; loom fork v1.4.0 (remote mode) |
| Test runner(s) | `go test` via `make test` (`-shuffle=on`) / `make itest` / `make verify-mcp-local`; testify, gopter, testcontainers (Redis, Mongo) |
| Test files / test cases | 303 files (+3), 1,439 `func Test` (+37); unit run executed 1,276 top-level (+66), 0 fail |
| Layer split (unit / integration / e2e) | ~1,275 unit funcs; 164 integration-cluster funcs (framework 25, tests 6, assistant 124, agent_features 9; was 154); 1 e2e quickstart inside the unit target |
| Total wall time (local) / (CI pipeline) | Unit (`-race -covermode=atomic`): **30.1 s fully warm** (69.7 s CPU), ~29.6 s test phase + ~3 min compile after the 30-commit delta (cold build). Integration (itest + assistant fixture, warm): 212 s, all green. CI: **never executed** (`gh api …/actions/runs` → `total_count: 0`) |
| Parallelism today | Package-level only; **2.3 of 10 cores** warm (69.7 s CPU / 30.1 s wall). `t.Parallel()` at 386 sites (+10). `make itest` still forces `-parallel 1` |
| Coverage (line / branch) | **59.7 %** statements (+0.3); branch N/A (Go). registry/ 34 % (understated by Docker skips); 4 zero-coverage packages remain attribution artifacts (C-12) |
| Skipped/disabled tests | 39 runtime skips (29 Docker-gated registry funcs — was 28 — + Mongo clientinfra + helpers); 2 permanent `t.Skip` now aged ~7.5 months; `MCP_CLI_TESTS` suite still dark |
| Snapshot tests (count / size on disk) | 53 `.golden` / 276 KB / 18 > 100 lines (net zero new goldens in delta; 2 modified) |
| Sleeps in tests (count / summed literal seconds) | 13 sites / ≈1.35 s literal (≈0.35 s effective: the new 1 s sleep at `registry/registry_test.go:27` is a subprocess fail-safe that normally never elapses). Integration tests: 0 sleeps |
| Bug-fix commits with tests (sampled ratio) | **30/30** in the delta (measured, not sampled); cumulative 45/45 |
| Sampling performed | 5 cluster subagents re-verified every prior finding + read the full delta; every mandatory grep re-run repo-wide at HEAD; 2 measured unit runs + 2-seed shuffle probe + integration ladder executed; ≥1 citation per subagent spot-checked in main context |

## 3. Findings

Finding IDs continue the `679fb76` numbering; each carries a status vs that baseline.

### C-1: CI has never run; local configuration is repaired, hosted execution unverified
- **Severity:** blocker · **Status:** configuration fixed, Actions run pending
- **Confidence:** measured
- **Evidence:** Hosted history remains `0` runs. Locally, `.github/workflows/ci.yml` now installs Go `1.26.1` in both jobs, pins Loom `v1.4.0`, runs `make build`, `make lint`, `make test`, and `make itest`, and caches all fixture module sums. `Makefile.itest` now owns assistant (124 funcs), agent_features (9), framework (25), and tests (6): 164/164 integration functions are selected by the canonical target, with `-count=1` preventing a cached scenario pass from masquerading as execution. `actionlint` and the complete local gate ladder pass; the uncached scenario package ran in 178.6 s.
- **Impact:** The selection defect is fixed, but reassurance is still incomplete until GitHub executes the workflow successfully; hosted permissions, Docker availability, and runner-specific behavior remain unproven.
- **Recommendation:** Finish local gates, commit, and verify the first actual Actions run. If the run count remains zero after the commit reaches GitHub, repository owner/admin intervention is required.

### C-2: Docker-gated registry tests fail closed in CI and pass locally via Colima
- **Severity:** high · **Status:** locally resolved, hosted confirmation pending
- **Confidence:** measured
- **Evidence:** The same 29 functions still use the shared Redis testcontainer. `registry.TestMain` now separates setup/cleanup, reports cleanup failures, and honors `LOOM_MCP_REQUIRE_DOCKER_TESTS=1`; CI sets that switch for `make test`, turning Docker/container startup failure into exit 1 instead of 29 skips. The switch has a focused table test, a no-socket strict probe failed closed with the expected message, and the full registry package passed against Colima/Redis under `-race -shuffle=on -count=1` in 71.8 s using the explicit Colima Docker socket.
- **Impact/Recommendation:** Verify all 29 execute on the hosted runner. Continue extracting hermetic sync logic where practical, but do not weaken the fail-closed CI contract.

### C-3: DSL/expr validation — ToolExpr target achieved, DSL sites remain
- **Severity:** high · **Status:** in progress (DSL error sites 6/59 → 26/59)
- **Confidence:** measured
- **Evidence:** 59 `eval.ReportError`/`InvalidArgError` sites exist in `dsl/*.go`; **26 are tested message-by-message**. `expr/agent/tool.go` is now above the planned 37/46 direct-condition threshold: `Validate` is 93.9 % statement-covered; Inject, MCP unsupported features, paging, tool-return bounds, and method-result bounds helpers are 96–100 %; ServerData and shape validation are above 94 %. Binding resolution and unsupported data shapes have exact error tests. `ToolExpr.Finalize` is called twice after successful validation and preserves the resolved method and shape hashes.
- **Impact:** The central expression validator is no longer a major blind spot. The remaining risk is concentrated in 33 DSL-level error sites that can reject or corrupt authored designs before expression validation.
- **Recommendation:** Preserve the ToolExpr matrix and raise DSL message-level coverage from 26/59 to at least 48/59.

### C-4: Only 2 files / 3 funcs of 231 codegen tests compile the generated output
- **Severity:** high · **Status:** still open (pattern reconfirmed in the delta)
- **Confidence:** measured
- **Evidence:** 231 `func Test` in `codegen/` at HEAD; real `go build` of generated output only in `codegen/agent/mcp_executor_compile_test.go:41` and `codegen/agent/tests/quickstart_test.go:223`. `projection_contract_test.go:58` still string-only. The delta reproduced the two-layer pattern exactly: `0dc4bce` and `0a0e261` added **string/signature-only** assertions at the codegen layer (`registry_toolset_specs_test.go:79-82` asserts the generated source *contains* `"func resolveLocalSchemaRef"`; `registry_client_test.go:42` asserts a method signature substring) while the real behavioral coverage went into the `agent_features` fixture module (+72/+64-line compiled tests) — which only runs via `make itest`, i.e. behind the C-1 gate.
- **Impact:** Unchanged: string assertions pass on uncompilable output, and the compensating fixture tests are dark until CI exists.
- **Recommendation:** Unchanged: extend the `writeGeneratedModule` harness with a design table (projected-only first).

### C-5: MCP spec areas with zero coverage anywhere: sampling, roots, progress
- **Severity:** medium · **Status:** still open (no delta movement)
- **Confidence:** measured
- **Evidence:** Re-grepped at HEAD: 0 hits for sampling/roots/progress across `integration_tests/scenarios/*.yaml`, framework, and fixture tests. `git diff --stat 679fb76..HEAD -- integration_tests/scenarios/` → empty (no scenario changes in 30 commits despite 13 MCP-runtime fix commits). Scenario counts unchanged: tools 24, protocol 18, resources 11, prompts 7, notifications 4, prompts_cli 1 (= 65).
- **Impact/Recommendation:** Unchanged; also note the delta's MCP fixes (protocol-version alignment, id-less inputs, SSE reconnect) were tested at fixture level, not as scenarios — the scenario layer is drifting from the runtime's actual behavior surface.

### C-6: Live, high-fan-in runtime packages with zero direct tests
- **Severity:** medium · **Status:** still open (all five sub-items re-verified)
- **Confidence:** measured
- **Evidence at HEAD:** `runtime/agent/tools/` — still no `*_test.go`. `runtime/agent/interrupt/` — still none. `runtime/agent/telemetry` — `Clue*` implementations referenced by zero tests (`clue.go:40,47,56`). `runtime/agent/planner/stream.go:31 ConsumeStream` — still zero callers and zero tests. `runtime/agent/stream/bridge` — still zero production importers (sole importer is an example test). Hot-file coverage: `hooks/events.go` 29 %, `stream/subscriber.go` 51 % (12-month churn 25 and 30 commits respectively).
- **Impact/Recommendation:** Unchanged.

### C-7: Model-adapter error/streaming matrix holes; Gemini worst — plus a new bedrock gap
- **Severity:** medium · **Status:** still open, one new sub-gap
- **Confidence:** measured
- **Evidence:** Gemini untouched in delta: mock still returns `m.response, nil` unconditionally (`gemini/client_test.go:392-397`, no generate-error field in the mock struct), `isRateLimited` (`client.go:877`) and `Stream` (`client.go:242`) still unexercised. Bedrock: zero hits for `smithy|ThrottlingException|429` in `bedrock/*_test.go` — the real error-shape classification (`client.go:559-579`) still only tested via injected sentinel. Malformed-SSE/NDJSON: still zero byte-level decode-failure tests in any adapter; all three ollama stream-failure branches (`stream.go:82,92,97`) untested. **New:** `c5f293a` added keyword-classification maps (`structured_output_schema.go:67-83,141-145`) — `if/then/else`, `propertyNames`, `dependentSchemas`, `prefixItems`, `contains`, `not`, `allOf`, `anyOf` recursion branches are all untested (only `oneOf` + `properties` covered by the new tests). Positive: all three bedrock fixes and the anthropic cache-checkpoint feature (`4f2c262`) landed with genuinely behavioral tests (copy-on-write assertions, real `document.NewLazyDocument` error injection, cache-control placement checks).
- **Impact:** Provider error classification and schema normalization change most across SDK bumps; a mis-classified `anyOf` or a real smithy throttle shape regressing would pass the suite.
- **Recommendation:** Unchanged (shared adapter conformance table) + a keyword-table test for `normalizeBedrockSchemaNode` covering every entry in the two maps.

### C-8: Mongo query syntax for prompt/memory stores never executes against real Mongo
- **Severity:** medium · **Status:** still open (delta improved decode-side coverage only)
- **Confidence:** measured
- **Evidence:** All four `clients/mongo/client_test.go` still fake-only (hand-rolled `fakeCollection`/`match()`); the only real-server test still covers runlog-append + session-transaction and skips without Docker (`clientinfra/mongo_driver_integration_test.go:122`). `features/runlog/mongo/store.go` still has zero tests. Delta improvement: `66b47c1` added a **real** `bson.Marshal/Unmarshal` round-trip test (`session/.../client_test.go:111-135`) and `NormalizeBSONValue` unit tests with real `bson.D`/`bson.A` — metadata normalization would now catch a real-BSON regression; query *filter* execution remains untested.
- **Impact/Recommendation:** Unchanged.

### C-9: No hermetic contract test between registry gRPC server and client adapter
- **Severity:** medium · **Status:** still open
- **Confidence:** measured (upgraded from inferred: `grep -rln bufconn registry/ runtime/registry/` → 0; adapter tests use a hand-written `mockGRPCRegistryClient`, `grpc_client_adapter_test.go:14`)
- **Impact/Recommendation:** Unchanged — `bufconn` contract test, Docker-free.

### C-10: Concurrency under-exercised; Finalize idempotency untested
- **Severity:** medium · **Status:** partially resolved (`ToolExpr.Finalize` covered; MCP/concurrency remain)
- **Confidence:** measured
- **Evidence:** `TestToolExprFinalizeIsIdempotent` now validates a real method binding, calls `Finalize()` twice, and proves the method pointer plus Args/Return hashes remain stable. `MCPExpr.Finalize` is still called once per fresh object in its four subtests; sleep-choreographed ordering and broader concurrency gaps remain.
- **Recommendation:** Add repeated MCP finalization next, then address the remaining race/ordering cases under their owning packages.

### C-11 (positive): Regression discipline is excellent — now measured at 30/30 on the delta
- **Severity:** low (positive) · **Status:** reconfirmed, stronger
- **Confidence:** measured
- **Evidence:** Every one of the 30 fix commits in `679fb76..HEAD` touched test files (checked individually via `git show --stat`). Quality spot-reads: `63f3a22` asserts cap size, FIFO eviction order, and concurrent-writer safety; `91028a1` asserts timeout bounding with elapsed-time and detached-context assertions; `eb3911e` race-hardened 1000-iteration loop. Not echoes.
- **Recommendation:** Preserve the norm; none.

### C-12: Four "0 % coverage" packages are attribution artifacts, not gaps
- **Severity:** low · **Status:** unchanged (re-verified in HEAD coverage profile: `codegen/testhelpers`, `registry/design`, `registry/gen`, `testutil` at 0 % with live test-time importers)
- **Confidence:** measured
- **Recommendation:** Unchanged.

### C-13 (new): Residual untested branches introduced by the delta's own fixes
- **Severity:** low
- **Confidence:** measured
- **Evidence:** (a) `providerServeCause` (`runtime/toolregistry/provider/provider.go:278`): new test exercises only the `errc`-ready branch, not the `default → ctx.Err()` fallback. (b) `15bcc4b` codifies a validation split: programmatic `registry.New()` rejects only *negative* health config; zero/`<100ms` rejection lives solely in `cmd/registry/main.go` env parsing — `New()` accepts a zero interval. (c) `ConsumeDeferredSkillDirectory` (`expr/mcp/root.go:255`): return-false/mid-slice-removal path untested. (d) `markLastContentBlockCached` (`anthropic/client.go:426-436`): "no cacheable preceding block" silent-drop path unasserted. (e) New global-mutation coupling: `mcp_generated_server_metadata_test.go:358-361` mutates `MCPMaxRequestBodyBytes` (correctly restored via `t.Cleanup`, correctly not parallel — but it is new shared-global test coupling).
- **Impact:** Individually low; collectively the delta's fix-test pairs cover the fixed branch but not the neighboring branches the fix created.
- **Recommendation:** Fold into the C-3/C-7 table work; add a `New()`-level zero-interval rejection (or a test documenting the intended split).

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

### V-1: Assistant-fixture consolidation — tool-search 65 funcs / OAuth 12 funcs across 7 files
- **Severity:** medium · **Status:** still open (counts re-verified at HEAD: 41+10+14 tool-search, 5+3+3+1 OAuth)
- **Confidence:** measured
- **Recommendation:** Unchanged — table-drive during other fixture work.

### V-2: codegen/mcp re-runs full `Generate` redundantly
- **Severity:** medium · **Status:** still open (one cosmetic improvement)
- **Confidence:** measured
- **Evidence:** `state_test.go:56-68` still 13 Generate calls (1 + `for range 12`). `transport_conformance_contract_test.go` now routes through one helper (`generateTransportConformanceFixture:19`) — but no memoization, invoked 7× (file grew to 7 tests in delta). `contract_test.go`: 15 Generate calls / 55 tests. Package is the suite's critical path: 20.6 s of the 30.1 s warm wall.
- **Recommendation:** Unchanged: 13→3 iterations; `sync.Once` the conformance fixture; group contract tests onto cached outputs.

### V-3: Parametrization candidates across four clusters
- **Severity:** low · **Status:** still open (spot-verified: dsl registry-security ×5 and from-registry ×4 intact; subscriber profile-toggle ×4 at same lines; `collectStreamChunks` still byte-identical in ollama:413/openai:1043; cache_property ×4)
- **Confidence:** measured
- **Recommendation:** Unchanged — fold into C-3/C-7 work.

### V-4: Tautological/echo tests — rare, unchanged (one grew)
- **Severity:** low · **Status:** still open
- **Confidence:** measured
- **Evidence:** `expr/agent/toolset_test.go:358-397` echo test now **3** subtests (was 2 — the delta added "registry provider" echo); `expr/mcp/mcp_test.go:264-279` unchanged. Repo-wide greps at HEAD: assert-true equivalents 0, commented-out tests 0.
- **Recommendation:** Delete the echo subtests.

### V-5: Dead weight — unchanged
- **Severity:** low · **Status:** still open
- **Confidence:** measured
- **Evidence:** Both legacy skips intact (`codegen/agent/default_adapters_test.go:23`, `service_toolset_test.go:24`, now ~7.5 months old). `DeleteMeta`/`SetDescription`/`SetTitle` still 0 callers repo-wide. `stream/bridge` still 0 production importers. `MCP_CLI_TESTS` still set nowhere (`integration_tests/tests/mcp_integration_test.go:78-79`).
- **Recommendation:** Unchanged.

### S-1: Top 5 % of tests = 95 % of summed test time; but warm wall is now the real story
- **Severity:** high (as dev-loop finding, reframed) · **Status:** still open, magnitudes updated
- **Confidence:** measured
- **Evidence:** Run 1 (cold build after delta, under subagent load): 1,276 tests, 43.1 s summed; top 63 tests = 41.0 s (95 %); `TestQuickstartGeneratesAndRuns` 12.3 s, `TestGenerate_FileOrderIsStableAcrossRuns` 10.7 s, `TestCacheExpirationAfterTTLProperty` 5.2 s. Run 2 (identical flags, fully warm, idle): **30.1 s wall / 69.7 s CPU**; slowest packages codegen/mcp 20.6 s, codegen/agent/tests 13.7 s, runtime/registry 10.0 s. The dominant *perceived* cost is compile: the cold build after 30 commits took ~3 min before the first test event (S-6).
- **Impact:** On a warm cache the suite is already fast (30 s); the quickstart+FileOrder+gopter trio still sets the package critical path and balloons to 40–100 s whenever subprocess/build caches are cold.
- **Recommendation:** Unchanged: quickstart out of `make test` (honor `-short`), FileOrder 13→3, share the conformance fixture, tune the TTL property test.
- **Achievable target (arithmetic):** warm: 30.1 s ≈ compile stat-check (~4 s) + codegen/mcp chain (20.6 s) + tail. After V-2 (codegen/mcp → ~8–10 s) and quickstart removal (codegen/agent/tests → ~1 s) and gopter tuning (registry → ~5 s): critical path ≈ **~15 s warm**. Cold-build cost (~3 min after a big delta) is `-race -covermode=atomic` recompilation and is unaffected by test changes.

### S-2: Order coupling in `dsl` — resolved and shuffle enforced
- **Severity:** high · **Status:** resolved
- **Confidence:** measured
- **Evidence:** `resetDSLRoots` now recreates and registers `mcpexpr.Root`; the three `expr/agent` tests that replace Goa/MCP roots call `preserveGlobalRoots`, which restores both roots with `t.Cleanup`. The complete non-integration package set passes with `-shuffle=101` and `-shuffle=202`, and the race-enabled `make test` passes with randomized order. `Makefile` now includes `-shuffle=on` in the normal unit gate.
- **Recommendation:** Complete. Keep shuffle in the canonical test target; the next parallelism blocker is S-3's process-global environment channel.

### S-3: `os.Setenv` in the framework still forces `-parallel 1` for all integration tests
- **Severity:** medium · **Status:** still open (file touched in delta, block untouched)
- **Confidence:** measured
- **Evidence:** `runner_transport.go:425` still `os.Setenv` for `MCP_`-prefixed scenario headers, zero `os.Unsetenv` in the package; `Makefile:44` still `-parallel 1`. 386 `t.Parallel` declarations sit neutralized behind it.
- **Recommendation:** Unchanged.

### S-4: Per-scenario server boot + prepared-clone leak — leak nearly doubled
- **Severity:** medium · **Status:** still open, worse
- **Confidence:** measured
- **Evidence:** Still one `framework.NewRunner()` + `startServer` per scenario (~65 scenarios; `runner_lifecycle.go:19` 200 ms poll). Prepared clone still removed only on error (`runner_fixture_prep.go:467`); still no `TestMain` anywhere in `integration_tests/`. Leak now **77 dirs / 103 MB** (was 42 / 54 MB five hours earlier — this audit's own runs contribute).
- **Recommendation:** Unchanged: boot once per YAML; `TestMain` cleanup.

### S-5: Core utilization 2.3/10; global expr roots serialize the expensive packages
- **Severity:** medium · **Status:** still open (utilization re-measured warm: 69.7 s CPU / 30.1 s wall)
- **Confidence:** measured
- **Recommendation:** Unchanged: mechanical `t.Parallel` for the verified-safe packages after S-2 hygiene.

### S-6: Build-cache invalidation dominates perceived runtime
- **Severity:** low (but the largest perceived-time factor) · **Status:** reconfirmed with cleaner data
- **Confidence:** measured
- **Evidence:** Same flags, same tree: cold build after the 30-commit delta ≈ 3 min before the first test event; fully warm rerun 30.1 s total. The 679fb76 audit's 98–111 s "warm" runs were partially cold subprocess caches (quickstart 26.7→12.3 s, FileOrder 13.3→10.7 s here).
- **Recommendation:** Unchanged: one canonical flag set; never compare timings across flag changes.

### S-7: Flakiness posture still good; sleep count 12 → 13, one new ordering sleep
- **Severity:** low · **Status:** one transport-lifecycle flake found and fixed; sleep work remains
- **Confidence:** measured
- **Evidence:** 13 `time.Sleep` sites in tests at HEAD (≈1.35 s literal / ≈0.35 s effective). All 5 previously-flagged replaceable ordering sleeps intact. New in delta: `registry/registry_test.go:161` (100 ms "let the server reach serving state" — replaceable with readiness polling) and `:27` (1 s subprocess fail-safe — legitimate, normally killed by SIGTERM first). Integration tests: 0 sleeps. Both unit runs + both shuffle seeds produced identical verdicts otherwise (no flakes observed; idle + loaded conditions).
- **Recommendation:** Convert the now-6 replaceable ordering sleeps before enabling more parallelism.

## 4. Checklist coverage

Phase 1 — Value (`value.md`):
- 1.1 Tautological tests — **found, minor** (V-4: echo tests now 3+1 subtests; assert-true equivalents 0 hits; zero-assertion tests 0; commented-out 0 — all greps re-run at HEAD).
- 1.2 Implementation-coupled — **found, contained**: delta reproduced the string-assertion pattern at the codegen layer (C-4) while fixture/runtime tests stayed behavioral; adapter echo ratios unchanged (C-7).
- 1.3 Duplicates/near-duplicates — **found** (V-3 all clusters re-verified; `collectStreamChunks` byte-identical diff-confirmed; cross-root layering unchanged — no new test roots in delta).
- 1.4 Snapshot/golden audit — **found, moderate**: 53 / 276 KB / 18 > 100 lines; delta added 0, modified 2 goldens alongside their generator change (disciplined).
- 1.5 Testing someone else's code — **not found** (no new SDK-behavior tests in delta).
- 1.6a Test roots & selection mapping — **found (major, unchanged)**: same roots; C-1 mapping audit re-verified at HEAD; 158/164 unreachable.
- 1.6 Dead weight — **found**: V-5 unchanged (2 permanent skips, dead setters, bridge, dark CLI suite); duplicate CI executions N/A (CI runs nothing).

Phase 2 — Completeness (`completeness.md`):
- 2.1 Criticality ranking — re-done at HEAD (top churn: `codegen/mcp/generate.go` 40, `runtime.go` 38, `workflow.go` 34, `bedrock/client.go` 34 …).
- 2.2 Coverage — re-measured via repo's own flags (59.7 %); per-directory × churn table rebuilt (worst hot dirs: registry 34 %/56 churn, codegen/agent 50 %/424, runtime/toolregistry 53 %/53, expr/agent 60 %/82, dsl 62 %/95); attribution artifacts re-confirmed (C-12); note `runtime/agent/runtime/runtime.go` and `planner/planner.go` legitimately contain few executable statements (facade files) — not attribution failures.
- 2.3 Untested public surface — top-20 churn files crossed with per-file coverage (hooks/events.go 29 %, tool.go 33 %, subscriber.go 51 %); C-6 sweeps re-verified.
- 2.4 Error paths — **found**: C-3 (6/59 DSL sites), C-7 (provider shapes), C-13 (delta residuals).
- 2.5 Boundaries/edge inputs — **partially found**, unchanged (C-3 umbrella).
- 2.6 Structural gaps — DB-only-mocked: found (C-8, decode-side improved); concurrency: found (C-10, with strong delta counterexamples); migrations N/A; flags: `MCP_CLI_TESTS` still dark; property-based present; protocol-layer e2e holes: C-5 (no scenario changes in 30 commits).
- 2.7 Regression discipline — **measured 30/30 on the full delta** (C-11).
- 2.8 Assertion strength — spot-read 5 delta fix tests (behavioral); no mutation tooling (none configured).

Phase 3 — Speed (`performance.md`):
- 3.1 Baseline — 2 measured runs (cold-build+load ≈3.5 min total / 29.6 s test phase; warm idle 30.1 s wall, 69.7 s CPU), variance discipline applied (identical flags; load conditions stated); CI history re-queried (0 runs, measured); distribution + overhead split done (S-1, S-6).
- 3.2 Usual suspects — fixtures: S-4 (worse); sleeps: 13 sites re-inventoried per-site (S-7); real I/O: quickstart + compile tests only; retries: none; lint-in-suite: none.
- 3.2b Flake hunt — baseline: 2 unit runs + 2 shuffle seeds reproduced deterministic S-2 coupling, not flakes. Remediation: both fixed seeds and a race-enabled random-seed unit run are now green.
- 3.2c Fixture architecture — verdict unchanged: **keep, improve cleanup** (amortization economics right; leak now 103 MB).
- 3.3 Parallelization readiness — utilization remains 2.3/10 warm; S-2 root hygiene is fixed and continuously gated by shuffled unit runs. Remaining blockers are S-3/S-5; multi-process shard experiment: not run (blocker list already concrete; noted as method limit).
- 3.4 CI-level structure — C-1 unchanged; hook latency: hooks remain lint-only on staged files (read, not timed — no test latency to measure).
- 3.5 Achievable target — arithmetic updated in S-1 (~15 s warm unit critical path; integration unchanged ~60–90 s post S-4).

## 5. Top opportunities (ranked)

Rank positions are preserved for traceability. Rank 2 is complete; the other opportunities remain open.

| Rank | Opportunity | Findings | Type | Effort | Expected payoff |
|---|---|---|---|---|---|
| 1 | **In progress:** repaired CI selection/toolchain and Docker fail-closed behavior; verify hosted run | C-1, C-2 | blocker | hours | 164 integration funcs + 29 registry tests become a real gate |
| 2 | **Completed:** fix `resetDSLRoots`, restore roots with `t.Cleanup`, enforce `-shuffle=on` | S-2 | complete | done | Order-coupling class eliminated; unlocks parallelism work |
| 3 | Unit-suite critical path: quickstart out of `make test`, FileOrder 13→3, memoize conformance fixture, tune gopter TTL | S-1, V-2 | quick win | hours | Warm wall 30 s → ~15 s; cold-cache worst case shrinks 40–100 s |
| 4 | **In progress:** DSL/expr validation tables (26/59 DSL; ToolExpr target achieved) | C-3, C-13, C-14 | structural | days | One current panic fixed; 22 more DSL sites needed |
| 5 | Compile-the-output design table on `writeGeneratedModule` | C-4 | structural | days | Kills the string-tests-pass-on-broken-output mode the delta just re-demonstrated |
| 6 | Replace `os.Setenv` header channel → drop `-parallel 1`; one server per YAML; `TestMain` cleanup | S-3, S-4 | structural | days | Integration ~60–90 s; stops the 103 MB leak |
| 7 | Shared adapter conformance table (+ Gemini/Bedrock error shapes, bedrock keyword table, Mongo prompt/memory integration) | C-7, C-8, V-3 | structural | days | Provider-regression class covered once, uniformly |
| 8 | Deletions & consolidation: echo tests, 2 stale skips, dead setters, bridge decision, tool-search/OAuth table-driving | V-1, V-4, V-5 | quick win | days | ~40–60 fewer funcs in the biggest bucket |
| 9 | Targeted unit tests: `tools` codec/ident, interrupt controller, `ConsumeStream`, telemetry Clue*, bufconn registry contract, double-Finalize | C-6, C-9, C-10 | structural | days–weeks | Direct coverage for the highest-fan-in plumbing |

## 6. Suggested refactoring sequence

The execution order below is intentionally different from the original opportunity ranking. The goal is not maximum line coverage or minimum runtime; it is to expose existing defects at contract boundaries and then make those checks unavoidable for every future change. Each phase is a separately verified commit on `main`. If a new test exposes a product bug, the regression test and root-cause fix land together so the branch remains green.

### Target state

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

**Findings:** C-1, C-2, V-5. **Expected defect yield:** immediate configuration failures plus failures formerly hidden in 158 dark integration functions and 29 Docker-gated registry tests; local selection/execution is now proven, hosted execution is next.

Current contract inventory after the local repair:

- both CI jobs use Go `1.26.1`, matching the root, assistant, and agent-feature modules;
- CI pins Loom `v1.4.0`, matching `scripts/loom_core_mode.sh`;
- `make itest` selects assistant, agent_features, framework, and scenario tests and forces uncached execution;
- `registry.TestMain` preserves local no-Docker skips but CI requires the 29 Docker-backed tests to be available;
- hosted execution remains unverified because the repository still has no Actions run history.

Commit-sized work:

1. **Implemented locally:** CI uses Go `1.26.1`, Loom `v1.4.0`, and repository make targets.
2. **Implemented locally:** `make itest` owns all three integration clusters, including the assistant fixture.
3. **Implemented locally:** registry Docker coverage fails closed in CI and remains skippable without Docker locally.
4. Enable `MCP_CLI_TESTS` only after its server/CLI prerequisites are made hermetic; otherwise delete the dead flag path rather than advertising false coverage.
5. Verify an actual GitHub Actions run. If the run count remains zero, stop: repository Actions permissions require owner/admin intervention and cannot be repaired in code.

Local proof complete: `actionlint`, `make lint`, `make test`, uncached `make itest`, `make verify-mcp-local`, and the strict Colima-backed registry suite are green. Remaining proof: the first green Actions run with zero unexpected skips. Do not call C-1 complete until hosted execution is observed.

### Phase 2 — attack design validation first

**Findings:** C-3, C-10, C-13. **Expected defect yield:** high; these are executable product contracts with roughly 53 historical error branches lacking assertions.

Owners and tests:

- add `dsl/history_test.go` using `runDSLExpectError` for duplicate `History`, non-positive values, `KeepRecentTurns`/compression conflicts, and invalid placement;
- extend DSL tables for `dsl/bounds.go`, registry duration parsing, and `Passthrough` resolution;
- add direct `ToolExpr.Validate()` tables in `expr/agent/tool_validation_test.go` for `Inject`, `ServerData`, paging, and bounded-result shapes;
- extend `expr/mcp/mcp_test.go` and agent expression tests so `Finalize()` is called twice and produces the same evaluated contract without duplication or panic;
- cover C-13 neighboring branches while the relevant tables are open.

Work red-green by validation cluster. Assert the exact stable message and the resulting expression state; do not merely assert `err != nil`. Proof after each cluster: targeted `go test -shuffle=on ./dsl ./expr/agent ./expr/mcp`, followed by all four repository gates because these packages are design/codegen inputs.

Progress: 26/59 DSL error sites and more than 37/46 `ToolExpr.Validate` conditions have direct tests; the ToolExpr threshold is achieved and one current panic has been fixed. The remaining Phase 2 exit criterion is at least 48/59 DSL error sites, plus repeated MCP finalization, before moving to codegen.

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

### Phase 4 — cover high-fan-in runtime and concurrency boundaries

**Findings:** C-6, C-9, C-10. **Expected defect yield:** medium to high because these paths fan into most agent executions but currently have little or no direct isolation.

Add deterministic tests at their owning packages:

- `runtime/agent/planner/stream_test.go`: `ConsumeStream` text/thinking/tool-call deltas, usage aggregation, metadata usage, EOF, receive errors, cancellation, and nil inputs using a raw model streamer only;
- `runtime/agent/interrupt/controller_test.go`: every wait operation, matching signal delivery, timeout/cancellation, and cross-signal isolation using an in-memory workflow context;
- `runtime/agent/tools/ident_test.go` and `idempotency_test.go`: malformed/empty identifiers, namespace splits, tag conflicts, and stable scope selection;
- `runtime/registry/grpc_client_adapter_test.go`: add a Docker-free `bufconn` generated-server contract instead of testing only a handwritten client mock;
- concurrency/reentrancy cases for repeated finalization and shared stream/session state, always under `-race`.

Proof: targeted race tests for each owner, then `make lint`, `make test`, `make itest`, and `make verify-mcp-local`.

### Phase 5 — provider conformance and persistence reality

**Findings:** C-7, C-8, V-3. **Expected defect yield:** medium; Gemini and persistence encode/decode boundaries are the least uniformly covered.

Define one provider-neutral conformance case model, while keeping SDK-specific stubs in each adapter package. Every supported adapter must prove: rate-limit classification; ordinary provider errors; malformed tool input; stream setup and receive errors; context cancellation; structured-output/tool-choice compatibility; usage accounting; and exactly one terminal event. Fill Gemini first, then Bedrock keyword normalization, then Anthropic/Ollama/OpenAI gaps. Reuse helpers only for observable model contracts, never SDK internals.

Add testcontainer round trips for Mongo prompt and memory stores covering encode + persist + retrieve + decode, with a CI fail-fast switch matching Redis. Keep fast codec unit tests for local feedback.

Exit criterion: every provider passes the common matrix or documents an intentional unsupported capability with a tested error; both Mongo stores have at least one real round trip in CI.

### Phase 6 — protocol scenarios, then suite throughput

**Findings:** C-5, S-1, S-3, S-4, S-5, S-7, V-1, V-2. **Expected defect yield:** protocol work medium; speed work is confidence-enabling rather than directly bug-finding.

Start sampling, roots, and progress as real client-vs-framework validation tests in `integration_tests/framework` or scenario YAML. If a test proves the protocol surface is absent, treat it as MCP feature work and follow the repo's `new-mcp-feature-development` red-green workflow rather than weakening the test.

Only after the correctness phases are green:

- replace process-global `os.Setenv` scenario headers so `-parallel 1` can be removed;
- boot one server per YAML group and add `TestMain` cleanup for prepared fixture clones;
- reduce `TestGenerate_FileOrderIsStableAcrossRuns` from 13 to 3 runs, memoize immutable conformance generation, and move the quickstart subprocess behind an explicit integration/slow gate;
- replace ordering sleeps with readiness/event synchronization;
- consolidate tool-search/OAuth fixture cases while preserving behavior assertions.

Exit criterion: warm unit wall at or below 20 seconds, integration wall at or below 90 seconds, no leaked prepared fixture directories after a run, and no reduction in the contract matrix or assertion strength.

### Commit and review discipline

- Work directly on `main` as requested, one coherent phase slice per commit.
- Before every commit run targeted red-green checks and the proportionate repository gates; framework-scale changes require all four gates.
- Never commit a red tree, bypass hooks, hand-edit generated files, or mix an exposed bug with unrelated cleanup.
- Update this audit in the same commit that changes a finding's status. Record newly exposed bugs in a remediation ledger directly below this plan.
- After Phases 2, 3, and 4, pause for a defect-yield review. Reorder remaining work using actual failures found, not coverage percentage alone.

### Remediation ledger

| Finding | Exposing test | Root cause | Fix commit | Status |
|---|---|---|---|---|
| C-15 | full make itest + repeated assistant fixture | parallel SDK clients shared the global HTTP transport, allowing cross-test connection-pool lifecycle interference | this change | fixed; 20× fixture + 50× race stress green |
| S-2 | shuffled `dsl` suite, seeds `101` and `202` | `resetDSLRoots` omitted `mcpexpr.Root`; three `expr/agent` tests leaked replaced globals | `2752473` | fixed |
| C-1 | CI contract inventory + uncached `make itest` | workflow used Go `1.25.x`, Loom `@latest`, selected only 6/164 integration functions, and allowed cached scenario passes | this change | local gates green; hosted run pending |
| C-2 | strict no-socket probe + full Colima registry suite | `TestMain` converted every Docker/container failure into skips even in CI | this change | 29 tests green locally; hosted run pending |
| C-14 | nil-result ServerData validation regression | `validateServerDataShapes` dereferenced `t.Method.Result` before checking it | this change | fixed |

## 7. Method notes

- **This is a refresh audit.** Baseline `TEST_AUDIT.md` was produced earlier today at `679fb76`; HEAD moved 30 commits (+4,227/−1,438, 116 files). Method: re-ran every mandatory repo-wide grep and every measurement at HEAD; dispatched 5 Explore subagents (dsl/expr, codegen, features, runtime+registry, integration_tests) to re-verify every prior finding with fresh file:line evidence and to read the full delta; spot-checked ≥1 citation per subagent in the main context (all passed; one correction: the string-only tests for `0dc4bce`/`0a0e261` are in `codegen/agent/`, not `codegen/mcp/`, and both commits also landed behavioral fixture-module tests the subagent initially under-credited).
- **Executed:** 2 full unit runs (`-race -covermode=atomic -count=1`; run 1 cold-build under subagent load with JSON per-test timings; run 2 fully warm on idle machine with `time -l`), 2-seed full-suite shuffle probe, coverage aggregation (per-directory and per-hot-file), per-fix-commit test-presence check (30/30), integration ladder (`make itest` equivalent + assistant fixture) at HEAD. Machine: 10-core arm64 mac, Go 1.26, loom mode = remote (v1.4.0).
- **Timing discipline:** run 1's wall includes ~3 min of delta-induced recompilation and subagent load — only its per-test relative distribution is used; all absolute wall/CPU claims come from run 2 (warm, idle, identical flags). The baseline audit's 98–111 s figures are not comparable to today's 30.1 s (different build-cache and subprocess-cache states), which is itself finding S-6.
- **Not run:** multi-process shard experiment, mutation tooling (none configured), hook timing (hooks are lint-only). The Docker-backed registry suite omitted from the baseline was run during remediation via Colima; Mongo integration remains separately gated per C-8.
- **Baseline integration:** all green — agent_features 0.6 s, framework 37.6 s, scenario suite (`./integration_tests/...` with itest flags) 172.5 s, assistant fixture 1.1 s. The baseline audit's working-tree WIP (which broke `make itest` at the time) landed as the delta commits and its scenario failures are gone. Scenario wall (172.5 s vs 203.5 s cold at baseline) reflects warm codegen/build caches, consistent with S-4's per-scenario boot cost dominating.
- **CI-remediation verification:** `actionlint` green; `make lint`, shuffled race-enabled `make test`, uncached expanded `make itest`, and `make verify-mcp-local` green. The scenario package ran in 178.6 s. With Colima's socket exported through `DOCKER_HOST` and `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE`, the strict 29-test registry package passed under race/shuffle in 71.8 s; without a discoverable socket, the strict switch failed closed as designed.
