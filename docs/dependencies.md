# Dependency baseline

The supported baseline is the latest stable Go toolchain, maintained libraries,
and MongoDB and Redis server releases. Resolve versions from module metadata,
upstream release tags, and the Docker official-images manifests. Pin concrete
versions and verify upgrades with the full framework and Docker test suites.

Verified on 2026-10-08:

| Dependency | Version |
| --- | --- |
| Go | 1.27.2 |
| Loom | v1.10.0-alpha.5 |
| MongoDB server / Go driver | 9.0.2 / v2.9.2 |
| Redis server / Go client | 8.10.2 / v9.23.0 |
| OpenAI Go SDK | v3.74.0 |
| Anthropic Go SDK | v1.79.1 |
| Google GenAI | v1.73.0 |
| OpenTelemetry | v1.47.0 |
| golangci-lint | v2.14.0 |

The OpenAI provider's `ResponseClient` interface now uses types from
`github.com/openai/openai-go/v3`. Applications providing their own client must
update imports and client construction together. Skill frontmatter uses the
maintained `go.yaml.in/yaml/v3` package.

Regenerate code when adopting Loom alpha.5. Protobuf scalar presence is now
represented by pointer fields; handwritten protobuf request construction must
set those fields explicitly (for example with `proto.String`). The tool union
renderer owns its private metadata types and does not depend on Loom's service
renderer metadata representation.

Explicit exceptions:

- Loom uses the published alpha.5 release by agreement for this upgrade.
- The MCP Go SDK remains at `v1.8.1-0.20260922085944-8075fb3cf313`.
  Its merged request-summary fix is newer than stable v1.8.0 and is required by
  the SDK bridge. Keep the root, fixtures, quickstart and workspace override aligned.
- Loom requires `github.com/CaliLuke/go-sse` at immutable commit `b29b33ae1a1e`.
  Preserve the version selected by the published Loom module.
- Some indirect modules, including Google's generated API definitions and
  platform support packages, publish only commit-based Go versions. Those are
  recorded in the module files; a pseudo-version alone does not identify an
  obsolete dependency.

Container tests use `mongo:9.0.2` and `redis:8.10.2-alpine`, rather than old
major-only image tags. The MongoDB tests still require a replica set for
transactional behavior.

Staticcheck v0.8.1 is built with the stable `golang.org/x/tools v0.51.0` importer
for Go 1.27.2 export-data support. `make tools` verifies both versions. Its check
selection and existing documented `lint:ignore` directives remain unchanged.
