# Testing agents and tools

Use these packages in the development loop to prove a new tool's wiring and
selection before evaluating full tasks:

- `agenttest` constructs an isolated in-memory runtime, scripts model responses,
  stubs activity tools, captures requests and repeats decisions.
- `expect` checks typed calls and recorded outcomes. Build its sealed subjects
  with `expect.Calls`, `expect.Trajectory` or `expect.Session`.
- `trajectory` derives causal steps from a sealed RunRecord and renders evidence
  for diagnosing failures.

Live-model tests are opt-in through `LOOM_LIVE_MODEL=1`; these helpers never enable
live models by default. `LiveModel(id)` confirms an application-registered client;
it does not construct a provider. `SkipUnlessLive(t, id)` gates a standalone test.
Unset or `0` disables these helpers, `1` enables them, and other values fail.
The gate applies only to `LiveModel` and `SkipUnlessLive`: undeclared application
clients are not blocked. Use scripted clients for deterministic default tests.

The examples below are compiled in the separate
[assistant fixture module](../integration_tests/fixtures/assistant/agenttest/guide_test.go).
The generated lookup tool stands in for an application's `search_orders` tool.
`generated` names the `gen/assistant/agents/assistant_runtime` package; `projected`
names `gen/assistant/toolsets/projected`. The examples use `testify/require` and
`testify/assert` alongside the three testing packages. Replace the fixture's
registration, agent ID and descriptors with your application's generated names.
The root module never imports a fixture module.

## Adding a tool: prove the wiring

Register the agent with its generated `Register…Agent` function and toolsets with
`RegisterUsedToolsets`. Register every application model inside `register`.
Construction seals runtime registration after installing capture gates. Later
model, agent or toolset registrations return `runtime.ErrRegistrationClosed`.
The fixture's `guidePlanner` advertises both generated lookup and status schemas
and propagates model errors; its implementation is outside the snippet regions.
These helpers supply registration and the argument predicate:

<!-- snippet: guide_registration -->
```go
func guideRegister(ctx context.Context, rt *runtime.Runtime) error {
    if err := generated.RegisterUsedToolsets(ctx, rt); err != nil {
        return err
    }
    return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: guidePlanner{}})
}

func guideQuery(p *projected.ProjectedLookupToolPayload) error {
    if p.Query != "order 1234" {
        return fmt.Errorf("query = %q", p.Query)
    }
    return nil
}
```

Add `ScriptedModel`, then `Stub(h, tool).Return`. A typed `When(ArgsEqual)` routes
the payload to the declared result. `Run` returns `(*Result, error)`. Check the
harness error before inspecting recorded evidence, then check typed arguments,
the result and completion:

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

Stubs are strict by default. An unstubbed activity call becomes a model-visible
tool error and a `*StubError` returned by Run and Close. Generated executors may
be omitted for stubbed tools. Use `h.Passthrough(tool)` or `PassthroughAll()` when
real execution is intentional; explicit stubs still take precedence.
`Return`, `Fail` and `Handle` append responses, used once unless configured with
`Times(n)` or `Always()`. Cleanup reports unused finite responses. Stubs preserve
normal payload validation, AfterTool interceptors and result materialization.
A RawStub response rejected by the registered result decoder is a `*StubError`,
reported by Run and Close and retained in both stub and harness call records.

For dynamic tool names without generated descriptors, use `expect.RawCall` to
inspect raw JSON arguments and results, or `expect.RawFailed` to inspect a recorded
tool error. JSON predicates receive copied original bytes, preserving large numbers.
A result predicate requires verified successful execution; a nil result predicate
checks arguments only. Raw failure checks require verified failed execution.
These expectations support the same ordered matching, `Exact` and child selection
as typed calls. Prefer typed descriptors when available.

## Prove selection

A wiring test scripts the chosen tool. To test the model's choice, capture its
first fully decorated request with `CaptureFirstRequest`, which returns
`(Request, error)`, then call `Decide` directly. Capture does not consume the
underlying model client. Tools scheduled before that invocation still follow
strict stub rules. Decide executes no tools; use `expect.Calls(d.Calls)`.

This helper takes an application's registration callback with the named model
already registered. In a live test, call it after opting in with
`LOOM_LIVE_MODEL=1`. The compiled fixture test sets that variable outside this
region and registers a scripted stand-in, so it makes no provider requests.
The positive case requires four successes in five attempts. The negative case
replaces the user message and forbids lookup in all five attempts:

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

`Repeat` takes `func(testing.TB)`, runs all attempts, and reports failed attempts'
diagnostics. An attempt that fails and then skips counts as failed; a skip
without failure skips the parent and stops. Each attempt restores Setenv and
Chdir (including PWD) and removes its TempDir directories during LIFO cleanup,
before the next attempt starts. `Repeat` obtains the parent's ArtifactDir once
on the calling test goroutine before starting attempts; a parent-directory
failure aborts `Repeat`. Each attempt creates its own distinct, initially empty
subdirectory on first ArtifactDir use. It stays stable within the attempt,
preserves files after cleanup, and reports subdirectory creation errors to that
attempt. Process-state changes still require a
non-parallel parent test. `Decide` returns a `*Decision` with request, response,
calls and latency, without recording a run. Result and completion expectations require a
trajectory subject and cannot be checked against model calls alone.

## Keep a decision as a fixture

`SaveRequest` and `LoadRequest` preserve deterministic version-1 JSON with a
verified request digest. Loading rejects unknown members in the envelope and
inside the request, including typed messages, parts and tools. The request must
match its canonical re-encoding; JSON whitespace and object member order remain
flexible. Save fixtures under the application module and keep
them gitignored: they hold full prompts and tool definitions. The test uses a
temporary directory; subsequent decisions need only a client, not a harness:

