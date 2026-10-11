# Agent Testing and Development Evals

Status: draft for peer review, revision 4. Date: 2026-10-09. Revision 4 aligns
the plan with context compaction revision 9's record.

This document plans two related capabilities for loom-mcp:

- **Plan A: agent and tool testing.** Deterministic and live-model tests that run
  under `go test`. The main use is proving a new or changed tool: the agent can
  call it, its contract decodes, and a real model selects it for the right
  requests.
- **Plan B: agent development evals.** A development loop for making an agent
  perform: run cases repeatedly, record full trajectories, judge them, find out
  why attempts fail, and compare one version of the agent with another.

Both plans share one foundation: typed tool descriptors, a **trajectory** record
built from the durable run log, and **model-call capture**.

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
| Model call | One request to a model client and its response, captured by the harness. |
| Scenario | A named kind of case with shared code, task, and assertions. |
| Item | One input to a scenario: a single prompt or a list of user turns, plus optional data. |
| Dataset | The items of a scenario. From Go values, a JSONL file, or Opik. |
| Attempt | One execution of one item. An item runs once per repeat. |
| Experiment | One execution of a selection of scenarios × items × repeats, with recorded identity. Plan B's unit of storage and comparison. Matches Opik's term. |
| Check | A deterministic pass/fail assertion with a diagnostic on failure. |
| Claim | An English sentence judged by a model. Labels: `entailed`, `contradicted`, `not_addressed`, `indeterminate`. A claim is gating by default; `Informational: true` records it without affecting the attempt result. |
| Score | A number with a name, deterministic or judged. Informational unless a threshold is set. |
| Attempt passed | No infrastructure error, every check passed, every gating claim `entailed`, every score at or above its threshold when one is set. Used by both `agenttest.Repeat` and experiments. |

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
  calls are not visible as events, by design (ROADMAP non-goals). So model calls
  must be captured at the client.
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
  inside the interceptors, and is shared by every run. Nothing places run or
  turn IDs in the context of a model call, and `model.Request.RunID` is
  optional, with no turn or call ID. `model.Request.PromptRefs` lists the
  prompts used.
- **Run results**: `RunOutput` holds the final assistant message (`Final`),
  tool events, notes, and aggregate `Usage`.
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
- **Codecs**: each toolset generates `<Tool>PayloadCodec` and
  `<Tool>ResultCodec` (`tools.JSONCodec[*T]`), `Specs`, and `Spec(name)`. There
  is no `TypedTool`. `ToolSpec` embeds codec functions, so it cannot be hashed
  directly.
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

These are target APIs. Names follow decision D1 and may change.

### Story (a): I added a `search_orders` tool

