# MCP SDK Server

This document describes the generated MCP SDK server: how to construct one,
the customization hooks exposed through `SDKServerOptions`, and the runtime
contract guarantees Loom-MCP makes for context propagation and per-call
request metadata.

## Construction

`loom gen` emits `gen/mcp_<service>/sdk_server.go` for every service that
declares an MCP block. The file exports a single constructor:

```go
package main

import (
    mcpassistant "example.com/assistant/gen/mcp_assistant"
)

server, err := mcpassistant.NewSDKServer(
    assistantapi.NewAssistant(), // your service implementation
    &mcpassistant.SDKServerOptions{
        PromptProvider: myPromptProvider{},
        RequestContext: func(ctx context.Context, r *http.Request) context.Context {
            // see "Request Context Callback" below
            return ctx
        },
        TransportObserver: myTransportObserver,
    },
)
if err != nil {
    log.Fatal(err)
}

mux := http.NewServeMux()
mux.Handle("/rpc", server.Handler)
http.ListenAndServe(":8080", mux)
```

`server.Handler` is a standard `http.Handler`. It accepts both POST (the
MCP streamable-HTTP JSON-RPC channel) and GET (the upstream SDK's standalone
SSE listener) on the same path. The loom-specific `events/stream` method is a
JSON-RPC-transport feature and is not routed (nor advertised) in SDK mode.

## SDKServerOptions

| Field            | Required | Description                                                                                                                                                            |
| ---------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PromptProvider` | yes      | Implementation that renders prompts declared with `StaticPrompt` and `DynamicPrompt`. Generated against the design.                                                    |
| `RequestContext` | no       | Per-request hook called once per MCP RPC. Receives the inbound request context and a synthetic `*http.Request` carrying the live transport headers; returns a new ctx. |
| `TransportObserver` | no    | Loom transport observer installed on the generated SDK handler. Request lifecycle events are delivered without external middleware wiring.                           |
| `StreamableHTTP` | no       | `*mcpsdk.StreamableHTTPOptions` passed through to the upstream SDK. A default `net/http.NewCrossOriginProtection()` is applied when the options or their protection field are nil. |

## Design-Declared Skill Resources

Services that declare `SkillDirectory(root)` expose local agent skills through
the generated SDK server resource surface. The server scans the configured root
at startup, lists each child directory with a `SKILL.md` file as
`skill://<skill>/SKILL.md`, and also publishes `skill://<skill>/_manifest`.
`resources/read` can read `SKILL.md`, `_manifest`, and supporting files that
stay inside the skill directory.

A service may expose only `SkillDirectory` resources and no method-backed
resources. Code generation still emits the complete resources capability,
service methods, adapter implementation, and SDK conversions for that shape.

`SKILL.md` may include structured YAML frontmatter (`id`, `name`,
`description`, `allowed_tools`, `preload`, and `reload`). Missing frontmatter is
compatible with older skills: the directory name becomes the ID and the first
heading or text line becomes the description. Duplicate IDs and invalid
metadata fail resource discovery.

The generated SDK server discovers and registers skill resources when
`NewSDKServer` is called. Add or remove skills before constructing a new SDK
server; an already-running SDK server keeps its registration snapshot. The
generated adapter and JSON-RPC `resources/list` path scan configured roots for
each list request.

## Optional tool arguments

MCP clients may omit `tools/call.arguments` or send JSON `null`. Generated
adapters normalize either form, as well as a whitespace-only value, to `{}`
before decoding a real tool payload.
Tools with all-optional payloads therefore execute normally, while tools with
required fields return the generated missing-field validation error and repair
hint.

Generated MCP JSON-RPC errors expose client-safe metadata such as the Loom error
name, retry flags, and remediation guidance. Internal Loom service error instance
IDs are retained for server-side logging and omitted from the wire `error.data`.

JSON-RPC batches are buffered one request at a time. Streaming `tools/call`
entries contribute only their final JSON-RPC response to the enclosing JSON
array; SSE retry and intermediate notification frames are never written into
the batch body.

## Request Context Callback

`RequestContext` is the supported extension point for propagating
transport-level information (auth tokens, correlation ids, allow/deny
lists, tenant ids) from inbound HTTP headers into the context that
generated tool, resource, and prompt handlers receive.

The callback fires once per MCP call. The supplied `*http.Request` is
synthesized for the call from two sources, merged in this order:

1. The HTTP headers of the outer streamable-HTTP request, threaded through
   ctx via `mcpruntime.WithRequestHeaders` at the top of the generated
   handler.
