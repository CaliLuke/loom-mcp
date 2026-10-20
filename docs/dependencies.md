# Dependency baseline

The supported baseline is the latest stable Go toolchain, maintained libraries,
and MongoDB and Redis server releases. Resolve versions from module metadata,
upstream release tags, and the Docker official-images manifests. Pin concrete
versions and verify upgrades with the full framework and Docker test suites.

Verified on 2026-10-08:

| Dependency | Version |
| --- | --- |
| Go | 1.27.2 |
| Loom | v1.10.0-alpha.14 (verified 2026-10-20) |
| MongoDB server / Go driver | 9.0.4 / v2.9.2 |
| Redis server / Go client | 8.10.2 / CaliLuke/go-redis v9.23.1-0.20261014134601-1073297e76ea (client verified 2026-10-14) |
| OpenAI Go SDK | v3.74.0 |
| Anthropic Go SDK | v1.79.1 |
| Google GenAI | v1.73.0 |
| OpenTelemetry | v1.47.0 |
| golangci-lint | v2.14.0 |

The OpenAI provider's `ResponseClient` interface now uses types from
`github.com/openai/openai-go/v3`. Applications providing their own client must
update imports and client construction together. Skill frontmatter uses the
maintained `go.yaml.in/yaml/v3` package.

Regenerate code when adopting Loom alpha.10. Protobuf scalar presence is
represented by pointer fields; handwritten protobuf request construction must
set those fields explicitly (for example with `proto.String`). Tool union codecs
use Loom's shared union generator and JSON branch analysis. Loom-MCP supplies
transport-local types and validators to that shared implementation.

Explicit exceptions:

- Loom uses the published alpha.14 release.
- The MCP Go SDK pins `v1.8.1-0.20261016161459-3e9d812b1814`, verified on
  2026-10-17 against upstream commit `3e9d812b1814`. Stable v1.8.0 still predates
  the request-summary API required by the SDK bridge. This pin includes the
  subsequent upstream fixes. Keep the root, fixtures, quickstart and workspace
  override aligned with `make update-mcp-go-sdk`.
  The runtime callers expose elicitation callbacks and stdio frame limits;
  generated servers support sequential elicitation with legacy clients. Requests
  before initialization now report JSON-RPC Invalid Request (`-32600`).
- Loom requires `github.com/CaliLuke/go-sse` at immutable commit `b29b33ae1a1e`.
  Preserve the version selected by the published Loom module.
- Some indirect modules, including Google's generated API definitions and
  platform support packages, publish only commit-based Go versions. Those are
  recorded in the module files; a pseudo-version alone does not identify an
  obsolete dependency.

Container tests use `mongo:9.0.4` and `redis:8.10.2-alpine`, rather than old
major-only image tags. The MongoDB tests still require a replica set for
transactional behavior.

MongoDB server 9.0.4 was re-verified on 2026-10-19. It includes the upstream
[Ubuntu kernel compatibility fix](https://jira.mongodb.org/browse/SERVER-131779)
for the startup refusal observed with 9.0.2 on the hosted Docker runner.

Staticcheck v0.8.1 is built with the stable `golang.org/x/tools v0.51.0` importer
for Go 1.27.2 export-data support. `make tools` verifies both versions. Its check
selection and existing documented `lint:ignore` directives remain unchanged.

## Loom alpha.10 migration

Regenerate service, client, and server code together. Custom String conversions
can return errors, and generated defaults now distinguish absent values from
explicit zero and empty values. Tool adapter transforms that can fail now return
`(value, error)`; callers must handle that error. Infallible transform signatures
remain unchanged. Generated providers reject failed payload conversions before
calling the service and reject failed result or server-data conversions before
publication. Nested validation retains the full field path.

Pulse client and registry APIs now use `github.com/CaliLuke/go-redis/v9`. Change
Redis client and hook imports together; upstream client types are not compatible.
The version is the immutable fork revision selected by the published Loom module.

Pulse sink-group filters and pool protocol 4 require coordinated migration of
existing deployments. Follow the [Pulse migration guide](https://github.com/CaliLuke/loom/blob/v1.10.0-alpha.10/docs/pulse.md)
before changing Redis state or mixing participants from different versions.

Close active Pulse sinks before intentionally destroying shared streams; active
sinks repair missing consumer groups. Transport validation messages are sanitized;
inspect the typed error name and status instead of parsing diagnostic text.

Authored MCP SDK retry-validation errors explicitly own their public repair messages under
Loom's error contract. Custom invalid-input errors need `LoomErrorName` or a safe
error remedy to expose repair text; the marker alone does not publish diagnostics.
Wrapped serialization failures retain private diagnostics only in the original error.

## Loom alpha.12 Pulse membership migration

Pulse sink membership version 2 records consumer keepalive state atomically with
membership. It rejects persisted version 1 membership records because they
cannot safely bind freshness to cleanup. Restarting with alpha.12 does not
migrate those records.

Before upgrading any process that reads or writes affected Pulse streams, stop
all older processes. To preserve stream entries, copy them to a fresh stream and
create new sink groups there. Consumer groups, cursors, and pending-entry
ownership are not copied, so new groups may redeliver copied entries; preserve
required progress with application checkpoints or idempotent processing. For an
intentional destructive reset, use `Stream.Destroy` before recreating the stream.
Do not mix old and new binaries. See the [alpha.12 Pulse migration guidance](https://github.com/CaliLuke/loom/blob/v1.10.0-alpha.12/docs/pulse.md#streams-readers-and-sinks).

## Loom alpha.13 adoption

Regenerate service, client, and server code together. This release fixes HTTP
response validator discovery for untagged unions. Status-derived custom error
names require explicit `ErrorNameFromStatus()` in the HTTP error response DSL;
existing designs retain their authored error identity and required-header rules.

## Loom alpha.14 shared payload codecs

Regenerate MCP adapters and agent tool codecs together. MCP arguments now use
Loom transport types and conversion plans, so nested defaults apply only to
absent values. Supplied zero, false, empty collections and allowed nulls retain
their meaning. Required fields remain required even when they declare defaults.
Tagged and untagged tool unions use Loom’s shared JSON codecs and branch
validation; ambiguous or unmatched untagged values are rejected.
