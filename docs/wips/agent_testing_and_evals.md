# Agent Testing and Development Evals

Status: draft for peer review, revision 5. Date: 2026-10-10. Revision 5 aligns
the plan with the built K8 record and the binding eval decisions.

This document plans two related capabilities for loom-mcp:

- **Plan A: agent and tool testing.** Deterministic and live-model tests that run
  under `go test`. The main use is proving a new or changed tool: the agent can
  call it, its contract decodes, and a real model selects it for the right
  requests.
- **Plan B: agent development evals.** A development loop for making an agent
  perform: run cases repeatedly, record full trajectories, judge them, find out
  why attempts fail, and compare one version of the agent with another.

Both plans share one foundation: typed tool descriptors and a **trajectory**
built from the transcript index and **`RunRecord`**, including K8 model-call
records.

Plan B is a development tool, not a CI gate. Its command does not encode
pass/fail in its exit status.

---

## Contents

1. Glossary
2. Background
3. Goals, non-goals, success criteria
4. Worked examples
5. Shared foundation
6. Plan A: agent and tool testing
7. Plan B: agent development evals
8. DeepEval trajectory work style: support map
9. Tickets and phasing
10. Open decisions
11. Alternatives considered
12. Risks
13. Data sensitivity
14. Changes to existing plans
15. Follow-ups

---

## 1. Glossary

| Term | Meaning |
|---|---|
| Run | One agent run (workflow execution), with a run ID. Unchanged from the runtime. |
| Session | The runtime session that groups runs. Child agent runs share the parent's session. |
| Trajectory | The typed record of one run and its child runs: model calls, tool calls, awaits, answer, usage, timing. |
| Session trajectory | The ordered trajectories of every run in one session. Used by multi-turn cases. |
| Model call | One request to a model client and its response, recorded by K8 for agent runs or held by `Decide` for a standalone call. |
| Scenario | A named kind of case with shared code, task, and assertions. |
| Item | One input to a scenario: a single prompt or a list of user turns, plus optional data. |
| Dataset | The items of a scenario. From Go values, a JSONL file, or Opik. |
| Attempt | One execution of one item. An item runs once per repeat. |
| Experiment | One execution of a selection of scenarios × items × repeats, with recorded identity. Plan B's unit of storage and comparison. Matches Opik's term. |
| Check | A deterministic pass/fail assertion with a diagnostic on failure. |
| Claim | An English sentence judged against an answer, trajectory, session or call prefix by a model. Labels: `entailed`, `contradicted`, `not_addressed`, `indeterminate`. A claim is gating by default; `Informational: true` records it without affecting the attempt result. |
| Score | A deterministic number with a name. Informational unless a threshold is set; numeric judge scores are out of scope. |
| Attempt passed | No infrastructure error, every check passed, every unit of every gating claim `entailed`, every score at or above its threshold when one is set. Used by both `agenttest.Repeat` and experiments. |

---

## 2. Background

### 2.1 goa-ai `eval/`

Reviewed at `goadesign/goa-ai` HEAD `ee13dfa`. Non-test code under `eval/` is
about 3,500 lines; 1,300 of those are the DSL code generator. The judge also
depends on `runtime/agent/tooloutput` (about 460 lines).

| Part | What it does |
|---|---|
| `eval` (types, runner) | Scenarios return a `Result` with checks and claims. The runner selects by ID or tag, runs with bounded concurrency and timeouts, calibrates the judge, and returns a JSON report in declaration order. |
| `eval/judge` | One model call per scenario. Each claim ID is a required property of a private tool schema. Only `entailed` passes. A separate `reference` argument carries shared facts. Built on `tooloutput.Run` (forced tool choice) or `tooloutput.RunAutomatic` (automatic choice, one valid call required). |
| `eval/evidence` | A collector turns stream events into `Evidence`: tool calls with canonical arguments and results, nested calls in causal order, the answer, a pending confirmation, the terminal phase. `ToolCalls` (started here) are kept apart from `ToolCompletions` (finished here), for continuations. `Expect` turns evidence into checks. |
| `eval/dsl`, `eval/codegen` | Suites in the Goa design; generated hook interfaces, typed inputs, and a one-time command scaffold. |

What it does well, and this plan keeps:

- strict result contracts: a result needs a check or claim; a failed check needs
  a diagnostic and a passed check must not have one; names are unique;
- typed tool assertions that break at compile time when a contract changes;
- a judge that cannot misassign judgments: claim IDs are schema properties,
  duplicate JSON members are rejected (including escaped spellings), claim IDs
  must be valid UTF-8, the schema has a 1 MiB limit with no truncation or
  splitting, the caller sets the output-token limit, temperature is 0, and
  there is one call plus at most three corrections;
- an empty answer labels every claim `not_addressed` without a model call;
- calibration on four fixed examples, one per label, under a two-minute
  deadline;
- runner rules: every selected case gets exactly one finished report, a
  never-started case has a zero start time, cancellation returns a partial
  report, the timeout covers the hook and the judging, empty, duplicate, and
  unknown selectors are rejected, and the longest timeouts are scheduled first
  while reports keep declaration order;
- `Expect` rules: a contains-match binds a declared tool to the first later call
  that satisfies it; `RequireAllAttemptsSuccessful`; a built-in terminal-phase
  check.

What it lacks for agent development:

- no run storage, identity, or comparison;
- one input per scenario; no datasets or repeats;
- no model calls, usage, or timing in the record;
- no way to replay one model decision;
- a CI pass/fail target, with API churn: breaking changes in commits `2f520f7`
  (Sep 4), `6e49350` (Sep 7), `60053d7` (Sep 9), and `2183e7c` (Sep 18).

### 2.2 DeepEval trajectory-based evaluation

Sources: <https://deepeval.com/guides/guides-ai-agent-evaluation>,
<https://deepeval.com/docs/evaluation-trajectory-based-llm-evals>, and the
metric pages.

1. A dataset holds goldens. A golden is the input for one trajectory, single
   turn or multi-turn. Multi-turn goldens carry a scenario, an expected outcome,
   and a persona; a conversation simulator can play the user.
2. Instrumentation captures the trajectory: LLM generations, tool calls,
   retries, and sub-agent handoffs, with timing, inputs, and outputs.
3. Each trajectory is linked to its golden.
4. Metrics read the trajectory and return a 0–1 score, a reason, and pass/fail
   against a threshold. Built-ins: task completion, step efficiency, plan
   quality, plan adherence (all model-judged), tool correctness (deterministic
   comparison with expected tools on the golden, plus an optional model check),
   argument correctness (model-judged, no reference), and custom criteria
   (GEval). Metrics can also be attached to individual spans.
5. Scores and reasons are stored with the trajectory for inspection.

Section 8 maps each part to this plan.

### 2.3 Our tooling

