# MCP Integration

This guide routes work on the current MCP surface. Use `docs/dsl.md` for
declarations and `docs/mcp_sdk_server.md` for the generated server lifecycle.

## Declare an MCP server

MCP belongs to a Loom service:

```go
Service("calculator", func() {
    MCP("calculator-mcp", "1.0.0")

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

Do not add an MCP-only `JSONRPC` block. The official MCP Go SDK owns protocol
negotiation and the wire transport. Explicit non-MCP `JSONRPC` transports are
independent and remain valid.

The MCP declaration can define resources, watchable resources, prompts, skills,
tool discovery, icons, and OAuth metadata. Keep these contracts in the design.

## Consume MCP tools

For an external MCP server, use the `runtime/mcp` caller for its transport.
All runtime callers use the official MCP Go SDK.

For an MCP adapter in the same Go process, use the generated local-provider
registration. This path does not open a network connection or create MCP
session state.

## Expose service-owned agent tools over MCP

A method-backed tool can share one design contract between the agent runtime and
an MCP surface:

```go
Tool("search", "Search documents", func() {
    Args(SearchArgs)
    Return(SearchResult)
    BindTo("documents", "search")
    Expose(AgentRuntime, MCPSurface)
    MCPPlacement("documents", "documents-mcp")
})
```

The runtime tool spec remains the schema source of truth. Generated MCP calls
use the same method dispatcher as runtime execution. Validation rejects
unsupported projected-tool features.

## Generated server

The generator emits minimal MCP service types, `MCPAdapter`, local-provider
registration, OAuth discovery, prompt provider, and `SDKServer`. It does not
emit a native MCP client, native server, custom SSE extension, session store, or
broadcaster.

Create the adapter, then create `NewSDKServer`. The generated handler checks
each present `Origin` header on every HTTP method. This check includes the GET
connection for SSE. Invalid origins receive HTTP 403.

Configure `SDKServerOptions.RuntimeCORS` when a trusted browser client requires
cross-origin access. Add the browser origin to
`SDKServerOptions.OriginProtection`. The CORS response policy and request
origin policy are separate.

## Progress and streaming methods

MCP tool and resource calls are unary. A streaming Loom service method remains
supported: the adapter collects its values into one standard MCP result. Use
`runtime/mcp.ReportProgress` for intermediate progress notifications.

## Resource subscriptions

Declare `WatchableResource` to enable standard SDK subscribe and unsubscribe
handlers. Call the generated `SDKServer.ResourceUpdated(ctx, uri)` method after
the resource changes. The method rejects unknown URIs.

Watchable resources require persistent Streamable HTTP sessions. Do not combine
them with stateless Streamable HTTP.

## Resource authorization

`MCPAdapterOptions` URI and name policies define the server's maximum resource
grant. Request-scoped allowed names can narrow that grant but cannot broaden it.
Request and server denies are additive and take precedence.

Authenticate before the generated handler. Derive principals and grants from
verified application policy. In `SDKServerOptions.RequestContext`, use
`runtime/mcp.WithAllowedResourceNames` and
`runtime/mcp.WithDeniedResourceNames` for trusted request narrowing.

## Session identity

Wrap the generated handler with authentication middleware. The SDK wrapper uses
`MCPAdapterOptions.SessionPrincipal`, with TokenInfo `UserID` as its default.
It binds each initialized session to the resolved principal and checks that
binding on POST, GET, and DELETE.

## OAuth and HTTP

OAuth declarations generate protected-resource metadata, challenges, and
audience helpers. They do not install authentication or per-operation scope
authorization. The application must verify tokens and enforce scopes before the
generated handler.

## Verification

After an MCP design change:

1. Regenerate with the module import path.
2. Inspect the adapter, local-provider, and SDK server output.
3. Confirm that no MCP `gen/jsonrpc` package was generated.
4. Run focused MCP and code-generation tests.
5. Run `make verify-mcp-local` and the full repository suite.

For a new MCP protocol feature, use the repo-local
`new-mcp-feature-development` skill. Start with a real SDK
client-versus-framework validation test.
