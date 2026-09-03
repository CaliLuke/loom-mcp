# Roadmap

Updated 2026-09-02.

This is the single source of truth for planned work that is not complete.
GitHub issues remain the source of truth for individual defects. Product and
architecture work belongs here instead of in separate plan files.

## Planning rules

- Start each batch with a current-contract inventory naming the affected
  structs, files, generated surfaces and invariants.
- Add a real client, generated-code or runtime contract test before changing
  behavior.
- Treat design packages as source. Regenerate `gen/`; never edit it directly.
- Keep workstreams separate unless one strictly depends on another.
- Remove a workstream from this document when its full verification ladder is
  green.

## Ready workstreams


### Durable cross-workflow continuations

Source: `goadesign/goa-ai` `v0.76.11` and its continuation fixes.

Current gap:

- Clarification, confirmation, typed-input and external-tool waits keep the
  current workflow open and resume through engine signals.
- The runtime has no immutable suspension contract, `Continue` API or
  `StartContinuation` API for resuming work in a new workflow.
- Terminal policy fields live in `RunInput.Policy`; continuations must copy and
  revalidate them.

Implement:

- Complete a workflow successfully when it requires clarification,
  confirmation, typed input, questions or external tool results.
- Store an immutable, versioned suspension in application-owned persistent
  storage.
- Add `Continue` and `StartContinuation` client APIs that start a new workflow
  on the current worker release.
- Bind the session, predecessor run, new run, turn, pending request, original
  model tool-call identity, runtime execution identity and required tools
  exactly.
- Restore the transcript and outstanding bounded-result chains as the
  successor run's replay seed.
- Revalidate saved payloads, results, terminal policies and tool registrations
  before scheduling successor work.
- Claim each suspension atomically. Duplicate responses must fail closed.

Primary owners:

- `runtime/agent/runtime`
- `runtime/agent/engine`
- `features/session`
- `features/runlog`
- agent DSL, expressions and generated registrations
- `integration_tests/fixtures/agent_features`

Proof:

- Focused contracts cover version checks, binding mismatches, duplicate claims
  and revalidation failures.
- Generated acceptance scenarios cover every supported wait type.
- A real Temporal worker-replacement test proves that a continuation created by
  one worker release resumes on the next release without keeping the old
  workflow open.

## Product decisions before engineering

These gaps are real, but implementation should not start until the named
decision has a concrete consumer.

### MCP OAuth transport follow-ups

Decision: choose which layer owns operation-specific HTTP authentication
responses. The generated `MCPAdapter` can inspect `TokenInfo.Scopes`, but an MCP
method handler has no `http.ResponseWriter`. The official SDK's bearer
middleware can emit 401 and 403 responses, but it runs before `tools/call` is
decoded and accepts only one static required-scope set.

Do not add tool-level scope enforcement until one of these contracts is
accepted:

- the official SDK exposes a transport hook for operation-specific scope
  checks; or
- `loom-mcp` owns an outer HTTP middleware that parses and restores MCP request
  bodies before the SDK.

If approved:

- Add tool-level `RequireScope(...)`.
- Return an RFC 6750 403 `insufficient_scope` challenge with the minimum scopes
  required for the operation and the Protected Resource Metadata URL.
- Preserve the current 401 discovery path for missing and invalid tokens.
- Replace the split `RequireBearerToken` and `WithOAuthChallenge` path only if
  the chosen owner can also emit the advisory `error="invalid_token"`
  parameter.
- Add OIDC issuer discovery only when a concrete client requires it. Prefer
  advertising the issuer over proxying its discovery document.

Client ID Metadata Documents require no protected-resource implementation in
`loom-mcp`. The authorization server advertises support through
`client_id_metadata_document_supported` in its Authorization Server Metadata,
and the MCP client hosts its own metadata document. Do not emit the
non-standard `client_registration_types_supported` field in Protected Resource
Metadata.

Proof after the transport decision:

- Start each protocol change with a client-versus-framework test against the
  bundled specification.
- Cover missing tokens, insufficient scopes, sufficient scopes and each
  supported discovery configuration.

### Generated agent evaluations