| Tool | Role | Use in this plan |
|---|---|---|
| Logal | Local OTLP collector into SQLite, 48h retention | Inspect spans of a local attempt |
| Opik (self-hosted, project `autok-agent-evals`) | System of record for traces, review queues, datasets, experiments | Shared review and comparison of experiments; dataset source |
| Earwig | Captures Claude Code, Codex, and OMP turns into Opik through a local spool | Not in the eval path. Its delivery model (local spool, deterministic IDs, idempotent export) is the template for B9. |

Auto-K is the application that owns the Opik project's datasets, review queues,
and experiment conventions.

### 2.4 Runtime facts this plan depends on

Verified against the code at the date above.

- **Run log** (`runtime/agent/runlog`): a durable, append-only store with stable
  order per run, listed by run ID. Appends are a correctness invariant: a failed
  append fails the activity. It records hook events including
  `tool_call_scheduled`, `tool_result_received`, `assistant_turn_committed`,
  `thinking_block`, `planner_note`, `retry_hint`, `policy_decision`, `usage`,
  `await_*`, `tool_authorization`, `run_phase_changed`, `child_run_linked`,
  `prompt_rendered`, and `hard_protection_triggered`.
- **Stream** (`runtime/agent/stream`): a client-facing projection of the hook
  events. It is best-effort (sink errors are logged, not returned), it is only
  emitted for runs in an open session, `assistant_reply` text can later be
  discarded (`model_presentation`), and it has no explicit model-turn
  boundaries. It is not the trajectory source in this plan.
- **Model calls**: `assistant_turn` events never carry tool calls; adapters put
  tool calls in `Response.ToolCalls`. `usage` events have no call ID, and
  streaming emits several per call. Model-recovery retries and rejected tool
  calls are not visible as observer events. K8 call records hold model-call
  evidence independently of those events (5.3).
- **Run log limits**: the default store is in-memory. Application `BeforeEvent`
  interceptors can drop or rewrite events before they are appended.
  `tool_result_received` events over 1 MB omit inline result and server bodies,
  retaining stored refs and a bounded preview (a fallback when no template exists).
  RunRecord resolves full results from canonical index refs, independently of
  these observer events. Record events, including child links, skip interception.
  `prompt_rendered` is skipped (and only logged) when
  the hook context is missing. `tool_call_args_delta` is never stored.
- **Interceptors**: `BeforeTool` can return a result without running the
  executor, but only for tools run by `ExecuteToolActivity`. Inline tools and
  agent-as-tool children skip interceptors. `BeforeModel` short-circuits only
  `Complete`, never `Stream`, and the response is still validated; it sees the
  request before later interceptors run and before the runtime re-applies cache
  policy, tool policy, and `tool_unavailable` decoration after `BeforeModel`. `AfterModel` sees the run and turn IDs and the final
  request, but receives a nil response for streams, and fires on streams as
  soon as the stream opens, before any chunk is read. `AfterModel` can replace
  a response, and tool-unavailable calls are rewritten after the client
  returns.
- **Model clients**: `RegisterModel` accepts any `model.Client` without
  validation. Validation applies when a client is built with
  `model.NewClient(provider)`. A registered client is the innermost layer,
  inside the runtime's call recorder and interceptors, and is shared by every
  run. The planner's model-client path places `model.CallInfo` in the call
  context (5.2). Direct `Runtime.ModelClient` calls do not receive it or get
  recorded. `model.Request.RunID` is optional, with no turn or call ID.
  `model.Request.PromptRefs` lists the prompts used.
- **Run results**: `RunOutput` holds the final assistant message (`Final`),
  tool-event refs, notes, and aggregate `Usage`.
- **Awaits** resume the same run (`ProvideConfirmation`, `ProvideClarification`,
  `ResumeRun`), so a tool call never starts in one run and finishes in another
  today. goa-ai's continuation handling is not needed until the ROADMAP's
  cross-workflow continuations land.
- **Child runs** share the parent's session ID. `child_run_linked` carries the
  tool name, call ID, child run ID, and child agent ID.
- **Prompts**: `prompt_rendered` carries prompt ID, version (a hash of the
  template, not of the rendered text), and scope, and fires only for
  registry-rendered prompts. `model.Request` has no system field; the system
  prompt is in `Messages`.
- **Codecs**: toolset specs packages name codecs `<TypeName>Codec`
  (`tools.JSONCodec[*T]`) and expose `Specs` and `Spec(name)`. Agent helper
  packages re-export them as `<Const>PayloadCodec` and `<Const>ResultCodec`.
  There is no `TypedTool`. `ToolSpec` embeds codec functions, so it cannot be
  hashed directly.
- **Tracing**: there is no run-level span, only planner and model spans. The
  default tracer is a no-op. The in-memory engine keeps the caller's context,
  so when a real tracer is installed (`WithTracer`), a harness-opened root span
  becomes the parent of the run's spans.
- **Typed completions** (`runtime/agent/completion`) give provider-enforced
  structured output from a `Spec[T]` that can be built at run time, but forbid
  tools.

---

## 3. Goals, non-goals, success criteria

Goals:

- Prove a new or changed tool in `go test`: it is wired, its contract decodes,
  and a real model selects it with correct arguments for the intended requests.
- Iterate on agent quality: run cases repeatedly, see the full trajectory of
  every attempt, judge outcomes, and compare two versions of the agent.
- Support the DeepEval trajectory work style natively, without manual
  instrumentation.
- Produce results that people and coding agents can both read.

Non-goals:

- CI gating semantics for experiments. Plan A tests can gate CI through
  `go test`.
- An eval DSL, eval codegen, or generated command scaffold.
- Significance tests in the first version.
- Production monitoring and online evaluation.
- A hosted service. Opik is the shared store.
- Carrying pending tool calls across successor runs. Not needed today (2.4).

Success criteria:

- A selection test for a new tool is under 30 lines of test code.
- Story (b) in section 4 takes three commands: run the baseline, change the
  prompt and run again with `--compare`, then `show` a failing attempt.
- A coding agent can diagnose a failed attempt from `show --json` output alone.
- An experiment records enough identity to tell whether a change reached the
  agent.

---

## 4. Worked examples

Story (a) reflects the implemented Plan-A APIs. The compiled examples in
[`docs/agent_testing.md`](../agent_testing.md) are authoritative. Story (b) remains
a Plan-B target API.

### Story (a): I added a `search_orders` tool

```go
func TestSearchOrdersSelection(t *testing.T) {
	ctx := t.Context()
	h := agenttest.New(t, bootstrap.Register, agenttest.LiveModel("default"))

	// Capture one real request from the agent, then reuse it.
	base, err := h.CaptureFirstRequest(ctx, chat.Agent, agenttest.User("Where is my order 1234?"))
	if err != nil {
		t.Fatal(err)
	}

	agenttest.Repeat(t, 5, 4, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("default"), base)
		expect.Require(at, expect.Calls(d.Calls),
			expect.Call(orders.SearchOrdersTool, func(p *orders.SearchOrdersPayload) error {
				if p.OrderID != "1234" {
					return fmt.Errorf("order_id = %q", p.OrderID)
				}
				return nil
			}, nil),
		)
	})

	// Negative case: a request that must not use the tool.
	other := base.WithUserMessage("What are your opening hours?")
	agenttest.Repeat(t, 5, 5, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("default"), other)
		expect.Require(at, expect.Calls(d.Calls), expect.Forbid(orders.SearchOrdersTool))
	})
}
```

