# MCP Integration

This is a routing guide for the current MCP surface. Use `docs/dsl.md` for declarations and `docs/mcp_sdk_server.md` for the generated SDK/server lifecycle.

## Declare an MCP server

MCP belongs to a Loom service. The current DSL names are `MCP(...)` and method-level `Tool(...)`:

```go
Service("calculator", func() {
    MCP("calculator-mcp", "1.0.0", ProtocolVersion("2025-06-18"))

    Method("add", func() {
        Payload(func() {
            Attribute("a", Int)
            Attribute("b", Int)
            Required("a", "b")
        })
        Result(func() {
            Attribute("sum", Int)
            Required("sum")
        })
        Tool("add", "Add two numbers")
    })
})
```

Do not use the removed `MCPServer(...)` or `MCPTool(...)` forms.

The same MCP declaration can define resources, subscriptions, prompts, skills, progressive tool discovery, icons, protocol/implementation metadata, and OAuth metadata. Keep those contracts in design so validation and code generation remain authoritative.

## Consume a generated MCP suite as agent tools

Declare a toolset backed by a service and MCP suite:

```go
var CalculatorTools = Toolset(FromMCP("calculator", "calculator-mcp"))

Agent("assistant", "Uses calculator tools", func() {
    Use(CalculatorTools)
})
```

Generated JSON-RPC code is owner-scoped under `gen/jsonrpc/<service>/...`. The generated caller constructor takes both the transport client and suite name:

```go
caller := calculatorclient.NewCaller(client, "calculator-mcp")
```

Then use the generated agent registration helper. Its exact name is emitted into the agent package; for the assistant fixture it is:

```go
err := assistant.RegisterAssistantAssistantMcpToolset(ctx, rt, caller)
```

Do not guess helper names. Read the generated `AGENTS_QUICKSTART.md` and package API after generation.

For a remote, non-generated MCP server, use the generic runtime caller appropriate to its transport and register it through the generated toolset helper. The caller owns JSON-RPC exchange; generated registration owns suite schemas and runtime tool specs.

## Expose service-owned agent tools over MCP

A method-backed tool can share one design contract between the agent runtime and an MCP surface:

```go
Tool("search", "Search documents", func() {
    Args(SearchArgs)
    Return(SearchResult)
    BindTo("documents", "search")
    Expose(AgentRuntime, MCPSurface)
    MCPPlacement("documents", "documents-mcp")
})
```

The runtime tool spec remains the schema source of truth. Generated MCP calls use the same method dispatcher as runtime execution. The design rejects unsupported projected-tool features rather than silently dropping them.

## Server lifecycle and delivery

Generated code owns the protocol adapter, JSON-RPC transport, and SDK-facing server construction. Application code supplies service implementations, resource/prompt/subscription handlers, authorization policy, broadcaster/session state, and lifecycle integration.

Important delivery contracts:

- `mcp.Broadcaster.Publish` is global broadcast.
- `mcp.SessionBroadcaster.PublishSession` delivers a message once within the target session, even with overlapping SSE connections.
- invalid or unknown session IDs return `ErrInvalidSessionID`.
- interceptors execute in declared order and can short-circuit the request.
- Tool-call interceptors can receive raw model arguments. Treat them as
  confidential and avoid logging them; see `docs/mcp_sdk_server.md`.
- the generated telemetry boundary records MCP operations without requiring handlers to duplicate instrumentation.

## Resource authorization

`MCPAdapterOptions` URI/name allow policies define the server's maximum
resource grant. Request-scoped allowed names may narrow that maximum but cannot
broaden it; request and server denies are additive and take precedence. This
also applies to `skill://` resources, where a skill name maps to its resource
prefix.

The native JSON-RPC transport accepts `x-mcp-allow-names` and
`x-mcp-deny-names` as request narrowing input. Never treat those client-chosen
headers as credentials or grant authority. Authenticate before the generated
handler and derive principals and deployment grants from verified application
policy.

The SDK transport passes request headers through for application inspection but
does not automatically map those raw headers to resource-name policy. Apply
trusted SDK narrowing in `SDKServerOptions.RequestContext` with
`runtime/mcp.WithAllowedResourceNames` and
`runtime/mcp.WithDeniedResourceNames`.

## Session identity

Wrap generated handlers with authentication middleware so verified identity is
available before MCP session processing. SDK servers use
`MCPAdapterOptions.SessionPrincipal`, falling back to TokenInfo `UserID`;
generated native JSON-RPC packages expose `MCPSessionPrincipal` with the same
default for custom identity systems.

An initialized session is bound to its resolved principal. Every later POST,
GET, and DELETE must present the same principal. Missing authenticated bindings,
mismatches, and authenticated adoption of anonymous sessions return HTTP 403.
Unknown, expired, or terminated IDs return HTTP 404. Session/principal state is
TTL-pruned and capacity-bounded together, and DELETE validates ownership before
termination.
Fresh native `initialize` requests omit `Mcp-Session-Id`. Unknown IDs return
HTTP 404, foreign owner-bound IDs return HTTP 403, and a valid owner-bound ID
may reach the adapter only to return the protocol-level `Already initialized`
error. Callers cannot reserve adapter session state with a chosen ID.

## OAuth and HTTP

Protected-resource metadata, authorization-server discovery, and OAuth scopes are design-owned. These declarations generate metadata, challenge, and audience-enforcement helpers; they do not install authentication or per-operation scope authorization. The application must verify tokens and enforce scopes before the generated handler. Use a dedicated `http.Client` for cross-origin protected-resource and authorization-server requests; do not rely on a same-origin client whose base URL or credentials are tied to the MCP server.

Treat a resource-server `invalid_token` response as an authentication failure that may require token refresh/re-authorization. Do not reinterpret it as proof that the authorization server is unavailable.

## Verification

After changing an MCP declaration:

1. Regenerate using the module import path, never a filesystem path.
2. Inspect generated JSON-RPC, adapter, server, and quickstart output.
3. Run focused MCP/codegen tests.
4. Run `make verify-mcp-local`, then the full repository suite required by the repo-local skill.

For new protocol surface or MCP spec catch-up, use the repo-local `new-mcp-feature-development` skill and begin with a real client-versus-framework validation test.