```go
func TestSearchOrdersSelection(t *testing.T) {
	ctx := t.Context()
	h := agenttest.New(t, bootstrap.Register, agenttest.LiveModel("default"))

	// Capture one real request from the agent, then reuse it.
	base := h.CaptureFirstRequest(ctx, chat.Agent, "Where is my order 1234?")

	agenttest.Repeat(t, 5, 4, func(t *testing.T) {
		calls := agenttest.Decide(t, h.Model("default"), base)
		expect.Require(t, calls,
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
	agenttest.Repeat(t, 5, 5, func(t *testing.T) {
		calls := agenttest.Decide(t, h.Model("default"), other)
		expect.Require(t, calls, expect.Forbid(orders.SearchOrdersTool))
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
	h.Stub(orders.SearchOrdersTool, func(ctx context.Context, p *orders.SearchOrdersPayload) (*orders.SearchOrdersResult, error) {
		return &orders.SearchOrdersResult{Status: "shipping"}, nil
	})

	traj := h.Run(ctx, chat.Agent, "Where is my order 1234?")
	expect.Require(t, traj, expect.Call(orders.SearchOrdersTool, nil, nil), expect.Completed())
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
		Harness:   func(ctx context.Context) (*agenttest.Harness, error) { ... },
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

### 5.2 Model-call identity (runtime change, T0)

Today a model client cannot tell which run or turn a call belongs to (2.4).
`AfterModel` cannot be joined to a stream's final response, and a shared client
serves concurrent runs. So T0 adds a runtime change:

- the runtime places an exported `model.CallInfo{AgentID, RunID, SessionID,
  TurnID, ModelID, Seq, Attempt}` in the context of every model call, in the planner's
  model-client path (`simplePlannerContext.ModelClient`);
- `model.CallInfoFrom(ctx)` reads it. `Seq` orders the calls of one run and is
  unique across retries and parallel calls; `Attempt` is the planner activity
  attempt, which T0 exposes through the engine (today the engine reads
  `activity.GetInfo` only for the heartbeat timeout and run ID; `temporaltrace`
  puts the attempt on spans but does not expose it to runtime code). `Seq` comes from the
  compaction plan's run store (`NextSeq`, ticket K2), the only source unique
  across retries and parallel calls under Temporal, so T0 follows K2
  (`context_compaction.md` 4.13).

T0 is a small, general runtime change. It also helps provider logging and
tracing. It blocks T2.

### 5.3 Model-call capture

> **Superseded for agent runs by the compaction plan's call record**
> (`context_compaction.md` 4.13, ticket K8): the runtime records every model
> call (request parts, response, error kind, usage) in the run store, keyed by
> `(RunID, Seq, Attempt)` from `CallInfo`. The trajectory reads it through
> `RunRecord`. Unlike the decorator, it also holds responses that runtime
> validation later rejected, rejected summaries, and the content each index
> entry adopted. The decorator below remains only for calls made outside a run,
> such as `Decide`. The text is kept until the two plans are merged into the
> roadmap.

A `model.Client` decorator, installed at registration, records every model
call, keyed by `CallInfo`:

- the request as sent to the provider (`model.Request`, including messages,
  tool definitions, and `PromptRefs`), with model ID and model class;
- the provider response as returned by the registered client (validated when
  the client was built with `model.NewClient`): the `Complete` result, or the
  final response of a stream after it finishes, with usage and latency;
- or the error: its validation kind for an `OutputValidationError`, otherwise
  its message.

A call without `CallInfo`, such as one made by `Decide`, is recorded under a
harness-generated key.

The decorator is the innermost layer. Changes made outside it are not
captured: `AfterModel` response replacements and the rewriting of
tool-unavailable calls. Raw rejected provider output is not recorded (ROADMAP
non-goal).

The ticket proves correct keying on the unary and streaming paths with
concurrent runs on one harness.

A capture codec gives `model.Request` a stable JSON form with a round-trip test.
Thinking signatures are provider-specific and are not replayed on another
provider.

### 5.4 Trajectory

Package `trajectory` (home per D1) builds a `Trajectory` for one run from:

1. the **run log**, listed by run ID after the run ends: tool calls
   (`tool_call_scheduled`, `tool_result_received`), committed assistant
   messages, thinking, planner notes, retry hints, policy decisions, awaits,
   phase changes, child links, prompt references, and the compaction steps
   (`context_compacted`, `context_compaction_skipped`, `context_recalled`,
   `context_overflow`). Await and
   clarification answers and typed inputs are not in the run log; they come
   from `RunRecord` (`context_compaction.md` 4.4);
2. **captured model calls** (5.3): the model call steps, with the tool calls
   each one chose, usage, and latency;
3. **`RunOutput`**: the final answer (`Final`) and aggregate usage.

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
  which is always on; calls outside a run appear only when the capture
  decorator was installed.
- Tool results are read at full size through `RunRecord` (compaction K8),
  which replaces the run log's 1 MB truncation, so typed result predicates
  always decode the whole result.
- The harness fails at construction when an application `BeforeEvent`
  interceptor is registered, because it could drop or rewrite events the
  trajectory depends on. An explicit option allows it, and the trajectory is
  then marked unverified.

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

Expectations apply to a trajectory, a session trajectory, or the tool calls of
one model call (`Decide`).

**Data-driven expected tools** for datasets that cannot hold Go code (JSONL,
Opik): `ExpectedTools` lists tool names with an optional JSON subset of
arguments, plus `ordered` and `exact` flags. It yields a check and a
`tool_correctness` score (matching calls divided by calls made), following
DeepEval's definition.

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
  configured client for that model ID; `agenttest.ScriptedModel(id,
  responses...)` uses the scripted model (A3). `h.Model(id)` returns the
  registered client, wrapped with capture;
- installs model-call capture (5.3) on every model;
- creates a unique session per run, so concurrent tests and repeats never
  collide;
- installs an OTel tracer through `WithTracer`, exporting to a configurable
  endpoint such as Logal, and opens a root span per run. Without an exporter the
  trace ID is recorded as empty.

`h.Run(ctx, agent, input)` starts a run, answers awaits through the configured
responder, waits for a terminal phase, and returns a trajectory.
`h.RunSession(ctx, agent, turns)` sends several user turns in one session and
returns a session trajectory.

Await responder: a policy that answers confirmations (approve or deny by tool),
clarifications, questions, and typed input from fixed values or a function. An
await without an answer fails the run with a diagnostic.

### A2. Tool stubs

A `BeforeTool` interceptor replaces execution of activity-run tools:

- `h.Stub(tool, func(ctx, P) (R, error))`, fixed results, fixed errors, and an
  optional delay;
- a tool called without a stub fails the run by default;
  `h.Passthrough(tool)` runs the real tool;
- randomness takes an explicit seed, which is recorded.

Limit: inline tools and agent-as-tool children skip interceptors. Children are
tested through their own model (scripted or live) and stubs on their tools.
Stubbing inline tools needs a runtime change, recorded as a follow-up.

### A3. Scripted model

A `model.Provider` wrapped with `model.NewClient`, so every scripted response
goes through the same request-contract validation as a real provider. It
supports `Complete` and `Stream`. Responses are built from typed descriptors
(`agenttest.ToolCall(tool, payload)`, `agenttest.Text(...)`), with unique call
IDs and a stop reason.

### A4. Decide and Repeat

- `h.CaptureFirstRequest(ctx, agent, input)` runs the agent until its first
  model call and returns the captured request. Tools are not run. Mechanism:
  the capture decorator records the request and returns a sentinel error
  without calling the provider, on both `Complete` and `Stream`. The in-memory
  engine does not retry activities, so the run ends failed, and the harness
  checks that the failure is the sentinel. A run that ends without any model
  call is an error. A planner that handles the sentinel itself instead of
  returning it makes the harness fail with a diagnostic. Capture happens in the
  decorator, not in `BeforeModel`, because only the decorator sees the
  effective request after every interceptor and the runtime's re-applied
  policies.
- `agenttest.Decide(t, client, request)` makes one model call and returns the
  chosen tool calls. No tools run. This is the main tool-selection test.
- The captured request is an `agenttest.Request`. Its transform methods return
  a copy: `WithUserMessage` (replaces the last user message), `WithSystem`
  (replaces the system-role messages), `WithToolSpecs` (current generated
  specs), `WithModel`.
- `agenttest.Repeat(t, n, minPass, fn)` runs `fn` `n` times and passes when at
  least `minPass` attempts pass (glossary). It reports the diagnostics of every
  failed attempt.
- Live-model tests are opt-in: they skip unless `LOOM_LIVE_MODEL=1` (D6).
  Unlike the opt-out `CODEX_INTEGRATION` smoke test, they never run by
  default.

### A5. Documentation and fixture

- A guide in `docs/` and the loom-mcp skill: "Adding a tool: how to test it".
- An integration fixture that uses the harness, stubs, the scripted model,
  `expect`, and `Decide` with a scripted model.

### Plan A proof

- Codegen tests show one descriptor per tool; a mismatched predicate type fails
  to compile (a compile-failure test fixture).
- Capture tests join calls to runs and turns on unary and streaming paths;
  request round-trip is exact.
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
	Task      string           // default task for items without one
	Reference string           // default reference for items without one
	Tags      []string
	Timeout   time.Duration
	Items     Dataset
	Claims    []Claim          // judged on every attempt
	Run       func(ctx context.Context, item Item, h *agenttest.Harness) (Assertions, error)
}

type Item struct {
	ID              string
	Input           string          // single-turn input
	Turns           []string        // multi-turn scripted user turns
	Task            string          // what success means; overrides Scenario.Task
	ExpectedOutcome string          // multi-turn: what the conversation should achieve
	Persona         string          // reserved for the simulated-user follow-up
	Reference       string          // shared facts for judging
	ExpectedTools   []ExpectedTool  // data-driven expectations (5.5)
	Data            json.RawMessage // application-specific values
}

type Assertions struct {
	Checks []Check
	Claims []Claim
	Scores []Score
}
```