A wiring test runs the full agent with a scripted model and a stubbed tool:

```go
func TestSearchOrdersWiring(t *testing.T) {
	ctx := t.Context()
	h := agenttest.New(t, bootstrap.Register, agenttest.ScriptedModel("default",
		agenttest.Respond(agenttest.ToolCall(orders.SearchOrdersTool, &orders.SearchOrdersPayload{OrderID: "1234"})),
		agenttest.Respond(agenttest.Text("Order 1234 ships tomorrow.")),
	))
	agenttest.Stub(h, orders.SearchOrdersTool).Handle(func(ctx context.Context, p *orders.SearchOrdersPayload) (*orders.SearchOrdersResult, error) {
		return &orders.SearchOrdersResult{Status: "shipping"}, nil
	})

	result, err := h.Run(ctx, chat.Agent, agenttest.User("Where is my order 1234?"))
	if err != nil {
		t.Fatal(err)
	}
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Call(orders.SearchOrdersTool, nil, nil), expect.Completed())
}
```

### Story (b): my agent fails 2 of 5 refund requests

```sh
go run ./cmd/evals run --scenario refunds --repeat 5 --label baseline
# edit the system prompt
go run ./cmd/evals run --scenario refunds --repeat 5 --label clearer-policy --compare last
go run ./cmd/evals show <experiment>/<attempt>
```

The first run prints a summary with failing attempts first. The comparison
reports pass counts per item (3/5 → 5/5), changed checks and claims with
rationales, token and latency changes, tool-sequence changes, and a warning when
the rendered system prompt and tool definitions did not change between the two
experiments. `show` renders one attempt's trajectory with its checks,
judgments, and trace ID.

The command is the application's own `main`:

```go
func main() {
	evalcli.Main(evalcli.Config{
		Harness:   eval.NewHarnessFunc(register, setup),
		Scenarios: []eval.Scenario{refundsScenario, ordersScenario},
	})
}
```

---

## 5. Shared foundation

### 5.1 Typed tool descriptors

Add `tools.TypedTool[P, R any]` to `runtime/agent/tools`:

```go
type TypedTool[P, R any] struct {
	Name    Ident
	Payload JSONCodec[P]
	Result  JSONCodec[R]
}
```

Codegen emits one descriptor per tool next to the existing codecs:

```go
var SearchOrdersTool = tools.TypedTool[*SearchOrdersPayload, *SearchOrdersResult]{
	Name:    SearchOrders,
	Payload: SearchOrdersPayloadCodec,
	Result:  SearchOrdersResultCodec,
}
```

`TypedTool` has an `Ident() Ident` method, and so does `tools.Ident` itself.
APIs that only need a tool's name, such as `expect.Forbid`, accept the small
interface `tools.Named` and work with both.

This is a user-facing codegen change. The ticket updates `docs/`, the loom-mcp
skill, and regenerates fixtures (`make regen-assistant-fixture`,
`make gen-registry`).

### 5.2 Model-call identity (T0, done)

T0 is implemented. A shared client can identify calls from concurrent runs:

- the runtime places an exported `model.CallInfo{AgentID, RunID, RootRunID,
  SessionID, TurnID, ModelID, Seq, Attempt}` in the context of every model call
  in the planner's model-client path (`simplePlannerContext.ModelClient`);
- `model.CallInfoFrom(ctx)` reads it. `RootRunID` is the shared run-store scope.
  `Seq` orders the calls of one run and is unique across retries and parallel
  calls. It comes from the run store's `NextSeq` (compaction K2), scoped by
  `(RootRunID, RunID)`. `Attempt` is the engine's planner activity attempt,
  starting at 1; it is 0 when a planner activity is invoked without an engine
  context.

This identity also helps provider logging and tracing. Direct
`Runtime.ModelClient` calls do not receive it automatically.

### 5.3 Model-call capture

K8 call records (`runtime/agent/callrecord`) are the capture for agent runs.
The runtime stores request parts, response, error kind, usage, and latency in
the run store, keyed by `(RunID, Seq, Attempt)` from `CallInfo` within
`RootRunID`. The trajectory reads them through `RunRecord`:

- the request as sent to the provider (`model.Request`, including messages,
  tool definitions, and `PromptRefs`), with model ID and model class;
- the provider response as returned by the registered client (validated when
  the client was built with `model.NewClient`): the `Complete` result, or the
  final response of a stream after it finishes, with usage and latency;
- or the error: its validation kind for an `OutputValidationError`, otherwise
  its message.

`callRecordingClient` is the innermost runtime layer around the registered
client. It records the effective request after `BeforeModel`, cache policy,
tool policy, and `tool_unavailable` decoration. Unary responses are recorded
before `AfterModel`. For streams, `AfterModel` runs when the stream opens and
the response is recorded at finalization. `AfterModel` response replacements
and the later rewriting of tool-unavailable calls are outside the record.
Calls short-circuited by `BeforeModel` do not reach the recorder. Raw rejected
provider output is not recorded (ROADMAP non-goal).

`callrecord.Encode`, `DecodeRequest`, `RequestDigest`, and `Rebuild` provide the
stable request codec and exact reconstruction. T3 adds `DecodeResponse` in
`callrecord`, without adding an export field. K8 tests cover request
round-trips and unary and streaming call recording.

Direct `Runtime.ModelClient` calls are not recorded. `Decide` holds its own
request and response.

Replay caveats: the unexported `completionValidate` callback is not carried
over; thinking signatures are provider-specific and are not replayed on
another provider; `Params.RunID` keeps the original run ID. T9 adds
`WithRunID` to change it explicitly.

### 5.4 Trajectory

Package `trajectory` (home per D1) builds a `Trajectory` for one run from the
**transcript index and `RunRecord`** (compaction K8):

1. `Run.Entries`: tool calls are loaded from index `Calls` hashes; results and
   their metadata come from entry `Output` refs, resolving full content through
   `RunRecord`. Committed assistant messages, clarification answers, and typed
   inputs also come from the record (`context_compaction.md` 4.4);
2. `Run.Calls`: model steps, including the requests, responses, chosen tool
   calls, usage, latency, and adopted, abandoned, or incomplete classification;
3. record events and the recorded transcript: child links, final answer, and
   record-derived timing. Compaction events (`context_compacted`,
   `context_compaction_skipped`, `context_overflow`) are record events that skip
   `BeforeEvent` (compaction design 4.5), added by their compaction tickets;
4. optional observed event fields: thinking, planner notes, retry hints, policy
   decisions, awaits, phase changes, and prompt-render metadata. These may be
   absent or rewritten by `BeforeEvent`; `EventsUnverified` marks them when an
   interceptor is registered. `context_recalled` arrives with K13; it is not an
   existing event.

