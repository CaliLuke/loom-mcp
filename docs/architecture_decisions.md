# Architecture decisions

This document records completed decisions that still constrain new work. It is
not a backlog; planned work belongs in [`ROADMAP.md`](../ROADMAP.md).

## The official SDK is the only MCP wire transport

Decision date: 2026-08-15. This decision resolves
[issue #274](https://github.com/CaliLuke/loom-mcp/issues/274).

### Problem

Loom generated two MCP wire transports for each MCP service. One transport used
the official MCP Go SDK. The other transport used generated Loom JSON-RPC code.

The two transports owned the same protocol rules. These rules included JSON-RPC
messages, lifecycle state, sessions, cancellation, and Streamable HTTP.

The native transport did not provide a required capability for a production
consumer. It also stopped at protocol version `2025-11-25` while SDK v1.7.0
added `2026-07-28`.

### Consumer inventory

The issue and the repository history identify one external consumer. They do
not identify an external consumer of the generated native client or server.

| Consumer | Surface before the cutover | Production use | SDK migration |
| --- | --- | --- | --- |
| Auto-K | Generated `NewSDKServer` | Yes | No migration was necessary. Auto-K already mounted the SDK handler. |
| Assistant fixture orchestrator | Generated native server and supporting Goa JSON-RPC server | No. This was repository test and example code. | The fixture now mounts only `NewSDKServer`. |
| Assistant fixture CLI and native transport tests | Generated native client and client adapter | No. These were repository test and scaffold code. | Official SDK client tests now call the generated SDK server. |
| Progressive-discovery fixture | Generated native client and server | No. This was repository acceptance code. | The SDK server and in-process `ToolsetRegistration` cover the two supported access paths. |
| All other MCP designs | Native packages were generated even without a caller | No known consumer | Generation now omits those packages. |

The callers in `runtime/mcp` consume external MCP servers. They did not depend
on the removed generated client.

### Capability matrix

“Native” describes the generated Loom transport immediately before its removal.
“SDK” describes the generated server with MCP Go SDK v1.7.0.

| Capability | Native transport | SDK-backed transport | Classification and requirement |
| --- | --- | --- | --- |
| Protocol versions | Supported legacy versions through `2025-11-25`. The design selected a default. | Supports `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`, and `2024-11-05`. | Protocol requirement. No consumer required the design-selected native default. |
| Stateless Streamable HTTP | No explicit stateless contract. The generated wrapper always created native session state. | Supported through `StreamableHTTPOptions.Stateless`. Watchable resources reject stateless mode. | Current-version protocol requirement. The native gap is obsolete, but subscriptions cannot use `2026-07-28`. |
| Stateful Streamable HTTP | Used Loom session, SSE, and cancellation registries. | The official SDK owns legacy sessions, POST, GET, DELETE, and SSE. | Protocol requirement for legacy versions. The SDK replaces the native code. |
| Initialization and notifications | Generated handlers implemented the legacy handshake and a fixed notification allowlist. | The SDK owns modern per-request metadata and the legacy handshake. | Protocol requirement. Issues #236 and #249 showed native lifecycle drift. |
| JSON-RPC batches | Used a generated batch writer and custom SSE buffering. | Accepts batches for versions through `2025-03-26`. Rejects them from `2025-06-18`. | Legacy protocol requirement. Issue #210 showed that the native writer corrupted tool-call batches. |
| Progress | Used generated notification routing and stream framing. | `runtime/mcp.ReportProgress` uses the SDK request session and progress token. | Protocol requirement. The SDK supplies the wire behavior. |
| Cancellation | Used `runtime/mcp.RequestCancellationRegistry` and generated notification inspection. | The SDK maps protocol cancellation to request contexts. | Protocol requirement. The native registry had no independent consumer. |
| Elicitation and request state | Did not expose a complete typed server-to-client flow. | The SDK owns elicitation. Loom protects bounded multi-round-trip request state for `2026-07-28`. | Protocol requirement. The SDK path has more capability. |
| Tools, resources, prompts, and completions | Routed generated service methods through the shared `MCPAdapter`. | Routes the same adapter through typed SDK registrations. | Active consumer requirement. No native wire code is necessary. |
| Skills | Exposed Loom skills as MCP resources through generated adapter code. | Exposes the same `skill://` resources through SDK resource registration. | Deliberate Loom projection. It is not a separate wire protocol. |
| Resource subscriptions and list changes | Used generated subscription methods, a session store, and a broadcaster. | Uses SDK subscribe handlers, list-change notifications, and `SDKServer.ResourceUpdated` in stateful mode. | Legacy protocol requirement. Watchable resources cannot use stateless `2026-07-28`. |
| Compact tool discovery | Supported `search_tools`, `call_tool`, visible pins, and direct hidden calls. | Supports discovery, dispatch, and pins. Direct hidden calls are rejected unconditionally. | Deliberate Loom extension. No consumer required direct hidden calls. |
| Session and principal binding | Used Loom session maps around the native server. | Loom binds verified principals around SDK sessions on POST, GET, and DELETE. | Application security requirement. Loom keeps the policy wrapper, not the protocol engine. |
| Request-context propagation | Used generated HTTP policy wrappers. | Uses `SDKServerOptions.RequestContext` and per-call SDK headers. | Active consumer requirement. The SDK exposes the required extension point. |
| OAuth integration | Shared generated protected-resource metadata with application-owned authentication. | Keeps that metadata and application boundary around the SDK handler. | Optional protocol integration. Native JSON-RPC behavior added no value. |
| Transport observation | Observed the custom native HTTP and JSON-RPC path. | `SDKServerOptions.TransportObserver` observes the generated SDK handler. | Deliberate Loom integration. It remains outside the protocol engine. |
| `events/stream` | Added a nonstandard method and broadcaster beside standard Streamable HTTP. | Not present. Standard progress and resource notifications use SDK sessions. | Obsolete Loom extension. No active consumer required it. |

### Native ownership cost

The cutover removed these native-only owners:

- The MCP DSL entries `ProtocolVersion`, `Notification`, `Subscription`,
  `SubscriptionMonitor`, and MCP-only `JSONRPC` blocks.
- The code generators `client_adapter_file.go`, `client_caller_file.go`,
  `handler_init_jennifer.go`, `jsonrpc_batch_handler_section.go`,
  `structured_sse_sections.go`, and `upstream_extensions.go`.
- Native sections in `generate.go`, `mcp_methods.go`, `mcp_types.go`, and
  the adapter generators.
- The runtime broadcaster, notification, request-cancellation, and Streamable
  HTTP session packages.
- Generated `gen/jsonrpc/mcp_*` clients and servers, client adapters,
  endpoints, protocol-version files, stream files, and native CLI scaffolds.
- Native transport scenarios and tests for batches, SSE, sessions, client
  bootstrap, and generated JSON-RPC dispatch.

The cutover commit changed 155 files in the main codegen, expression, DSL, and
fixture scopes. That diff removed 40,918 lines. Of those lines, 8,544 came from
`codegen/mcp` and 31,700 came from the assistant fixture. Generated files
accounted for much of the fixture total.

### Defect ownership

| Issue | Owner before the cutover | Result of SDK-only generation |
| --- | --- | --- |
| #201: null tool arguments | Shared adapter payload boundary | This contract remained. The adapter now normalizes absent and null arguments to `{}`. |
| #209: omitted empty results | Native JSON-RPC response encoder | The owner was removed. The SDK now encodes protocol results. |
| #210: corrupted batch output | Native batch writer and tool-call SSE stream | The owners were removed. The SDK applies version-specific batch rules. |
| #235: missing Origin validation on GET | Native HTTP policy wrapper | The owner was removed. The SDK now owns transport Origin checks. |
| #236: missing `notifications/initialized` | Generated native client bootstrap | The client was removed. Official clients own the lifecycle. |
| #237: dead resource subscriptions | Native capability types, counters, and broadcaster | The owners were removed. SDK handlers and `ResourceUpdated` provide the stateful path. |
| #249: responses to unknown notifications | Native HTTP wrapper and JSON-RPC dispatcher | The owners were removed. The SDK owns notification semantics. |
| #250: repeated SSE retry frames | Native reconnect-hint writer | The owner was removed. The SDK owns SSE framing. |
| #261: swallowed `events/stream` errors | Native GET handler and stream opener | The owners were removed with `events/stream`. |
| #265: wrong negotiated version header | Generated native client wrappers | The clients were removed. The SDK owns version state. |
| #266: ignored JSON `Accept` value | Native tool-call stream handler | The handler was removed. The SDK owns content negotiation. |
| #267: accepted `id: null` | Native envelope parser and dispatcher | The owners were removed. The SDK owns JSON-RPC validation. |

Eleven listed defects had native wire owners that the cutover removed. Issue
#201 crossed the adapter boundary, so the SDK cutover kept that contract.

### Decision

Loom uses outcome A: SDK-only. The official MCP Go SDK is the only MCP wire
transport for generated servers.

Loom owns these layers:

- the MCP DSL and evaluated design.
- generated service types and the `MCPAdapter`.
- SDK type conversion and service registration.
- authorization policy, principal binding, and request-context hooks.
- protected-resource discovery, transport observation, and agent integration.

The official SDK owns these layers:

- JSON-RPC envelopes, results, errors, and notifications.
- protocol version behavior and lifecycle state.
- Streamable HTTP, standard SSE, sessions, batches, and cancellation.

Outcome B keeps a compatibility surface without a consumer. Outcome C keeps
the same protocol rules in two implementations. The inventory does not justify
either cost.

### Completed implementation and migration

Commit `364f745` completed the clean cutover and release
`v2.1.0-alpha.5` published it.

The implementation removed native generated APIs and migrated all repository
callers. It also regenerated the fixtures and removed obsolete generated files.

The migration guide is in [`mcp_sdk_server.md`](mcp_sdk_server.md). It covers
the DSL changes, `NewSDKServer`, progress, resource updates, and obsolete
imports.

`TestGenerateTransportConformance` is the generated-code regression. It makes
sure that generation emits `sdk_server.go` and omits every native transport
artifact. Official client tests cover the generated server behavior.

Future MCP protocol work starts with an official client against a generated
fixture. Loom adds SDK extension points only for a verified consumer need.

## Runtime and Temporal activity boundaries

Decision date: 2026-03-14.

### Problem

Queue wait and heartbeat liveness are workflow-engine mechanics. Exposing them
through generic runtime worker configuration makes the in-memory engine look
like an incomplete Temporal implementation.

### Decision

- The public runtime owns semantic run, planner and tool-attempt budgets.
- `runtime.WorkerConfig` owns queue placement, not engine activity options.
- The Temporal adapter owns schedule-to-start and heartbeat-liveness tuning.
- Runtime planner and tool code emits cooperative heartbeats through the
  engine-neutral helper. The engine decides what those signals mean.
- The in-memory engine applies semantic budgets and ignores Temporal-only
  liveness mechanics.

### Consequences

Applications configure Temporal mechanics where they construct the Temporal
engine. Runtime documentation and tests distinguish semantic time budgets from
adapter deployment tuning.

## Code generation uses partial evaluation

Decision date: 2026-03-16.

### Problem

Generated code used to rediscover design-known structure at runtime through
loops, generic maps, JSON round trips and lazy initialization.

### Decision

- Generator data and templates own every structural decision known from the
  evaluated design.
- Generated runtime code handles only payload values, responses, streams and
  execution errors.
- Generated MCP caller validation emits direct checks for known bindings.
- MCP resource query construction uses precomputed field order, query names,
  access paths and scalar/repeated shapes.
- Registry hint maps are omitted when empty and emitted as direct literals when
  present.
- Generators use stable upstream sections and `NameScope` helpers. They do not
  inspect or rewrite rendered Go source.

### Consequences

Generator data can be richer, but generated behavior remains direct and
reviewable. Tests must prove executable generated contracts; source goldens
alone are insufficient.

## MCP protected-resource discovery ownership

Decision date: 2026-03-30; updated to the implemented contract.

### Problem

Applications should not hand-build RFC 9728 routes, protected-resource metadata
or `WWW-Authenticate` resource pointers for generated MCP endpoints. They also
must not confuse that protocol glue with authorization-server ownership.

### Decision

- `loom-mcp` owns MCP protected-resource metadata, canonical URL derivation,
  Bearer challenge helpers, validation and generated mounting.
- The canonical routes are `/.well-known/oauth-protected-resource` and the
  path-qualified form such as `/.well-known/oauth-protected-resource/mcp`.
- `loom-mcp` references authorization servers but does not issue tokens, host
  authorization-server or OpenID Provider metadata, host JWKS, or provide login
  and consent flows.
- Authentication middleware and token verification remain application-owned.
- Forwarded headers are untrusted by default. Applications opt in only when a
  controlled proxy supplies them.
- Generated servers do not add non-canonical `/mcp/.well-known/*` aliases.
- The official MCP Go SDK remains the wire-protocol owner.

### Consequences

Generated endpoints provide canonical discovery without duplicating downstream
route glue. Applications retain identity-provider policy and deployment
credentials. OAuth follow-up work remains in [`ROADMAP.md`](../ROADMAP.md).
