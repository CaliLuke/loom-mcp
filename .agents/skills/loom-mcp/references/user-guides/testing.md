# Testing loom-mcp Changes

Use fast contract tests close to the changed layer, then run the repository verification ladder. `integration_tests/README.md` is the canonical catalog for end-to-end scenarios and commands.

## Choose the smallest authoritative test

| Change | Start with |
| --- | --- |
| DSL validation/expression | `dsl/*_test.go`, `expr/**/*_test.go` |
| Generator/template | `codegen/**/tests`, golden/contract tests |
| Runtime planner/tool behavior | package tests under `runtime/agent/...` |
| Model provider | provider tests plus shared conformance tests |
| MCP protocol/transport | `runtime/mcp/...`, generated adapter tests, real client validation where required |
| Registry/Pulse behavior | `registry/...` and runtime registry tests with Redis-backed integration coverage |
| Cross-layer user behavior | generated fixture packages and `integration_tests/framework` |

The agent-feature fixture runs shared generated coordinator scenarios against
both the in-memory engine and a real Temporal development server. Keep the
Temporal CLI version pinned in the fixture. Put retry, replay, cancellation,
child-agent, human/external await, worker-replacement, and active-time wait
regressions in this shared acceptance surface instead of relying only on the
Temporal SDK's simulated environment. For cross-layer lifecycle cases, assert
runtime output, runlog, session state, hooks, and streams together.

Prefer table-driven tests, deterministic inputs, explicit error assertions, and generated constants/types over stringly typed fixtures.

## Generated-code changes

Design packages and generator templates are authoritative; generated `gen/` files are not.

1. Add or update a focused generator contract/golden test.
2. Change the design, expression, or generator source.
3. Regenerate with the repository Make target or `loom gen <module-import-path>/design`.
4. Confirm generated churn is intentional.
5. Run the generated package and integration tests.

Never edit `gen/` by hand. Run `loom example` only when new scaffold files are intentionally desired.

## Model providers

Every provider must preserve the shared model contract, not merely compile against the interface. Provider work should cover the applicable matrix:

- text and multimodal requests;
- complete and streaming responses;
- tool calls and structured output;
- native thinking/reasoning representation;
- token counting/capability reporting;
- cancellation;
- normalized provider errors, including `model.ErrRateLimited` on setup and receive-time failures;
- provider-safe tool-name mapping with canonical round trips.

Run the provider package with `-race`. Add or extend its conformance test instead of copying incomplete one-off assertions.

Each capability row must contain one supported case or one unsupported case.
Streaming providers must prove setup, receive, terminal, and rate-limit behavior,
plus their event state machine, premature EOF result, cancellation after partial
delivery, and close-error propagation. State-machine cases must assert observable
chunk order and identifiers across fragmented tool calls, interleaved content
indexes, thinking/text deltas, usage, and stop events as supported by the provider
grammar. If a provider cannot receive a late rate limit, prove where the setup
error occurs.

## Runtime event contracts

When testing hooks, streams, run logs, or memory, assert the intended reliability boundary:

- workflow state is the live deterministic execution authority;
- `runlog.Store` is the canonical append-only introspection log and is fail-closed;
- stream/hook-bus delivery is live observability and may be best-effort unless the API explicitly promises acknowledgement;
- `memory.Store` is a derived transcript projection and does not replace the run log.

Test event identity/deduplication, ordering, audience/profile filtering, and failure behavior explicitly. Do not infer durable delivery from a successful publish call alone.

## Persistent adapter contracts

Storage migrations and concurrency behavior must run against the real driver,
not only fake collections. Pre-seed legacy documents before client construction
so startup backfills and index failures are observable. For Mongo, cover prompt
fingerprints, legacy-plus-bucket memory ordering, runlog duplicate identities,
and session terminal-state races. A terminal run status is monotonic even when
late updates add metadata.

Pulse durability tests must leave a delivery unacknowledged, replace the sink,
and prove the pending entry is reclaimed before acknowledgement. Also stop the
real Redis service and require bounded publishing to return an error. The
Docker lane includes a generated Temporal worker replacement that closes the
first Mongo connection and creates fresh session/runlog clients for the second
worker.

## Verification ladder

For ordinary focused work, run the closest package tests first. Before calling framework, dependency, refactor, or integration work complete:

```bash
make loom-local
make verify-mcp-local
make lint
make test
make itest
make test-docker
```

Regenerate affected registry or assistant fixture outputs before `make verify-mcp-local`:

```bash
make gen-registry
make regen-assistant-fixture
```

Use only the targets relevant to the changed design, but never skip a red repository target. Before CI-facing verification or a commit, restore the pinned remote fork with `make loom-remote` as required by the repository rules, then run `make verify-generated`. This target snapshots the current diff, regenerates every tracked surface, and fails if generation changes it. Both local `make ci` and hosted CI use this same target.

`make test` applies the global coverage floor and the critical package-group
floors. The group floors omit generated, mock, and design packages. It forces
Docker-backed tests off so focused and full unit runs never start containers.
`make test-docker` is the explicit fail-closed Mongo, Pulse, registry, and
generated Temporal/Mongo replacement gate. It writes `docker-cover.out` and
enforces a floor for each root-module Docker owner.

Run the repeated lifecycle lane after concurrency or shutdown changes:

```bash
make test-stress
```

CI runs this target each week with Docker-backed tests set as required. The
complete `make ci` contract runs `make test-docker` exactly once and preserves
`docker-cover.out` from Mongo, Pulse, and registry integration packages.

## Failure policy

- A red required target is blocking, even if the failure appears outside the edited package.
- Do not bypass hooks or suppress failures.
- If a confirmed upstream Loom regression breaks this repository, stop and capture an upstream ticket with exact failing scenarios; do not ship a local compatibility workaround.
