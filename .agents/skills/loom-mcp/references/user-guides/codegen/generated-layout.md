# Generated Layout

Use `references/codegen-contracts.md` for invariants and inspect the generated
package API for the exact evaluated design.

```text
gen/
├── <service>/
│   ├── service.go, endpoints.go, client.go
│   ├── agents/<agent>/
│   │   ├── agent.go, config.go, registry.go
│   │   └── specs/                    aggregate agent catalog
│   └── toolsets/<toolset>/           owner specs/codecs/transforms/providers
├── mcp_<service>/                    generated MCP protocol package
├── jsonrpc/, http/, grpc/            transport packages
```

Ownership rules:

- Never edit `gen/`.
- Change `design/`, DSL, or generator code and regenerate.
- Put business logic in application-owned service/planner/executor packages.
- Treat `cmd/` and `internal/agents/` emitted by `loom example` as
  application-owned scaffolding.
- Agent aggregate specs do not own defining toolset types. Import the
  owner-scoped `gen/<service>/toolsets/<toolset>` package when typed transforms
  or specs are required.
