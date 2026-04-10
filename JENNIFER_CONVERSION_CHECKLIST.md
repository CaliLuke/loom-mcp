# Jennifer Conversion Checklist

Goal: replace template-backed Go code generation where Jennifer materially improves correctness, refactor safety, or generator maintainability. Do not rewrite docs/markdown or large static stubs just for style.

## Completed

- [x] MCP JSON-RPC server mount rewrite hardened against upstream section-type churn
- [x] MCP client caller generator (`client_caller_file.go` — fully Jennifer)
- [x] MCP register helper generator (`register_file.go` — fully Jennifer)
- [x] MCP SDK server generator (`sdk_server_file.go` — fully Jennifer, `sdk_server.go.tpl` removed)
- [x] MCP client adapter generator (`client_adapter_file.go` — fully Jennifer, `mcp_client_wrapper.go.tpl` removed)
- [x] MCP prompt provider generator (template removed, Jennifer-backed)
- [x] MCP adapter sections: resources, prompts, notifications, subscriptions, broadcast — all Jennifer-backed via `adapter_jennifer_sections.go`; standalone `.go.tpl` files removed
- [x] MCP adapter core generator (`adapter_core_jennifer.go` — Jennifer-backed; `adapter_core.go.tpl` no longer used by `generate.go`)
- [x] MCP adapter tools generator (`adapter_tools_jennifer.go` — Jennifer-backed; `adapter_tools.go.tpl` no longer used by `generate.go`)
- [x] MCP JSON-RPC top-level server sections now come from upstream Loom Jennifer generators; local `jsonrpc_server_*` template overrides removed
- [x] MCP example CLI JSON-RPC patcher now emits source from Jennifer-backed code in `example.go`; `cli_dojsonrpc.go.tpl` removed
- [x] `codegen/mcp/templates/client_retry_helpers.go.tpl` removed as dead template while migrating the client adapter surface

## Priority 3: High Value Agent Conversions

- [x] `codegen/agent/templates/agent.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/config.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/registry.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/registry_client.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/registry_client_options.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/mcp_executor.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/service_executor.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_provider.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/tool_transforms.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/used_tools.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/agent_tools.go.tpl` — Jennifer-backed; template removed
- [x] `codegen/agent/templates/agent_tools_consumer.go.tpl` — Jennifer-backed; template removed

Reason:

- These files mix control flow, imports, and many conditional branches.
- They are good Jennifer candidates if broken into small builders.

## Priority 4: Schema / Codec / Spec Conversions

- [x] `codegen/agent/templates/tool_codecs.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_spec.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_types.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_union_types.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_transport_types.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/tool_transport_validate.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/registry_toolset_specs.go.tpl` — code-backed render section; template removed
- [x] `codegen/agent/templates/specs_aggregate.go.tpl` — Jennifer-backed; template removed

Reason:

- Good candidates when split into focused builders.
- High payoff if type/reference branching keeps causing subtle template bugs.
- Lower priority than MCP transport/adapter work because current pain has been MCP-heavy.

Status:

- All active agent-side Go generator templates have now been removed.
- Remaining template files under `codegen/agent/templates/` are scaffold/docs stubs intentionally left as text.

## Keep As Templates Unless There Is Concrete Pain

- `codegen/agent/templates/agents_quickstart.go.tpl`
- `codegen/agent/templates/bootstrap_internal.go.tpl`
- `codegen/agent/templates/cmd_main.go.tpl`
- `codegen/agent/templates/example_executor_stub.go.tpl`
- `codegen/agent/templates/planner_internal_stub.go.tpl`
- `codegen/mcp/templates/example_mcp_stub.go.tpl`

Reason:

- Stub/doc/scaffold output is often easier to read and maintain as text.
- Jennifer does not automatically improve large static boilerplate.

## Migration Rules

- Convert one generator surface at a time.
- Add or strengthen generated-file assertions, not just helper-level tests.
- Prefer small Jennifer builders over giant chained monoliths.
- Remove downstream string patching when the owning generator can emit the final shape directly.
- Run `make verify-mcp-local`, `make lint`, `make test`, and `make itest` after each meaningful batch.
