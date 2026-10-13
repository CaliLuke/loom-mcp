# loom-mcp DSL Reference

Use `docs/dsl.md` as the exhaustive, canonical reference. This skill reference
is a routing map plus the contracts most likely to affect implementation work.

## Imports and ownership

```go
import (
    . "github.com/CaliLuke/loom/dsl"
    . "github.com/CaliLuke/loom-mcp/v2/dsl"
)
```

Agent and MCP declarations live inside Loom `Service` definitions. Service-owned
toolsets are the defining owner of their specs/codecs/transforms; agents and MCP
servers consume or project them.

## Agent composition

| DSL | Purpose |
| --- | --- |
| `Agent(name, description, dsl)` | Declare an agent inside a service |
| `Use(source, dsl?)` | Consume a toolset source |
| `Export(name, dsl)` | Export an agent-as-tool surface |
| `AgentToolset(service, agent, toolset)` | Reference an exported agent toolset |
| `UseAgentToolset(...)` | Reference and consume an exported agent toolset |
| `DisableAgentDocs()` | Disable root `AGENTS_QUICKSTART.md` generation |

Agent-as-tool execution is a real child workflow with `ChildRunLinked` and a
parent `RunLink`, not an inline shortcut.

## Toolset sources

| Source | Contract |
| --- | --- |
| `Toolset(name, dsl)` | Service-owned local toolset |
| `Toolset(name, FromMCP(service, suite))` | Generated/external MCP caller-backed toolset |
| `Toolset(name, FromRegistry(registry, remote))` | Registry-discovered toolset |
| `Toolset(name, FromSkills(...))` | Model-facing local skill tools |
| `Toolset(name, FromArtifacts(...))` | Model-facing artifact access |
| `Toolset(name, FromMemory(...))` | Transcript/indexed/long-term memory tools |

Use `Description`, `Version`, and source-specific options documented in
`docs/dsl.md`.

## Tool declarations

| DSL | Contract |
| --- | --- |
| `Tool(name, description, dsl)` | Declare a method-level MCP tool or toolset tool |
| `Args(...)`, `Return(...)` | Model-facing payload/result schemas |
| `BindTo(service, method)` | Bind a toolset tool to a Loom method |
| `Inject(fields...)` | Keep fields in public/transport payloads but hide them from the model schema |
| `ServerData(kind, type, dsl?)` | Server-only output/sidecar data, never server-injected input |
| `BoundedResult(...)` | Runtime-owned canonical bounds contract |
| `Cursor`, `NextCursor` | Opaque pagination field declarations |
| `Confirmation(...)` | Runtime-owned explicit approval flow |
| `Idempotent()` | Emit transcript-scoped metadata; built-in runtime does not de-duplicate calls |
| `CallHintTemplate`, `ResultHintTemplate` | UI display hints |
| `ResultReminder` | Backstage reminder after a tool result |
| `Expose`, `MCPPlacement` | Project a method-backed tool into a same-service MCP server |

Do not author canonical `returned`, `total`, `truncated`, `refinement_hint`, or
configured next-cursor fields in a tool `Return`. `BoundedResult` projects those
runtime-owned fields. Successful executions populate `planner.ToolResult.Bounds`.

Toolset tools are runtime-only by default. Projection requires both
`AgentRuntime` and `MCPSurface`, a same-service `MCPPlacement`, and a
method-backed tool. Projection supports injection, bounded results, and
agent-only result reminders. It rejects confirmation and server data because
MCP cannot preserve their authorization and privacy contracts.

## Run policy and workflows

| DSL | Contract |
| --- | --- |
| `RunPolicy(...)` | Agent execution policy |
| `DefaultCaps(MaxToolCalls(...), MaxConsecutiveFailedToolCalls(...))` | Run caps |
| `TimeBudget(...)` | Active execution budget; external-input waits pause it |
| `Timing(func() { Budget(...); Plan(...); Tools(...) })` | Semantic run, planner, and tool budgets |
| `Cache(AfterSystem(), AfterTools())` | Prompt cache checkpoints |
| `InterruptsAllowed`, `OnMissingFields` | Human-input and validation policy |
| `RetryAndReflect(...)` | Structured reflected retry policy |
| `PreloadMemory`, `PreloadLongTermMemory` | Planner-input memory projection |
| `Interceptors(ids...)` | Application-supplied named runtime interceptors |

