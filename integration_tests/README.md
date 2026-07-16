# MCP integration tests

This directory verifies generated MCP behavior against real generated fixtures.
It combines YAML protocol scenarios, generated-server and SDK tests, codegen
conformance tests, and agent-feature acceptance tests.

## Run the suite

From the repository root, use the canonical target:

```bash
make itest
```

The target runs, in order:

1. the generated quickstart acceptance test;
2. every test in `integration_tests/fixtures/assistant`;
3. every test in `integration_tests/fixtures/agent_features`;
4. the full `integration_tests/...` suite with the race detector and CLI
   scenarios enabled.

For a narrower scenario run:

```bash
go test -race -count=1 ./integration_tests/tests -run TestMCPProtocol
go test -race -count=1 ./integration_tests/tests -run 'TestMCPProtocol/initialize_basic'
go test -race -count=1 ./integration_tests/tests -run TestMCPTools
go test -race -count=1 ./integration_tests/tests -run TestMCPResources
go test -race -count=1 ./integration_tests/tests -run TestMCPPrompts
go test -race -count=1 ./integration_tests/tests -run TestMCPNotifications
```

Run framework and generated-fixture tests directly when working on those
surfaces:

```bash
go test -race -count=1 ./integration_tests/framework
go test -C ./integration_tests/fixtures/assistant ./... -count=1
go test -C ./integration_tests/fixtures/agent_features ./... -count=1
```

## Supported environment variables

Only the following environment variables affect the integration harness:

| Variable | Purpose |
| --- | --- |
| `TEST_SERVER_URL` | Use an already-running server instead of starting the managed fixture. Supply its base URL; the harness calls `/rpc`. |
| `TEST_SKIP_GENERATION=true` | Reuse the existing generated assistant fixture instead of regenerating it. Intended for focused local debugging. |
| `MCP_TEST_READY_TIMEOUT_SECONDS` | Override the managed server's 30-second startup timeout with a positive integer. |
| `MCP_CLI_TESTS=true` | Enable CLI scenarios in `prompts_cli.yaml`. `make itest` sets this automatically. |

Go's normal test flags provide filtering, timeouts, verbosity, and parallelism.
For example:

```bash
go test -v -timeout=2m ./integration_tests/tests -run 'TestMCPTools/tool_analyze_sentiment'
```

There are no `TEST_FILTER`, `TEST_DEBUG`, `TEST_KEEP_GENERATED`,
`TEST_PARALLEL`, or `TEST_TIMEOUT` harness variables. Managed-server stdout
and stderr tails are included in startup and early-exit failures.

## Directory map

```text
integration_tests/
├── fixtures/
│   ├── assistant/       generated MCP fixture and SDK/JSON-RPC tests
│   └── agent_features/  generated agent DSL/runtime acceptance fixture
├── framework/           scenario runner, fixture preparation, and conformance tests
├── scenarios/           YAML protocol, tool, resource, prompt, CLI, and notification cases
└── tests/               YAML scenario entry points
```

The managed runner prepares an isolated temporary assistant fixture, builds one
server binary, waits for it to become ready, and cleans up processes and test
artifacts through `testing.T.Cleanup` and package `TestMain` hooks. Parallel
scenario groups never edit the shared fixture in place.

## YAML scenarios

Scenario files use a multi-step format:

```yaml
scenarios:
  - name: initialize_basic
    defaults:
      client: jsonrpc.mcp_assistant
      headers:
        Accept: application/json
    pre:
      auto_initialize: false
    steps:
      - name: initialize
        op: Initialize
        input:
          protocolVersion: "2025-11-25"
          capabilities: {}
          clientInfo: { name: "test-client", version: "1.0.0" }
        expect:
          status: success
          result:
            protocolVersion: "2025-11-25"
```

The principal fields are:

- `defaults.client`: generated client hint; current HTTP scenarios use
  `jsonrpc.mcp_assistant`.
- `defaults.client_mode`: `http` or `cli`.
- `defaults.headers`: headers inherited by every step.
- `pre.auto_initialize`: whether the runner initializes the MCP session before
  executing steps; defaults to true.
- `steps[].op`: generated endpoint operation such as `Initialize`, `ToolsList`,
  or `ToolsCall`.
- `steps[].input`: operation payload fields.
- `steps[].notification`: send a JSON-RPC notification with no request ID.
- `steps[].expect`: non-streaming status/result/error expectations.
- `steps[].stream_expect`: partial SSE event expectations.
- `steps[].expect_retry`: generated-client retry prompt expectations.

Expected maps are subset matches: scenarios need to state only the fields that
matter to the contract under test.

## Coverage surfaces

The YAML suite covers initialization/version negotiation, JSON-RPC errors,
tools, resources, prompts, notifications, SSE behavior, and generated-client
retry guidance. Go integration tests additionally cover:

- official SDK Streamable HTTP sessions;
- sampling, elicitation, roots, progress, and prompt completion;
- OAuth discovery, challenges, audience binding, proxy trust, and session
  principal continuity;
- origin protection, runtime CORS, transport observation, and cancellation;
- compact tool discovery and projected tools;
- SDK/native JSON-RPC parity, metadata, unions, and schema preservation;
- pure JSON-RPC code generation and official Go SDK conformance;
- generated agent toolsets, memory, artifacts, workflows, and registry
  capability behavior.

Do not maintain a separate "pending features" checklist here. Add a scenario or
fixture test for a new contract and describe its surface in this section once
the test is green.

## Adding coverage

1. Choose the smallest owner: YAML scenario, `framework/`, assistant fixture,
   or agent-features fixture.
2. For protocol behavior, add or update the relevant file under `scenarios/`
   and its entry-point test under `tests/` only when a new group is required.
3. For generated SDK/native behavior, add a focused test in the assistant
   fixture or framework.
4. For DSL-to-runtime agent behavior, extend the `agent_features` fixture and
   regenerate it with `make regen-agent-feature-fixture`.
5. Run the focused test, then `make itest` before submitting the change.

Generated fixture files remain generator-owned. Change the corresponding
design or generator and regenerate; do not patch `gen/` by hand.

When the framework runner needs application-owned replacement wiring, keep the
Go program under `framework/testdata/` and embed/copy that checked-in fixture.
Do not place large Go source strings in the runner: ordinary fixture files are
reviewable, format-checkable, and reusable by focused harness tests.