Child runs are found through `child_run_linked` and loaded the same way. Their
trajectories are placed under the parent tool call.

A trajectory contains:

- identity: run ID, session ID, agent ID, trace ID when the harness opened a
  root span;
- steps in causal order: model calls, tool calls (name, call ID, parent call ID,
  canonical arguments, result, bounds, structured error, duration), child runs,
  awaits;
- the final answer;
- prompt references and hashes of the rendered system messages and tool
  definitions (from captured requests);
- totals: tokens per model, wall time, model calls, tool calls, failed calls,
  identical repeated calls.

"Canonical arguments" means the JSON bytes recorded by the runtime, compared
after key-sorted normalization.

Rules:

- Unknown, duplicate, or mismatched tool results are errors.
- Nested calls are placed directly after their parent, independent of how
  concurrent child runs interleave.
- A trajectory is built only for a run in a terminal phase. This rule is new;
  goa-ai's collector does not enforce it.
- Model-call steps of an agent run come from the call record (compaction K8),
  which is always on. Abandoned and incomplete calls remain in step order and
  are marked by class. `Decide` holds standalone call evidence itself (5.3).
- Tool results are read at full size through `RunRecord` (compaction K8),
  which replaces the run log's 1 MB truncation, so typed result predicates
  always decode the whole result.
- T6 sets `EventsUnverified` when any `BeforeEvent` interceptor is registered.
  Record-derived steps remain verified; observed fields are optional and
  marked unverified. Observed events never feed totals. Wall time uses only
  record-event timestamps (`planner_step`, `run_sealed`, `child_run_linked`,
  and compaction record events).

A **session trajectory** is the ordered list of trajectories of the runs a case
started in one session.

Rendering: `Render(limits)` produces deterministic text for terminal output and
judges. Every truncation is marked in the text. The JSON form is stable and
documented.

Optional adapter: applications that only see the stream remotely can build a
partial trajectory from stream events. It is marked partial, because the stream
is best-effort and lacks model-call steps.

### 5.5 Expectations

Package `expect`. Each expectation produces a `Check` with a diagnostic on
failure.

- `expect.Call(tool, argsPred, resultPred)` and `expect.Decoded(codec, pred)`:
  typed predicates through the generated codecs.
- `expect.Bounds(tool, pred)`.
- `expect.Forbid(tools ...tools.Named)`.
- `expect.Failed(tool, argsPred, errPred)`: a failed call, with a predicate over
  the structured tool error.
- `expect.Await(kind, pred)`: covers every await kind.
- `expect.Completed()`: the run reached the completed phase.
- `expect.AllAttemptsSuccessful(tool)`.
- `expect.Exact(...)` versus the default in-order subsequence match, where a
  declared call binds to the first later call that satisfies it.
- Budgets: `expect.MaxModelCalls(n)`, `expect.MaxTokens(n)`.
- `expect.Require(t, subject, expectations...)` evaluates the expectations and
  fails the test with every failed check's diagnostic.

Subjects are sealed. Construct them with `expect.Trajectory(tr)`,
`expect.Session(runs)` or `expect.Calls(d.Calls)` for one model decision.

**Data-driven expected tools** for datasets that cannot hold Go code (JSONL,
Opik): `ExpectedTools{Tools, Ordered, Exact}` is a struct. `Tools` lists tool
names with an optional JSON subset of arguments; the flags control matching.
It yields a check and a
`tool_correctness` score (matching calls divided by calls made), following
DeepEval's definition.

The data shape is `*expect.ExpectedTools` on `eval.Item`; nil means absent.
An explicit empty `tools` list expresses no expected calls. Set `exact` to
reject extra calls.

```go
type ExpectedTool struct {
	Name tools.Ident     `json:"name"`
	Args json.RawMessage `json:"args,omitzero"`
}

type ExpectedTools struct {
	Tools   []ExpectedTool `json:"tools"`
	Ordered bool           `json:"ordered"`
	Exact   bool           `json:"exact"`
}
```

---

## 6. Plan A: agent and tool testing

Package: `agenttest` (name per D1). All of Plan A runs under `go test`.

### A1. Harness

`agenttest.NewHarness(ctx, register, opts...) (*Harness, error)` builds a
runtime on the in-memory engine. `agenttest.New(t, register, opts...)` wraps it
for tests: it uses `t.Context()`, fails the test on error, and registers cleanup.
The harness:

- calls the application's registration function;
- registers models from options, keyed by model ID as in
  `RegisterModel(id, ...)`: `agenttest.LiveModel(id)` uses the application's
  configured client for that model ID and builds no provider;
  `agenttest.ScriptedModel(id, turns...)` uses the scripted model (A3).
  `h.Model(id)` returns the registered client;
- configures `RunStore` and `RunEventStore` for K8 recording and `RunRecord`
  reconstruction (T6);
- creates a unique session per run, so concurrent tests and repeats never
  collide;
- uses a caller-supplied OTel `TracerProvider` through `WithTracerProvider`
  and `telemetry.NewTracer`, and opens a root span per run. The caller owns
  exporter configuration and shutdown. Without a provider the trace ID is empty.

Runtime additions used by Plan A are `Runtime.HasEventInterceptors`,
`telemetry.NewTracer`, `Runtime.ToolDispatchMode` and `Runtime.ModelIDs`.

`h.Run(ctx, agent, input)` starts a run, answers awaits through the configured
responder, waits for a terminal phase, and returns `(*Result, error)`. Agent
failures populate `Result.RunErr`; harness failures return an error.
`h.RunSession(ctx, agent, turns...)` returns `(*SessionResult, error)` with ordered
runs and a session trajectory; history comes from the agent memory policy.

Await responder: a policy that answers confirmations (approve or deny by tool),
clarifications, questions, and typed input from fixed values or a function. An
await without an answer fails the run with a diagnostic.

### A2. Tool stubs

A `BeforeTool` interceptor replaces execution of activity-run tools:

- `agenttest.Stub(h, tool).Handle(func(context.Context, P) (R, error))`,
  `.Return(value)`, `.Fail(err)` and an optional `.After(delay)`;
- activity tools are strict by default. An unstubbed call becomes a model-visible
  tool error and a `*StubError` in Run's harness error and Close;
  `h.Passthrough(tool)` runs the real tool.

Recorded-seed randomness is deferred follow-up work.

Limit: inline tools and agent-as-tool children skip interceptors. Children are
tested through their own model (scripted or live) and stubs on their tools.
Stubbing inline tools needs a runtime change, recorded as a follow-up.

### A3. Scripted model

A `model.Provider` wrapped with `model.NewClient`, so every scripted response
goes through the same request-contract validation as a real provider. It
supports `Complete` and `Stream`. Responses are built from typed descriptors
(`agenttest.ToolCall(tool, payload)`, `agenttest.Text(...)`), with unique call
IDs and a stop reason. `ScriptedModel(id, turns ...Turn)` builds a script;
`ScriptedModelFrom(id, script)` shares an existing script. `NewScript(...).AnyOrder()`
selects the first unconsumed turn whose matchers pass.

