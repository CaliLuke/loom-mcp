# Loom MCP: Design-First Agentic Systems

Build intelligent agents, MCP servers, and registry-integrated toolsets from your Loom designs. `loom-mcp` layers agent orchestration, MCP protocol support, and centralized registries onto the core Loom design/codegen pipeline.

## What you get

- **Agents**: Durable plan/execute loops with policy enforcement, memory, and streaming
- **MCP**: Tools, resources, and prompts exposed through the official MCP Go SDK
- **Registries**: Centralized tool catalogs with federation, caching, and semantic search
- **Unified Toolsets**: Single `Toolset` construct with providers (local, MCP, registry)

## How it works

For each service annotated with agents or MCP, the plugin:

1. Derives service expressions from your DSL (see `expr/agent/` and `expr/mcp/`).
2. Runs the standard Loom code generation pipeline:
   - Service layer via `codegen/service` (service, endpoints, client)
   - JSON-RPC transport via `jsonrpc/codegen` (server, client, types; SSE when streaming)
   - Agent workflows, activities, and tool specs via `codegen/agent`
3. Emits loom-mcp adapters and replaces stable named generator sections where
   MCP requires transport behavior beyond the upstream defaults. Every
   replacement is driven by evaluated generator data and fails on missing or
   duplicate sections; rendered Go source is never inspected or rewritten.

We compose on top of the shared Loom generation pipeline and keep the resulting
output deterministic and covered by golden and compile tests. JSON serialized
at generation time for embedding in generated Go source uses deterministic
object-member ordering; runtime JSON keeps its independent wire semantics.

## Layout

- Agent packages: `gen/<svc>/agents/<agent>/`
- Agent aggregate catalog: `gen/<svc>/agents/<agent>/specs/`
- Toolset-owned specs, codecs, and transforms: `gen/<svc>/toolsets/<toolset>/`
- MCP service: `gen/mcp_<service>/`
- Registry clients: `gen/<svc>/registry/<name>/`

## Unified Toolset Model

Loom MCP provides a unified `Toolset` construct with configurable providers:

```go
// Local toolset (inline schemas)
var LocalTools = Toolset("utils", func() {
    Tool("summarize", "Summarize text", func() {
        Args(func() { Attribute("text", String) })
        Return(func() { Attribute("summary", String) })
    })
})

// MCP-backed toolset
var MCPTools = Toolset("assistant", FromMCP("assistant-service", "assistant-mcp"))

// Registry-backed toolset (discovered at runtime)
var RegistryTools = Toolset("enterprise", FromRegistry(CorpRegistry, "data-tools"))
```

All toolsets are first-class citizens—agents use `Use(toolset)` uniformly regardless of provider.

## Registry Integration

Declare centralized registries for tool discovery and agent publication:

```go
var CorpRegistry = Registry("corp-registry", func() {
    Description("Corporate tool registry")
    URL("https://registry.corp.internal")
    APIVersion("v1")
    Security(CorpAPIKey)
    SyncInterval("5m")
    CacheTTL("1h")
})

// Federated external registry
var AnthropicRegistry = Registry("anthropic", func() {
    URL("https://registry.anthropic.com/v1")
    Security(AnthropicOAuth)
    Federation(func() {
        Include("web-search", "code-execution")
        Exclude("experimental/*")
    })
})
```

### Runtime Components

- **Registry Manager** (`runtime/registry/manager.go`): Multi-source catalog merging
- **Schema Cache** (`runtime/registry/cache.go`): TTL-based caching with fallback
- **Federation Sync**: Periodic catalog synchronization from external registries
- **Search**: Semantic and keyword-based tool discovery

## MCP Server Definition

Enable MCP for a service with `MCP`:

```go
Service("calculator", func() {
    MCP("calc", "1.0.0")
    Method("add", func() {
        Payload(func() { Attribute("a", Int); Attribute("b", Int) })
        Result(func() { Attribute("sum", Int) })
        Tool("add", "Add two numbers")
    })
})
```

The official MCP Go SDK owns protocol negotiation and all wire transport
behavior. An MCP service does not require a `JSONRPC` declaration.

### Generated server

The generator emits the service types, `MCPAdapter`, local-provider
registration, OAuth discovery, prompt provider, and `SDKServer`. It does not
emit an MCP JSON-RPC client, native server, SSE extension, or session store.

