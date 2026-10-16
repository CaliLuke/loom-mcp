# Application agent and tool testing

Use `agenttest`, `expect` and `trajectory` for the application's development loop.
Read [the compiled guide](../../../../docs/agent_testing.md) and the generated
application registration before writing tests. Do not change runtime or generated
code to make a test pass. The root module never imports a fixture module; tests
that import `example.com/assistant` belong in `integration_tests/fixtures/assistant`
and run with `go -C integration_tests/fixtures/assistant ...`.

## Choose the proof

- Wiring: use generated `Register…Agent` and `RegisterUsedToolsets`, a
  `ScriptedModel` and typed `Stub(h, tool).Return`. Run the agent and check
  `expect.Trajectory(r.Trajectory)`, typed arguments/results and `Completed`.
- Selection: capture one fully decorated request, then use `Decide` and
  `expect.Calls(d.Calls)`. Repeat positive choices at 5/4 and negative `Forbid`
  choices at 5/5. Do not execute tools merely to check selection.
- Fixture: use `SaveRequest`/`LoadRequest` to keep a decision request under the
  application module, gitignored. Files contain full prompts; reuse them directly
  with a model client and Decide, without constructing a harness.

Register all models, agents and toolsets inside `register`. Harness construction
installs its model registration hook before calling register, freezes gate
identities, seals registration and verifies every stored client against the
snapshot. Registration changes after the snapshot fail construction. Later
registrations, including model replacements, return `runtime.ErrRegistrationClosed`.
`LOOM_LIVE_MODEL` gates only `LiveModel` and `SkipUnlessLive`. Unset or `0` disables,
`1` enables and any other value fails. LiveModel creates no provider: the
application registers the named client. Undeclared application clients are not
blocked, so the variable is not a universal network guard. Default tests use
scripted clients; real providers need explicit opt-in.

`LastUserContains`, `LastToolResult` and `LastToolResultParts` accept value parts
and non-nil pointer parts equally for text and tool-use/result lookups.
Script diagnostics use the same text lookup.

## Wiring recipe

The generated assistant fixture uses `guideRegister` to register toolsets and the
agent, and `guideQuery` to check `Query == "order 1234"`. Those helpers and the
lookup/status planner are in the compiled guide fixture. Adapt their generated
names to the application. `projected` is the generated toolset package.

<!-- snippet: wiring -->
```go
func TestGuideLookupWiring(t *testing.T) {
    h := agenttest.New(t, guideRegister, agenttest.ScriptedModel("fixture",
        agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
            &projected.ProjectedLookupToolPayload{Query: "order 1234"})),
        agenttest.Respond(agenttest.Text("Order 1234 ships tomorrow.")),
    ))
    agenttest.Stub(h, projected.ProjectedLookupToolTool).
        Return(&projected.ProjectedLookupToolResult{Answer: "ships tomorrow", Source: "orders"}).
        When(agenttest.ArgsEqual(projected.ProjectedLookupToolTool,
            &projected.ProjectedLookupToolPayload{Query: "order 1234"}))
    r, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
    require.NoError(t, err)
    require.NoError(t, r.RunErr)
    expect.Require(t, expect.Trajectory(r.Trajectory),
        expect.Call(projected.ProjectedLookupToolTool, guideQuery, func(r *projected.ProjectedLookupToolResult) error {
            if r.Answer != "ships tomorrow" || r.Source != "orders" {
                return fmt.Errorf("unexpected lookup result: %+v", r)
            }
            return nil
        }),
        expect.Completed(),
    )
}
```

Activity tools are strict by default. Register the real toolset even when its
executor is omitted. Use `Passthrough(tool)` or `PassthroughAll()` to allow real
execution intentionally; explicit stubs retain precedence. Return/Fail/Handle
responses are consumed once unless Times or Always changes that. Cleanup reports
unused finite responses. RawStub result-decoder failures are StubErrors reported
by Run and Close and retained in stub and harness call records. ArgsEqual uses the typed payload codec's canonical JSON.
Inline and agent-as-tool tools cannot be stubbed; test children through their own
models and activity tools.

## Selection recipe

The registration callback must register the named model before constructing the
harness. In the fixture, the caller sets opt-in and supplies a scripted stand-in
outside this region; it does not contact a real provider.