### A4. Decide and Repeat

- `h.CaptureFirstRequest(ctx, agent, input)` runs the agent until its first
  model call and returns `(Request, error)`. Tools scheduled before that call
  follow normal strict stub rules. At construction the harness wraps every
  registered model with a gate returning a sentinel on both `Complete` and
  `Stream`; register all models inside the registration callback. K8 records
  the effective request before calling that client. The
  in-memory engine does not retry activities, so the run ends failed, and the
  harness checks that the failure is the sentinel. A run that ends without any
  model call is an error. A planner that handles the sentinel itself instead
  of returning it makes the harness fail with a diagnostic. The harness reads
  the request from the run's `runrecord` `Calls[0].Request`, after every
  interceptor and the runtime's re-applied policies.
- `agenttest.Decide(t, client, request)` makes one model call and returns the
  `*Decision` containing request, response, calls and latency. No tools run.
  This is the main tool-selection test.
- The captured request is an `agenttest.Request`. Its transform methods return
  a copy: `WithUserMessage` (replaces the last user message), `WithSystem`
  (replaces the system-role messages), `WithToolSpecs` (current generated
  specs; it mirrors a private tool-spec mapping), `WithModel`, `WithRunID`
  (replaces the original run ID for replay).
- `agenttest.Repeat(t, n, minPass, func(testing.TB))` runs the callback `n` times
  and passes when at least `minPass` attempts pass (glossary). It reports the diagnostics of every
  failed attempt.
- `LiveModel(id)` and `SkipUnlessLive(t, id)` require `LOOM_LIVE_MODEL=1`
  (D6); undeclared application clients are not blocked. Live-model tests skip
  by default through these helpers.
  Unlike the opt-out `CODEX_INTEGRATION` smoke test, they never run by
  default.

### A5. Documentation and fixture

- A guide in `docs/` and the loom-mcp skill: "Adding a tool: how to test it".
- An integration fixture that uses the harness, stubs, the scripted model,
  `expect`, and `Decide` with a scripted model.

### Plan A proof

- Codegen tests show one descriptor per tool; a mismatched predicate type fails
  to compile (a compile-failure test fixture).
- Capture is covered by K8 tests: calls join to runs and turns on unary and
  streaming paths; request round-trip is exact.
- Trajectory tests cover causal order, nested child runs, duplicate and orphaned
  results, non-terminal runs, every await kind, totals, and deterministic JSON.
- Expect tests cover exact and in-order matching, first-binding, forbidden
  tools, typed predicates, bounds, failed calls, budgets, data-driven expected
  tools, and diagnostics.
- Harness tests: unstubbed tools fail, passthrough runs, unanswered awaits fail,
  scripted responses are validated, and two concurrent runs stay isolated.

---

## 7. Plan B: agent development evals

Package: `eval` (name per D1).

### B1. Scenarios, items, results

```go
type Scenario struct {
	ID        string
	Agent     agent.Ident
	Task      string
	Reference string
	Tags      []string
	Timeout   time.Duration
	Items     Dataset
	Claims    []Claim
	Expect    []expect.Expectation
	Drive     func(ctx context.Context, h *agenttest.Harness, item Item) (*agenttest.SessionResult, error)
	Assert    func(ctx context.Context, a *Attempt) (Assertions, error)
}

type Item struct {
	ID              string                `json:"id"`
	Input           string                `json:"input,omitempty"`
	Turns           []string              `json:"turns,omitempty"`
	Task            string                `json:"task,omitempty"`
	ExpectedOutcome string                `json:"expected_outcome,omitempty"`
	Persona         string                `json:"persona,omitempty"`
	Reference       string                `json:"reference,omitempty"`
	ExpectedTools   *expect.ExpectedTools `json:"expected_tools,omitempty"`
	Data            json.RawMessage       `json:"data,omitzero"`
}

type Claim struct {
	ID            string
	Text          string
	Informational bool
}

type Assertions struct {
	Checks []expect.Check
	Claims []Claim
	Scores []expect.Score
}

type Attempt struct {
	Scenario string
	Item     Item // Task and Reference resolved
	Harness  *agenttest.Harness
	Session  *agenttest.SessionResult
}

func (a *Attempt) Subject() expect.Subject
func (a *Attempt) Final() *model.Message

type Outcome struct {
	Scenario, Item, Task, Reference string
	Session                         *agenttest.SessionResult
	Checks                          []expect.Check
	Scores                          []expect.Score
	Claims                          []Claim
	Err                             error
}

func Items(items ...Item) Dataset
func JSONL(fsys fs.FS, name string) Dataset
func (d Dataset) Items() []Item
func (d Dataset) Len() int
func (d Dataset) Err() error
func (d Dataset) Source() string
func (d Dataset) Hash() string
func DecodeJSONL(r io.Reader, source string) (Dataset, error)
func EncodeJSONL(w io.Writer, d Dataset) error
func Validate(scenarios ...Scenario) error
func Execute(ctx context.Context, h *agenttest.Harness, s Scenario, item Item) (*Outcome, error)
```

- T11 owns single-attempt execution. The default `Drive` calls `RunSession` for
  either input shape; a custom `Drive` returns its session evidence. `Assert`
  adds assertions over that evidence. Agent is required unless Drive is set.
- Checks run in order: built-in `runs_succeeded` from RunErr, scenario `Expect`,
  item `ExpectedTools`, then `Assert`. Quality failures leave `Outcome.Err` nil;
  infrastructure errors, panics, timeouts and broken assertion contracts set it.
  The separate error return covers only invalid arguments or a nil harness.
- Task and Reference resolve from the item before scenario defaults. The final
  answer is `Attempt.Final()`, from the last session trajectory.
- Datasets are immutable, with eagerly captured deferred errors. JSONL version 1
  requires `{"format":"loom-mcp.eval.items","version":1}` on line 1 and strict
  item objects thereafter. Lines use LF only, have no leading/trailing
  whitespace, and contain at most 16 MiB excluding LF; the final LF is optional.
  Canonical JSONL preserves number tokens; its SHA-256 hash is independent of
  whitespace inside objects and member order. Header-only files report
  ErrEmptyDataset at line 2. Validate prefixes every diagnostic line with its
  scenario location, preserving DecodeError unwrapping.
- Scenario and item IDs and tags match `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`.
  Claim IDs match `[a-z0-9_]{1,64}` and must be unique. Execute declares and resolves claims; the runner delegates judging through
  `eval.OutcomeJudge`. At least one user check or claim must remain;
  `runs_succeeded` alone does not satisfy `ErrNoAssertions`.
- `Persona` remains reserved and rejected. ExpectedOutcome requires Turns.
- T12 owns selection, repeats, concurrency, variants and attempt records.

### B2. Runner

