# MCP SDK Server

This document describes the generated MCP SDK server: how to construct one,
the customization hooks exposed through `SDKServerOptions`, and the runtime
contract guarantees Loom-MCP makes for context propagation and per-call
request metadata.

## Migration from native MCP transport

Version `v2.1.0-alpha.5` removes Loom's native MCP JSON-RPC client and server.
It also removes `ProtocolVersion`, `Notification`, `Subscription`, and
`SubscriptionMonitor` from the MCP DSL.

To migrate:

1. Remove those DSL declarations and any MCP-only `JSONRPC` blocks.
2. Regenerate with `loom gen <module-import-path>/design`.
3. Construct only the generated `NewSDKServer` for remote MCP access.
4. Replace custom stream events with `runtime/mcp.ReportProgress`.
5. Replace broadcaster updates with `WatchableResource` and
   `SDKServer.ResourceUpdated`.
6. Remove imports of obsolete generated `gen/jsonrpc/mcp_*` packages.

Explicit non-MCP HTTP and JSON-RPC transports are unchanged. Application SSE
and WebSocket event buses are also unchanged.

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
        RequestStateKey: []byte(os.Getenv("MCP_REQUEST_STATE_KEY")),
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

`server.Handler` is a standard `http.Handler`. The official SDK owns the MCP
wire protocol, Streamable HTTP, and sessions.

## Response content negotiation

The generated wrapper checks the `Accept` header before each HTTP POST.
POST responses use SSE by default. Set `StreamableHTTP.JSONResponse` to `true`
to use a plain JSON response.

The handler returns HTTP 406 when the request does not accept the selected
response type. The handler does not send SSE framing with this error.

The MCP specification requires clients to send both response media types. The
wrapper also supports a client that sends only the selected media type. It adds
both media types only to the cloned request that enters the official SDK.
The wrapper does not modify the original request.

## Generated/runtime bridge boundary

Generated SDK servers and adapters are compact, typed bindings over
`runtime/mcp/sdkbridge`. Generated code supplies service descriptors and
closures. The bridge does not use reflection for dispatch.

| Generated for each service | Owned once by `sdkbridge` |
| --- | --- |
| Public service payload and result types | Official SDK registration loops |
| JSON schemas, descriptors, icons, and result conversion | Streamable HTTP defaults and cross-origin protection |
| Typed tool, prompt, and resource service closures | Tool interceptor composition and prompt or resource dispatch |
| Resource query codecs and prompt argument validation | Resource policy, skill dispatch, and common request logging |
| Dynamic prompt completion values and watchable resource URI set | Request context, sessions, subscriptions, CORS, and observation |

Application authentication and authorization stay outside the bridge. The
generated adapter supplies session-principal hooks after application
middleware has put a verified principal in the request context. The official
SDK remains the owner of MCP negotiation and wire behavior.

### Compatibility and consumer regeneration

For the checked-in assistant fixture, `gen/mcp_assistant/adapter_server.go`
decreased from the 2,818-line issue #276 baseline to 2,665 lines. Generated
code still owns typed codecs, service calls, and result conversion.

The runtime owns common tool interceptor composition. It also owns prompt and
resource dispatch, resource policy, skill dispatch, and request logging. Direct
runtime tests cover these contracts. Official-SDK integration tests cover the
generated closures and the complete server.

The current bridge compatibility version is `2`. The generator writes this
value as a literal in each generated server. The generated server never reads
the runtime constant during initialization. A release can include compatible
runtime corrections without another version increase.

Keep the version unchanged for compatible additions. Examples include internal
bug fixes, new optional fields, and new hooks with safe zero-value behavior.
A consumer can link these runtime changes without regeneration.

Increment `sdkbridge.CompatibilityVersion` when old generated descriptors or
callbacks are not safe with the new runtime contract. This includes these
changes:

- A required field is added to `sdkbridge.Config` or a binding type.
- A field or callback changes meaning, required behavior, or invocation order.
- The runtime removes behavior that old generated closures require.
- The runtime cannot safely interpret the old generated literal.

Do not increment the version for an additive runtime fix. Do not support two
contract versions with a compatibility path.

For an incompatible change, update the runtime constant first. The generator
then emits the new literal. You can repeat regeneration until you commit the
change. Commit the runtime version, the design, and generated files as one
change. Run these commands before release:

```bash
make regen-assistant-fixture
make regen-progressive-discovery-fixture
make regen-sdkbridge-consumer-fixture
make verify-generated
make verify-mcp-local
```

A stale generated server fails during construction. The error contains the
generated version and the runtime version.

The external fixture at `integration_tests/fixtures/sdkbridge_consumer` stays
outside `go.work`. Its commands use `GOWORK=off` and link the current runtime
through a module replacement. A real official SDK client calls its generated
server. This proves that same-version runtime fixes do not require consumer
regeneration.

`make verify-generated` regenerates current checked-in surfaces but leaves this
compatibility fixture frozen. CI compares the design and generated snapshot
with the pull request or push base. `make verify-mcp-local` links the frozen
server against the current runtime and runs its official SDK client test.