2. The per-JSON-RPC-call headers that the upstream MCP SDK exposes on
   `RequestExtra.Header`.

**Per-call values from `RequestExtra.Header` overlay the ctx-bridged
values.** Step 2 wins on conflict. This guarantees that when a client
sends a distinct value of a header (e.g. `X-Request-ID`) for each
call (initialize, tools/call, resources/read), the tool handler sees its
own call's value rather than a stale value left over from session
establishment.

The contract is pinned by two regression tests in the repository:

- `TestRequestContextSeesPerCallHeaders` (assistant fixture) drives a
  real generated SDK server and asserts a tools/call invocation sees its
  own `X-Request-ID`, not the initialize value.
- `TestGenerateSDKServer_MergesContextRequestHeadersIntoSyntheticRequest`
  (codegen contract) compares substring positions of the two header copy
  loops in the rendered code to lock in the precedence order.

### Example

```go
RequestContext: func(ctx context.Context, r *http.Request) context.Context {
    if r == nil {
        return ctx
    }
    if rid := r.Header.Get("X-Request-ID"); rid != "" {
        ctx = context.WithValue(ctx, requestIDKey{}, rid)
    }
    if allow := r.Header.Get("X-Mcp-Allow-Names"); allow != "" {
        ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)
    }
    return ctx
}
```

Treat inbound allow/deny headers as untrusted request input. The generated
native JSON-RPC mount recognizes `x-mcp-allow-names` and
`x-mcp-deny-names`, and an SDK application can opt into the same context
helpers as above, but an allow header is never an authentication or grant
mechanism. Derive principals and deployment grants in application-owned auth
middleware from verified credentials. Use request allow values only to narrow
that trusted maximum grant.

`SDKServerOptions.Adapter.ToolSearch` passes through to the generated MCP
adapter. When set, SDK-backed servers use the same compact public discovery
surface as JSON-RPC adapters: SDK `tools/list` registers synthetic
`search_tools` and `call_tool` entries plus real tools pinned in
`ToolSearchOptions.AlwaysVisible`. Hidden real tools are not registered directly;
clients discover them through `search_tools` and invoke them through `call_tool`.
Search ranking uses the same generated DSL defaults and runtime
`ToolSearchOptions` knobs as the JSON-RPC adapter, including exact-match
narrowing, fuzzy name/title matching, broad fallback control, score cutoff, and
field weights.

Method-backed toolset tools projected into MCP with
`Expose(AgentRuntime, MCPSurface)` and `MCPPlacement(...)` are included in the
same generated adapter catalog as method-level MCP tools. SDK `ListTools`
advertises their `ToolInfo` schemas from generated toolset specs, compact mode
can pin them with `AlwaysVisible`, and SDK `call_tool` invokes them through the
generated MCP adapter so execution still uses the shared method-backed
dispatcher.

`ToolSearchOptions.AllowDirectHiddenCalls` is unsupported for SDK-backed compact
mode. `NewSDKServer` fails construction when that option is true because the SDK
cannot directly call tools that were intentionally omitted from the registered
public catalog.

The same generated adapter can be registered with an in-process agent runtime
through `New<Service><MCP>LocalToolsetRegistration(adapter)`. This local path
uses the adapter's compact catalog, search ranking, synthetic names, visibility
rules, interceptors, and real-tool dispatch directly. It does not use the SDK
server, HTTP, JSON-RPC, initialization, or MCP sessions.

### Resource access policies include skills

`MCPAdapterOptions.AllowedResourceURIs`, `DeniedResourceURIs`,
`AllowedResourceNames`, and `DeniedResourceNames` apply to both DSL resources
and `skill://` resources. URI entries are exact unless they end in `/`; a
trailing slash authorizes or denies the full prefix, such as
`skill://code-review/`. A skill's main resource name also maps to that prefix,
so allowing or denying `code-review` covers `SKILL.md`, `_manifest`, and
supporting files. Request-scoped `x-mcp-allow-names` and
`x-mcp-deny-names` values use the same matching rules. Server allow policies
and request allow policies are independent constraints: when both are present,
the requested resource must match both. A request allow can therefore narrow
`MCPAdapterOptions`, but cannot add a resource outside the server's maximum
grant. Server and request denies are additive and always take precedence.

If the server has no configured allow policy, its maximum is unrestricted and
a request allow still narrows that surface. Do not mistake this narrowing for
authorization: a client can choose its own raw header. Install authentication
and authorization before the generated handler, and configure
`MCPAdapterOptions` from trusted deployment policy.