- The runner owns execution: it runs the item through the harness and builds
  the trajectory. `Run` is optional. When present, it receives the item and the
  harness, may drive the agent itself, and returns extra assertions.
- `Item.Task` and `Item.Reference` override the scenario defaults.
- The answer under judgment is the trajectory's final answer. There is no
  separate output field.
- Datasets come from a Go slice or a JSONL file; Opik import is optional (B9).
- A simulated user is a follow-up. `Persona` is in the schema now so datasets
  do not change shape later; the first version rejects items that set it.

### B2. Runner

- Runs scenarios × items × repeats with bounded concurrency. Each attempt has a
  unique session.
- **Variants.** An experiment may declare named variants. A variant is a set of
  run labels (which select prompt-registry overrides) and `PolicyOverrides`
  (including the context settings of `context_compaction.md` 4.17). Attempts
  run per variant, so the matrix is variants × scenarios × items × repeats.
  Without variants, an experiment has one implicit variant.
- Per-attempt timeout covers the run and the judging.
- Selection by scenario, tag, or item. Empty, duplicate, and unknown selectors
  are rejected.
- Every selected attempt gets exactly one finished record. Cancellation stops
  new attempts, cancels running ones, and saves the partial experiment.
- Longest timeouts start first; records keep declaration order.
- Infrastructure errors are recorded separately from quality failures.
- A token or cost limit stops new attempts when exceeded (`--max-tokens`).
- Progress is printed as attempts finish; the summary lists failing attempts
  first.