- Runs variants × scenarios × items × repeats with bounded concurrency and a
  fresh harness/session per attempt. Harness-local run/session IDs can repeat;
  `AttemptKey{Variant, Scenario, Item, Repeat}` is the identity.
- **Variants.** Each carries labels, cloned per-run `PolicyOverrides` and opaque
  agenttest options. Per-agent context compaction/recovery are runtime options in
  `Variant.Options`, not policy fields. No variants means `default`.
- `HarnessFunc(ctx, variant, opts...)` forwards supplied options and returns a new
  harness. `NewHarnessFunc(register, setup, base...)` provides the standard builder.
  Scripts and stubs are isolated per harness; use ScriptedModel, not shared scripts.
- Scenario timeout covers Drive and Assert; judging is a later layer. Zero
  repeats/concurrency means one. All repeats run without early quality exit.
- Select variants, scenarios, tags and bare/qualified scenario/item IDs. OR within
  each kind, AND across kinds; empty, duplicate, unknown and ineffective selectors
  and an empty plan are joined errors. Validate every scenario before selection.
- Exactly one record per planned key, including `not_run` records for unstarted
  work. Cancellation preserves completed evidence and returns the partial report
  with the context cause. Harness Close always runs for each newly built harness.
- Longest timeouts start first (zero longest); records keep declaration order.
  OnAttempt is serial, once per record, with skipped records delivered last.
- Infrastructure errors are separate from quality failures. Verdict precedence is
  error, failed, pending, passed; gating claims stay pending when no judge is configured.
  Groups reports counts without gating. Live-disabled factories stop new work and
  return an error; the runner never enables live models.
- Tokens count all completed calls in returned records, including descendants and
  abandoned attempts. Direct Decide calls in Assert and unreturned Drive runs are
  excluded. MaxTokens stops starts at >= the limit; running attempts finish, so up
  to Concurrency-1 additional attempts may finish beyond the crossing attempt.
  No cost limit. Token stops return no error. Concurrent skipped keys may differ.
- Records expose version-1 runrecord exports, session IDs and optional trace IDs
  for T13. Exports remain in memory. Progress printing and failing-first summaries
  belong to T15; T12 supplies OnAttempt and Groups.

### B3. Experiment record and local store

Identity of an experiment:

- experiment ID (UUIDv7), start time, label, parent experiment (for re-runs);
- git commit and dirty flag; loom-mcp and loom versions;
- agent IDs, model IDs per model class, judge model and judge prompt hash;
- prompt references from recorded `Started.Prompts`, plus existing system and
  tool content hashes from `Started.Request.System` and `.Tools`;
- dataset hash, items, repeats;
- per variant: its labels, policy overrides, the context configuration
  fingerprint, and the prompt references it resolved.

Each attempt records: scenario, item, repeat index, each run as a self-contained
`runrecord.Export` (`context_compaction.md` 4.5), checks, judgments, scores,
tokens, latency, trace ID, and any infrastructure error. Each run also stores its
final message, `EventsUnverified`, session ID, trace ID, and agent error text.
Trajectory reconstruction needs these fields beside the export. Compaction
replay and `show` work after the run's store is gone.

The reserved version-1 schema includes optional claim `subject` and `tool`, decoded
into `StoredClaim`. Judgments have optional `subject` and `step`, where the step
locator contains `run`, `index`, `tool`, and `call_id`. Attempts have optional
`judge_usage` and `judge_tokens`; zero values are omitted. T13 preserves these
fields on decode. Judging integration populates these fields when writing.
The current claim mapping uses explicit fields so extending `Claim` does not
break the store's build.

Storage: an experiment directory with `experiment.json` and `attempts.jsonl`
under `.loom-evals` beside the nearest ancestor `go.mod` (D2). The store creates its
own `.gitignore`. Both schemas use version 1. The JSONL header identifies the
experiment, and each attempt embeds its exports inline. `last` is computed from
non-running summaries. Comparison also requires an overlapping scenario/item pair,
with no pointer file. New directories use mode 0700 and new files use mode 0600.
The caller owns existing directory permissions, which `Open` preserves.

### B4. Show

`evalcli show <experiment>/<attempt>` renders one attempt: the trajectory
steps, failed checks with diagnostics, judgments with rationales, scores, and
the trace ID. `eval.Show` and `ShowJSON` own the rendering. `--json` embeds decoded
trajectories for coding agents; `--export` additionally includes raw run exports.
Missing trajectory evidence remains a visible diagnostic. Text defaults to bounded
rendering; `--full` removes the limits. `Summary` and `SummaryJSON` put non-passed
groups first and use full experiment IDs in attempt references.

### B5. Compare

`compare(baseline, candidate)`:

- compares only (scenario, item) pairs present in both, and lists the rest;
- warns when dataset hashes differ;
- warns when the rendered system messages and tool definitions are identical
  in both (the change may not have reached the agent), unless the variants'
  context fingerprints, prompt references, models, or policy differ;
- reports pass counts, changed checks and claims with rationales, score
  changes, median tokens and latency, and tool-sequence edits.

Variants of one experiment compare the same way: `compare <experiment>
--variants a,b` treats each variant as a side, and with more than two variants
prints one column per variant against the first.

A re-run of failed attempts (`--rerun-failed <experiment>`) is a new
experiment linked to its parent. Compare treats it as standalone unless asked to
merge it with its parent (`CompareOptions.Parent`, exposed as `--merge-parent`).
The supplied parent must match the candidate's recorded parent. Candidate groups
replace parent groups by (variant, scenario, item), including all repeats.

Pass fractions exclude not-run attempts and include pending attempts as non-passes.
Integer cross-products determine regression or improvement; no ran attempts on
either side means not comparable. Checks use passed/total and claims use
entailed/total judged units. Scores use medians. Tool sequences use each group's
modal sequence, flatten child tools depth-first, and report LCS edits. Unavailable
trajectories are excluded and counted. Uncalibrated and truncated judgments qualify
results without suppressing status. JSON keeps candidate declaration order; text
lists regressions, improvements, not-comparable groups, a collapsed unchanged count,
and intersection-only totals.

### B6. Judge core

- `eval/judge` accepts rendered subjects and ordered claims; it imports neither
  `eval` nor `trajectory`. Labels and rationales follow the rules in 2.1.
- D3 is decided: one forced private `record_judgments` tool, using `Complete`,
  temperature zero, no thinking and a required positive output-token cap. At most
  three corrections follow the first call. Automatic tool choice is deferred.
  The Codex adapter rejects the required cap and cannot serve as this judge.
- Rendering may visibly elide content and set `Input.Truncated`. The judge never
  cuts the assembled request: over `MaxInputBytes` (default 512 KiB) returns
  `ErrInputTooLarge`; schemas over 1 MiB return `ErrSchemaTooLarge`.
- Calibration runs once per Judge on four built-in examples plus user examples,
  one call each, under a two-minute default timeout. Completed passes and failures
  are cached by prompt hash, model, output cap and canonical examples until
  `Recalibrate`. Infrastructure failures are not cached and leave results
  uncalibrated without blocking judging. Judgments themselves are never cached.