### Ctx-Cached Values Can Be Stale — Read `r.Header` First

The supplied `*http.Request` carries fresh per-call values. The supplied
`ctx`, however, can carry stale values: the upstream MCP SDK threads a
session-scoped context through tool dispatches, so values written into
ctx during the `initialize` call (for example, by an HTTP middleware that
populated a request-id key on the inbound request's context) are visible
on later tool invocations.

The framework cannot strip those values without a broader behavior change
to the SDK. The rule for application callbacks is therefore:

- Read `r.Header` first and treat that as authoritative.
- Fall back to ctx-derived values only when the header is absent.
- Don't derive a "current request id" by reading ctx first and writing it
  back to ctx on cache miss — that pattern silently propagates the
  initialize-time value forward.

A callback that always reads `r.Header` (as in the example above) is
already correct. A callback that wants compatibility with both
header-bearing and header-less callers should explicitly prefer the
header:

```go
RequestContext: func(ctx context.Context, r *http.Request) context.Context {
    rid := ""
    if r != nil {
        rid = r.Header.Get("X-Request-ID")
    }
    if rid == "" {
        rid = correlationIDFromCtx(ctx) // optional, header-absent fallback
    }
    if rid != "" {
        ctx = withCorrelationID(ctx, rid)
    }
    return ctx
}
```

## Elicitation

Generated SDK servers place an MCP elicitor in the context passed to tool,
resource, and prompt implementations. Service code can request client-side
form input with the runtime API:

```go
result, err := mcpruntime.Elicit(ctx, mcpruntime.ElicitRequest{
    Mode:    "form",
    Message: "Provide the missing summary.",
    RequestedSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "summary": map[string]any{"type": "string"},
        },
        "required": []any{"summary"},
    },
})
```

The generated server adapts that call to the official Go SDK's
`ServerSession.Elicit`, which sends `elicitation/create` to clients that
advertise the `elicitation` capability. If service code calls
`mcpruntime.Elicit` outside an MCP SDK request context, the runtime returns
`mcpruntime.ErrElicitorUnavailable`.

## Sampling

Generated SDK servers also place an MCP sampler in the context passed to tool,
resource, and prompt implementations. Service code can request text generation
from the connected client:

```go
result, err := mcpruntime.Sample(ctx, mcpruntime.SampleRequest{
    Messages: []mcpruntime.SampleMessage{
        {Role: "user", Text: "Summarize the deployment plan."},
    },
    SystemPrompt: "Answer concisely.",
    MaxTokens:    256,
})
```

The generated server adapts this transport-neutral contract to the official Go
SDK's `ServerSession.CreateMessage`, which sends `sampling/createMessage` to a
client that advertises the `sampling` capability. The current runtime contract
supports text messages and text results; image, audio, and sampling tool-use
content require a richer future contract. Calling `mcpruntime.Sample` outside
an MCP SDK request context returns `mcpruntime.ErrSamplerUnavailable`.

## Client Roots

Generated SDK request contexts can retrieve filesystem roots exposed by the
connected MCP client:

```go
roots, err := mcpruntime.ListRoots(ctx)
for _, root := range roots {
    fmt.Printf("%s (%s)\n", root.Name, root.URI)
}
```

The shared SDK adapter sends `roots/list` through the active
`ServerSession` and maps the response to transport-neutral `mcpruntime.Root`
values. Calling `ListRoots` outside an MCP SDK request context returns
`mcpruntime.ErrRootListerUnavailable`. Applications can observe client root
changes through `SDKServerOptions.Server.RootsListChangedHandler`; the official
SDK invokes it for `notifications/roots/list_changed` when the client advertises
`roots.listChanged`.

## Progress Notifications

For generated SDK tool calls, the server preserves the request's MCP
`progressToken` in context. Service code can report progress without handling
the session or token directly:

```go
err := mcpruntime.ReportProgress(ctx, mcpruntime.ProgressUpdate{
    Progress: 2,
    Total:    3,
    Message:  "Processing",
})
```

The shared SDK adapter sends `notifications/progress` through the active
session with the original string or numeric token. `ReportProgress` returns
`ErrProgressReporterUnavailable` outside an MCP SDK request and
`ErrProgressTokenUnavailable` when the caller did not request progress.
Applications are responsible for increasing `Progress` monotonically and
stopping notifications when the operation completes.

## Session and Response Writer Helpers

The generated handler also exposes the MCP session id and the active HTTP
response writer through `mcpruntime` helpers usable from tool
implementations:

```go
sessionID := mcpruntime.SessionIDFromContext(ctx)
w := mcpruntime.ResponseWriterFromContext(ctx)
```

`SessionIDFromContext` returns the value of the `Mcp-Session-Id` header
captured at request entry. `ResponseWriterFromContext` returns the active
writer for the streamable-HTTP request; it is non-nil only while the
generated handler is still composing the response.

### Session principal binding

Authentication middleware must wrap the generated handler so verified identity
is present in the request context before MCP session handling. Generated SDK
servers resolve the stable session owner with
`MCPAdapterOptions.SessionPrincipal`; when that callback is nil, they use
`mcpauth.TokenInfoFromContext(ctx).UserID`. Generated native JSON-RPC server
packages expose `MCPSessionPrincipal`, with the same TokenInfo default, for
applications that use a different verified principal source. Configure that
variable before mounting or serving the generated server.

When initialization returns a session ID, the transport binds that ID to the
resolved principal. Every subsequent POST, GET, and DELETE carrying the session
ID must resolve to the same principal. A mismatch, an absent principal for an
authenticated session, or an authenticated attempt to adopt a session issued
without a principal fails with HTTP 403. Unknown, expired, or terminated session
IDs remain HTTP 404; a required but missing session header remains HTTP 400.
A fresh `initialize` request must not carry `Mcp-Session-Id`; an unknown ID gets
HTTP 404 so the client restarts initialization without a session header. A
repeated initialize may carry its already-issued, owner-bound ID so the adapter
can return the protocol-level `Already initialized` error. Foreign IDs fail
ownership validation. This prevents caller-chosen IDs from consuming bounded
adapter session state before the transport issues them.

Session records are TTL-pruned after 24 hours and capped at 4096 entries.
Expiry or capacity eviction removes the principal binding with the session, so
a surviving upstream connection cannot fail open after its binding disappears.
DELETE validates ownership before termination; a rejected DELETE leaves the
rightful principal's session usable. Anonymous operation remains supported when
no principal resolver is configured and both initialization and later requests
resolve to an empty principal.

## Transport Observability

The generated handler emits classified request lifecycle events through the dependency-free
`github.com/CaliLuke/loom/observability/transport` contract from the
upstream Loom module for the streamable HTTP path.

Set `SDKServerOptions.TransportObserver` to receive these events directly.
Applications that need one observer across multiple handlers may instead wrap
the parent mux with `transport.HTTPMiddleware(observer)` and leave the option
unset. A missing observer is a cheap no-op. Generated code never
emits raw bodies, JSON-RPC params, MCP tool arguments, credentials, or
result payloads — events carry only low-cardinality classification fields
safe for metric labels and log enrichment. See the
`observability/transport` package documentation in the Loom module for
the complete `Reason` enumeration.

The observer integration is additive to the existing structured `adapter.log(...)`
calls; both channels remain present in generated SDK server output.

The generated `MCPAdapter` also exposes OpenTelemetry configuration through
`MCPAdapterOptions.TelemetryName`, `Tracer`, and `Meter`. Its stable metrics are:

| Metric | Unit | Meaning |
| --- | --- | --- |
| `loom_mcp.mcp.calls` | `{call}` | Total generated adapter calls |
| `loom_mcp.mcp.errors` | `{call}` | Calls that completed with an error |
| `loom_mcp.mcp.duration_ms` | `ms` | Generated adapter call duration |

Adapter spans and metrics describe MCP methods. `TransportObserver` describes
the lower-level HTTP request lifecycle; configuring one does not replace the
other.

## Cross-Origin Protection

When `SDKServerOptions.StreamableHTTP` is omitted, the generator wires
`net/http.NewCrossOriginProtection()` into the streamable HTTP handler.
Override `StreamableHTTP` to supply a custom protection policy. A nil
`CrossOriginProtection` inside non-nil options is replaced with the same safe
default; it is not a disable switch. Configure trusted origins on a custom
policy when cross-origin browser access is intentional.

## Module Dependency

`loom-mcp` consumes `observability/transport` from
`github.com/CaliLuke/loom` through the `replace github.com/CaliLuke/loom => ../loom`
directive in `go.mod`. Non-local releases that drop the replace must bump
`github.com/CaliLuke/loom` to a tag that contains the
`observability/transport` package — otherwise generated SDK server code
will not compile against the public Loom module.
