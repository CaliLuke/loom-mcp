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
before the next attempt starts. Process-state changes still require a
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

## Limits

- Inline and agent-as-tool tools cannot be stubbed. Test child agents through
  their own models and activity-tool stubs.
- Models registered after `New` are not gated by capture. Register every model
  inside `register` before constructing the harness.
- Unanswered child-run awaits time out instead of reporting an unanswered await.
- The harness has no Temporal mode.

## Runtime reference

See [Harness](runtime.md#harness), [Capturing and deciding](runtime.md#capturing-and-deciding),
[Stubbing tools](runtime.md#stubbing-tools), [Expectations](runtime.md#expectations)
and [Trajectories from RunRecord](runtime.md#trajectories-from-runrecord) for the
full contracts.
