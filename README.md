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

- `github.com/CaliLuke/loom v1.10.0-alpha.16`
- `github.com/modelcontextprotocol/go-sdk v1.8.1-0.20261016161459-3e9d812b1814`
- Go `1.27.3` or later

The dependency baseline uses current stable releases, including MongoDB `9.0.4`,
Redis `8.10.2`, the MongoDB Go driver `v2.9.2`, Redis Go client `v9.23.0`,
and OpenAI Go SDK `v3.74.0`. See [dependency policy](docs/dependencies.md) for
version verification and the explicit unreleased pins.

The workspace-level `go.work` file centralizes local multi-module overrides for dependencies that must stay in sync across the root module and integration fixtures.
Use `make update-mcp-go-sdk MCP_GO_SDK_VERSION=vX.Y.Z` when bumping the MCP Go SDK.
The current pseudo-version pins upstream commit `3e9d812b1814` while the merged
request-summary API awaits a tagged SDK release.

The standard CLI for generation is:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.10.0-alpha.16
```

## Working in this repo

Commit and push on `main`, then run `make release SOURCE=<full-commit-sha>`
to dispatch a CI-verified alpha. Daily alphas use the same publisher. Promote an
existing alpha without changing its source with `make release-promote`. See
[releases](docs/releases.md) for inputs, CI evidence, and recovery.

Common commands:

```bash
make loom-status
make loom-local
make loom-remote
make verify-generated
make regen-assistant-fixture
make verify-mcp-local
make lint
make test
make test-docker
make ci-integration
make ci
make qualification
make test-stress
```

`make ci` runs the same routine checks as hosted CI: generated output, build,
lint, full short unit/coverage tests, engine and generated application integration,
and Docker contracts/coverage. GitHub runs static, race, coverage, integration and Docker checks in separate
jobs; all five must pass for release eligibility. Race detection and atomic
coverage run the same short unit suite separately to avoid combined instrumentation
overhead; no tests or coverage floors are removed.
`make ci-integration` runs the routine integration checks alone.

`make qualification` runs exhaustive longrun scenarios, statistical evidence,
and 1,000-step payload/recall acceptance. The **Exhaustive qualification** workflow
runs it weekly or on manual dispatch, independently of per-push release eligibility.
Run it when changing compaction qualification, statistical evaluation, or growth
contracts. It preserves the full workloads and does not rerun the calibration CLI.
`make itest` remains the combined routine and exhaustive integration command;
`make verify-mcp-local` also retains the complete longrun module.
`make verify-generated` regenerates current surfaces and verifies the frozen
external SDK bridge consumer without rewriting it. Increment
`sdkbridge.CompatibilityVersion` before you regenerate the consumer. You can
repeat regeneration while the version increase is not committed. Commit the
version, the design, and generated files as one change.
`make loom-remote` preserves generator dependencies for isolated fixtures.
The Makefile pins `protoc` and both Go protobuf plugins. Run
`make install-protoc` if the pinned compiler is not already first on `PATH`.
`make test` enforces global and critical package-group coverage floors without
starting containers. It also pins exact floors for the agent runtime, Bedrock,
and Gemini so broader package groups cannot hide regressions in those owners.
The 1,000-step recall acceptance test skips under `make test` (`-short`).
`make qualification` (and the combined `make itest`) runs it with race detection
and a 30-minute timeout.
`make test-docker` runs the Mongo, Pulse, and registry contracts once under
race detection and enforces an exact floor for each package.
The scheduled stress workflow repeats race-enabled lifecycle tests and requires
the Docker-backed Mongo, Redis, and registry tests.

Design changes should always start in `design/*.go`. Regenerate after changing the DSL and do not hand-edit generated `gen/` files.

## Documentation

Start here:

- [`ROADMAP.md`](ROADMAP.md)
- [`docs/architecture_decisions.md`](docs/architecture_decisions.md)
- [`docs/overview.md`](docs/overview.md)
- [`docs/dsl.md`](docs/dsl.md)
- [`docs/runtime.md`](docs/runtime.md)
- [`docs/agent_testing.md`](docs/agent_testing.md)
- [`docs/mcp_sdk_server.md`](docs/mcp_sdk_server.md)
- [`docs/operations.md`](docs/operations.md)
- [`docs/tool_payload_defaults.md`](docs/tool_payload_defaults.md)
- [`docs/glossary.md`](docs/glossary.md)
- [`quickstart/README.md`](quickstart/README.md)
- [`AGENTS.md`](AGENTS.md)

## License

MIT. See [`LICENSE`](LICENSE).
