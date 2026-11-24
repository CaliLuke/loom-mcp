# Dependency baseline

The supported baseline is the latest stable Go toolchain, maintained libraries,
and MongoDB and Redis server releases. Resolve versions from module metadata,
upstream release tags, and the Docker official-images manifests. Pin concrete
versions and verify upgrades with the full framework and Docker test suites.

Dependency baseline for the 2026-11-15 refresh:

| Dependency | Version |
| --- | --- |
| Go | 1.27.4 |
| Selvedge | v0.8.1 (verified 2026-11-08) |
| Loom | v1.12.0 |
| MongoDB server / Go driver | 9.0.8-resolute / v2.10.0 |
| Redis server / Go client | 8.10.3 / CaliLuke/go-redis v9.23.1-0.20261105165422-21c4e561f514 (client verified 2026-11-08) |
| OpenAI Go SDK | v3.99.0 |
| Anthropic Go SDK | v1.84.0 (verified 2026-10-20) |
| Google GenAI | v1.77.0 (verified 2026-11-08) |
| Bedrock Runtime / AWS core / Smithy | v1.63.13 / v1.48.0 / v1.28.9 |
| Google auth / OAuth2 | v0.24.4 / v0.38.0 |
| OpenTelemetry | v1.47.0 |
| golangci-lint | v2.15.0 |
| Staticcheck / x/tools | v0.8.2 / v0.52.0 |
| Temporal SDK / API | v1.51.0 / v1.64.3 |
| SQLite / libc | v1.62.0 / v1.81.2 |
| Testcontainers | v0.45.0 |
| PostgreSQL conformance image / pgx | 18.7-alpine / v5.13.0 |

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

- The MCP Go SDK pins `v1.8.1-0.20261113120057-0fa5e8407aa3`, verified on
  2026-11-15 against upstream commit `0fa5e8407aa3`. Stable v1.8.0 still predates
  the request-summary API required by the SDK bridge. This pin includes the
  subsequent upstream fixes. Keep the root, fixtures, quickstart and workspace
  override aligned with `make update-mcp-go-sdk`.
  The runtime callers expose elicitation callbacks and stdio frame limits;
  generated servers support sequential elicitation with legacy clients. Requests
  before initialization now report JSON-RPC Invalid Request (`-32600`).
- SSE support now uses upstream `github.com/tmaxmax/go-sse v0.12.0`, as
  selected by Loom; the former fork dependency is removed.
- Some indirect modules, including Google's generated API definitions and
  platform support packages, publish only commit-based Go versions. Those are
  recorded in the module files; a pseudo-version alone does not identify an
  obsolete dependency.

Container tests use `mongo:9.0.8-resolute` and `redis:8.10.3-alpine`, rather than old
major-only image tags. The MongoDB tests still require a replica set for
transactional behavior.

