# MCP Protected-Resource Discovery Design

## Goal

Make MCP protected-resource discovery batteries-included for downstream
Loom and loom-mcp applications while remaining strictly canonical to the MCP
Streamable HTTP spec and related OAuth discovery RFCs.

Downstream apps should not need to hand-wire well-known protected-resource
routes, manually derive `resource_metadata` URLs, or invent local aliases such
as `/mcp/.well-known/oauth-protected-resource`.

This feature is intentionally limited to protected-resource discovery. loom-mcp
should not become the authorization server and should not own issuer metadata
hosting.

## Problem

Today, loom-mcp owns MCP transport generation and mounting, but it does not own
the canonical protected-resource discovery surface for protected MCP endpoints.
That leaves downstream applications to manually add route glue for:

- protected-resource metadata
- `WWW-Authenticate` challenges with canonical `resource_metadata`
- consistent mapping from MCP path to public discovery URLs

That split is too low-level. The canonical routing and URL derivation rules are
protocol mechanics, not application business logic. When downstream apps own
them, they can easily introduce:

- non-canonical alias routes under `/mcp/.well-known/*`
- inconsistent protected-resource and issuer references
- staging and reverse-proxy breakage due to private vs public base URL drift
- challenges that point to the wrong protected-resource metadata URL

The result is deployment confusion and avoidable interoperability failures.

## Standards Ground Truth

For an MCP Streamable HTTP endpoint mounted at `/mcp`:

- The MCP endpoint is `/mcp`.
- Protected-resource discovery should use:
  - `WWW-Authenticate: Bearer resource_metadata="..."`
  - or RFC 9728 well-known protected-resource metadata endpoints
- Canonical path-qualified protected-resource metadata is:
  - `/.well-known/oauth-protected-resource/mcp`
- Canonical root protected-resource metadata is:
  - `/.well-known/oauth-protected-resource`
- Authorization server metadata discovery should use issuer-derived canonical
  RFC 8414 / OIDC endpoints such as:
  - `/.well-known/oauth-authorization-server`
  - `/.well-known/openid-configuration`
- Non-canonical `/mcp/.well-known/*` aliases should not be generated or
  encouraged.

For a subpath deployment such as `/api/mcp`, the canonical path-qualified
protected-resource metadata becomes:

- `/.well-known/oauth-protected-resource/api/mcp`

## Decision

### 1. Make loom-mcp own only the MCP protected-resource discovery surface

loom-mcp should own:

- canonical protected-resource metadata route generation
- canonical `resource_metadata` URL derivation
- `WWW-Authenticate` challenge construction for MCP HTTP endpoints
- startup validation and diagnostics for MCP protected-resource discovery configuration

This belongs in loom-mcp because it depends on MCP endpoint shape and is
already adjacent to current MCP transport codegen and mounting.

### 2. Do not make loom-mcp own authorization-server metadata

loom-mcp should not:

- host `/.well-known/oauth-authorization-server`
- host `/.well-known/openid-configuration`
- synthesize or mirror issuer metadata locally
- act as the authorization server

Instead, loom-mcp should reference an externally owned issuer in
protected-resource metadata and challenges. The application or external IdP
remains responsible for canonical issuer metadata.

### 3. Do not generate non-canonical compatibility aliases by default

loom-mcp must not generate:

- `/mcp/.well-known/oauth-protected-resource`
- `/mcp/.well-known/oauth-authorization-server`
- `/mcp/.well-known/openid-configuration`

If compatibility shims are ever needed for migration, they must be explicit
opt-ins and clearly deprecated.

## Ownership Model

### Loom core

Loom core should not own MCP-specific protected-resource discovery behavior.

Core may eventually host shared URL-normalization helpers if they become
transport-agnostic, but the initial feature should not be split across repos.
The MCP-specific route semantics and canonical path derivation belong in
loom-mcp.

### loom-mcp

loom-mcp should own:

- MCP protected-resource discovery DSL and expression model
- MCP protected-resource discovery codegen
- canonical route generation for protected-resource metadata
- runtime helpers for discovery document rendering
- runtime helpers for canonical challenge construction
- validation and startup diagnostics
- documentation and migration guidance

loom-mcp should not own:

- issuer metadata hosting
- token issuance
- authorization endpoint behavior
- JWKS hosting
- login or consent UX

### Application code

Application code should still own:

- real authentication and authorization enforcement
- token issuance
- login UX
- canonical issuer metadata hosting, if the app is the issuer
- JWKS hosting
- any non-MCP app routes
- compatibility aliases, if an app insists on them

The app should configure protected-resource discovery, not hand-build it.

## Proposed DSL And Config Model

The DSL should stay service-scoped because MCP is already declared on the
service via `MCP(...)`.

### Proposed DSL

```go
Service("assistant", func() {
    Description("Assistant MCP surface")

    HTTP(func() {
        Path("/mcp")
    })

    MCP("assistant-mcp", "1.0.0",
        ProtocolVersion("2025-06-18"),
        Discovery(func() {
            PublicBaseURL("https://api.example.com")
            ProtectedResourceMetadata()
            AuthorizationServerURL("https://auth.example.com")
        }),
    )
})
```

### Minimal downstream contract

The minimal explicit batteries-included contract should be:

- the MCP service path from the service HTTP design
- `PublicBaseURL(...)`
- `AuthorizationServerURL(...)`

The framework should infer everything else canonically.

### Proposed DSL functions

- `Discovery(fn func())`
- `PublicBaseURL(url string)`
- `ProtectedResourceMetadata()`
- `AuthorizationServerURL(url string)`
- `LegacyDiscoveryAliases()` optional, deprecated opt-in

### Expression model sketch

Add a `DiscoveryExpr` to `expr/mcp/mcp.go` and attach it to `MCPExpr`:

```go
type MCPExpr struct {
    // existing fields...
    Discovery *DiscoveryExpr
}

type DiscoveryExpr struct {
    eval.Expression

    PublicBaseURL string
    AuthorizationServerURL string
    ProtectedResourceEnabled bool
    LegacyAliases bool
}
```

That keeps the design narrow and honest. loom-mcp only needs enough
authorization-server URL information to advertise `authorization_servers` in
protected-resource metadata.

## Runtime Behavior

### 1. Canonical route generation

When an HTTP MCP server is enabled and protected-resource discovery is
configured, generated mount
helpers should automatically register:

- `/.well-known/oauth-protected-resource`
- `/.well-known/oauth-protected-resource/{mcpPath}`

Where `{mcpPath}` is the MCP HTTP path with no leading slash. Examples:

- `/mcp` -> `/.well-known/oauth-protected-resource/mcp`
- `/api/mcp` -> `/.well-known/oauth-protected-resource/api/mcp`

loom-mcp should not generate:

- `/.well-known/oauth-authorization-server`
- `/.well-known/openid-configuration`

Those remain external to this feature.

### 2. `WWW-Authenticate` behavior

When MCP auth enforcement rejects a request, the response should include:

```text
WWW-Authenticate: Bearer resource_metadata="https://public.example.com/.well-known/oauth-protected-resource/mcp"
```

The framework should derive this URL from:

- the declared public base URL
- the actual MCP mount path

It should not derive it from private bind addresses, request host headers alone,
or local route aliases.

### 3. Protected-resource metadata contents

Protected-resource metadata should include:

- the protected resource URL corresponding to the MCP endpoint
- `authorization_servers`

`authorization_servers` should contain the externally owned issuer declared in
the DSL.

All rendered URLs must be internally consistent with:

- `PublicBaseURL`
- the MCP endpoint path
- the configured authorization-server issuer

### 4. Root-path, subpath, and reverse-proxy handling

The framework should handle:

- root MCP path deployments such as `/mcp`
- nested MCP path deployments such as `/api/mcp`
- reverse-proxy deployments where the public origin differs from the local bind
  origin

Canonical URL rendering must always use `PublicBaseURL`.

The app should not need to stitch together:

- `X-Forwarded-*` parsing
- discovery URL rewriting
- duplicate route glue under proxy prefixes

### 5. Startup diagnostics

At startup, the generated/runtime-owned MCP setup should log the effective
protected-resource discovery contract:

- MCP endpoint public URL
- root protected-resource metadata URL
- path-qualified protected-resource metadata URL
- advertised authorization server URL
- whether legacy aliases are enabled

This should be a single concise structured log block so deployment mistakes are
visible immediately.

## Failure Prevention

The framework should fail fast for invalid or inconsistent protected-resource discovery
configuration.

### DSL validation

Validation errors should include:

- `Discovery(...)` used without an HTTP MCP path
- `ProtectedResourceMetadata()` enabled without `PublicBaseURL(...)`
- missing `AuthorizationServerURL(...)`
- malformed or non-absolute `PublicBaseURL`
- malformed or non-absolute authorization-server URL

