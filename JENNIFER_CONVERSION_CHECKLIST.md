# Jennifer Conversion Checklist

Goal: replace template-backed Go code generation where Jennifer materially improves correctness, refactor safety, or generator maintainability. Do not rewrite docs/markdown or large static stubs just for style.

## Priority 0: In Progress / Done

- [x] MCP JSON-RPC server mount rewrite hardened against upstream section-type churn
- [x] MCP client caller generator
- [x] MCP register helper generator

## Priority 1: High Value MCP Conversions

- [ ] `codegen/mcp/templates/jsonrpc_server_struct.go.tpl`
- [ ] `codegen/mcp/templates/jsonrpc_server_init.go.tpl`
- [ ] `codegen/mcp/templates/jsonrpc_server_handler.go.tpl`
- [ ] `codegen/mcp/templates/jsonrpc_mixed_server_handler.go.tpl`
- [ ] `codegen/mcp/templates/jsonrpc_server_mount.go.tpl`
- [x] `codegen/mcp/templates/sdk_server.go.tpl`
- [ ] `codegen/mcp/templates/adapter_core.go.tpl`
- [ ] `codegen/mcp/templates/adapter_tools.go.tpl`
- [x] `codegen/mcp/templates/adapter_resources.go.tpl`
- [x] `codegen/mcp/templates/adapter_prompts.go.tpl`
- [x] `codegen/mcp/templates/adapter_notifications.go.tpl`
- [x] `codegen/mcp/templates/adapter_subscriptions.go.tpl`
- [x] `codegen/mcp/templates/adapter_broadcast.go.tpl`
- [x] `codegen/mcp/templates/client_retry_helpers.go.tpl` removed as dead template while migrating the client adapter surface

Reason:
- Transport and adapter code has branching, protocol rules, and upstream coupling.
- These files are where type/section churn hurts most.
- Jennifer should cut string surgery and make regressions easier to catch.

## Priority 2: Medium Value MCP Conversions

- [x] `codegen/mcp/templates/mcp_client_wrapper.go.tpl`
- [x] `codegen/mcp/templates/prompt_provider.go.tpl`
- [ ] `codegen/mcp/templates/cli_dojsonrpc.go.tpl`
- [ ] `codegen/mcp/templates/example_mcp_stub.go.tpl`

Reason:
- Useful, but less coupled to upstream Loom internals.
- Some are example/stub-heavy, so migration should only happen if helpers stay readable.

## Priority 3: High Value Agent Conversions

- [ ] `codegen/agent/templates/agent.go.tpl`
- [ ] `codegen/agent/templates/config.go.tpl`
- [ ] `codegen/agent/templates/registry.go.tpl`
- [ ] `codegen/agent/templates/registry_client.go.tpl`
- [ ] `codegen/agent/templates/registry_client_options.go.tpl`
- [ ] `codegen/agent/templates/mcp_executor.go.tpl`
- [ ] `codegen/agent/templates/service_executor.go.tpl`
- [ ] `codegen/agent/templates/tool_provider.go.tpl`
- [ ] `codegen/agent/templates/tool_transforms.go.tpl`
- [ ] `codegen/agent/templates/used_tools.go.tpl`

Reason:
- These files mix control flow, imports, and many conditional branches.
- They are good Jennifer candidates if broken into small builders.

## Priority 4: Schema / Codec / Spec Conversions

- [ ] `codegen/agent/templates/tool_codecs.go.tpl`
- [ ] `codegen/agent/templates/tool_spec.go.tpl`
- [ ] `codegen/agent/templates/tool_types.go.tpl`
- [ ] `codegen/agent/templates/tool_union_types.go.tpl`
- [ ] `codegen/agent/templates/tool_transport_types.go.tpl`
- [ ] `codegen/agent/templates/tool_transport_validate.go.tpl`
- [ ] `codegen/agent/templates/registry_toolset_specs.go.tpl`
- [ ] `codegen/agent/templates/specs_aggregate.go.tpl`

Reason:
- Good candidates when split into focused builders.
- High payoff if type/reference branching keeps causing subtle template bugs.
- Lower priority than MCP transport/adapter work because current pain has been MCP-heavy.

## Keep As Templates Unless There Is Concrete Pain

- `codegen/agent/templates/agents_quickstart.go.tpl`
- `codegen/agent/templates/bootstrap_internal.go.tpl`
- `codegen/agent/templates/cmd_main.go.tpl`
- `codegen/agent/templates/example_executor_stub.go.tpl`
- `codegen/agent/templates/planner_internal_stub.go.tpl`
- `codegen/mcp/templates/example_mcp_stub.go.tpl` if it stays mostly scaffold text

Reason:
- Stub/doc/scaffold output is often easier to read and maintain as text.
- Jennifer does not automatically improve large static boilerplate.

## Migration Rules

- Convert one generator surface at a time.
- Add or strengthen generated-file assertions, not just helper-level tests.
- Prefer small Jennifer builders over giant chained monoliths.
- Remove downstream string patching when the owning generator can emit the final shape directly.
- Run `make verify-mcp-local`, `make lint`, `make test`, and `make itest` after each meaningful batch.
