# loom-mcp Quickstart

Use the repository's `quickstart/README.md` as the canonical runnable guide.
This reference is intentionally short so generated paths and APIs do not drift
in two places.

## Prerequisites

- Go 1.27 or later.
- Loom CLI pinned to the repository's supported release:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.7.1
loom version
```

Temporal is optional. Generated examples use the in-memory engine until the
application explicitly configures `runtime/agent/engine/temporal`.

## Project flow

1. Initialize a Go module and add `github.com/CaliLuke/loom` plus
   `github.com/CaliLuke/loom-mcp/v2`.
2. Author the Goa service and loom-mcp agent/MCP declarations under `design/`.
3. Generate with a Go import path:

```bash
loom gen example.com/project/design
```

4. Run `loom example example.com/project/design` only when application-owned
   scaffolding is intentionally desired.
5. Implement planners and service logic outside `gen/`.
6. Register models, toolset executors, MCP callers, and agents before the first
   run, then call `Runtime.Seal(ctx)` when the application wants startup
   validation before serving traffic.

## Generated ownership

- `gen/<service>/agents/<agent>/`: agent configuration and aggregate catalog.
- `gen/<service>/toolsets/<toolset>/`: owner-scoped specs, codecs, transforms,
  dispatchers, and provider helpers.
- `gen/mcp_<service>/`: generated MCP adapter, SDK server, local registration,
  and protocol helpers.
- `internal/agents/`: application-owned output from `loom example`.
- `AGENTS_QUICKSTART.md`: generated project-specific wiring guide.

Never edit generated `gen/` files. Change the design or generator and
regenerate.

## Verification

In this repository use:

```bash
make loom-local
make verify-mcp-local
make lint
make test
make itest
```

For downstream projects, run their generated compile/tests plus the smallest
end-to-end run that proves planner, registration, and tool execution wiring.

## Canonical references

- `quickstart/README.md`: runnable setup.
- `docs/dsl.md`: complete DSL and generated-helper semantics.
- `docs/runtime.md`: runtime, planner, memory, provider, and MCP caller contracts.
- `docs/mcp_sdk_server.md`: generated MCP server behavior.