### Startup validation

Generated/runtime startup should also check:

- the MCP mount path used at runtime matches the design-time route expectation
- canonical discovery URLs can be derived without ambiguity
- the configured authorization-server URL is absolute and consistent with the protected-resource
  metadata contract

If a runtime override breaks canonical discovery, startup should error instead
of silently serving a partial or contradictory contract.

### Alias prevention

By default, generated code must not serve `/mcp/.well-known/*`.

If the framework later adds `LegacyDiscoveryAliases()`, it should:

- be explicit in the DSL
- default to off
- emit startup warnings
- document a removal timeline

## Recommended Package, Codegen, And Runtime Changes

### DSL and expression changes

Update:

- `dsl/mcp.go`
- `expr/mcp/mcp.go`
- `expr/mcp/root.go`

Add protected-resource discovery DSL nodes and validation.

### Codegen changes

Extend:

- `codegen/mcp/generate.go`
- `codegen/mcp/templates/jsonrpc_server_mount.go.tpl`

Add generated protected-resource discovery mount helpers beside the existing
MCP transport mount.

The generated surface should:

- mount canonical protected-resource routes automatically
- never mount issuer metadata routes
- never mount non-canonical aliases unless explicitly configured

Codegen should evaluate path and route structure at generation time where
possible. The runtime should render documents from compact generated config, not
re-derive design structure dynamically.

### Runtime package changes

Add helpers under `runtime/mcp`, for example:

- `runtime/mcp/discovery.go`
- `runtime/mcp/discovery_test.go`

Responsibilities:

- canonical protected-resource path derivation
- public URL normalization and joining
- rendering protected-resource metadata documents
- constructing `WWW-Authenticate` bearer challenges
- formatting startup diagnostics

The runtime helper should be small and data-driven. Generated code should feed
it explicit config rather than requiring it to inspect service design at
runtime.

## Concrete Generated API Sketch

Generated server package additions could look like:

```go
type DiscoveryConfig struct {
    PublicBaseURL string

    ProtectedResourceEnabled bool
    ProtectedResourcePath string
    ProtectedResourceRootPath string

    AuthorizationServerURL string

    LegacyAliases bool
}
```

And mount helpers such as:

```go
func MountDiscovery(mux goahttp.Muxer, cfg DiscoveryConfig)
```

Or, more likely, fold protected-resource discovery mounting into the existing generated server mount
function so downstream code still calls one mount helper and gets the full
canonical surface automatically.

That is the preferred model.

## Test Plan

### 1. DSL and expression tests

Add tests for:

- valid protected-resource discovery config
- missing `PublicBaseURL`
- missing `AuthorizationServerIssuer`
- malformed issuer URLs
- malformed public base URLs

### 2. Codegen contract tests

Extend `codegen/mcp/contract_test.go` to assert generated server mounts contain:

- `/.well-known/oauth-protected-resource`
- `/.well-known/oauth-protected-resource/mcp`
- `/.well-known/oauth-protected-resource/api/mcp`

Add explicit negative assertions that default output does not contain:

- `/mcp/.well-known/oauth-protected-resource`
- `/mcp/.well-known/oauth-authorization-server`
- `/mcp/.well-known/openid-configuration`
- `/.well-known/oauth-authorization-server`
- `/.well-known/openid-configuration`

### 3. Runtime helper tests

Add table-driven tests for:

- root path MCP deployments
- nested path MCP deployments
- public base URL normalization
- `WWW-Authenticate` challenge rendering
- protected-resource document rendering

### 4. Integration tests

Add end-to-end HTTP tests covering:

- canonical route responses for `/mcp`
- canonical route responses for `/api/mcp`
- `WWW-Authenticate` on unauthorized MCP requests
- absence of `/mcp/.well-known/*` by default
- absence of generated issuer metadata routes
- optional compatibility aliases only when explicitly enabled

## Migration Plan

### Preferred migration: clean cutover

Existing downstream applications that manually wire well-known routes should:

1. add `Discovery(...)` to the MCP service DSL
2. set `PublicBaseURL(...)`
3. set `AuthorizationServerURL(...)`
4. regenerate code
5. delete manual protected-resource route glue
6. keep issuer metadata where it already lives
7. verify canonical routes and `WWW-Authenticate` behavior

This should be the default migration path.

### Temporary compatibility mode

If a downstream app must preserve legacy aliases for a short transition:

- it must explicitly opt in with `LegacyDiscoveryAliases()`
- the framework should log deprecation warnings at startup
- docs should state that aliases are temporary and non-canonical

No indefinite compatibility shim should be the default.

### Documentation updates

This feature requires updates to:

- `docs/dsl.md`
- `docs/runtime.md`
- `docs/overview.md`
- generated quickstart guidance if it mentions MCP HTTP mounting
- external published docs for this project that explain MCP deployment

The docs should explicitly show canonical protected-resource paths and
explicitly warn against `/mcp/.well-known/*` aliases.

The docs should also state clearly that loom-mcp does not host authorization
server metadata and that issuer metadata remains app- or IdP-owned.

## Adjacent Roadmap Item: Large-Catalog Tool Search

Another strong roadmap item is first-class MCP tool search for large catalogs.

This is a better near-term fit for loom-mcp than sandboxed code execution
because it improves tool discovery and token efficiency without introducing a
new code-execution trust model.

### Why it is useful

When a server exposes hundreds or thousands of tools, sending the entire tool
catalog to the model up front is expensive and often harms tool selection
quality. A search-oriented discovery layer lets the model discover tools on
demand instead of paying the full catalog cost immediately.

That is especially relevant for loom-mcp because:

- large exported tool surfaces are a natural outcome of design-first generation
- MCP clients and models do better with progressive disclosure than giant flat
  catalogs
- tool visibility already depends on auth and request-scoped filtering, so a
  discovery surface needs to respect runtime filtering anyway

### Recommended scope

The initial feature should be narrow:

- hide most tools from the default listing
- expose synthetic discovery tools instead
- support ranked search for relevant tools
- support schema lookup for selected tools
- keep normal tool invocation semantics intact

Recommended first version:

- `search_tools(query, limit?, tags?)`
- `get_tool_schema(tools, detail?)`

Possible later additions:

- optional `call_tool` proxy for constrained clients
- alternate search strategies such as regex
- tag browsing
- richer ranking or semantic search backends

### Recommended behavior

The search surface should:

- search across tool names, descriptions, parameter names, and parameter
  descriptions
- return compact results by default
- allow full schema detail on demand
- respect the same auth and visibility pipeline as normal `list_tools`
- avoid leaking hidden or admin-only tools through search

For loom-mcp, the preferred default should be ranked natural-language search,
not regex search. Query-oriented search is a better fit for how models ask for
tools.

### Ownership split

This feature fits naturally in loom-mcp's MCP runtime and server exposure
layer.

Recommended ownership:

- loom-mcp owns the MCP discovery-mode behavior for large catalogs
- runtime helpers own ranking and result rendering
- codegen may expose configuration hooks for generated MCP servers
- application code continues to own any custom visibility policy or tags

### Possible UX

Possible DSL or runtime-facing shapes:

```go
MCP("assistant-mcp", "1.0.0", func() {
    ToolDiscovery(func() {
        Search()
        SearchLimit(5)
        SchemaLookup()
    })
})
```

Or a runtime-only opt-in on generated MCP server wiring:

```go
server := assistantmcp.New(..., assistantmcp.Options{
    ToolDiscovery: assistantmcp.ToolDiscoverySearch,
})
```

The exact entrypoint can be decided later, but the feature should stay opt-in
and experimental at first.

### Suggested roadmap position

This should be tracked as a follow-on usability and scale feature after
canonical protected-resource discovery, and ahead of Code Mode.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. large-catalog tool search for MCP servers
4. standard MCP JSON config generation for client launch
5. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: Resources As Tools Compatibility Bridge

Another high-priority roadmap item is a first-class resources-as-tools bridge
for MCP clients that only support tools.

This is not a speculative ergonomics improvement. We already know there is real
downstream demand because some clients, including Claude in practice for parts
of our usage, did not support MCP resources directly. That forced downstream
projects to remodel resources as tools purely for client compatibility.

That is exactly the kind of compatibility shim the framework should own.

### Why it is useful

Some MCP clients can:

- list tools
- call tools

but cannot:

- list resources
- read resources
- use resource templates natively

When that happens, downstream teams end up duplicating the same workaround:

- convert read-only resources into pseudo-tools
- maintain parallel resource and tool surfaces
- document client-specific caveats

A framework-owned bridge would let applications keep the correct MCP resource
model while still serving tool-only clients.

### Recommended scope

The initial feature should expose resources through generated read-only tools.

