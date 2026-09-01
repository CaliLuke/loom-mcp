# loom-mcp Integration Tests

This directory verifies generated MCP servers and generated agent behavior
against real compiled fixtures. The harness uses the official MCP Go SDK for
valid client behavior and raw Streamable HTTP requests for malformed or
otherwise invalid wire inputs.

## Canonical commands

Run the complete integration suite from the repository root:

```bash
make itest
```

The target runs, in order:

1. the generated quickstart acceptance test;
2. every assistant fixture test with the race detector;
3. every agent-feature fixture test with the race detector;
4. every integration framework test with the race detector.

Run the MCP fixture and framework ladder without the quickstart acceptance
test:

```bash
make verify-mcp-local
```

Run an owning module directly for focused development:

```bash
go test -C ./integration_tests/fixtures/assistant -race ./... -count=1
go test -C ./integration_tests/fixtures/agent_features -race ./... -count=1
go test -race -count=1 ./integration_tests/framework
```

The fixture directories are nested Go modules. A recursive `go test
./integration_tests/...` from the repository root does not cross into them.

## Directory map

```text
integration_tests/
├── fixtures/
│   ├── assistant/       generated MCP server and SDK acceptance fixture
│   └── agent_features/  generated agent DSL/runtime acceptance fixture
└── framework/           managed server, raw HTTP, SDK, and codegen contracts
```

`fixtures/assistant/design/design.go` and
`fixtures/assistant/progressive_discovery/design/design.go` own the assistant
generated surfaces. `fixtures/agent_features/design/design.go` owns the
generated agent acceptance surface. Never edit their `gen/` directories by
hand.

## Coverage owners

### Framework

`framework/` owns:

- compilation and lifecycle of an isolated generated assistant server;
- raw Streamable HTTP initialization, fallback, malformed-input, session,
  notification, resource-policy, tool-validation, and terminal SSE error
  contracts;
- official SDK initialization, sessions, catalog listing, tools, resources,
  prompts, progress, and closed-loop flows;
- generated SDK conformance and generated prompt-only or JSON-RPC-independent
  compilation contracts;
- cleanup of prepared fixture roots and compiled server binaries.

Use raw HTTP only for contracts a conforming SDK cannot express, such as
malformed JSON or calls made outside the initialization lifecycle. Use the
official SDK for valid client behavior.

### Assistant fixture

`fixtures/assistant` owns generated-server behavior that can be tested directly
with `httptest`, including:

- SDK/native adapter parity and projected tools;
- metadata, icons, schemas, and compact tool discovery;
- OAuth discovery, challenges, audience binding, proxy trust, and session
  principal continuity;
- origin protection and runtime CORS;
- request-scoped context, transport observation, and progress;
- prompt completion and single- or multi-step elicitation;
- resource subscriptions, cancellation, and unary collection of Loom streams.

### Agent-feature fixture

`fixtures/agent_features` owns DSL-to-generated-runtime acceptance for:

- generated registrations and method-backed dispatch;
- workflow graphs, branching, retries, awaits, and typed input;
- parity between the in-memory engine and a pinned real Temporal development
  server, including activity retry, active-time waits, cancellation, replay,
  and worker replacement;
- generated agent-as-tool links, parent-to-child cancellation, confirmation,
  clarification, external tool results, and queued pause/resume signals;
- cross-layer agreement between runtime output, runlog, session state, hooks,
  and streams for generated lifecycle transitions;
- artifacts, transcript memory, long-term memory, and local skills;
- named interceptors, retry hints, debug state, and registry capabilities;
- generated registry schema reference resolution and Unicode validation.

The Temporal acceptance test uses the SDK development-server harness and pins
Temporal CLI `v1.6.1`. The SDK downloads that executable into the operating
system temporary directory on first use and reuses it for later focused runs.
It is part of the normal agent-feature fixture and therefore runs in
`make verify-mcp-local`, `make itest`, and `make ci`.

## Supported environment variables

| Variable | Purpose |
| --- | --- |
| `TEST_SERVER_URL` | Use an already-running generated server instead of starting the managed fixture. Supply the server base URL or its `/rpc` endpoint. |
| `TEST_SKIP_GENERATION=true` | Reuse the checked-in assistant fixture in focused framework debugging. |
| `MCP_TEST_READY_TIMEOUT_SECONDS` | Override the managed server's 30-second readiness timeout with a positive integer. |

Go's standard test flags provide filtering, timeouts, verbosity, shuffle, and
repeat counts. For example:

```bash
go test -race -count=1 ./integration_tests/framework -run TestGeneratedServerRaw
go test -C ./integration_tests/fixtures/assistant -race ./... -run ToolSearch -count=1
```

Managed-server stdout and stderr tails are included in startup and early-exit
failures.

## Generated fixture workflow

Regenerate only through the repository targets:

```bash
make regen-assistant-fixture
make regen-progressive-discovery-fixture
make regen-agent-feature-fixture
```

After regeneration, inspect the generated diff and run the owning fixture
module. CI regenerates the quickstart and every fixture, then requires a clean
diff before it runs `make itest`.

When the framework needs application-owned server wiring, keep the source under
`framework/testdata/`. The runner embeds and copies those checked-in sources
into an isolated fixture clone. Do not put large Go source strings in tests.

## Adding coverage

1. Put DSL validation and expression tests under `dsl/` or `expr/`.
2. Put generator structure, determinism, and compile contracts under
   `codegen/`.
3. Put valid generated MCP server behavior in the assistant fixture or SDK
   framework tests.
4. Put malformed or invalid MCP wire behavior in raw framework tests.
5. Put DSL-to-runtime agent behavior in the agent-feature fixture and
   regenerate it.
6. Run the focused test, then `make verify-mcp-local` and `make itest`.

Do not document a coverage surface until an executable test owns it.