## JSON-RPC errors

The shared SDK bridge installs receiving middleware that normalizes handler
failures into typed JSON-RPC errors. Invalid parameters, invalid retry input,
missing resources, and duplicate initialization return `-32602`. Internal and
unknown handler failures return `-32603`. The middleware hides private details
unless the service declares an explicit safe message. Errors that use official
SDK types pass through unchanged.


The official MCP Go SDK performs its pre-initialization method gate before
server receiving middleware. In SDK v1.8.0-pre.2, that upstream-owned path
still serializes its untyped error with code `0`. The raw transport tests allow
only that known value or the expected future `-32602`, so an upstream correction
can land without weakening the generated adapter contract.

## SDKServerOptions

| Field            | Required | Description                                                                                                                                                            |
| ---------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PromptProvider` | yes      | Implementation that renders prompts declared with `StaticPrompt` and `DynamicPrompt`. Generated against the design.                                                    |
| `RequestContext` | no       | Hook for each JSON-RPC call and each non-POST transport request. Its returned context reaches generated handlers. |
| `RequestStateKey` | for elicitation | Stable 32-byte key used to AES-GCM encrypt and authenticate multi-round-trip `requestState`. All replicas serving an endpoint must share it. |
| `TransportObserver` | no    | Loom transport observer installed outside origin validation. It starts with the ingress context. Rejected origins and SDK requests emit request lifecycle events. |
| `RuntimeCORS` | no | Optional Loom runtime CORS response policy. It is separate from request origin validation. |
| `OriginProtection` | no | Bridge-owned `*sdkbridge.OriginProtection` settings. The settings contain trusted origins and an optional deny handler. The bridge creates the safe default when nil. |
| `StreamableHTTP` | no | Bridge-owned `*sdkbridge.StreamableHTTPOptions` containing supported official SDK transport settings. The type excludes the deprecated SDK origin field. |
| `Server` | no | `*mcpsdk.ServerOptions` passed to the official SDK. Loom installs its generated completion handler when required; the SDK infers registered capabilities. |

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
`NewSDKServer` runs. Add or remove skills before you construct a new server.
An active server keeps its registration snapshot.

## Optional tool arguments

MCP clients may omit `tools/call.arguments` or send JSON `null`. Generated
adapters normalize either form, as well as a whitespace-only value, to `{}`
before decoding a real tool payload.
Tools with all-optional payloads therefore execute normally, while tools with
required fields return the generated missing-field validation error and repair
hint.

Generated MCP service types use `loom.Nullable[any]` for optional arbitrary
JSON such as tool arguments, structured tool results, prompt arguments, and
resource metadata. The zero value means absent, `loom.NullValue[any]()` means
explicit JSON `null`, and `loom.NullableValue[any](value)` carries a concrete
value. SDK and dispatch boundaries preserve a contained `jsontext.Value`
without decoding it. The adapter marshals other concrete values only when a
raw JSON boundary requires them.

Loom streaming service methods remain valid tool and resource sources. The
adapter collects their results and returns one standard MCP result.

Use `mcpruntime.ReportProgress` for intermediate progress. The official SDK
sends `notifications/progress` with the client progress token.

## Request Context Callback

`RequestContext` propagates transport information into generated tool,
resource, and prompt handlers. This information can include authentication
tokens, correlation IDs, resource policies, and tenant IDs.

The shared SDK bridge calls the hook once for each JSON-RPC call. The hook
receives a synthetic POST request with the live headers from the official SDK
`RequestExtra.Header` value. The bridge also stores these headers through
`mcpruntime.WithRequestHeaders`. The returned context continues through the
official SDK middleware and reaches the generated handler.

For streamable GET and DELETE requests, the hook receives the real inbound
request. The transport observer starts before this hook. Thus, transport
observations use the ingress context, while application handlers use the
returned context.

Two regression tests define this contract:

- `TestRequestContextSeesPerCallHeaders` uses a real generated server. It makes
  sure that `tools/call` sees its own `X-Request-ID` value.
- `TestRequestContextMiddlewarePropagatesReturnedContextOnce` makes sure that
  the hook runs once and that the returned context reaches the SDK handler.

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

Treat inbound allow and deny headers as untrusted request input. An allow
header is not an authentication or grant mechanism. Derive grants from verified
credentials in application middleware. Use request values only to narrow a
trusted maximum grant.

`SDKServerOptions.Adapter.ToolSearch` passes through to the generated MCP
adapter. When set, SDK-backed servers use the same compact public discovery
surface as the local adapter: SDK `tools/list` registers synthetic
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

Rich projected tools use the following MCP contracts:

| Tool feature | MCP contract |
| --- | --- |
| `Inject(...)` | The application supplies `ToolCallMeta` from verified request context. Injected fields stay out of `inputSchema`. |
| `BoundedResult(...)` | The server returns canonical bounds in `structuredContent` and in the JSON text content. |
| `ResultReminder(...)` | The reminder stays in the agent runtime and does not enter MCP content. |
| `Confirmation(...)` | Design validation rejects the projection. Elicitation cannot replace authorization evidence. |
| `ServerData(...)` | Design validation rejects the projection. MCP metadata cannot guarantee model exclusion. |

Install injected values in `SDKServerOptions.RequestContext` after the
application verifies the request identity:

```go
RequestContext: func(ctx context.Context, r *http.Request) context.Context {
    meta := agentruntime.ToolCallMeta{SessionID: verifiedSessionID(r)}
    return mcpruntime.WithProjectedToolCallMeta(ctx, meta)
},
```

Do not copy unverified client headers into this metadata. A projected tool with
`Inject(...)` returns a tool error if the metadata is absent. These mappings
do not require an MCP client capability. Clients without structured-result
support can use the matching JSON text content.

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
`x-mcp-deny-names` values use the same matching rules. SDK applications must
derive trusted narrowing in their
`RequestContext` callback and install it with
`mcpruntime.WithAllowedResourceNames` and
`mcpruntime.WithDeniedResourceNames`; the SDK handler intentionally does not
treat raw client-chosen headers as authorization input. Server allow policies
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

## Resource Subscriptions

Declare a resource with `WatchableResource` to enable standard MCP
subscriptions. The generated server advertises `resources.subscribe: true`
during initialization, and it rejects unknown resource URIs.

Call `server.ResourceUpdated(ctx, uri)` after the resource changes. The SDK
sends `notifications/resources/updated` to subscribed clients.

Watchable resources require persistent Streamable HTTP sessions.
`NewSDKServer` rejects a stateless configuration when the design contains a
watchable resource.

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

The generated handler adapts that call to the official multi-round-trip request
contract. It returns an `InputRequiredResult` whose `inputRequests` map contains
`elicitation/create`. The client fulfills the request and retries the original
tool, prompt, or resource call with `inputResponses`; Loom then re-enters the
service implementation and returns the elicitation result at the same runtime
call site.

This means implementation code before `mcpruntime.Elicit` can run more than
once. Keep that prefix retry-safe and protect non-idempotent effects with stable
idempotency keys. Loom carries earlier responses between multi-step retries in a
bounded, versioned opaque `requestState`. Every elicitation round, including the
first, returns AES-GCM encrypted and authenticated state bound to the original
MCP method and logical parameters. Configure a stable 32-byte
`SDKServerOptions.RequestStateKey`; all replicas serving the endpoint must share
it so retries survive restarts and load balancing. Rotating the key invalidates
in-flight retries. Responses without valid server-issued state, modified state,
state replayed against another operation or payload, and response IDs or
elicitation contracts not issued in the pending round are rejected as protocol
errors rather than tool-level `isError` results.

The protected state contains client-supplied elicitation responses. Protection
prevents disclosure and undetected modification while the state is in transit,
but those responses remain client assertions and must never be used as
authorization evidence or server-owned state.

Each `mcpruntime.Elicit` call yields one input request, so N sequential
elicitations require N protocol round trips. Multi-step elicitation requires an
MCP `2026-07-28` client: the official SDK's legacy compatibility middleware
re-enters a handler only once, so older clients support a single elicitation
step only. Under the official Go SDK, `2026-07-28` streamable HTTP runs
stateless: each retry is a separate POST and does not carry
`Mcp-Session-Id`. Stateful streamable HTTP negotiates the legacy protocol
instead.

Loom accepts only `ElicitResult` responses for its elicitation request IDs,
requires an `accept`, `decline`, or `cancel` action, rejects content on
non-accept actions, and bounds both carried response count and encoded state
size. If service code calls `mcpruntime.Elicit` outside an MCP SDK request
context, the runtime returns `mcpruntime.ErrElicitorUnavailable`.

MCP `2026-07-28` deprecates sampling and roots. Loom does not expose the
deprecation-window `sampling/createMessage` or `roots/list` compatibility APIs;
servers should call model providers directly and accept paths through tool
parameters, resource URIs, or configuration.

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
`mcpauth.TokenInfoFromContext(ctx).UserID`. Configure the callback before
mounting the generated server when the application uses another stable
principal source.

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

The shared SDK bridge validates each present `Origin` header before MCP
processing. This validation applies to all HTTP methods, including the GET
connection for SSE. An invalid origin receives HTTP 403 Forbidden.

The bridge uses `net/http.NewCrossOriginProtection()` internally. Supply
`SDKServerOptions.OriginProtection` with `TrustedOrigins` when browser clients
use a different origin. `DenyHandler` optionally customizes rejected responses.
The bridge applies the policy to safe and unsafe HTTP methods before SDK handling.

The CORS policy and origin validation are separate. Configure
`SDKServerOptions.RuntimeCORS` when trusted browser clients require CORS
response headers.

## Module Dependency

`loom-mcp` pins `github.com/CaliLuke/loom v1.9.0-alpha.14`. This Loom release contains the canonical inline JSON Schema behavior and the dependency maintenance used by the SDK bridge.

Run `make loom-local` to use the sibling Loom checkout during development. Run `make loom-remote` before you commit or release changes.