<!-- snippet: fixture_reuse -->
```go
func TestGuideFixtureReuse(t *testing.T) {
    h := agenttest.New(t, guideRegister, agenttest.ScriptedModel("fixture"))
    r, err := h.CaptureFirstRequest(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
    require.NoError(t, err)
    path := filepath.Join(t.TempDir(), "request.json")
    require.NoError(t, agenttest.SaveRequest(path, r))
    loaded := agenttest.LoadRequest(t, path)
    turn := agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
        &projected.ProjectedLookupToolPayload{Query: "order 1234"}))
    first, firstScript := agenttest.ScriptClient(t, turn)
    second, secondScript := agenttest.ScriptClient(t, turn)
    // Decide reuses either request directly, without a harness.
    assert.Equal(t, agenttest.Decide(t, first, r).Calls, agenttest.Decide(t, second, loaded).Calls)
    assert.Zero(t, firstScript.Remaining())
    assert.Zero(t, secondScript.Remaining())
    assert.Equal(t, firstScript.Requests(), secondScript.Requests())
}
```

Private completion validators are not persisted. Do not replay provider thinking
signatures across providers. Re-capture when a planner decorates changed tool
definitions rather than assuming a tool-spec replacement is identical.

## Reading failures

`Result.RunErr` describes an agent failure. Run's returned error describes a
harness failure, such as setup, timeout, reconstruction or a stub dispatch
failure. A stub failure can return populated evidence alongside the error even
when the planner later completes. Use `errors.As` to inspect `*agenttest.StubError`
for the tool, call index, run ID, tool-call ID, arguments and reason.

<!-- snippet: diagnose -->
```go
func TestGuideDiagnoseStubError(t *testing.T) {
    // NewHarness lets this test inspect the expected Close error itself.
    h, err := agenttest.NewHarness(t.Context(), guideRegister, agenttest.ScriptedModel("fixture",
        agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
            &projected.ProjectedLookupToolPayload{Query: "order 1234"})),
        agenttest.Respond(agenttest.Text("Lookup unavailable.")),
    ))
    require.NoError(t, err)
    t.Cleanup(func() {
        var stubErr *agenttest.StubError
        assert.ErrorAs(t, h.Close(), &stubErr)
    })
    r, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
    var stubErr *agenttest.StubError
    require.ErrorAs(t, err, &stubErr)
    assert.Equal(t, projected.ProjectedLookupTool, stubErr.Tool)
    require.NotNil(t, r)
    require.NoError(t, r.RunErr) // The planner completed after seeing a tool error.
    t.Log(r.Trajectory.Render(trajectory.RenderOptions{Limits: trajectory.DefaultLimits()}))
}
```

`agenttest.New` registers cleanup automatically. The diagnostic example uses
`NewHarness` so the expected Close error can be inspected without failing cleanup.
An unanswered root-run await returns `ErrUnansweredAwait`. Use
`Trajectory.Render` with explicit limits to read causal model/tool steps and
marked truncation. `Completed` and await checks require verified observed
evidence; BeforeEvent middleware marks that evidence unverified.

## Story (b): improve an agent across repeated requests

Use an application-owned `cmd/evals` command to run, compare and inspect experiments.
This is the authoritative CLI recipe. The generated assistant’s status request
stands in for an application task such as a refund request. This configuration
uses a fresh scripted model and a typed status stub for each attempt:

<!-- snippet: eval-cli-fixture -->
```go
func fixtureEvalCLI() evalcli.Config {
	return evalcli.Config{
		Harness: eval.NewHarnessFunc(guideRegister, func(h *agenttest.Harness, _ eval.Variant) error {
			agenttest.Stub(h, projected.ProjectedStatusToolTool).
				Return(&projected.ProjectedStatusToolResult{Status: "ready"})
			return nil
		}, agenttest.ScriptedModel("fixture",
			agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})),
			agenttest.Respond(agenttest.Text("ready")),
		)),
		Scenarios: []eval.Scenario{{
			ID: "status", Agent: "assistant.assistant_runtime",
			Items: eval.Items(eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{
				Exact: true, Tools: []expect.ExpectedTool{{Name: projected.ProjectedStatusTool}},
			}}),
			Expect: []expect.Expectation{expect.Completed()},
		}},
	}
}
```

