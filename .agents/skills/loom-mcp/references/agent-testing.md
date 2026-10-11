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
installs capture gates and seals runtime registration. Later registrations,
including model replacements, return `runtime.ErrRegistrationClosed`.
`LOOM_LIVE_MODEL` gates only `LiveModel` and `SkipUnlessLive`. Unset or `0` disables,
`1` enables and any other value fails. LiveModel creates no provider: the
application registers the named client. Undeclared application clients are not
blocked, so the variable is not a universal network guard. Default tests use
scripted clients; real providers need explicit opt-in.

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

Capture returns `(Request, error)`; it propagates a sentinel rather than consuming
the model. Decide returns `*Decision` and runs no tools. Repeat takes
`func(testing.TB)`. An attempt that fails and then skips counts as failed; only
a skip without failure skips the parent and stops. Attempt cleanup restores
Setenv and Chdir (including PWD) and removes TempDir directories before the next
attempt. Process-state changes still require a non-parallel parent test.
Save/load full request fixtures only under a gitignored
application path. Re-capture when planner decoration changes tool definitions.

## Eval runner

- Run matrices with `eval.Run` and `eval.NewHarnessFunc(register, setup, base...)`.
  Factories receive the variant and supplied options, forward options and return
  fresh harnesses. Use ScriptedModel for per-harness scripts; never share scripts.
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

## Diagnose failures

Inspect Run's harness error before its populated Result. `RunErr` is the agent
outcome, while a joined `*StubError` reports strict, exhausted, unmatched or decode
failures and also appears at Close. Use `errors.As` and read the tool and arguments.
`Trajectory.Render` provides causal evidence with explicit truncation limits.
An unanswered root await returns `ErrUnansweredAwait`; child awaits time out
instead. There is no Temporal harness mode. Consult
[the runtime reference](../../../../docs/runtime.md#harness) for details.