- T13 reserves the experiment-level `judge.calibrated` field and per-judgment
  `uncalibrated`; `eval/judging` maps judge results to those records. Compare and export flag
  uncalibrated judgments. T16 itself returns `Result.Calibrated`.

### B7. Judge subjects and built-in metrics

| Subject | Target | Built-in |
|---|---|---|
| Answer | the last run's final answer (default subject) | custom claims |
| Trajectory | the last run's rendered trajectory, including children | task completion: "The agent completed the task: {task}." Step efficiency: "The agent took no unnecessary steps for the task." Plan quality and plan adherence are documented custom claims, not built-ins, because they apply only to agents that state a plan in thinking or planner notes. |
| Session | the session trajectory | conversation completeness against `ExpectedOutcome`; custom claims such as knowledge retention |
| Step | every adopted root-run model call that chose tools, or every root-run call of a named tool | argument correctness: "The arguments of this call are correct given the conversation so far." |

- The task is always fixed by the item or scenario. The judge never infers the
  task from the trajectory it grades.
- Claims produce labels. Over repeats, the fraction of `entailed` labels is the
  item's score. Numeric 0–1 judge scores (DeepEval style) are not produced in
  the first version (see 11).
- Deterministic scores need no model: `model_calls` (adopted), `tool_calls`,
  `failed_tool_calls` and `repeated_tool_calls` are always prepended and sum
  every session run plus descendants. `tool_correctness` comes from expected
  tools. Tokens and latency are record fields, not duplicate scores.
- `Execute` resolves `{task}` and `{expected_outcome}` before judging. Missing
  values are contract errors. Step subjects hide the current call's outcome and
  later steps; zero matching units yield `not_addressed` without a model call.
- `eval/judging` implements `eval.OutcomeJudge` without coupling `eval` to
  `eval/judge`. Run prepares calibration before dispatch. `JudgeTimeout` is
  separate from the scenario timeout and defaults to five minutes. Gating judge
  errors make attempts `error`; missing judgments leave them `pending`.
  Judge tokens count toward `MaxTokens`, excluding calibration.

### B8. Replay (optional)

`evalcli replay <experiment>/<attempt>/<call> --repeat n` with request
transforms (A4) runs one captured model call `n` times and checks the chosen
calls with `expect`. A replayed call can be saved as a test fixture and used
with `agenttest.Decide` in `go test`. This turns a failure found while iterating
into a permanent test.

### B9. Opik export and import

After the D5 spike:

- experiment → Opik experiment; item → dataset item; attempt → experiment item
  linked to its trace;
- checks, judgments, and scores → feedback scores, with rationales as reasons;
- deterministic IDs so export is idempotent and can be retried;
- import Opik dataset items as `Items` (optional, T20).

### B10. Command-line helper

`evalcli.Main(config)` in the application's own `main`. Subcommands and flags:
`run` (`--scenario`, `--tag`, `--item`, `--repeat`, `--concurrency`, `--label`,
`--rerun-failed`, `--compare <experiment|last>`, `--max-tokens`, `--out`),
`show`, `compare`, `replay`, `export opik`. `last` is the most recent
experiment in the store with an overlapping selection. Readable output by
default, `--json` everywhere.

### Plan B proof

- Runner: selection rules, repeats, concurrency, timeouts, cancellation and
  partial records, one record per attempt, infrastructure versus quality
  failures, re-runs, token limit.
- Store: deterministic layout, versioned schema, round-trip.
- Show and compare: rendering, intersection, hash warnings, pass-count and score
  changes, sequence edits.
- Judge: schema construction, duplicate-member rejection, UTF-8 IDs, schema
  size limit, empty-answer rule, label validation, calibration and the
  uncalibrated flag, each subject.
- Replay: a captured call reproduces its request exactly; each transform
  applies.
- An integration fixture runs `evalcli` with a scripted model and a scripted
  judge, and a live variant behind the environment variable.

---

## 8. DeepEval trajectory work style: support map

| DeepEval | loom-mcp | Coverage |
|---|---|---|
| Golden, single-turn | `Item.Input` (B1) | Full |
| Conversational golden (scenario, expected outcome, persona) | `Item.Turns`, `ExpectedOutcome`; `Persona` reserved (B1) | Partial: scripted turns only |
| Conversation simulator | — | Follow-up |
| Dataset from code, CSV/JSON, cloud | Go slice, JSONL (B1); Opik (B9) | Partial: Go slice and JSONL; Opik import optional (T20); no CSV |
| Dataset synthesis | — | Not planned |
| Instrumentation (`@observe`, integrations) | Not needed for runtime steps: transcript index and `RunRecord`, including K8 call records (5.3, 5.4) | Gap: steps inside a planner or tool, such as a retrieval, are invisible unless they are tools. Follow-up: custom step events. |
| Trajectory: generations, tool calls, retries, handoffs, timing | Model calls, tool calls with errors and duration, retry hints, child runs (5.4) | Full, except provider retries rejected by validation (non-goal) |
| `evals_iterator`: run, capture, evaluate, store | Runner and store (B2, B3) | Full |
| `TaskCompletionMetric` | Trajectory claim with fixed task (B7) | Full; task never inferred |
| `StepEfficiencyMetric` | Trajectory claim plus deterministic counts (B7) | Full |
| `PlanQualityMetric`, `PlanAdherenceMetric` | Trajectory claims from thinking and planner notes (B7) | Full when the agent states a plan |
| `ToolCorrectnessMetric` | `ExpectedTools` with ordered and exact flags, score (5.5); typed `expect.Call` (5.5) | Full, minus the optional model check of available tools |
| `ArgumentCorrectnessMetric` | Step claim per tool call (B7) | Full |
| GEval custom criteria | Claims on any subject (B7) | Full |
| Span-level metrics | Step subject: every model call or every call of a tool (B7) | Full, applied after the run |
| Conversational metrics | Session claims (B7) | Partial: completeness built in, others as custom claims |
| Score, threshold, strict mode, reason | Labels with rationales; repeat fraction as score; thresholds on deterministic scores (B7) | Different by design (11) |
| `assert_test` in pytest | `expect.Require`, `agenttest.Repeat` in `go test` (A4) | Full |
| Inspect span tree of a failure | `show` (B4); trace in Logal or Opik | Full |
| Compare versions | `compare` (B5); Opik experiments (B9) | Full |
| Cache, `ignore_errors`, verbose mode | Calibration cache; infrastructure errors recorded, not fatal | Partial |

---

## 9. Tickets and phasing

One ticket is one commit. Each ticket includes its tests and documentation.