Source: `goadesign/goa-ai` `v0.78.6`, especially `eval/` and
[`docs/evals.md`](https://github.com/goadesign/goa-ai/blob/v0.78.6/docs/evals.md).

Decision: agree on the local DSL, package ownership, judge contract, report
format and generated command boundary before implementation. The upstream
design is input to that discussion, not an accepted local architecture.

Current gap:

- `loom-mcp` has framework tests and generated acceptance fixtures, but no
  product eval DSL, generated eval suites, evidence collector, model judge or
  programmatic eval runner.

If approved:

- Add evaluation DSL and expressions for suites, scenarios, descriptions,
  typed inputs, tags and timeouts.
- Generate `gen/evals/<suite>` contracts with one hook per scenario, validated
  concrete inputs and typed access to the agent's generated tool contracts.
- Add a programmatic runner with scenario/tag selection, bounded concurrency,
  timeouts, deterministic checks, model-judged claims and JSON reports.
- Add evidence collection for assistant output, canonical tool calls, nested
  child calls, confirmation state and terminal workflow phase.
- Add typed tool expectations using generated payload and result codecs.
- Generate an application-owned `cmd/<suite>-evals` example once without
  overwriting later edits.
- Add an integration fixture that runs a real generated agent evaluation.

Primary owners:

- new top-level `eval`, `eval/dsl`, `eval/expr`, `eval/codegen`,
  `eval/evidence` and `eval/judge` packages
- agent codegen data needed to expose reachable tool contracts
- a generated evaluation integration fixture
- `docs/`

Proof:

- DSL tests reject invalid names, missing descriptions, invalid timeouts and
  unsupported input shapes.
- Generated suites compile and fail compilation when a scenario hook is
  missing.
- Runner tests cover filtering, concurrency, timeout, malformed results,
  infrastructure errors, judge verdicts, report JSON and failing exit status.
- Evidence tests cover causal child-tool ordering, duplicate IDs, malformed
  events, terminal states and typed argument/result checks.
- The fixture executes through both the programmatic runner and generated
  command.

Keep environment simulation separate from this first contract. Evals measure
agent behavior; simulation controls faults and responses.

### Persistent session state

Decision: choose the state-store contract and required scopes: session, user,
application and temporary run state.

If approved:

- Keep state separate from long-term memory and transcript storage.
- Replace `noopAgentState` with a runtime-backed implementation when configured.
- Record mutations through hook/run events for replay and debugging.
- Add durable Mongo support only after the core contract is stable.

### A2A protocol interoperability

Decision: accept A2A as a product goal or record it as a non-goal.

If approved:

- Expose a Loom agent through an agent card and HTTP handler.
- Consume a remote A2A agent as a model-facing tool or agent-as-tool.
- Preserve Loom run and session identities in metadata.
- Keep A2A ownership separate from MCP codegen.

### Durable long-term-memory backends

Decision: choose a real storage and retrieval requirement.

If approved:

- Add a Postgres-backed `memory.Service`.
- Add Mongo or provider/RAG-backed services only for a concrete deployment.
- Decide whether automatic ingest is runtime policy or an application
  callback/interceptor.

### Evaluation environment simulation

Decision: define the deterministic fault model needed by eval consumers.

If approved:

- Add interceptor-backed tool errors, fixed responses, argument predicates and
  latency.
- Keep probabilistic behavior explicitly seeded and report the seed.
- Reuse real runtime and generated fixtures instead of building a fake agent
  framework.

### Artifact scoping and versioning

Decision: require a cross-session user-file or versioned-asset use case.

If approved, add named versions and user/session scopes without changing
run-scoped artifacts.

### Live bidirectional multimodal sessions

Decision: require a voice/video product target.

If approved, design a separate live-session interface with explicit binary
media transport. Reuse interruption and stream concepts without stretching
the current event-stream contract.

### Agent configuration and generic runners

Decision: document generic YAML/Agent Config and web/API runners as non-goals,
or name a consuming workflow.

If approved, compile the new input form into existing design and runtime
contracts rather than creating a second runtime.

### Resources-as-tools compatibility

Decision: confirm a tool-only MCP client that must consume resources.

If approved:

- Keep MCP resources as the source of truth.
- Expose opt-in `list_resources` and `read_resource` compatibility tools.
- Route bridge calls through the same authorization, visibility, URI-template
  matching and resource implementation as native resource requests.
- Encode binary content safely without duplicating application handlers.

### Prompts-as-tools compatibility

Decision: confirm a tool-only MCP client that must consume prompts.

If approved:

- Keep MCP prompts as the source of truth.
- Expose opt-in `list_prompts` and `get_prompt` compatibility tools.
- Preserve prompt arguments, structured messages, authorization and visibility.
- Reuse native prompt providers rather than duplicating application handlers.

### MCP background tasks

Decision: adopt the current successor to SEP-1686 after validating the bundled
MCP specification and official Go SDK support.

If approved:

- Expose protocol-native task creation, status, progress and result retrieval
  for long-running tools.
- Map task lifecycle onto the existing runtime and engine instead of adding a
  second scheduler.
- Preserve durable behavior with Temporal and state the weaker in-memory
  guarantees explicitly.
- Add resources or prompts only after the tool contract is stable.

### MCP client-configuration export

Decision: choose ownership between the Loom CLI and `loom-mcp` codegen.

If approved:

- Export standard `mcpServers` JSON for stdio-launched generated servers.
- Support command, arguments, environment and explicit file or stdout output.
- Emit absolute executable paths where clients require them.
- Do not edit client configuration files implicitly; client-specific merge
  helpers remain follow-up work.

## Open architecture issues

- [#276: replace large generated SDK servers with a typed runtime bridge](https://github.com/CaliLuke/loom-mcp/issues/276)
- [#275: support capability-aware projection of rich `ToolSpec` features](https://github.com/CaliLuke/loom-mcp/issues/275)
- [#274: evaluate retiring or isolating the Loom-native MCP JSON-RPC transport](https://github.com/CaliLuke/loom-mcp/issues/274)
- [#271: enrich SDK transport observation with parsed MCP envelope metadata](https://github.com/CaliLuke/loom-mcp/issues/271)

Resolve architecture decisions before implementation where an issue changes
ownership between generated code, runtime code and the official SDK.

## Defect backlog

GitHub issues are canonical; copying individual defects here would create two
status sources. As of 2026-09-02, the repository has 59 open issues:

- [28 medium-severity defects](https://github.com/CaliLuke/loom-mcp/issues?q=is%3Aissue+state%3Aopen+label%3Aseverity%3Amedium)
- [27 low-severity defects](https://github.com/CaliLuke/loom-mcp/issues?q=is%3Aissue+state%3Aopen+label%3Aseverity%3Alow)
- four open architecture issues listed above

Fix medium-severity defects before low-severity work unless a low-severity
defect blocks a committed workstream.

## Trigger-only refactoring

Do not schedule generic cleanup as an independent workstream. When a contract,
DSL, codegen or fixture change is already in progress:

- name the design source, evaluated expression, generator data, generated owner
  and runtime consumer;
- remove duplicate contract interpretation in the touched path;
- keep refactoring separate from behavior changes;
- stop if an abstraction adds more indirection than it removes.

## Explicit non-goals

- Do not port Goa-AI's MCP transport or generator stack. The official MCP Go
  SDK remains the sole wire owner.
- Do not bundle JWT/JWKS verification or an authorization server.
- Do not add dynamic OAuth client registration.
- Do not make agent-as-tool inline. Child agents remain real child workflows.
- Do not claim that Temporal activities or registry tool calls run exactly
  once.
- Do not retain compatibility aliases indefinitely. Use explicit workflow and
  wire migrations with fail-fast version checks.
- Do not expose rejected model text, tool names, arguments or raw schemas in
  public errors, telemetry or durable correction evidence.
- Do not weaken generated codecs with repair, coercion, truncation or
  heuristic parsing.

## Verification ladder

Every implemented workstream starts with focused red-green contract tests and
ends with:

1. `make loom-local`
2. Regenerate affected outputs intentionally.
3. `make verify-mcp-local`
4. `make lint`
5. `make test`
6. `make itest`
7. `make test-docker` when registry, persistence, Pulse or worker replacement
   changes.
8. `make test-stress` when concurrency or lifecycle behavior changes.
9. `make loom-remote`
10. `make verify-generated`

Any red required gate blocks completion. Report confirmed upstream Loom
regressions upstream rather than patching around them in this repository.