The generated `MCPAdapterOptions` provides hooks for logging, error mapping,
tool search, resource authorization, request-state encryption, and principal
resolution. `SDKServerOptions` provides Streamable HTTP, runtime CORS,
transport observation, and SDK runtime options.

All outbound MCP callers in `runtime/mcp` use the official SDK. In-process
agents can use the generated local-provider registration without a wire
transport.

## MCP progress and resource updates

A streaming Loom service method remains valid as an MCP tool or resource
implementation. The generated adapter collects its values into one standard
MCP result. Use `runtime/mcp.ReportProgress` for intermediate progress.

Declare `WatchableResource` to enable SDK subscription handlers. After a
resource changes, call the generated `SDKServer.ResourceUpdated(ctx, uri)`
method. Watchable resources require stateful Streamable HTTP sessions.

## Agent run lifecycle streaming contract

The runtime emits a single terminal lifecycle event per run via `hooks.RunCompletedEvent`.
The stream subscriber translates it into a `workflow` stream event (`stream.WorkflowPayload`)
that UIs and stream bridges can consume without heuristics.

- **Terminal status**
  - `status="success"` → `phase="completed"`
  - `status="failed"` → `phase="failed"`
  - `status="canceled"` → `phase="canceled"`

- **Cancellation is not an error**
  - For `status="canceled"`, the stream payload **must not** include a user-facing `error`.
  - Consumers should treat cancellation as a terminal, non-error end state.

- **Failures are structured**
  - For `status="failed"`, the stream payload includes:
    - `error_kind`: stable classifier for UX/decisioning (provider kinds like `rate_limited`, `unavailable`, or runtime kinds like `timeout`/`internal`)
    - `retryable`: whether retrying may succeed without changing input
    - `error`: **user-safe** message suitable for direct display
    - `debug_error`: raw error string for logs/diagnostics (not for UI)

This keeps consumers simple: render `error`, gate “Retry” on `retryable`, and treat `canceled` as non-error.

## Tool Input Schema

For each tool with a non-empty payload, the plugin derives a compact JSON Schema from the authored attribute definition and exposes it in `tools/list` under `inputSchema`. Union payloads preserve their discriminator envelope (`oneOf` plus `discriminator.propertyName`) instead of collapsing to an empty object. This uses the shared OpenAPI schema machinery for complete JSON Schema draft 2020-12 support.

## Tool Identification

Tools are identified by canonical IDs in the format `<toolset>.<tool>` (dot-separated). The generated code produces typed constants (e.g., `MyTool tools.Ident`) matching this format.

## Agents Quickstart & Example Scaffold

A contextual quickstart file `AGENTS_QUICKSTART.md` is emitted at the module root on `loom gen`, summarizing what was generated and how to wire it. To opt out, invoke `DisableAgentDocs()` inside your API DSL.

The `loom example` phase generates application-owned scaffold under `internal/agents/`:

- `internal/agents/bootstrap/bootstrap.go`: constructs a minimal runtime and registers generated agents
- `internal/agents/<agent>/planner/planner.go`: planner stub implementing `PlanStart`/`PlanResume`
- `internal/agents/<agent>/toolsets/<toolset>/adapter.go`: stubs for mapping method-backed tools

## Security considerations

- Resource policy: use deny/allow lists to constrain which URIs can be read
- Registry authentication: use the design-level security schemes (`APIKeySecurity`, `OAuth2Security`, etc.)
- Logging: avoid logging sensitive payloads and results in production

## Error code mapping

The adapter maps service errors with name `invalid_params` to JSON-RPC `-32602`, `method_not_found` to `-32601`, and otherwise defaults to `-32603` (internal).

## Contributing

- Add agent concepts in `expr/agent/` and update the expression builders
- Add MCP concepts in `expr/mcp/` and update the MCP expression builder
- Add registry concepts in `expr/agent/registry.go`
- Keep new templates small and transport-agnostic; compose on the existing JSON-RPC outputs

## Summary

This plugin gives you agents, MCP, and registries with familiar Loom patterns, minimal surface area, and a directory layout that feels natural. It's accurate, easy to maintain, and designed to evolve with the Loom toolchain.
