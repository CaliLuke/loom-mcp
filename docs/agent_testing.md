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

Failed checks do not change the command’s exit code. Read the summary to assess
quality. Use `--json` for one structured run document. Progress stays on stderr,
and `--quiet` suppresses it. Cancellation and operational failures return `1`.
Usage errors return `2`.

```sh
go run ./cmd/evals run --rerun-failed <experiment> --compare <experiment> --merge-parent
go run ./cmd/evals compare <experiment> --variants baseline,candidate
go run ./cmd/evals list --limit 20
```

Reruns retain all repeats and inherit the parent’s repeat count unless overridden.
Selection flags cannot accompany `--rerun-failed`. `--out <dir>` overrides the
store for run, show, compare and list. Otherwise the default is `.loom-evals`
beside the nearest ancestor `go.mod`.

For live variants, declare `agenttest.LiveModel("model-id")` and set
`LOOM_LIVE_MODEL=1` in the calling environment. The CLI never sets it.
A disabled live variant produces a stored stopped experiment and an opt-in hint.
The examples use no confirmation flows.

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
- Capture rejects the harness if any registered agent has a non-nil History
  policy, including `Compress`, `KeepRecentTurns`, and custom policies. It returns
  `*agenttest.CaptureHistoryError` naming the agent before creating a session or
  calling any policy or model. History functions can retain clients outside the
  capture gates, and their captured state cannot be inspected. Capture never
  skips the policy to manufacture a different request. Ordinary `Run` remains
  available with History policies.
- Child-run awaits return `ErrUnansweredAwait`; test their responder flows
  by running the child agent directly.
- The harness has no Temporal mode.

## Runtime reference

See [Harness](runtime.md#harness), [Capturing and deciding](runtime.md#capturing-and-deciding),
[Stubbing tools](runtime.md#stubbing-tools), [Expectations](runtime.md#expectations)
and [Trajectories from RunRecord](runtime.md#trajectories-from-runrecord) for the
full contracts.
