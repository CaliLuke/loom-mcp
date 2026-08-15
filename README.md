# loom-mcp

`loom-mcp` is the home for the agent, MCP, and registry framework in this repository.

It combines:

- a Loom-powered design DSL for describing agents, toolsets, MCP servers, and registries,
- code generation driven by `github.com/CaliLuke/loom`,
- a runtime for planning, execution, streaming, memory, and durable workflows.

## Current status

The repository is named `loom-mcp`. Its current Go module path is
`github.com/CaliLuke/loom-mcp/v2`; the `/v2` suffix is required by Go semantic
import versioning.

### Upgrading from v1

Update imports and module requirements from `github.com/CaliLuke/loom-mcp` to
`github.com/CaliLuke/loom-mcp/v2`. Version 2 intentionally removes the
deprecated MCP sampling and roots runtime APIs. It also changes
`sdkclient.WithClientFeatures` to accept `ClientFeaturesOptions` for official
multi-round-trip elicitation. Generated SDK servers that use elicitation must
configure a stable 32-byte `SDKServerOptions.RequestStateKey`; replicas serving
the same endpoint must share that key. There is no v1 compatibility shim.

## What lives here

- `design/`: design source of truth.
- `dsl/`: agent, MCP, and registry DSL.
- `codegen/`: generators for agents, MCP adapters, codecs, and registries.
- `runtime/`: execution runtime, planners, engines, MCP callers, and streaming.
- `features/`: production adapters for model providers, MongoDB stores, prompt overrides, policies, and Pulse streaming.
- `registry/`: registry service implementation and generated transports.
- `docs/`: in-repo technical documentation.
- `quickstart/`: runnable starter project and generated walkthrough material.

## Dependencies

This repo currently targets:

- `github.com/CaliLuke/loom v1.8.0-alpha.5`
- `github.com/modelcontextprotocol/go-sdk v1.7.0`
- Go `1.27rc2` or later; this preview requirement will move to Go `1.27` after its stable release

The workspace-level `go.work` file centralizes local multi-module overrides for dependencies that must stay in sync across the root module and integration fixtures.
Use `make update-mcp-go-sdk MCP_GO_SDK_VERSION=vX.Y.Z` when bumping the MCP Go SDK.

The standard CLI for generation is:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.8.0-alpha.5
```

## Working in this repo

Common commands:

```bash
make loom-status
make loom-local
make loom-remote
make regen-assistant-fixture
make verify-mcp-local
make lint
make test
make itest
```

Design changes should always start in `design/*.go`. Regenerate after changing the DSL and do not hand-edit generated `gen/` files.

## Documentation

Start here:

- [`docs/overview.md`](docs/overview.md)
- [`docs/dsl.md`](docs/dsl.md)
- [`docs/runtime.md`](docs/runtime.md)
- [`docs/mcp_sdk_server.md`](docs/mcp_sdk_server.md)
- [`docs/operations.md`](docs/operations.md)
- [`docs/tool_payload_defaults.md`](docs/tool_payload_defaults.md)
- [`docs/glossary.md`](docs/glossary.md)
- [`quickstart/README.md`](quickstart/README.md)
- [`AGENTS.md`](AGENTS.md)

## License

MIT. See [`LICENSE`](LICENSE).