<!-- snippet: selection -->
```go
func guideSelection(t testing.TB, register agenttest.Register) {
    t.Helper()
    h := agenttest.New(t, register, agenttest.LiveModel("fixture"))
    r, err := h.CaptureFirstRequest(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
    require.NoError(t, err)
    agenttest.Repeat(t, 5, 4, func(at testing.TB) {
        d := agenttest.Decide(at, h.Model("fixture"), r)
        expect.Require(at, expect.Calls(d.Calls), expect.Call(projected.ProjectedLookupToolTool, guideQuery, nil))
    })
    negative := r.WithUserMessage("What are your opening hours?")
    agenttest.Repeat(t, 5, 5, func(at testing.TB) {
        d := agenttest.Decide(at, h.Model("fixture"), negative)
        expect.Require(at, expect.Calls(d.Calls), expect.Forbid(projected.ProjectedLookupToolTool))
    })
}
```

Capture gates both registration APIs before the register callback runs. Clients
retained from rt.ModelClient or RegisterModelClient stay gated in interceptors,
planners, executors and adapters using the supplied run context. Runtime initializes
a setter for private Options.modelHook through internal/agenttesthook; agenttest
sets it before construction. Applications cannot install a wrapper. The hook runs
after context-limit decoration under the registry lock and must return a non-nil
client without calling runtime methods. Ordinary runtimes have no hook. Gate
identities are frozen before Seal and verified afterward.

Interceptor side calls and their outer recorded call share one capture identity.
Direct runtime clients do not record requests: stopping before a recorded call
returns ErrCaptureNoModelCall without provider execution. Use planner-context
clients for recorded requests. Original application-held clients (even if also
registered), unregistered clients and h.Model clients for Decide remain outside
the gate. Preserve supplied contexts; detached contexts without run or call
identity cannot be associated with capture.

Capture supports default and explicit context policies. ContextModel uses a
registered model ID and the same registration-time gate; capture does not consume
planner or summary scripted responses. Ungated clients remain rejected by FA2.

Capture returns `(Request, error)`; it propagates a sentinel rather than consuming
the model. Decide returns `*Decision` and runs no tools. Repeat takes
`func(testing.TB)`. An attempt that fails and then skips counts as failed; only
a skip without failure skips the parent and stops. Attempt cleanup restores
Setenv and Chdir (including PWD) and removes TempDir directories before the next
attempt. Repeat obtains the parent ArtifactDir once on the calling test goroutine
before starting attempts; a parent-directory Fatal aborts Repeat. ArtifactDir
lazily creates a stable, distinct attempt subdirectory under that base; files
survive attempt cleanup and subdirectory creation errors fail that attempt. Process-state changes still require a non-parallel parent test.
Save/load full request fixtures only under a gitignored
application path. Loading rejects unknown envelope and nested request members
by comparing the full request to its canonical re-encoding before verifying the
digest. JSON whitespace and object member order remain flexible.
Re-capture when planner decoration changes tool definitions.

## Eval runner

- Run matrices with `eval.Run` and `eval.NewHarnessFunc(register, setup, base...)`.
  Factories receive the variant and supplied options, forward options and return
  fresh harnesses. Use ScriptedModel for per-harness scripts; never share scripts.
- For per-item scripts or fixtures, set `AttemptHarness` instead of `Harness` in
  eval.Config or evalcli.Config. It receives `AttemptInfo{Key, Item, Variant}`
  before construction; Key includes scenario and repeat. Item and mutable variant
  metadata are owned snapshots; option closures remain application-owned. Item
  values precede scenario task/reference defaults. Choose registration/scripts,
  forward opts to NewHarness, and retain normal sealing. Exactly one factory field
  is required. See the assistant fixture's `attempt_factory_test.go`.
- Put labels and cloned run policy on Variant; put model and context runtime
  settings in Variant.Options. A nil variant Policy preserves earlier policy
  defaults. WithDefaultRunOptions precedes caller options on
  Run, RunSession and CaptureFirstRequest; RunID remains reserved.
- Plan validates all scenarios and selector intersections. OR within each kind,
  AND across kinds; item selectors may be scenario/item. Records keep declaration
  order while starts use longest timeouts first (zero longest). Zero repeats and
  concurrency mean one. OnAttempt is serial and includes skipped records last.
  OnAttempt panics stop dispatch and disable further callbacks; Run drains workers
  before returning a partial report and an error containing the panic and stack.