| Phase | Ticket | Blocked by |
|---|---|---|
| 1. Foundation | T0 Model-call identity in the runtime (5.2), done | — |
| | T1 `TypedTool` descriptors, `tools.Named`, codegen (5.1), done | — |
| | T3 Trajectory from `RunRecord`, including `callrecord.DecodeResponse` (5.4), done | compaction K8 (done) |
| | T4 Trajectory rendering and JSON schema (5.4), done | T3 |
| | T5 `expect` package, including `ExpectedTools` (5.5), done | T1, T3 phase 1 |
| 2. Testing | T6 Harness: runtime, RunStore, RunEventStore, models, sessions, tracer, `Run`, await responder (A1), done | T3 |
| | T7 Tool stubs (A2), done | T1, T6 |
| | T8 Scripted model (A3), done | — |
| | T9 `CaptureFirstRequest`, `Decide`, request-fixture encoding, transforms, `Repeat`, live gating (A4), done | T5, T6, T8, D6 (decided) |
| | T10 Docs, skill, fixture (A5) | T6–T9 |
| 3. Dev loop | T11 Scenarios, items, JSONL datasets (B1) | T5, T6 |
| | T12 Runner (B2) | T11 |
| | T13 Experiment store (B3) | T12, D2 (decided), compaction K8 (done) |
| | T14 `show` and `compare` (B4, B5) | T4, T13 |
| | T15 `evalcli` (B10, without replay and export) | T14 |
| 4. Judge | T16 Judge core and calibration (B6) | D3 |
| | T17 Subjects and built-in metrics (B7) | T4, T12, T16 |
| 5. Opik | T18 Opik export (B9) | T13, T17, D5 |
| Optional | T19 Replay command and fixture export (B8) | T9, T13, T15 |
| | T20 Opik dataset import (B9) | T18 |

The first version ends after T18. T19 and T20 are built when there is a real
need for them.

Plan A is complete after phase 2. Each phase ends with the repository
verification ladder.

T0–T9 are done; T2 was dropped and its work landed in T3 and T9. T10 is last
in Plan A. The separate capture ticket is dropped: `DecodeResponse` belongs to
T3 and request-fixture encoding to T9.

---

## 10. Open decisions

| ID | Decision | Proposal | Blocks |
|---|---|---|---|
| D1 | Package names and homes | Decided: `runtime/agent/model` (`CallInfo`, done); `runtime/agent/tools` (`TypedTool`, `Named`); top-level `trajectory`, `expect`, `agenttest`, `eval`, `eval/judge`, `eval/judging` (the eval-to-judge adapter; added by T17), `eval/evalcli`. K8 call records provide capture (5.3). | — |
| D2 | Local store format and path | Decided: JSON files under the application module, gitignored; each run stored as `runrecord.Export`, trajectories derived from it | — |
| D3 | Judge mechanism and truncation | Decided: forced `record_judgments` tool only; renderer elision is visible and flagged, while the assembled request fails over its byte cap. Schemas are capped at 1 MiB. | — |
| D4 | Trajectory source | Decided: transcript index and `RunRecord`, including K8 call records; observed fields optional; stream adapter only for remote clients | — |
| D5 | Opik exporter home and trace mapping | Spike: how OTel trace IDs map to Opik traces, and whether the exporter lives in `features/` or in Auto-K on top of the store format | T18 |
| D6 | Live-test gating | Decided: opt-in environment variable `LOOM_LIVE_MODEL=1`; the test names the model ID | — |
| D7 | Trace ID under Temporal | In-memory: harness tracer and root span (decided). Temporal: open | Not blocking; Temporal support is a follow-up |

---

## 11. Alternatives considered

- **Port goa-ai `eval` wholesale.** Rejected. It depends on runtime pieces we
  lack (`tooloutput`, `FailureKind`, typed descriptors), reads the stream, and
  its DSL and codegen are the costliest part with the least value for a
  development tool.
- **Build trajectories from the stream.** Rejected as the default. The stream is
  best-effort, needs an open session, carries discardable text, and has no model
  call boundaries. Kept as an adapter for remote clients.
- **Build trajectories from OTel spans.** Rejected. Spans carry bounded and
  serialized attributes and depend on exporter configuration. Spans are linked
  by trace ID instead.
- **Use Opik's Python evaluation SDK against the agent over HTTP.** Rejected for
  the core loop: no typed tool assertions, no in-process stubs or scripted
  models, no replay. Opik stays the shared store.
- **Numeric 0–1 judge scores as in DeepEval.** Deferred. Labels with rationales
  are easier to validate and calibrate. Over repeats, label fractions give a
  score with a clear meaning. Revisit if label fractions prove too coarse.
- **Keep goa-ai's calibration as a hard stop.** Rejected for a development tool;
  uncalibrated judgments are flagged instead, so the developer is never blocked
  but never misled.

---

## 12. Risks

- **Runtime change for capture: resolved.** T0 model-call identity and K8
  call recording are implemented and covered by their tests.
- **Judge variance.** Repeats and calibration reduce it but do not remove it.
- **Rendering size.** Long trajectories can exceed judge context; D3 fixes the
  policy.
- **Storage growth.** Full trajectories with captured calls are large. The tool
  never deletes experiments.
- **Cost.** Live repeats × items × judge calls add up. Token totals are recorded
  per experiment, and `--max-tokens` stops new attempts.
- **Stub coverage.** Inline tools cannot be stubbed until the runtime change in
  the follow-ups.

---

## 13. Data sensitivity

Trajectories and captured model calls contain full prompts, model text, tool
arguments, and tool results, which can include customer data. Experiments are
stored locally under a gitignored path. Configuring a judge sends rendered
subjects, including prompts, tool results and customer data, to the judge model
provider. Export to Opik (B9) is explicit and
sends the same data to the shared instance. Neither is telemetry. The stored and exported records hold
what the compaction record holds: responses that runtime validation rejected,
rejected summaries, and server-only data (`ServerData`, injected arguments).
Output that never reaches the runtime model wrapper is not recorded.

---

## 14. Changes to existing plans

This plan replaces two ROADMAP entries and reverses parts of them:

- "Generated agent evaluations": drops the eval DSL, generated suites, the
  generated command, and failing exit status for experiments.
- "Evaluation environment simulation": folded into tool stubs (A2). The old
  entry said to keep simulation separate from evals; stubs are needed for
  testing, so they move here.

When this plan is accepted, both ROADMAP entries are replaced by one entry that
points to this document, in the same change. ROADMAP rules say plans live in
ROADMAP; this document is a design record for review, and the ROADMAP entry
stays the source of truth for status.

---

## 15. Follow-ups

- Simulated users with personas.
- Stubbing inline tools (runtime change).
- Custom step events for work inside planners and tools.
- A tool failure classification, then failure-kind policies in `expect`.
- Static tool-contract checks (descriptions, field descriptions, schema size).
- Significance tests in `compare`.
- Model check of tool choice against available tools.
- Carrying pending calls across successor runs, when cross-workflow
  continuations land.
- Scenarios that cross the context compaction threshold, including offline
  compaction replay (`context_compaction.md` 7.1). The call record itself
  (K8) is a dependency of T3 and T13, not a follow-up.
