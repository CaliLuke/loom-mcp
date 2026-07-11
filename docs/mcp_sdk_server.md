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
| `StreamableHTTP` | no       | `*mcpsdk.StreamableHTTPOptions` passed through to the upstream SDK. When `nil`, a default `net/http.NewCrossOriginProtection()` is applied.                            |

## Design-Declared Skill Resources

Services that declare `SkillDirectory(root)` expose local agent skills through
the generated SDK server resource surface. The server scans the configured root
at startup, lists each child directory with a `SKILL.md` file as
`skill://<skill>/SKILL.md`, and also publishes `skill://<skill>/_manifest`.
`resources/read` can read `SKILL.md`, `_manifest`, and supporting files that
stay inside the skill directory.

`SKILL.md` may include structured YAML frontmatter (`id`, `name`,
`description`, `allowed_tools`, `preload`, and `reload`). Missing frontmatter is
compatible with older skills: the directory name becomes the ID and the first
heading or text line becomes the description. Duplicate IDs and invalid
metadata fail resource discovery.

## Optional tool arguments

MCP clients may omit `tools/call.arguments`. Generated adapters normalize an
omitted or whitespace-only value to `{}` before decoding a real tool payload.
Tools with all-optional payloads therefore execute normally, while tools with
required fields return the generated missing-field validation error and repair
hint.

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
        ctx = context.WithValue(ctx, allowNamesKey{}, allow)
    }
    return ctx
}
```

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

### Resource access policies include skills

`MCPAdapterOptions.AllowedResourceURIs`, `DeniedResourceURIs`,
`AllowedResourceNames`, and `DeniedResourceNames` apply to both DSL resources
and `skill://` resources. URI entries are exact unless they end in `/`; a
trailing slash authorizes or denies the full prefix, such as
`skill://code-review/`. A skill's main resource name also maps to that prefix,
so allowing or denying `code-review` covers `SKILL.md`, `_manifest`, and
supporting files. Request-scoped `x-mcp-allow-names` and
`x-mcp-deny-names` values use the same rules, with deny entries taking
precedence.

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

## Transport Observability

The generated handler emits classified events through the dependency-free
`github.com/CaliLuke/loom/observability/transport` contract from the
upstream Loom module: request start/finish/failure for the streamable HTTP
path, stream open/close/failure for the events stream, and
`mcp_session_missing`, `mcp_session_not_found`,
`mcp_session_principal_mismatch`, and `mcp_events_stream_write_failed`
reasons for the events-stream rejection branches.

Wire an observer at the HTTP layer with
`transport.HTTPMiddleware(observer)` to receive these events. Generated
constructor signatures stay unchanged regardless of whether an observer
is attached; a missing observer is a cheap no-op. Generated code never
emits raw bodies, JSON-RPC params, MCP tool arguments, credentials, or
result payloads — events carry only low-cardinality classification fields
safe for metric labels and log enrichment. See the
`observability/transport` package documentation in the Loom module for
the complete `Reason` enumeration.

The observer integration is additive to the existing structured `adapter.log(...)`
calls; both channels remain present in generated SDK server output.

## Cross-Origin Protection

When `SDKServerOptions.StreamableHTTP` is omitted, the generator wires
`net/http.NewCrossOriginProtection()` into the streamable HTTP handler.
Override `StreamableHTTP` to supply a custom protection policy or pass
`&mcp.StreamableHTTPOptions{CrossOriginProtection: nil}` to disable it
for trusted local-only deployments.

## Module Dependency

`loom-mcp` consumes `observability/transport` from
`github.com/CaliLuke/loom` through the `replace github.com/CaliLuke/loom => ../loom`
directive in `go.mod`. Non-local releases that drop the replace must bump
`github.com/CaliLuke/loom` to a tag that contains the
`observability/transport` package — otherwise generated SDK server code
will not compile against the public Loom module.