### B3. Experiment record and local store

Identity of an experiment:

- experiment ID (UUIDv7), start time, label, parent experiment (for re-runs);
- git commit and dirty flag; loom-mcp and loom versions;
- agent IDs, model IDs per model class, judge model and judge prompt hash;
- prompt references (from the run log) and hashes of rendered system messages
  and tool definitions (from captured requests);
- dataset hash, items, repeats;
- per variant: its labels, policy overrides, the context configuration
  fingerprint, and the prompt references it resolved.

Each attempt records: scenario, item, repeat index, the trajectory, captured
model calls, `RunRecord`'s self-contained export (`context_compaction.md` 4.5,
so compaction replay and `show` work after the run's store is gone), checks,
judgments, scores, tokens, latency, trace ID, and any infrastructure error.

Storage: an experiment directory with `experiment.json` and `attempts.jsonl`
under a gitignored path (D2). The schema is documented and versioned.

### B4. Show

`evalcli show <experiment>/<attempt>` renders one attempt: the trajectory
steps, failed checks with diagnostics, judgments with rationales, scores, and
the trace ID. `--json` produces the full record for coding agents.

### B5. Compare

`compare(baseline, candidate)`:

- compares only (scenario, item) pairs present in both, and lists the rest;
- warns when dataset hashes differ;
- warns when the rendered system messages and tool definitions are identical
  in both (the change may not have reached the agent), unless the variants'
  context fingerprints or prompt references differ;
- reports pass counts, changed checks and claims with rationales, score
  changes, median tokens and latency, and tool-sequence edits.

Variants of one experiment compare the same way: `compare <experiment>
--variants a,b` treats each variant as a side, and with more than two variants
prints one column per variant against the first.

A re-run of failed attempts (`--rerun-failed <experiment>`) is a new
experiment linked to its parent. Compare treats it as standalone unless asked to
merge it with its parent.

### B6. Judge core

- Claims follow goa-ai's design and rules (2.1).
- Mechanism per D3: `completion` with a schema built per call, or a port of
  `tooloutput` with forced and automatic tool choice. Both give one call plus
  bounded corrections.
- Judge input is the rendered subject. Truncation must be visible (D3 decides
  whether truncation is allowed at all or the call fails, as in goa-ai).
- Calibration runs once per judge configuration (model and prompt hash) and is
  cached. If it fails, every judgment in the experiment is marked
  uncalibrated; `compare` and the Opik export flag those judgments.

### B7. Judge subjects and built-in metrics

| Subject | Target | Built-in |
|---|---|---|
| Answer | the final answer | custom claims |
| Trajectory | the rendered trajectory | task completion: "The agent completed the task: {Task}." Step efficiency: "The agent took no unnecessary steps for the task." Plan quality and plan adherence are documented custom claims, not built-ins, because they apply only to agents that state a plan in thinking or planner notes. |
| Session | the session trajectory | conversation completeness against `ExpectedOutcome`; custom claims such as knowledge retention |
| Step | every model call, or every call of a named tool | argument correctness: "The arguments of this call are correct given the conversation so far." |

