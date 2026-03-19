# Runtime Contracts

Use this file for current Goa-AI runtime behavior in this repo. Prefer it over stale external notes.

## Planner Streaming

- `PlannerContext.ModelClient(id)` returns a runtime-decorated client.
- With the decorated client, drain the `Streamer` yourself with `Recv()`.
- Do not pass a decorated stream to `planner.ConsumeStream`.
- Use `planner.ConsumeStream` only with a raw `model.Client`.
- Mixing the two paths double-emits thinking and assistant text events.

## Agent-As-Tool

- Agent-as-tool runs as a real child workflow, not an inline local shortcut.
- Parent and child runs are linked with a `ChildRunLinked` event.
- Parent tool results carry a `RunLink` to the child run.
- Runtime execution goes through `ExecuteAgentChildWithRoute`.
- `AgentToolConfig.Route` is required; there is no fallback to ad hoc local lookup.
- Consumer-side prompt rendering is optional and payload-only. Provider-side context belongs in the provider planner/runtime, not the consumer.
- Generated helper packages expose `NewRegistration(...)`; runtime internals build the underlying registration with `runtime.NewAgentToolsetRegistration(...)`.

## Streams

- Streams are session-owned.
- `stream.StreamProfile` controls visibility by audience.
- Child runs are linked, not flattened, by default.
- Use profile selection to shape chat, debug, or metrics views instead of changing core runtime behavior.

## Prompt Runtime

- `Runtime.PromptRegistry` stores baseline prompt specs.
- `runtime.WithPromptStore(...)` adds scoped overrides.
- Planners should render prompts through `RenderPrompt(...)` so provenance flows into model requests.

## Tool Execution Contracts

- Runtime-owned tool specs and codecs are the schema source of truth.
- Use generated `tool_specs.Specs` and codecs for payload/result schema and encoding needs.
- Do not introspect `docs.json` at runtime.
- Tool results and retry hints should stay structured; avoid best-effort coercion when contracts fail.

## Where To Verify

- `docs/runtime.md`
- `runtime/agent/runtime/agent_tools.go`
- `runtime/agent/runtime/model_wrapper.go`
- `runtime/agent/stream/stream.go`