Plain `Workflow`/`Step` generates a sequential planner. Graph workflows use
`Parallel`, `Join`, `RequestInput`, `Loop`, `Branch`, and `BranchDefault` with
stable node IDs, acyclic dependencies, and bounded loops.

## MCP service declarations

Use `MCP(name, version, opts...)` inside a service. Method-level MCP tools use
`Tool(...)` in the method context; there are no `MCPServer`, `MCPTool`, or
`MCPToolset` aliases in the current public contract.

`StaticPrompt(...)` can appear before or after `MCP(...)` in the same service. The service must
declare `MCP(...)`.

Current MCP surfaces include:

- implementation metadata: `WebsiteURL`, server icons, and list-surface icons;
- tools plus compact discovery: `ToolSearch(...)`, discovery metadata, and
  call-template arguments;
- resources, templates, and `WatchableResource` subscriptions;
- `StaticPrompt`, `DynamicPrompt`, `RuntimePrompt`, prompt icons, and enum-backed
  completion;
- `SkillDirectory(...)` MCP resources;
- OAuth protected-resource metadata, scopes, audience binding, and proxy trust.

The official MCP Go SDK owns protocol negotiation. Do not declare
`ProtocolVersion`, `Notification`, `Subscription`, `SubscriptionMonitor`, or an
MCP-only `JSONRPC` block.

`OAuthScope` advertises metadata; application auth enforces scopes.
`WithOAuthChallenge` adds the standard resource-metadata challenge but is not
error-aware and does not add `invalid_token` automatically.

## Memory distinction

- `memory.Store`: raw per-run transcript events.
- `memory.Searcher`: indexed raw event search.
- `memory.Service`: durable long-term `memory.Entry` values.

Use `MemoryLongTerm()` and long-term preload intentionally. Do not overload raw
transcript memory with extracted long-term facts.

## Generation

```bash
loom gen <module-import-path>/design
```

Expected owners:

- `gen/<service>/agents/<agent>`: agent registration/aggregate catalog.
- `gen/<service>/toolsets/<toolset>`: defining tool types/specs/codecs/transforms.
- `gen/mcp_<service>`: generated MCP adapter/server/local provider.

Never edit generated output. Use `loom example` only for intentional
application-owned scaffolding.

## Context compaction

Inside RunPolicy, Context enables threshold compaction and accepts TriggerAt
(exact decimal fraction, strictly between zero and one, at most four decimals),
KeepRecentTokens (positive), Summarize, Recall and Disable. Context conflicts with
Workflow. Each declaration is unique in its parent.

Summarize requires non-empty Instructions or Prompt. Prompt is parsed and
executed during design validation with the runtime summary prompt's empty custom
function map and only .Previous, .Evicted, .Instructions and .Budget. It must
reference .Evicted and, if Instructions is declared, .Instructions. The probe
uses nonempty strings and Budget 100, so it validates only the branches taken
with those values. In particular, the else branch of if .Previous is not checked.
Test authored templates with empty and nonempty previous summaries.

Every planner config has optional ContextModel *runtime.ContextModel;
Validate requires it only for Summarize. Register its model ID before Seal.
Recall(false) sets ContextRecovery.DisableRecall. Disable emits ContextDefault.Disabled and omits threshold policy;
it may accompany Recall(false), but not threshold or summary settings, and does
not cancel runtime options.

KeepResultInContext in Tool emits a spec flag and conflicts with Bookkeeping.
Flagged specs join explicit ContextCompaction.KeepResultTools at registration;
the set is owned, sorted and stamped. Later toolsets do not alter the keep set.
Keep protects clearing, not whole-unit eviction. Context agents cannot use tool
names containing recall_context.