- Verdicts use error, failed, pending, passed precedence. Gating claims remain
  pending until judged; informational claims do not gate. Groups reports counts.
  HarnessErr includes factory/setup/panic/reuse/Close errors. NotRun distinguishes
  canceled, token_limit, live_model_disabled and callback_panic. Attempts recheck
  cancellation before factory entry. The first stop reason wins; later
  cancellation joins existing errors, including ErrLiveModelDisabled.
  Cancellation/live gating return
  partial reports and errors; token stops and quality failures do not.
- Usage counts completed calls in all returned run records, descendants and
  abandoned attempts included, once. Direct Decide calls in Assert and unreturned
  Drive runs are excluded. MaxTokens stops starts at >=; running attempts finish,
  allowing Concurrency-1 additional attempts beyond the crossing attempt. Runs()
  exports completed session evidence, retained in memory. Key storage by
  AttemptKey; isolated harnesses may repeat run/session IDs.

## Eval CLI

- Use eval/evalcli.Main(ctx, args, Config) in an application-owned cmd/evals.
  Supply Harness and Scenarios. The caller supplies signal cancellation and
  passes the exit code to os.Exit. Main installs no signal handlers.
- Run baseline, run candidate with --compare last, then show a failed attempt.
  See docs/agent_testing.md story (b) for the compiled three-command fixture.
  Keep generated imports in the assistant fixture module.
- --out and --json apply to run/show/compare/list. Selectors accept repeated
  flags and comma lists. Flags work on either side of positional arguments.
  Store precedence is --out, Config.StoreDir, then DefaultStoreDir(Config.Dir).
- --rerun-failed inherits parent repeats unless --repeat overrides them. It
  rejects selector flags and uses the store's variant/item cross-product.
  --merge-parent overlays a candidate rerun on its recorded parent for comparison.
- Attempt progress goes to stderr after persistence. --quiet suppresses it.
  Stdout contains the stored summary or one version-1 loom-mcp.eval.run document.
  Exit 0 includes failed/pending/error verdicts, token stops and nothing-to-rerun.
  Operational errors return 1; usage errors return 2.
- Live variants require explicit LOOM_LIVE_MODEL=1. Disabled live runs remain
  stored as stopped/live_model_disabled and return 1 with an opt-in hint.
  The CLI never sets the variable. Invalid values return 2 before storage.

- Optional Config.NewJudge func(cache judge.Cache) (*judge.Judge, error) receives
  the selected store's _calibration directory cache. Pass it to judge.Config.Cache.
  The CLI wraps the judge with judging.New and stores judgments and provenance.
  --no-judge skips the factory. An error wrapping ErrLiveModelDisabled prints a
  stderr notice and continues without judging, leaving gating claims pending.
  Normal exit rules apply; other factory errors return 1 before Begin.

## Diagnose failures