- The task is always fixed by the item or scenario. The judge never infers the
  task from the trajectory it grades.
- Claims produce labels. Over repeats, the fraction of `entailed` labels is the
  item's score. Numeric 0–1 judge scores (DeepEval style) are not produced in
  the first version (see 11).
- Deterministic scores need no model: model calls, tool calls, identical
  repeated calls, failed calls, tokens, latency, `tool_correctness`.

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
| Instrumentation (`@observe`, integrations) | Not needed for runtime steps: run log plus model capture (5.3, 5.4) | Gap: steps inside a planner or tool, such as a retrieval, are invisible unless they are tools. Follow-up: custom step events. |
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
| 1. Foundation | T0 Model-call identity in the runtime (5.2) | D1, compaction K2 |
| | T1 `TypedTool` descriptors, `tools.Named`, codegen (5.1) | D1 |
| | T2 Model-call capture and request codec (5.3) | T0, D1 |
| | T3 Trajectory from run log, captured calls, and `RunOutput` (5.4) | T2, compaction K8 |
| | T4 Trajectory rendering and JSON schema (5.4) | T3 |
| | T5 `expect` package, including `ExpectedTools` (5.5) | T1, T3 |
| 2. Testing | T6 Harness: runtime, models, sessions, tracer, `Run`, await responder (A1) | T2, T3 |
| | T7 Tool stubs (A2) | T1, T6 |
| | T8 Scripted model (A3) | T1, T6 |
| | T9 `CaptureFirstRequest`, `Decide`, transforms, `Repeat`, live gating (A4) | T5, T6, D6 |
| | T10 Docs, skill, fixture (A5) | T6–T9 |
| 3. Dev loop | T11 Scenarios, items, JSONL datasets (B1) | T5, T6 |
| | T12 Runner (B2) | T11 |
| | T13 Experiment store (B3) | T12, D2, compaction K8 |
| | T14 `show` and `compare` (B4, B5) | T4, T13 |
| | T15 `evalcli` (B10, without replay and export) | T14 |
| 4. Judge | T16 Judge core and calibration (B6) | T4, D3 |
| | T17 Subjects and built-in metrics (B7) | T12, T16 |
| 5. Opik | T18 Opik export (B9) | T13, T17, D5 |
| Optional | T19 Replay command and fixture export (B8) | T9, T13, T15 |
| | T20 Opik dataset import (B9) | T18 |

The first version ends after T18. T19 and T20 are built when there is a real
need for them.

Plan A is complete after phase 2. Each phase ends with the repository
verification ladder.

---

## 10. Open decisions

| ID | Decision | Proposal | Blocks |
|---|---|---|---|
| D1 | Package names and homes | `runtime/agent/model` (`CallInfo`); `runtime/agent/tools` (`TypedTool`, `Named`); top-level `capture` (decorator and request codec), `trajectory`, `expect`, `agenttest`, `eval`, `eval/judge`, `eval/evalcli` | T0, T1, T2 |
| D2 | Local store format and path | JSON files; path under the application module, gitignored | T13 |
| D3 | Judge mechanism and truncation | Spike `completion` against a `tooloutput` port on Anthropic and OpenAI; decide whether over-limit input fails or truncates visibly | Phase 4 |
| D4 | Trajectory source | Decided: run log plus model capture; stream adapter only for remote clients | — |
| D5 | Opik exporter home and trace mapping | Spike: how OTel trace IDs map to Opik traces, and whether the exporter lives in `features/` or in Auto-K on top of the store format | T18 |
| D6 | Live-test gating | Opt-in environment variable `LOOM_LIVE_MODEL=1`; the test names the model ID | T9 |
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

- **Runtime change for capture.** T0 adds model-call identity to the runtime.
  It is small but touches the planner path, so it gets its own review.
- **Judge variance.** Repeats and calibration reduce it but do not remove it.
- **Rendering size.** Long trajectories can exceed judge context; D3 decides the
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
stored locally under a gitignored path. Export to Opik (B9) is explicit and
sends the same data to the shared instance. Neither is telemetry. Both hold
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
