# loom-mcp glossary

This glossary distinguishes similarly named product surfaces. The linked guides
remain authoritative for behavior and APIs.

| Term | Meaning | Authoritative guide |
| --- | --- | --- |
| Agent | A designed planner configuration, policy, prompts, and toolsets identified by `service.agent`. | [DSL](dsl.md) |
| Tool | A model-callable operation. Canonical generated identifiers are `<toolset>.<tool>`; method-level MCP tools remain MCP-only unless explicitly projected. | [DSL](dsl.md) |
| Toolset | A named source of tools, such as inline tools, bound service methods, MCP, registry discovery, skills, memory, or another agent. | [Overview](overview.md) |
| Planner | Retry-safe logic that turns transcript input into assistant output, tool calls, or an await request. A logical turn may have multiple activity attempts. | [Runtime](runtime.md) |
| Runtime | The orchestration layer that owns policy, planner/tool activities, awaits, hooks, run/session state, and engine-neutral execution. | [Runtime](runtime.md) |
| Engine | The workflow scheduler. In-memory and Temporal engines share runtime semantics but have different durability and retry infrastructure. | [Runtime](runtime.md) |
| Run / turn / session | A run is one agent execution; a turn groups one user interaction and may contain multiple planner steps or retry attempts; a session groups related runs and client metadata. | [Runtime](runtime.md) |
| Workflow state | Live deterministic execution authority for an active run. It is not the public append-only event log. | [Runtime](runtime.md) |
| Run log | `runlog.Store`, the canonical append-only hook-event record used for introspection and replay-oriented projections. | [Runtime](runtime.md) |
| Transcript ledger | Mutable workflow-local conversation state, including the current assistant turn. It can be rebuilt from transcript events and is not durable run history by itself. | [Runtime](runtime.md) |
| Transcript memory | `memory.Store` / `memory.Searcher`, a derived per-run event projection used to rebuild planner messages. Default subscriber delivery is best effort. | [Runtime](runtime.md) |
| Long-term memory | `memory.Service`, an explicit durable store of extracted `memory.Entry` values. It is separate from transcript events and the run log. | [Runtime](runtime.md) |
| Hook | An internal lifecycle event. Critical subscribers may fail hook processing; default derived projections are generally best effort. | [Runtime](runtime.md) |
| Stream | A client-facing live projection of runtime events. Streams are not a canonical durable log. | [Runtime](runtime.md) |
| Pulse sink / subscriber | The Pulse-backed implementation under `features/stream/pulse`; it implements generic runtime stream contracts. Use manual-ack delivery for durable consumers. | [Runtime](runtime.md) |
| MCP server / caller | A generated server exposes designed operations through MCP. A caller consumes an external or generated MCP server as a tool source. | [MCP SDK server](mcp_sdk_server.md) |
| MCP skill resource | A `SkillDirectory(...)` projection exposed as `skill://...` resources through MCP `resources/list` and `resources/read`. | [MCP SDK server](mcp_sdk_server.md) |
| Model-facing skill tools | `Toolset(FromSkills(...))` discovery/load tools that place instruction packages in an agent's model context. They are not MCP resources. | [DSL](dsl.md) |
| Registry service / runtime registry | The service publishes versioned toolsets; the runtime manager discovers and registers those toolsets for agents. Prompt overrides use a separate prompt registry. | [Runtime](runtime.md) |
| `CapsState` / `TimeBudget` | `CapsState` owns counter budgets. The runtime-owned `TimeBudget` deadline measures active work and pauses for human or external waits; deprecated `CapsState.ExpiresAt` does not enforce time. | [Runtime](runtime.md) |
| Streamable-HTTP session | A server-issued MCP session ID bound to a verified principal. Missing required IDs are 400, invalid/expired IDs are 404, and ownership failures are 403. | [MCP SDK server](mcp_sdk_server.md) |
| Provider conformance | The shared adapter test contract for complete/streaming behavior, structured output, tool choice, thinking, counting, cancellation, and normalized errors. A provider may explicitly report streaming unsupported. | [Runtime](runtime.md) |
| Generated owner | The service/toolset/agent package selected by codegen to own shared specs, codecs, dispatch, and registration helpers. Generated `gen/` files are never edited by hand. | [Overview](overview.md) |
| `Idempotent()` | Transcript-scoped metadata for planners/orchestrators. The built-in runtime does not suppress duplicate calls or cache results. | [DSL](dsl.md) |

## Identity conventions

- Agent: `service.agent`
- Tool: `<toolset>.<tool>`
- MCP suite: service plus MCP server name
- Run, session, turn, tool-call, and event IDs identify different lifecycle
  scopes and must not be substituted for one another.