Inspect Run's harness error before its populated Result. `RunErr` is the agent
outcome, while a joined `*StubError` reports strict, exhausted, unmatched or decode
failures and also appears at Close. Use `errors.As` and read the tool and arguments.
`Trajectory.Render` provides causal evidence with explicit truncation limits.
Unanswered root awaits and child-run awaits return `ErrUnansweredAwait`.
Run the child agent directly to test its responder flow. There is no Temporal harness mode. Consult
[the runtime reference](../../../../docs/runtime.md#harness) for details.

## Release-criteria library (K19a)

- Use `Harness.OpenSession`, `Session.Run`, and `Session.Result` for dynamic turns.
  Supply caller history explicitly. `RunSession` retains its existing behavior.
- Put context variants in `Variant.Policy.Context`. Plans own policy pointers.
  Stored `ContextFingerprints` include every root and child run. Store version stays 1.
- Use `eval/evidence.Classify` on caller-ordered records and a materializer-owned Spec.
  Only adopted planner requests establish need points or recalled evidence.
  Retained summary evidence is present and does not qualify as recall.
  Asset evidence pairs its digest with `Token`, the rendered grammar token.
  Received request bytes must match to support it. Detect checkpoints by metadata, then use
  `checkpoint.Sections` selects the first user label, last receipt label, then last
  summary label before that receipt. Label-free components split exactly; embedded
  labels use that precedence. Only user_index/summary/receipt attribution is affected:
  sources are reported, never gated; presence and gated measures are unaffected.
- Grammar tokens require received input from an adopted planner request. User
  messages, tool results including recall, and checkpoint sections support tokens.
  Earlier assistant text, tool-call arguments and final outputs do not. Decoy
  detection is unchanged (K19 Amendment 1).
- `eval/gate.FromStored` adapts reserved checks and scores after Parent overlays.
  `Evaluate` uses cumulative manifest items, paired repeats, equal scenario weights,
  and shared stratified PCG bootstrap draws. `Decide` enforces the stopping rule.
  Exact margin equality is inconclusive, including recall (Q17).
- `CalibrateCandidates` uses the production evaluator and stopping rule for three
  fixed unsupported margins (0.02/0.03/0.05), each with 1620 grid cells and two
  sensitivity rows. Keep 2000 decision/400 other simulations and 10000 bootstrap
  draws. `CalibrateChecks` supplies two 200-simulation reproduction cells.
- Scenario guards use the upper success-interval bound strictly below -0.10.
  Preserve per-variant terminal evidence. COMPLETE describes scheduling only.
  Rerun exact missing keys through Selection.Keys without replacing valid rows.
- Select the smallest eligible margin separately for each variant. Require worst
  null PASS >= 0.80 and every worst boundary PASS <= 0.05. Preserve ineligibility
  honestly. Neither eligible means diagnostic calibration without a freeze.
- V2 provenance binds roles, capabilities, actual runtime-stamped settings,
  pools/materialization, validation, calibration, inventory and executable owners.
  Evidence commands require explicit CGO_ENABLED=0 and reject stale binaries.
  Race gates retain cgo. Source bundles exclude evidence and protected go.work.sum.
- Inventory the explicitly declared historical store before archiving v1. Unknown,
  orphaned or consumed live evidence blocks migration. Archived v1 freezes retain
  exact bytes and source and remain pool locks. Never reinterpret them as v2.
- Complete offline profile validation precedes calibration. Missing/duplicate/
  unexpected/failed structural rows block it. Zero observed q is valid; missing
  q is not. Live rates require the exact complete two-repeat baseline pilot and
  matching recorded roles/source. Stored reports need no model environment.
- Protocol 19 profiles declare attachment support without live constructors.
  Supporting profiles recall both assets; zero-support profiles retain evidence
  with one residency event and zero threshold compactions. Use shipped defaults
  and explicit KeepNonTextResident, without special multimodal retention budgets.
- Materializer k19c-v4 preserves raw pools but expands base steps by two (model_change by four).
  Current effective workloads are 36/72 steps. Keep three-compaction coverage and
  actual baseline requests below 128k tokens. Synthetic planner windows of 1m/128k
  and summarizer window of 200k apply consistently across offline variants. Compacted
  model_change keeps its 8k pretend stress. Do not confuse aggregate multi-run
  bytes with one request. Historical materialization requires retained v1 source.
- Regenerate generated agents with make regen-longrun-fixture. Never hand-edit
  pools or generated files. Keep dry structural proof separate from calibration
  eligibility and live qualification. The shipped qualification table stays empty.

### Calibration progress

The calibration CLI emits phase changes and a 30-second stderr heartbeat with
cell/simulation counts, elapsed time, throughput and an explicitly approximate
phase ETA. Shared candidate populations count once; infeasible cells add no
simulations. Progress advances within cells and does not enter deterministic
artifacts or stdout. Completion follows successful evidence verification and
artifact writes. `gate.WithCalibrationProgress` provides serialized per-call
library observations without adding observer state to evidence configuration.

The retained offline evidence belongs to commit
88eabc2283bc4d790e81cde84348737aae8212ef and source
f6aef6fe246341995996075bcdef03dc60040c6fe8c5c607dd8dc50bf3eacafd.
Both variants are ineligible. It is historical diagnostic evidence, not current
source authorization. The artifact test verifies exact retained bytes and the
original verifier in a temporary local checkout; current code reproduces both
full-bootstrap check cells and tests progress equivalence. No new full grid or
qualification is claimed. Keep production source matching strict: changed code
refuses historical calibration/freeze/pilot/look inputs before opening stores.
Future qualification requires fresh source-bound validation and calibration.
