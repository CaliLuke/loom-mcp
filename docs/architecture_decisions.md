# Architecture decisions

This document records completed decisions that still constrain new work. It is
not a backlog; planned work belongs in [`ROADMAP.md`](../ROADMAP.md).

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