MongoDB server 9.0.4 was re-verified on 2026-10-19. It includes the upstream
[Ubuntu kernel compatibility fix](https://jira.mongodb.org/browse/SERVER-131779)
for the startup refusal observed with 9.0.2 on the hosted Docker runner.

Staticcheck v0.8.2 is built with the stable `golang.org/x/tools v0.52.0` importer
for Go 1.27.4 export-data support. `make tools` verifies both versions. Its check
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

## Loom alpha.15 typed activities

The engine activity types now use Loom's shared `execution` contracts. Agent
activity defaults remain in this module; workflow orchestration and backend
lifecycles retain their existing owners. See the [runtime activity API](runtime.md)
for typed registration and execution. The release also selects pgx v5.11.0.

## Loom alpha.16 compiler improvements

Regenerate service, client, and server code together. This release reuses
immutable compiler analysis and bounds concurrent file rendering. Existing
consumer APIs remain unchanged. See the [alpha.16 release notes](https://github.com/CaliLuke/loom/releases/tag/v1.10.0-alpha.16).

## Loom v1.12.0 stable adoption

The published baseline is `v1.12.0` (commit `bd761923c`). Generated loom-mcp
code is unchanged apart from the recorded Loom version; regenerate to refresh
`gen/loom.json`. Since v1.10.0, Loom adds the `slogbridge` OpenTelemetry handler
and removes `logrusbridge` (v1.11.0), fixes `loom vet` HTTP error-contract false
positives (v1.10.1), and adds `admission.KeyedRate`, custom `RateLimit`
rejection responders, and OTLP gRPC and endpoint-URL options in
`observability/otel.New` (v1.12.0). Loom-MCP itself uses none of these
packages. Applications that import them directly must review the breaking
changes: `logrusbridge` and the `server-error-fault` vet rule are removed, nil
`Propagators` now means the global propagator rather than W3C TraceContext,
`loomotel.New` always sets the global propagator and rejects endpoints with a
scheme or path, and named HTTP client spans are renamed. See the
[v1.10.1](https://github.com/CaliLuke/loom/releases/tag/v1.10.1),
[v1.11.0](https://github.com/CaliLuke/loom/releases/tag/v1.11.0), and
[v1.12.0](https://github.com/CaliLuke/loom/releases/tag/v1.12.0) release notes.

## Loom v1.10.0 stable adoption

The previous baseline was `v1.10.0` (commit `71810f706`). Regenerate services,
clients, and servers together. Since alpha.30, Loom reduces generator memory
costs and refreshes its dependencies, including pgx v5.13.0 and the corrected
libopenapi header-example API. See the
[stable release notes](https://github.com/CaliLuke/loom/releases/tag/v1.10.0).

The framework refresh also updates OpenAI, AWS Bedrock, Temporal, Google auth,
OAuth2, Go support libraries, and Docker client dependencies. The MCP SDK snapshot
adds session-specific list-change notifications and tightens MCP parameter-header
validation. Temporal status queries use the supported `DescribeWorkflow` API
and map its raw response through the existing lifecycle rules. Existing runtime
contracts remain unchanged.

The progressive-discovery benchmark's Python and SchemaRouter lock remains
frozen with its recorded comparison results. It is an experiment reproduction
environment, not a runtime dependency. The MCP specification submodule likewise
remains the reference snapshot for implemented protocol behavior.

## Loom alpha.30 adoption

The previous baseline was `v1.10.0-alpha.30` (commit `453ea0b5`). Regenerate
service, client, and server code together. Since alpha.28, Loom preserves inherited
required fields in OpenAPI projections, completes result declarations before
localization, retains object openness in result views, and validates declaration
metadata and examples independently of their use. Applied OpenAPI view selections
are consumed so they cannot trigger recursive projection. See the
[release comparison](https://github.com/CaliLuke/loom/compare/v1.10.0-alpha.28...v1.10.0-alpha.30).

The dependency refresh also adopts OpenAI Go v3.97.0 and its current JSON helper
dependencies. Its new scheduling APIs do not change the existing Responses
provider integration. Other active dependency and tool pins were rechecked
against published module versions, upstream releases, and official image manifests.

## Loom alpha.28 adoption

The previous baseline was `v1.10.0-alpha.28` (commit `f6c17e1f`). Regenerate
service, client, and server code together. This release adds status-selected
HTTP responses and SSE handshake metadata, expands OpenAPI imports, and corrects
response presence and integer storage conversions. Review generated client
signatures when upgrading. Custom union renderer integrations must supply branch
method and field names from `unionnames.Resolve`; the MCP adapter follows this
contract. Tool `Args` and `Return` customization callbacks execute once and
materialize occurrence-local `Required` fields on their private named type,
without changing the shared type used by other tools or results. Its module
graph also selects Selvedge v0.8.1,
pgx v5.12.0, gRPC v1.85.0, SQLite v1.61.1, and upstream go-sse v0.12.0. See the
[release notes](https://github.com/CaliLuke/loom/releases/tag/v1.10.0-alpha.28).

The new Loom `execution/selvedge` producer and worker roles provide typed job
submission and lookup, but do not expose signals, queries, cancellation, child
workflows, or application transactions. The agent engine retains its native App
integration because it requires those facilities. New HTTP response selectors,
SSE handshake metadata, and OpenAPI import APIs are available through the pinned
Loom release; they do not require new MCP protocol surfaces.

The frozen discovery comparison under `integration_tests/fixtures/assistant/discoverybench`
retains its original Python lockfile and competitor revision so recorded results
remain reproducible. Go, Protobuf tools, and the current GitHub Actions major
references were checked and already select current releases.

## Loom alpha.22 adoption

The previous baseline was `v1.10.0-alpha.22` (commit `6a1229f9`). Regenerate
service, client, and server code together. This release preserves distinct HTTP
media contracts with typed variants and ordered OpenAPI schema examples. See the
[release comparison](https://github.com/CaliLuke/loom/compare/v1.10.0-alpha.21...v1.10.0-alpha.22).

## Loom alpha.21 adoption

The previous baseline was `v1.10.0-alpha.21` (commit `73762ebc`). Regenerate
service, client, and server code together. Since alpha.16, Loom has corrected
optional collection and nullable enum validation, expanded untagged union
support, and preserved independent security scope contracts. The upgrade also
updates the indirect libopenapi dependency. See the
[release comparison](https://github.com/CaliLuke/loom/compare/v1.10.0-alpha.16...v1.10.0-alpha.21).

## Selvedge v0.7.0 adoption

Selvedge now uses the published v0.7.0 release instead of a development commit.
It requires Go 1.27.3 and a fresh database; earlier development schemas have no
automatic migration. Finish existing runs before switching databases. See the
[Selvedge runtime notes](runtime.md) for tracing and
history compatibility details.

## Model-provider SDK update set

The 2026-10-20 update covers every model-provider SDK imported by this module:
OpenAI, Anthropic, Google GenAI (Gemini and Vertex), and Bedrock with its AWS core
and Smithy dependencies. Root, fixture, longrun, and quickstart module pins move
together. Codex, Ollama, and the model gateway use direct transports rather than
separate provider SDKs; their contract tests remain part of the same provider run.
Google auth and OAuth2 were checked and already use their latest releases.
A final check on 2026-10-21 picked up OpenAI v3.84.0, published during verification;
all other provider versions remained current.

Release sources: [OpenAI v3.84.0](https://github.com/openai/openai-go/releases/tag/v3.84.0),
[Anthropic v1.84.0](https://github.com/anthropics/anthropic-sdk-go/releases/tag/v1.84.0),
[Google GenAI v1.75.0](https://github.com/googleapis/go-genai/releases/tag/v1.75.0),
and [AWS releases](https://github.com/aws/aws-sdk-go-v2/releases).
Individual AWS module versions are resolved from published Go module metadata.

Google GenAI 1.75 enables automatic continuation by default. The adapter disables
that SDK loop for both Gemini and Vertex so its existing request budget, output
validation, and per-hop usage accounting remain authoritative. Transport-level
regressions exercise limits zero, one, and three with the real SDK. This adopts
the fix reviewed in [PR #370](https://github.com/CaliLuke/loom-mcp/pull/370).
