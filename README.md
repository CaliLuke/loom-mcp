# loom-mcp

`loom-mcp` is the home for the agent, MCP, and registry framework in this repository.

It combines:
- a Loom-powered design DSL for describing agents, toolsets, MCP servers, and registries,
- code generation driven by `github.com/CaliLuke/loom`,
- a runtime for planning, execution, streaming, memory, and durable workflows.

## Current status

This repository has been rehomed and detached from the original fork network to prepare for a fresh release line.

Two facts are important right now:
- The repository name is `loom-mcp`.
- The Go module path is `github.com/CaliLuke/loom-mcp`.

Repo identity and module identity are now aligned.

## What lives here

- `design/`: design source of truth.
- `dsl/`: agent, MCP, and registry DSL.
- `codegen/`: generators for agents, MCP adapters, codecs, and registries.
- `runtime/`: execution runtime, planners, engines, MCP callers, and streaming.
- `registry/`: registry service implementation and generated transports.
- `docs/`: in-repo technical documentation.
- `quickstart/`: runnable starter project and generated walkthrough material.

## Dependencies

This repo currently targets:
- `github.com/CaliLuke/loom v1.0.2`
- Go `1.25.5`

The standard CLI for generation is:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.0.2
```

## Working in this repo

Common commands:

```bash
make goa-status
make goa-local
make goa-remote
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
- [`quickstart/README.md`](quickstart/README.md)
- [`AGENTS.md`](AGENTS.md)

## Release prep note

This repo has intentionally been reset to a clean branch/repository identity. Old branch structure and inherited tags have been removed, and the next published tag should start the new `loom-mcp` release line.

## License

MIT. See [`LICENSE`](LICENSE).