In the application’s `main`, call `evalcli.Main` with a cancellation context,
`os.Args[1:]`, and your configuration. Pass its returned code to `os.Exit`.
See [Eval CLI](runtime.md#eval-cli) for the signal-handler example and all flags.
The CLI itself installs no signal handlers.

To judge scenario claims, set `Config.NewJudge func(cache judge.Cache) (*judge.Judge, error)`
and return a judge configured with the supplied `Cache`. The CLI stores calibration
at `<store dir>/_calibration`, adapts the judge through `judging.New`, and persists
judgments and judge provenance. `run --no-judge` skips the factory and leaves gating
claims pending. A factory error wrapping `agenttest.ErrLiveModelDisabled` prints a
stderr notice and continues without judging under normal exit rules; other factory
errors return `1` before any experiment is stored.

```sh
go run ./cmd/evals run --scenario status --repeat 5 --label baseline
# Revise the application prompt or model configuration.
go run ./cmd/evals run --scenario status --repeat 5 --label clearer-policy --compare last
go run ./cmd/evals show <baseline-experiment>/#1
```

The [compiled fixture test](../integration_tests/fixtures/assistant/agenttest/evalcli_fixture_test.go)
calls these three commands through `Main`. The baseline passes three of five
attempts. The candidate passes all five. The third command reads a failed baseline
attempt and reconstructs its trajectory from the stored export.

Failed checks do not change the `run` command’s exit code. Read the summary to assess
quality. Use `--json` for one structured run document. Progress stays on stderr,
and `--quiet` suppresses it. Cancellation and operational failures return `1`.
Usage errors return `2`.

To gate a saved experiment in shell automation, invoke the compiled binary so the
shell receives its exit code directly:

```sh
go build -o ./evals ./cmd/evals
./evals check last --policy strict
./evals check <experiment> --policy strict --json
```

`check` needs no harness or scenarios and makes no model calls or reruns. Strict
requires status `complete`, a nonempty and fully recorded plan, an intact attempt
log, and all verdicts passed. Exit `0` means pass; `3` means policy rejection;
`1` means a storage, decode or output failure; `2` means an invalid invocation or
reference. Output includes verdict counts and rejection reasons. `last` remains
the newest non-running experiment, which can be stopped and rejected by this gate.

```sh
go run ./cmd/evals run --rerun-failed <experiment> --compare <experiment> --merge-parent
go run ./cmd/evals compare <experiment> --variants baseline,candidate
go run ./cmd/evals list --limit 20
```

Reruns retain all repeats and inherit the parent’s repeat count unless overridden.
Selection flags cannot accompany `--rerun-failed`. `--out <dir>` overrides the
store for run, show, compare, list and check. Otherwise the default is `.loom-evals`
beside the nearest ancestor `go.mod`.

For live variants, declare `agenttest.LiveModel("model-id")` and set
`LOOM_LIVE_MODEL=1` in the calling environment. The CLI never sets it.
A disabled live variant produces a stored stopped experiment and an opt-in hint.
The examples use no confirmation flows.

## Frozen candidate content

`eval/candidate` stores the content that an application can later bind to an attempt.
The package accepts byte submissions. It does not read a source directory or install runtime behavior.
Existing eval factories, stores and quality requirements retain their behavior.

A host declares a `candidate.Surface` and an authorized baseline with `candidate.Open`.
Each slot declares accepted media types, positive byte/count limits, permitted operations and a versioned host binding.
`Store.Discover` returns this declaration, the baseline digest and its component dependency edges.
An absent slot is unsupported. Slot names describe application capabilities, without a fixed component taxonomy.

The content workflow uses these public methods:

1. Call `Create(ctx)` to start from the registered baseline, or `Fork(ctx, digest)` to start from a stored frozen candidate.
2. Call `Edit(ctx, id, revision, edits)` with an explicit batch of `Put` and `Delete` operations.
3. Call `Validate(ctx, id, revision)` to apply core and host validators.
4. Call `Freeze(ctx, id, revision)` to retain immutable content.
5. Call `Load(ctx, digest)` to reopen that content after a process restart.

Each edit uses the expected revision. A stale edit returns `ErrRevision` without a mutation.
A batch can add a skill and its helper together. Missing dependencies reject the complete batch.
Deleting a referenced helper also fails. Core uses explicit dependency names and never infers references from prose.
Host validators receive owned copies and validate semantic dependencies, activation and renderer syntax.
Ordinary template braces remain valid unless the host renderer rejects them.

A frozen candidate binds the surface, baseline, sorted component hashes, dependency edges and host binding versions through SHA-256.
`Frozen.Manifest`, `Components` and `Read` return owned values. `Closure` returns sorted transitive dependencies for an explicit selection.
Frozen drafts reject edits with `ErrFrozen`. Further changes require a new draft.
Component names must be normalized relative paths. Store files must be regular files, without symlinks.

The host owns one `Store` writer per directory. Its methods serialize concurrent goroutines.
Cross-process writer coordination and directory authorization remain host responsibilities.
A reopen requires the same surface and baseline, plus explicit registration of host validators.
A changed validator or installer contract requires a new host binding version.
The store retains bytes independently of later source-directory changes and rejects corrupt or missing stored snapshots.

The [candidate tests](../eval/candidate/store_test.go) provide executable examples of atomic edits, freeze, fork and durable reopen.
See [Candidate surfaces](runtime.md#candidate-surfaces) for activation modes and host capability boundaries.

## Limits

- Inline and agent-as-tool tools cannot be stubbed. Test child agents through
  their own models and activity-tool stubs.
- Harness construction gates both model registration APIs before `register`
  runs, snapshots the gates, seals registration and verifies every stored client
  against that snapshot. Registration changes after the snapshot fail construction.
  Register every model inside `register`; later registrations return
  `runtime.ErrRegistrationClosed`.
- Clients retained from `rt.ModelClient` or `RegisterModelClient` during
  registration stay gated in hooks and planners. Preserve the supplied run
  context so capture can identify the call. Interceptor side calls share their
  outer call's capture identity. Direct runtime clients do not record requests;
  stopping before a recorded call returns `ErrCaptureNoModelCall` without calling
  the provider. Use planner-context clients for recorded requests.
- Original application-held clients, unregistered clients, and `h.Model` clients
  for direct `Decide` calls remain outside the gate. Registering a client does
  not modify the original object. Detached contexts without run or call identity
  also remain outside capture.
- Capture supports default and explicit context policies. Summary clients are
  registered by ContextModel.ID and use the same registration-time capture gate.
  Capturing does not consume the planner or summary scripted response.
- Child-run awaits return `ErrUnansweredAwait`; test their responder flows
  by running the child agent directly.
- The harness has no Temporal mode.

## Runtime reference

See [Harness](runtime.md#harness), [Capturing and deciding](runtime.md#capturing-and-deciding),
[Stubbing tools](runtime.md#stubbing-tools), [Expectations](runtime.md#expectations)
and [Trajectories from RunRecord](runtime.md#trajectories-from-runrecord) for the
full contracts.

## Release-criteria library

`eval/gate` provides deterministic statistics for paired context-compaction experiments.
`eval/evidence` classifies exported records without a model or harness.
The long-running fixture and commands are described below under Release-criteria
suite, including the current calibration and freeze prerequisites.

### Drive a session

Use `h.OpenSession(ctx)` when later inputs depend on earlier results.
Call `session.Run(ctx, agentID, input, options...)` for each turn.
Call `session.Result()` to return the completed session from a scenario's `Drive` function.
The result contains an owned snapshot of the run lists.
Individual results retain their original evidence objects.

The caller supplies each turn's conversation history.
Session does not append messages automatically.
Threshold-enabled agents use the runtime's existing continuation contract when the caller history matches its visible prefix.
Baseline agents with threshold disabled do not publish or adopt continuation.
`RunSession` uses this API and retains its existing behavior.

Context variants use `Variant.Policy.Context`, including `ContextWindow` for the pretend window.
The execution plan owns a deep copy before harness construction starts.
Stored fingerprints include sorted, unique `ContextFingerprints` from root and child runs.
This optional field retains store schema version 1 and compatibility with older experiments.

### Classify evidence

Call `evidence.Classify(records, spec, threshold)` with records in caller session order.
`Spec.Needs` declares each trigger and its required evidence IDs.
`Spec.Evidence` maps each ID to one literal value or lowercase SHA-256 asset digest.
For image or PDF claims, supply `Evidence{Asset: digest, Token: renderedToken}`.
The materializer declares the token rendered in the asset. Matching bytes in received
input of an adopted planner request support that token; the digest alone does not.
`Spec.Answer` declares the tokens required across the final answer and final action arguments together.
`Spec.Decoys` and `Spec.Grammar` define deterministic unsupported claims.
A grammar token requires support from received input of an adopted planner request.
User messages, tool results including recall, and checkpoint sections provide support.
Earlier assistant text, tool-call arguments, and final outputs do not provide support.
Stored content alone does not establish support.
Decoys remain unsupported even when received input contains them.

Only adopted planner requests establish need points.
The first request containing a trigger is that need's point, ordered by run, sequence, and attempt.
An absent value counts as recalled only in a later adopted request in the same run.
A result after the last adopted request does not count.
All absent values across all needs must return for recall success.

The sources are `pins`, `user_index`, `summary`, `receipt`, `tail`, and `recall`.
Checkpoint identity comes from message metadata.
`checkpoint.Sections` splits section bodies without trimming whitespace.
It selects the first user label, the last receipt label after it, and the last
summary label between those boundaries. This is exact for label-free components.
Embedded labels make the original boundaries ambiguous; the same precedence applies.
This affects only source attribution among `user_index`, `summary`, and `receipt`.
Sources are reported, never gated; presence and all gated measures are unaffected.
Retained checkpoint attachments count as `user_index`.
Image and PDF evidence requires matching bytes in the request.
An unsupported-asset recall result does not restore those bytes.

A hybrid summary that retains evidence is present evidence, not a recall attempt.
A missing need point makes evidence use and recall qualification false.
`Result.Checks(runsSucceeded, actionOK)` returns the two reserved checks.
`Result.Scores()` returns the reserved evidence and context-operation scores.
The scenario supplies its deterministic action check.

### Evaluate and decide

Use `gate.FromStored` after overlaying Parent reruns by attempt key.
It requires `task_success`, `no_unsupported_claim`, `evidence_used`, `recall_qualifying`, and `recall_success`.
Missing or nonbinary measures produce errors.
Infrastructure failures remain missing pairs. Agent run errors remain quality outcomes.

Call `gate.Evaluate(table, config, look)` on cumulative items.
Supply one or two active gated variant IDs, the baseline ID, scenario IDs, two repeats, and the explicit frozen Policy (including its bootstrap seed and alpha).
Supply `Config.Items` from the cumulative freeze manifest to detect entirely missing items.
Without a manifest, the observed item union defines the plan.
Reported variants and scenarios do not enter the gate.

The bootstrap resamples items within each scenario in sorted item-ID order.
Both repeats move together.
All measures and variants share each PCG draw.
The estimate gives each scenario equal weight.
Production uses 10,000 resamples and family alpha 0.05 divided across three looks.
Recall uses the item agreement design effect and a Wilson interval.
Exact margin equality is inconclusive for every criterion, including recall, as required by Q17.
The scenario guard fails only when the upper success-interval bound is strictly below -0.10.

Call `gate.Decide(evaluation, previous)` for the stopping decision.
For ordered execution, supply the consecutive decision prefix.
Each variant retains its terminal result while an active sibling continues.
An incomplete look requires exact missing-key reruns before any later look.
An empty operational prefix starts look 1. Use `DecideMode` with `Offline` for isolated offline analysis.
`gate.Render` reports criteria, precision, scenario estimates, and exact missing run keys.

### Calibrate and freeze

`gate.Calibrate(ctx, config)` runs all 1,620 cells plus two copula sensitivity rows.
`gate.CalibrateCandidates` evaluates all three fixed margins while sharing identical bootstrap work.
It uses the same evaluator and stopping rule as real experiments.
Decision cells use 2,000 simulations. Other cells use 400.
Qualification is an independent Bernoulli draw from the offline validated fraction.
Recall quality and the other measures use the Gaussian copula.
This preserves the stated recall rate among qualifying runs.
Baseline rates come from the pilot or labelled assumed defaults.
Worker count does not change the report bytes.
`gate.CalibrateChecks` runs the two reference cells with 200 simulations and the production bootstrap.
Only package tests can reduce the bootstrap count.
`gate.VerifyCalibrationReport` checks population identity, feasibility, outcome totals,
Monte Carlo uncertainty, failure causes, and scheduling cost without rerunning simulations.
The fixture also verifies the full grid and required simulation counts.

`gate.Freeze` stores the experiment identity, policies, margins, pool hashes, item hashes, and calibration provenance.
Pool keys in `FreezeInputs` use `scenario/partition`.
Materialized bytes use globally unique item IDs.
Pool digests cover the exact dataset file bytes.

Before each look or report, call `Freeze.Verify` with the trusted original freeze hash and current input bytes.
Then call `Freeze.VerifyLabel` for each stored experiment.
`Freeze.Label` creates `gate/<configuration>/<freeze-sha8>/look<N>`.
Call `gate.CanGenerateItems` before pool generation.
The library refuses regeneration when a freeze exists.
The fixture also keeps archived v1 freezes as pool locks.
The caller owns file creation, write-once enforcement, and storage of the trusted freeze hash.

## Release-criteria suite

The `integration_tests/fixtures/longrun` module contains the K19 operational suite.
Its generated agents cover deployment, ledger, and dispatch tasks.
Each task has standard, no-summarizer, and forced-rejection registrations.
`make regen-longrun-fixture` generates all nine registrations from the design.

The suite compares `baseline`, `receipt_only`, and `hybrid` through recorded
`Variant.Policy.Context` values. It reports `clear_only` at look 1.
Scenario overrides apply only to threshold-enabled variants.
Session scenarios supply the same caller history and earlier final messages to every variant.

Each scenario has separate pilot, look 1, look 2, look 3, and smoke pools.
The partitions contain 6, 12, 12, 24, and 3 items respectively.
The reported model-change scenario has no pilot pool.
CI runs only the smoke items. Live runs require `LOOM_LIVE_MODEL=1`.
Offline model results describe fixture structure, not model quality.

The evidence commands require an explicit pure-Go build (`CGO_ENABLED=0`).
The CLI refuses a stale executable or a different source identity before evidence writes.
Race tests use their normal cgo environment.

For an existing v1 fixture, declare the complete historical store before migration:

```sh
CGO_ENABLED=0 go run ./cmd/compaction-gate inventory --config offline --out .loom-evals --declare-complete-store
CGO_ENABLED=0 go run ./cmd/compaction-gate archive-v1 --config offline --inventory gate/inventory_offline.json
CGO_ENABLED=0 go run ./cmd/compaction-gate source --config offline --base origin/main
CGO_ENABLED=0 go run ./cmd/compaction-gate validate --config offline --workers 4
CGO_ENABLED=0 go run ./cmd/compaction-gate source --config offline --verify
CGO_ENABLED=0 go run ./cmd/compaction-gate calibrate --config offline --workers 4
CGO_ENABLED=0 go run ./cmd/compaction-gate freeze --config offline --inventory gate/inventory_offline.json
```

The declaration covers the named store only. It cannot discover undeclared external stores.
The inventory includes incomplete, orphaned, temporary, and malformed entries.
Unknown or consumed live evidence blocks migration.
The archive preserves exact v1 artifacts and their retained source revision.
Archived freezes continue to lock their pools. V1 evidence cannot authorize v2 looks.

`validate` uses offline clients with the selected configuration's recorded capability profile.
It collects every required item, variant, and repeat, including failed structural obligations.
Missing, duplicate, unexpected, or failed rows block calibration.
Recorded runtime settings supply the effective policies, including rules that depend on the context window.
Observed qualifying fractions of zero are valid. Missing observations are errors.
These fake outcomes describe structure, not live model quality.

The materializer version is `k19c-v4`. Raw pool bytes, item IDs, seeds, and partitions retain their historical values.
Stored `steps` describes the base workload. Materialization doubles that length, or quadruples it for `model_change`.
The current inputs therefore execute 36 steps per run, or 72 for `model_change`.
Materialized parameters record this effective length.
Each baseline request must contain fewer than 128,000 input tokens. Total bytes across separate runs do not represent one request.
The retained v1 source reproduces the historical workload.

Runtime compaction defaults remain unchanged. Named fixture stresses use 24k windows, or 8k for model change and recall pressure.
Recall pressure retains its three discovery and three read allowances.
Offline profiles declare synthetic windows of 1,000,000, 128,000, and 200,000 tokens for planner, small planner, and summarizer.
These capacities are fixture inputs, not claims about live providers.
Supporting profiles must recall both attachments. Zero-support profiles retain the evidence, record one residency event, and perform no threshold compaction.

Calibration measures all three unsupported-claim margins: 0.02, 0.03, and 0.05.
Each candidate includes 1,620 grid cells, two sensitivity cells, and the fixed 10,000-resample bootstrap.
Decision cells use 2,000 simulations. Other grid cells use 400.
Two additional 200-simulation cells provide reproducible checks.
Each variant selects its smallest eligible margin independently.
Eligibility requires worst null PASS of at least 0.80 and every worst boundary PASS of at most 0.05.
Reports retain uncertainty, stopping costs, and joint failure causes.
Identical bootstrap work can be shared without changing a candidate's counts or decisions.
The calibration CLI writes phase changes and a progress heartbeat every 30 seconds to stderr.
Updates include completed and total cells, completed and total synthetic simulations, percentage,
elapsed time, simulation throughput, and an approximate ETA for the current phase.
Simulation counts describe actual shared populations across candidates, not the sum of candidate counts.
Progress advances within long cells. Infeasible cells count as completed cells without simulated populations.
The ETA is unknown before measured progress and can change as cell costs vary; it is not a completion guarantee.
Input verification, check cells, candidate computation, selection, final verification, and artifact writing have distinct phases.
Only a successful artifact write produces the final complete status. Errors and cancellation retain failure status.
Progress stays out of stdout and saved calibration artifacts. Redirect stderr to retain the operator log.
Library callers can attach `gate.WithCalibrationProgress` to their context; observers must return promptly.

Calibration files are write-once. Freeze and report verification reuse complete results after checking their identities and counts.
A repeated `calibrate` command refuses to overwrite them; that refusal does not verify their contents.
Report-only changes do not require new simulations.

The committed offline calibration is a historical diagnostic snapshot from
`88eabc2283bc4d790e81cde84348737aae8212ef`, with source identity
`f6aef6fe246341995996075bcdef03dc60040c6fe8c5c607dd8dc50bf3eacafd`.
Both variants are ineligible. Progress reporting changes the executable identity;
the current executable has no matching calibration authorization and rejects
these historical inputs for calibration, freeze, pilot and operational looks.
The artifact test compares all retained evidence and pool bytes with that commit
and runs its unchanged artifact verifier in a temporary local checkout.
The current evaluator separately reproduces the two full-bootstrap check cells
and passes observed/unobserved equality tests. These bounded checks do not claim
that the full grid was rerun on the current source or transfer qualification.
Future qualification requires fresh current-source validation and calibration;
preserve the historical files before following the write-once evidence workflow.

`CALIBRATION_INELIGIBLE` excludes a variant from qualification work.
If neither variant is eligible, the complete diagnostic calibration remains available and `freeze` refuses authorization.
Offline assumed rates never qualify a live configuration.
Live calibration requires a complete baseline pilot with two repeats, matching roles, source, and pilot partitions.

For an eligible frozen configuration, run held-out looks in order:

```sh
CGO_ENABLED=0 go run ./cmd/compaction-gate look 1 --config offline
CGO_ENABLED=0 go run ./cmd/compaction-gate report --config offline --verify
```

A scenario guard stops a variant only when the success interval's upper bound is less than −0.10.
Each variant stops independently. Its terminal evidence remains unchanged.
`INCOMPLETE` requires an exact missing-key rerun of the same look, with original repeat numbers and a recorded `Parent`.
Valid observations cannot be replaced. A missing baseline runs once for its active dependants.
`COMPLETE` means that scheduling is finished. It does not mean that all variants passed.
The report’s `variants` list includes the latest eligible decisions and explicit `CALIBRATION_INELIGIBLE` siblings. Its `decisions` list retains the eligible-only operational prefix.
Informational failures cannot block gated advancement. Planned informational results with no stored attempts have `available: false` in `diagnostics`; observed results have `available: true`, regardless of their quality verdict.

`report` uses stored evidence and recorded model identities, without model environment variables or provider construction.
With `--verify`, it recomputes measures from exported records.
V2 verification binds policy, roles, capabilities, effective settings, pools, materialized items, validation, calibration, inventory, and executable source.
The source bundle reconstructs executable owners against a retained base revision.
Protected auxiliary sums and evidence outputs do not become executable source owners.

`items` refuses to regenerate pools after an active or archived freeze exists.
The embedded `run`, `show`, `compare`, and `list` commands use `evalcli.Main`.
Their ordinary `run` command uses the disjoint smoke pool.

Live configurations are `anthropic`, `bedrock-anthropic`, `openai`, and `gemini`.
Each configuration requires explicit model names for planner, small planner, and summarizer.
The environment variables use `LONGRUN_<CONFIG>_<ROLE>` with uppercase names and underscores.
For example, `LONGRUN_BEDROCK_ANTHROPIC_PLANNER_SMALL` names the small Bedrock model.
Provider API keys use the normal `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, and `GEMINI_API_KEY` variables.
Bedrock uses `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and optional `AWS_SESSION_TOKEN`.
The CLI never enables live models automatically.

## Prepared attempts and probes

Use `eval.Config.PrepareAttempt` when registration, inputs and outputs depend on one frozen candidate.
The host returns a lease with isolated resources, a receipt and explicit quiescence, collection and release callbacks.
Use `Store.Begin` → `Run` → `Writer.Finish` for evaluations.
Use `Store.BeginProbe` → `Probe` → `Writer.Finish` for operational tasks without a quality rubric.
Assign the admitted writer to `PreparedWriter` before execution.
Both paths require positive preparation and cleanup timeouts.

Probes always report `quality: not_assessed`.
A successful probe is `unassessed`; execution and infrastructure failures remain errors.
All probes stay outside quality denominators.
Strict checks reject probes, and evaluation rerun overlays do not apply to them.

The host collects declared output bytes before release.
Missing output differs from an explicitly retained empty file.
`Store.ReadArtifact` verifies retained size and SHA-256 before returning owned bytes.
Failed quiescence preserves a provisional mark on retained artifacts.

See [Prepared attempts and operational probes](runtime.md#prepared-attempts-and-operational-probes) for admission and lifecycle contracts.
Run `go test ./eval -run TestComponentProbe` for the submitted-source fixture.
This fixture freezes new Go source, compiles and executes it through a tool, and retains evidence after resource release.
The framework supplies no general shell, sandbox or optimization policy.

## Inspect retained experiment evidence

`eval/inspect` publishes immutable views of retained experiment evidence.
The host submits task sources, notes and evaluator references as explicit bytes.
A source remains inspectable even when the solver never opens it.
The host also supplies each attempt's expected deliverables before it releases the environment.
The eval store retains present outputs. Declarations identify missing outputs.

The host chooses the role, policy name and exact manifest scope.
List and read requests contain no role or scope override.
An editor can inspect components, linked diffs, approved runs, outputs and authorized notes.
A selector receives approved task sources and outputs, with opaque labels.
Only an evaluator can receive an explicit evaluator reference.
A locator from an editor view grants no access in a selector view.

After `Writer.Finish`, use this host function to publish an editor view.
Its inputs come from the admitted attempt and the host's retained snapshots.
The function applies no artifact redaction.
Its nil `RunContent` callback omits canonical content leaves.

Import `context`, `github.com/CaliLuke/loom-mcp/v2/eval`, `github.com/CaliLuke/loom-mcp/v2/eval/candidate` and `github.com/CaliLuke/loom-mcp/v2/eval/inspect`.

<!-- snippet: inspect-publish-editor -->
```go
func PublishEditorView(
	ctx context.Context,
	dir string,
	experiments *eval.Store,
	candidates *candidate.Store,
	origin inspect.Origin,
	candidateDigest string,
	declarations []eval.Deliverable,
	source []byte,
) (*inspect.View, error) {
	workspace, err := inspect.Open(dir, experiments, candidates, inspect.DefaultLimits())
	if err != nil {
		return nil, err
	}
	ref, err := workspace.Publish(ctx, inspect.Publication{
		Origin:               origin,
		CandidateDigest:      candidateDigest,
		ExpectedDeliverables: declarations,
		Attachments: []inspect.Attachment{{
			Name: "task-source.txt", Kind: inspect.TaskSource,
			MediaType: "text/plain", Data: source,
			Audiences: []inspect.Role{inspect.Editor},
		}},
		Policies: []inspect.Policy{{Role: inspect.Editor, Name: "application.editor.v1"}},
	})
	if err != nil {
		return nil, err
	}
	return workspace.View(
		inspect.Scope{Manifests: []inspect.ManifestRef{ref}},
		inspect.ViewPolicy{Role: inspect.Editor, Name: "application.editor.v1"},
	)
}
```

`View.List(ctx, inspect.ListQuery{})` returns the first page.
`View.Read` accepts an entry's opaque locator, byte offset and requested byte limit.
Both operations enforce the host's positive caps.
A zero request limit uses the configured cap. A larger limit clamps to that cap.
Follow `NextOffset` until it is nil to reconstruct the complete body or entry list.
Offsets count bytes, including multibyte text and binary data.
`Truncated` describes the response boundary. It does not change the artifact's state.

Artifact states distinguish available, missing, withheld and provisional evidence.
An available empty file has size zero and the digest of empty bytes.
A missing file has no fabricated digest or body.
Failed quiescence preserves provisional output marks from the eval store.
A generic release error alone does not mark retained bytes provisional.

The host computes optional `Rendering.Data` before publication.
Each rendering names the original source, its exact digest and a renderer version.
Inspect checks the declared source identity. The host owns the renderer's semantic correctness.
Raw bytes and rendered bytes remain separate artifacts.
The host also owns redaction consistency between those artifacts.

Policies approve or transform bytes once, during publication.
Nil `ArtifactContent` approves structurally permitted original bytes. It provides no automatic secret or PII removal.
The canonical deny rules still exclude reasoning, server-only content, metadata, diagnostics, raw attachments and system/developer instructions.
A permissive `RunContent` callback cannot restore those leaves.
Reopened views use persisted approved bytes and the original policy name.
A changed policy requires a new name and publication.

The host must retain the candidate, eval and inspect store roots for the locator's lifetime.
Inspect reuses original component and output owners rather than copying those bodies.
Owner data loss becomes an explicit missing or integrity state.
Publication does not grant access by itself. The host must bind each reader to an authorized finite scope.

Run the complete source, output and restart proof with:

```sh
go test ./eval -run '^TestComponentInspectRetainsEvidenceAcrossRestart$' -count=1
```

The fixture compiles two submitted helpers and retains distinct outputs.
It publishes a physical source absent from every solver request, linked diffs, a decision note and an evaluator reference.
A new reader process reopens all three stores after environment removal.
It checks exact bytes, renderer identity, role restrictions and stable pagination without model or compiler calls.

## Control candidates through native tools

`eval/control` exposes the candidate, trial and evidence operations through one Go service.
Register its native tools explicitly before sealing the controller runtime:

Import `github.com/CaliLuke/loom-mcp/v2/eval/control` and `github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime`.

<!-- snippet: control-register-tools -->
```go
func RegisterExperimentTools(rt *runtime.Runtime, cfg control.Config) error {
	service, err := control.New(cfg)
	if err != nil {
		return err
	}
	registration, err := control.NewToolsetRegistration(control.ToolsetConfig{
		Name: "experiment", Service: service,
	})
	if err != nil {
		return err
	}
	return rt.RegisterToolset(registration)
}
```

The host supplies one candidate store, one experiment store, fixed task bindings,
a verified scope, an authorization callback and explicit operation grants.
Set `Limits: control.DefaultLimits()` or provide positive read/page caps.
The host also supplies the `Executor` and `EvidenceProvider` for enabled trial/evidence operations.
No tools are installed by default. The service does not start a worker or an improvement loop.

A controller uses the advertised baseline to create its own draft.
It reads component bytes, edits with `expected_revision`, validates, freezes and submits the frozen digest.
Use `content` to author Go or Markdown directly. Use `content_base64` for binary bytes.
A put requires exactly one field, including an explicit empty string when empty content is intended.
Both forms submit the same exact candidate bytes and use the same slot limits.
A rejected edit preserves the revision. Fix its indicated field and retry that revision.
A stale revision requires `get_candidate` before retrying.

`submit_trial` defaults to an operational probe.
An evaluation requires an advertised evaluation plan and retains its quality requirements.
Retry an uncertain submission with the same `request_key` and identical inputs.
The host must persist request/job identity before acknowledging acceptance.
A different input under that key is a conflict, including after restart.
A new key represents a new intentional trial.

Poll `get_job`; use `cancel_job` to request cancellation.
`cancel_requested` records intent and does not prove execution stopped.
Only the host can commit a terminal status after execution and finalization.
An uncertain lost execution becomes `interrupted`; it is not automatically replayed.
Already committed terminal states remain unchanged.
For a failed or interrupted job, inspect retained diagnostics before deciding
whether another trial is appropriate. Submit a deliberate new trial with a new
`request_key`; the original key continues to return the original job and evidence.
Known queued or running jobs should be polled, not resubmitted as new trials.
`compare_trials` returns existing execution statistics; probes have `not_comparable` quality and no quality counts.

For repeated trials, pass `baseline_job_ids` and `candidate_job_ids` instead of
the existing `baseline_job_id` and `candidate_job_id` pair. For example:

```json
{"baseline_job_ids":["base-case-a-1","base-case-a-2","base-case-b-1","base-case-b-2"],"candidate_job_ids":["new-case-a-1","new-case-a-2","new-case-b-1","new-case-b-2"]}
```

Each list contains 1 through `Limits.MaxPageEntries` jobs (100 by default).
The two request forms cannot be mixed. Repeated job IDs and aliases of the same
stored experiment/attempt are rejected, including across sides. Every job must
be authorized, terminal and associated with a committed complete single-attempt
experiment. Missing or unfinished evidence returns `evidence_not_ready`.

Each side selects one candidate and one execution kind with compatible
installation bindings. Cases match by recorded scenario/item identity. All
occurrences of a case must share its task ID, task snapshot, host/component
bindings and, for evaluations, evaluation plan. Different cases can have
different task snapshots and plans. Candidate model settings can differ between
sides. A conflicting case binding is rejected rather than merged.

All selected repeats of a shared case are pooled using existing eval statistics.
Independent submissions may all have `Repeat: 1`; their original experiment and
attempt identities remain unchanged. Unequal repeat counts use all selected
observations, without pairing repeats or inventing missing observations.
`groups` reports safe member job IDs/states and per-case statistics.
`only_baseline` and `only_candidate` report jobs whose cases have no counterpart;
these jobs are excluded from the top-level totals. No shared cases yields
`not_comparable` and zero totals.

Pending judgments stay pending. Interrupted, failed and canceled terminal jobs
remain visible when their committed attempt is available. A side cannot mix
probes and evaluations; comparing a probe side with either kind omits quality
counts on both sides. Warnings retain existing eval qualifications, with
`no_agent_change` only when every matched source comparison agrees.
The response omits protected case identifiers, configuration, annotations and
lineage; original lineage stays in the store and existing job evidence controls.
Comparison never executes a trial, calls a judge, selects a winner or promotes a candidate.

Evidence requests take a job ID and bounded pagination or locator fields.
The host provider resolves the authorized durable job to its committed manifest,
then constructs a fresh exact `inspect.View` with a fixed role and policy name.
The service never grows an existing view's scope to include future jobs.
Publication alone does not grant job access; the durable job association must be committed.
Task creation, credentials, live execution permission, filesystem roots and evidence roles are host configuration.
They cannot be supplied or widened through tool arguments.

For a CLI, register the leaf `eval/controlcli` adapter through `evalcli.Config.Extensions`.
Its binder receives the selected `*eval.Store` and must wire execution and evidence to that store.
Use `store.Dir()` to derive adjacent host-owned candidate, inspection or admission
directories, for example `store.Dir() + "-candidates"`. It returns the absolute
directory resolved when the store opened, including the default, `Config.StoreDir`
or a common `--out` before or after the command. Reopen the same selected store to
derive the same adjacent locations; no additional root option or argv parsing is needed.
The syntax is `experiment <operation> --request '<JSON object>'`.
For example, a text edit uses:

```text
experiment edit_candidate --request '{"draft_id":"<draft>","expected_revision":1,"operation":"put","name":"helper.go","slot":"helpers","media_type":"text/x-go","content":"package main\nfunc main() {}\n"}'
```

Core `--out` and `--` handling stays with `evalcli`; the native tools have no store-path argument.
The Go, native and CLI surfaces share request validation and static safe error codes with repair guidance.
Raw compiler, filesystem and executor diagnostic strings stay out of public errors.
Approved retained diagnostics can be published as evidence under host policy.

The generated agent-feature fixture exercises the controller journey through native tools.
The disk-backed executor under `eval/control/testfixture` proves crash recovery and cancellation arbitration.
It is an explicit test host, not a production scheduler.
Run the focused checks with:

```sh
go test ./eval/control/... ./eval/controlcli/... ./eval/evalcli/...
make verify-agent-feature-fixture
go test -race -shuffle=on -count=20 -timeout=15m ./eval/control -run '^(TestControlRecovery|TestControlCancellation)'
```

## Reuse procedures across consumer tasks

A candidate is a complete content snapshot. Reuse is an explicit selection of component bytes and dependencies.
Copy imports selected component bytes and content metadata. Components can contain facts.
Applications must keep current task facts in separately bound inputs and sources.
Copy transfers no execution state, credentials or tool authority.
`eval/candidate.Store.PreviewCopy` returns the complete transitive closure and every destination collision without changing the draft.
The controller must inspect that plan before calling `Copy` with its `Digest` and exact `Replace` names.
A changed destination revision or selection requires a new preview.
The native operations are `experiment.preview_copy` and `experiment.copy_candidate`.
The CLI uses the same operation names after `experiment`, with a JSON `--request` object.
Both operations require current read, edit and operation-specific grants.

The copy transaction writes bytes and immediate-source origins together, then increments the destination revision once.
A copied component records its source candidate, component name and source byte digest.
It owns its copied bytes; it is not a live reference.
A later explicit edit clears that component's origin, including an identical-byte put.
Origin-bearing candidates use manifest version 2. Candidates with no origins retain their existing version 1 identity.
Keep immediate source snapshots available while inspecting provenance.
Copy provenance is independent of eval experiment `Parent` and rerun behavior.

The agent-feature fixture's `TestExperimentSupportConsumers` covers document and graph tasks under revision and composition.
The controller creates every draft through native tools; fixture setup contains only a baseline and a fixed task registry.
Revision authors a new Go procedure, skill and child instruction.
Composition executes two independent proposals, then executes a third candidate containing selected components from both.
The host compiles the exact submitted Go bytes and invokes the resulting executable through `components.run_helper`.
The next model request consumes its actual stdout. It sends that analysis to the generated delegated specialist.
The host registers the generated specialist through public runtime registration with exactly one current-source read grant.
It uses generated agent/workflow/activity identities, delegated routing and typed result codecs.
The child performs that read and checks its actual facts against the helper analysis.
The child receives the exact authored instruction in its canonical system message.
Its actual response enters a subsequent parent request and the retained consumer output.
The finite fixture child grammar is `section=<label>` followed by a newline; arbitrary child programs are unsupported.

Every task B starts from baseline defaults, without A-authored helper or skill bytes.
The controller reads those defaults, previews the selected A skill's full closure, and confirms both baseline replacements.
The copy imports only that skill and its helper. It preserves B's baseline child configuration.
The procedure reads fresh B facts at execution; it contains no A document, node, edge or beneficiary identifiers.
Receipts bind B's source and input snapshots. Component origins identify the selected immediate A or composition candidate.
This separates reusable procedure evidence from claims that a prior answer is valid for a new task.

The document host reads a current document, proposes an edit, then accepts the exact proposed bytes.
Its retained receipt checks the current document identity, a new version and preserved protected text.
The graph host discovers and calls a permitted read tool against current release, capability and beneficiary nodes.
It retains a draft-aware release description and the exact node and edge identities without editing the graph.
Both hosts retain sources, diffs and outputs through M3 publication after prepared writer finalization.
Reopened stores return the same authorized pages and exact artifact bytes after the temporary host directory is released.
An unopened physical source remains outside canonical solver requests.
Private canonical evidence proves child system bytes; public run projections continue to hide system and developer messages.

The fixture advertises only these installation contracts:

| Component class | Fixture installation and authority |
| --- | --- |
| System instructions | Exact per-attempt standing messages; no tool grants |
| Guardrails | Standing instruction content; host validation and tool authority remain fixed |
| Tool guidance | Descriptions on the host's existing permitted domain tools |
| Child instructions | Exact generated specialist system message with bounded declared grammar |
| Skills | Discoverable `SKILL.md`, activated through real skill list/load tools |
| Scripts and helpers | Exact stdlib Go source compiled into an isolated host executable |
| Memory | Unadvertised; edits are rejected before execution |
| Plugins and hooks | Unadvertised; edits are rejected before execution |

A security permission is host configuration, not an editable component class.
Unknown binding versions and unsupported activation modes fail before installation or model dispatch.
`eval/testfixture.InstallComponentBindings` is a bounded fixture seam for existing fresh host directories.
It returns real skill/helper registrations and executable identity, without sealing a runtime or registering a solver.
The caller validates its complete surface first and owns quiescence, collection and cleanup.
Existing `ComponentHost` still accepts only its original two bindings.
This compiler host is not an untrusted-code sandbox or a production plugin loader.

Production consumer adoption remains application work.
For a document assistant, snapshot prompts, tool guidance and document state once before registering and preparing a run.
Bind document reads and propose/accept tools to that snapshot and preserve protected document content.
For a graph assistant, pass an explicit prompt catalog directory and keep capability, beneficiary, draft and node/edge evidence current.
Do not mutate process-global prompt roots between attempts.
Retain the application's shared review/evaluation authority, including its existing Opik review workflow.
These fixtures do not change either consumer's production source or replace its authorization and review policy.

Run the focused consumer proof from the generated agent-feature fixture module:

```sh
go test . -run '^TestExperimentSupport' -count=1 -timeout=180s
go test -race . -run '^TestExperimentSupport' -count=1 -timeout=180s
```