Recommended first version:

- `list_resources`
- `read_resource`

Recommended behavior:

- list static resources and resource templates
- read a specific resource by URI
- match template URIs to the underlying resource template
- return binary content in a transport-safe encoded form
- mark generated tools as read-only where the transport supports those hints
- route through the normal auth, visibility, and middleware chain

### Ownership split

This belongs in loom-mcp, not in downstream application glue.

Recommended ownership:

- loom-mcp owns the compatibility transform or export mode
- runtime code owns the bridging behavior and URI/template matching
- codegen may expose opt-in configuration for generated MCP servers
- application code continues to own the actual resource implementations

### Design constraints

The bridge should:

- preserve resources as the source-of-truth model
- avoid forcing applications to redefine resources as tools
- respect auth and visibility exactly as native resource reads do
- stay explicit and opt-in rather than silently changing the public surface

The bridge should not:

- encourage abandoning MCP resources as a concept
- create divergent business logic between resource reads and tool calls
- require duplicated application handlers

### Possible UX

Possible DSL shape:

```go
MCP("assistant-mcp", "1.0.0", func() {
    Compatibility(func() {
        ResourcesAsTools()
    })
})
```

Possible generated/runtime shape:

```go
server := assistantmcp.New(..., assistantmcp.Options{
    ResourcesAsTools: true,
})
```

The exact entrypoint can be decided later. The important part is that the
bridge is framework-owned and opt-in.

### Suggested roadmap position

This should rank above large-catalog tool search because it addresses a known
client compatibility gap we already had to work around manually.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. prompts-as-tools compatibility bridge
4. large-catalog tool search for MCP servers
5. standard MCP JSON config generation for client launch
6. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: Prompts As Tools Compatibility Bridge

Another high-priority roadmap item is a first-class prompts-as-tools bridge for
MCP clients that only support tools.

This is the prompt-side counterpart to the resources-as-tools bridge. Some
clients can call tools but cannot list prompts or render prompts through the MCP
prompt protocol. In those environments, downstream teams end up re-expressing
prompts as ad hoc tools purely for compatibility.

That is another workaround the framework should absorb.

### Why it is useful

Some MCP clients can:

- list tools
- call tools

but cannot:

- list prompts
- get prompts
- render prompt templates natively

When that happens, downstream teams end up duplicating the same workaround:

- convert prompts into pseudo-tools
- maintain parallel prompt and tool surfaces
- lose a clean separation between prompts and executable tools

A framework-owned bridge would let applications keep prompts as prompts while
still serving tool-only clients.

### Recommended scope

The initial feature should expose prompts through generated tools.

Recommended first version:

- `list_prompts`
- `get_prompt`

Recommended behavior:

- list available prompts and their arguments
- render a specific prompt by name with provided arguments
- return prompt output in a transport-safe structured message format
- route through the normal auth, visibility, and middleware chain

### Ownership split

This belongs in loom-mcp, not in downstream application glue.

Recommended ownership:

- loom-mcp owns the compatibility transform or export mode
- runtime code owns prompt listing and rendering through the bridge
- codegen may expose opt-in configuration for generated MCP servers
- application code continues to own the actual prompt implementations

### Design constraints

The bridge should:

- preserve prompts as the source-of-truth model
- avoid forcing applications to redefine prompts as tools
- respect auth and visibility exactly as native prompt retrieval does
- stay explicit and opt-in rather than silently changing the public surface

The bridge should not:

- encourage collapsing prompts into executable tools by default
- create divergent business logic between prompt retrieval and tool invocation
- require duplicated application handlers

### Possible UX

Possible DSL shape:

```go
MCP("assistant-mcp", "1.0.0", func() {
    Compatibility(func() {
        PromptsAsTools()
    })
})
```

Possible generated/runtime shape:

```go
server := assistantmcp.New(..., assistantmcp.Options{
    PromptsAsTools: true,
})
```

The exact entrypoint can be decided later. The important part is that the
bridge is framework-owned and opt-in.

### Suggested roadmap position

This should rank alongside resources-as-tools as a client compatibility bridge
we should own at the framework level.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. prompts-as-tools compatibility bridge
4. mature OpenTelemetry and observability
5. large-catalog tool search for MCP servers
6. standard MCP JSON config generation for client launch
7. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: Mature OpenTelemetry And Observability

Another roadmap item is making loom-mcp observability more mature and batteries
included.

This is not a greenfield feature. loom-mcp already has a meaningful telemetry
surface today:

- runtime logger, metrics, and tracer interfaces
- tracer injection through `runtime.WithTracer(...)`
- spans and events in agent runtime and tool-registry execution paths
- trace-context propagation helpers in the tool-registry layer
- runtime documentation describing tracing hooks and tracer interfaces

That means the opportunity is not "add tracing." The opportunity is to make the
current tracing story more complete, more consistent, and easier to adopt.
The preferred direction is to do that through the cleaner middleware and
interceptor model, rather than through more ad hoc point integrations.

### Why it is useful

For distributed agent systems, tracing is not optional once deployments become
real. Teams need to understand:

- which planner decision led to which tool calls
- where time is spent across planning, tool execution, retries, and streaming
- how child runs relate to parent runs
- how external MCP calls and delegated tool execution fit into a single trace
- where policy denials, retries, cancellations, and transport failures occur

We already have pieces of this, but the product story can be made much more
complete and turnkey.

### Recommended scope

The roadmap item should focus on making observability feel first-class.

Recommended areas:

- standardize span naming and attributes across runtime, MCP server, MCP caller,
  tool registry, and child-run execution
- define a stable attribute taxonomy for agents, runs, sessions, tools,
  toolsets, planners, MCP methods, and delegated execution
- improve end-to-end trace propagation across local and remote MCP boundaries
- document recommended OTEL setup for local development and production
- add batteries-included defaults and helpers for common OTEL setups
- add stronger test coverage for tracing behavior

This work should compose directly with the interceptor model:

- interceptors should be the primary place where request-scoped tracing,
  metrics, and structured logging are attached
- runtime internals should still emit spans for deeper execution boundaries
- the public observability story should feel unified rather than split across
  unrelated extension points

### What “batteries included” should mean here

The framework should still let applications bring their own exporter and SDK,
but it should do more out of the box in terms of conventions and setup.

Reasonable goals:

- a documented default tracer wiring path
- standard span names for core runtime operations
- standard attributes for run IDs, session IDs, agent IDs, tool IDs, and MCP
  operations
- built-in trace propagation through MCP caller and server layers
- startup guidance and examples for OTLP backends
- test helpers or in-memory tracer fixtures for repo and downstream tests
- built-in observability interceptors for common patterns such as request
  logging, timing, and tracing

### Recommended maturity targets

Areas that would make the existing telemetry story feel more mature:

- consistent parent-child relationships across planner, tool, child-run, and
  stream spans
- clearer tracing for agent-as-tool execution and linked child runs
- tracing coverage for MCP server operations, not just internal agent runtime
- better visibility into retries, policy denials, cancellations, and fallback
  behavior
- a documented semconv-like schema for loom-mcp-specific attributes
- examples that show local development and production OTEL wiring without custom
  spelunking

### Ownership split

This feature belongs primarily in loom-mcp.

Recommended ownership:

- loom-mcp runtime owns span boundaries and tracing contracts
- loom-mcp MCP runtime owns MCP-specific tracing and propagation
- loom-mcp interceptor/middleware surface owns the primary request-level
  observability integration points
- docs own the recommended setup story and examples
- application code continues to own exporter choice, backend choice, and any
  custom spans

### Suggested roadmap position

This should sit above tool-search and code-execution work because it improves
operability of the framework we already have, rather than adding a new advanced
mode.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. prompts-as-tools compatibility bridge
4. mature OpenTelemetry and observability
5. SEP-1686 MCP background task protocol support
6. large-catalog tool search for MCP servers
7. standard MCP JSON config generation for client launch
8. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: SEP-1686 MCP Background Task Protocol Support

Another worthwhile roadmap item is native support for the MCP background task
protocol defined by SEP-1686.

This should be framed carefully. loom-mcp already has much of the underlying
execution machinery:

- durable execution through the runtime and engine layers
- long-running workflows and activities
- worker/process separation
- progress and event streaming
- resumable runs

So the opportunity is not to invent a background task system from scratch. The
opportunity is to expose the existing capability through the actual MCP task
protocol in a batteries-included way.

### Why it is useful

In MCP, blocking request/response interactions are a poor fit for operations
that take seconds, minutes, or longer.

Protocol-native task support would let MCP clients:

- start work and receive a task ID immediately
- track status and progress using MCP-standard task flows
- retrieve results when work is finished

That is better than ad hoc "job started" tool responses because it is:

- standardized
- client-visible in a consistent way
- easier to support across clients and server implementations

### Recommended scope

The feature should implement the actual SEP-1686 task protocol, not a custom
approximation.

Recommended first version:

- MCP task protocol support for long-running tool operations first
- task creation, polling/status, and result retrieval using SEP-1686 semantics
- progress reporting bridged from existing runtime events and stream machinery
- durable behavior when the runtime uses Temporal
- best-effort local behavior when using the in-memory engine

Possible later extensions:

- task support for resources
- task support for prompts
- richer retry and cancellation semantics at the MCP layer

### Design direction

The implementation should sit on top of loom-mcp's existing runtime and engine
primitives, not beside them.

Recommended approach:

- expose SEP-1686 at the MCP server layer
- map MCP task lifecycle onto existing loom-mcp execution and progress events
- avoid introducing a second task scheduler abstraction inside the repo

That keeps the protocol surface honest while preserving a single execution
model underneath.

### Ownership split

This belongs in loom-mcp.

Recommended ownership:

- loom-mcp MCP runtime owns SEP-1686 protocol exposure
- loom-mcp runtime/engine layers provide the underlying execution semantics
- codegen may expose opt-in configuration for generated MCP servers
- application code continues to own which operations are suitable for task mode

### Suggested roadmap position

This should sit above tool search and JSON config generation because it exposes
a substantial protocol capability on top of execution machinery we already
have.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. prompts-as-tools compatibility bridge
4. mature OpenTelemetry and observability
5. mature middleware and interceptor model
6. SEP-1686 MCP background task protocol support
7. large-catalog tool search for MCP servers
8. standard MCP JSON config generation for client launch
9. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: Mature Middleware And Interceptor Model

Another roadmap item is a cleaner, more batteries-included middleware or
interceptor model for cross-cutting server behavior.

This should not be framed as inventing middleware from scratch. loom-mcp
already has important pieces today:

- runtime hooks and event buses
- policy enforcement surfaces
- logger, metrics, and tracer injection
- MCP runtime composition points
- request and execution context propagation

The opportunity is to productize those pieces into a clearer author-facing
model, not to add yet another overlapping abstraction with unclear semantics.

### Why it is useful

Downstream users routinely need the same cross-cutting behaviors:

- authentication and authorization
- request logging and timing
- rate limiting
- response shaping or truncation
- caching
- request validation and transformation

Today, loom-mcp has enough primitives to support much of this, but the surface
is more piecemeal than it should be. A mature interceptor model would make the
framework easier to reason about and easier to adopt.

### Recommended framing

The right goal is:

- unify hooks, policy, and MCP request interception into a coherent model

not:

- add a completely separate middleware system that duplicates existing
  capabilities

### Recommended scope

The feature should provide a single request/response interception model with
clear composition semantics.

Recommended areas:

- request enters interceptor chain
- interceptor can inspect or modify context
- interceptor can allow, deny, or short-circuit
- interceptor can observe or modify the response
- interceptor can catch and transform errors

Recommended scopes:

- all MCP messages
- tool calls
- resource reads
- prompt gets
- list operations

Potential later scope:

- agent runtime execution interception beyond the raw MCP surface

### Batteries-included surface

The maturity win is not only the abstraction, but also built-in batteries on
top of it.

Strong candidates:

- logging
- timing
- auth helpers
- rate limiting
- response limiting
- maybe caching, with explicit warnings around auth-sensitive outputs

### Design constraints

The model should:

- reduce conceptual sprawl rather than increase it
- compose cleanly with existing runtime hooks and policy layers
- define ordering semantics explicitly
- work predictably with mounted or composed MCP surfaces
- make request and response context handling consistent

The model should not:

- create a second competing extension system
- duplicate business logic across hooks, policy, and middleware layers
- leave ambiguous which abstraction owns auth or request denial behavior

### Ownership split

This belongs primarily in loom-mcp.

Recommended ownership:

- loom-mcp runtime owns the interceptor contract
- loom-mcp MCP runtime owns MCP-operation-specific interception
- existing hooks and policy layers are adapted underneath the cleaner surface
- application code supplies custom interceptors and configuration

### Suggested roadmap position

This is a maturity track, not just a feature add. It belongs near observability
because both are about making the framework operationally coherent and easier to
use in production.

Recommended ordering:

1. canonical protected-resource discovery for HTTP MCP
2. resources-as-tools compatibility bridge
3. prompts-as-tools compatibility bridge
4. mature OpenTelemetry and observability
5. mature middleware and interceptor model
6. SEP-1686 MCP background task protocol support
7. large-catalog tool search for MCP servers
8. standard MCP JSON config generation for client launch
9. experimental sandboxed code-execution mode

## Adjacent Roadmap Item: MCP Client Configuration Export

An adjacent roadmap item worth adding is first-class generation of standard MCP
JSON client configuration for stdio-launched servers.

This is separate from protected-resource discovery:

- protected-resource discovery helps clients discover and authenticate to an
  HTTP MCP endpoint
- MCP JSON export helps users install and launch local or CLI-backed MCP
  servers in MCP-compatible clients

Both features improve downstream usability, but they solve different problems
and should be tracked separately.

### Why it is useful

The de facto `mcpServers` JSON format is now widely used by:

- Claude Desktop
- Cursor
- VS Code
- other MCP-compatible tools

Downstream teams repeatedly need to produce the same JSON shape:

```json
{
  "mcpServers": {
    "server-name": {
      "command": "executable",
      "args": ["arg1", "arg2"],
      "env": {
        "VAR": "value"
      }
    }
  }
}
```

That makes it a good framework or tooling feature because:

- the format is repetitive
- command and argument construction are easy to get wrong
- absolute-path handling matters
- env var encoding needs to be consistent
- teams want copy-pasteable output for multiple clients

### Recommended scope

The loom or loom-mcp roadmap item should focus on exporting client
configuration, not installing it into every editor automatically.

Recommended initial scope:

- generate a standard `mcpServers` JSON object for a server
- support stdio-launched MCP servers first
- emit absolute paths where needed
- include command, args, and env
- support output to stdout for piping
- support writing to a file as an explicit option

Nice-to-have later:

- client-specific wrappers for Claude Desktop, Cursor, and VS Code
- clipboard integration
- project-aware defaults
- merge helpers for existing client config files

### Ownership split

This feature fits better as a Loom CLI or generator/tooling feature than as MCP
runtime behavior.

Recommended ownership:

- Loom core or the Loom CLI owns config export commands
- loom-mcp can provide generated metadata or helpers when the server comes from
  loom-mcp codegen
- application code supplies any app-specific env vars or wrapper command
  overrides

That split keeps runtime concerns separate from developer tooling.

### Possible UX

Possible command shapes:

```bash
loom mcp config stdio ./cmd/server
loom mcp config stdio ./cmd/server --name "My Server"
loom mcp config stdio ./cmd/server --env API_KEY=...
loom mcp config stdio ./cmd/server --output .vscode/mcp.json
```

Or, if the config is generated from design/codegen metadata:

```bash
loom gen-mcp-config <module-import-path>/design
```

The output should be a ready-to-paste `mcpServers` entry or full JSON object.

### Suggested roadmap position

This should be tracked as a follow-on usability feature after canonical
protected-resource discovery lands.

It is valuable, but it is orthogonal to the current protocol-correctness work:

- current feature: canonical protected-resource discovery for HTTP MCP
- follow-on feature: standard MCP JSON config generation for client launch

## Tradeoffs And Non-Goals

### Tradeoffs

- This increases the MCP DSL surface slightly, but it removes a more dangerous
  amount of downstream handwritten route glue.
- The framework can guarantee canonical routes for generated mounts, but it
  cannot stop an app from manually adding arbitrary extra routes to its own mux.
- Requiring an explicit issuer URL is slightly more configuration, but it keeps
  the ownership boundary honest and avoids fake framework-owned auth surfaces.

### Non-goals

This feature should not attempt to:

- implement OAuth authorization flows
- issue tokens
- host issuer metadata
- replace a real identity provider
- infer public origins from request headers without explicit public-base-url
  configuration
- normalize non-canonical aliases into accepted framework defaults

## Recommended Final Shape

The recommended framework contract is:

- loom-mcp owns canonical MCP protected-resource discovery
- loom-mcp owns canonical MCP `WWW-Authenticate` `resource_metadata` challenges
- loom-mcp references, but does not host, the authorization server issuer
- canonical protected-resource routes are generated automatically
- issuer metadata remains outside loom-mcp
- non-canonical `/mcp/.well-known/*` aliases are never generated by default
- configuration is explicit, validated, startup-visible, and designed for clean
  reverse-proxy deployment

That gives downstream users a batteries-included MCP protected-resource
discovery surface while keeping auth ownership where it belongs.
