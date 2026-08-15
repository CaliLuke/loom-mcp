# loom-mcp Code Generation

`docs/dsl.md`, `DESIGN.md`, and `references/codegen-contracts.md` are the
canonical sources. This guide records only the user workflow and ownership
boundaries.

## Commands

Commands take Go import paths, never filesystem paths:

```bash
loom gen example.com/project/design
loom example example.com/project/design
```

- `loom gen` evaluates and validates the design, then replaces generator-owned
  output. Run it after every design change.
- `loom example` creates application-owned scaffolding. Run it only when new
  scaffold files are intentionally desired.
- Commit generated output so CI can verify regeneration produces no diff.

## loom-mcp generated surfaces

```text
gen/
├── <service>/
│   ├── agents/<agent>/
│   │   ├── agent.go
│   │   ├── config.go
│   │   ├── registry.go
│   │   └── specs/               aggregate catalog/aliases
│   └── toolsets/<toolset>/      owner-scoped types/codecs/specs/transforms
├── mcp_<service>/               MCP adapter, SDK server, local provider
├── http/
└── grpc/
```

MCP generation does not create `gen/jsonrpc`. An explicitly designed non-MCP
JSON-RPC transport can still create that tree.

Exact files vary with the evaluated design. Do not infer a file exists from an
older guide; inspect the generated package API.

At module root, agent generation may emit `AGENTS_QUICKSTART.md`. Disable it
only with `DisableAgentDocs()` in the design.

## Application-owned surfaces

`loom example` owns scaffolding under `internal/agents/` and `cmd/`. Business
logic, planners, service implementations, model construction, and runtime
wiring belong outside `gen/`.

## Generated contracts

- Canonical runtime tool IDs are `<toolset>.<tool>` for service-owned toolsets.
- Owner packages contain the defining `ToolSpec`, codec, and method dispatcher.
- Agent packages aggregate used toolsets and expose typed runtime registration
  helpers such as `RegisterUsedToolsets` plus `With<Toolset>Executor`.
- MCP packages expose generated registration helpers for runtime callers and,
  when tools exist, an in-process local `ToolsetRegistration` built from the
  same `MCPAdapter`.
- Method payload/result transforms use generated `Init<Tool>MethodPayload` and
  `Init<Tool>ToolResult` helpers when the evaluated design requires them.

## Repository verification

When changing this framework:

```bash
make loom-local
make gen-registry                 # when registry generation is affected
make regen-assistant-fixture      # when MCP generation is affected
make verify-mcp-local
make lint
make test
make itest
```

Never patch generated fixtures manually. Regenerate through the repository make
targets and review the resulting diff.
