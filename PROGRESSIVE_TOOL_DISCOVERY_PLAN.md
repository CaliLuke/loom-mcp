# Progressive Tool Discovery Plan

Generated MCP servers will add opt-in progressive discovery without inventing a protocol method. Disabled mode keeps the full `tools/list` catalog. Enabled mode makes `tools/list` the authoritative compact public surface, exposes synthetic `search_tools` and `call_tool` plus validated `AlwaysVisible` real tools, rejects direct calls to hidden real tools by default, and returns Tool-shaped search descriptors that instruct clients to invoke targets through `call_tool`. The test strategy is fixture-first: unit and generator tests protect local contracts, but acceptance depends on regenerating `integration_tests/fixtures/assistant` and proving behavior through generated adapter, generated JSON-RPC, and generated SDK integration tests.

## Status

- 2026-07-06 — Plan created.
- 2026-07-06 — Fresh-agent review found sequencing, SDK, outputSchema, direct hidden-call, collision, proxy-proof, docs-scope, and final-gate blockers; plan rewritten to separate DSL, generated contracts, JSON-RPC adapter behavior, SDK behavior, docs, review, and final verification.
- 2026-07-06 — Focused review found SDK `AllowDirectHiddenCalls` inconsistency and an unassigned old direct-call test; plan updated to make SDK compact mode reject direct-hidden compatibility and to require fixture-first generated integration coverage.
- 2026-07-06 — Second focused review found missing spec/SDK contract inventory and generated JSON-RPC wire tests; plan updated to add both before development.
- 2026-07-06 — Fourth focused review found proxy dispatch could re-enter the hidden-tool public gate, synthetic `search_tools` outputSchema lacked a named proof, and generated fixture compile proof was missing after Tool DTO regeneration; plan updated to require a private real-tool execution helper and those proofs.
- 2026-07-06 — Fifth focused review found generated JSON-RPC compact-mode setup, proxy telemetry semantics, and positive query/pattern structured-output proofs were still implicit; plan updated to pin those contracts.
- 2026-07-06 — Focused fresh-agent readiness review returned READY on test strategy and sequencing: regenerated assistant fixture, generated JSON-RPC wire tests, SDK integration tests, red-before-green ordering, and full local Loom/repo gates are explicit enough to start development. Non-blocking note: keep first fixture tests narrow and run them immediately after regeneration to isolate codegen, runtime, and wire failures.
- 2026-07-06 — Execution started. Workflow recited before code edits; `.agents/skills/loom-mcp/SKILL.md`, `.agents/skills/loom-mcp/references/codegen-contracts.md`, and `/Users/luca/code/skills/skills/design-docs-execution-plans/SKILL.md` were read. Generated `gen/` files will be regenerated, not hand-edited.
- 2026-07-06 — MCP 2025-06-18 contract inventory: `tools/list` and `tools/call` remain the protocol methods for discovery and invocation; `Tool` includes `title`, `outputSchema`, and `_meta`; tool-originated failures return `isError: true`. Local SDK inventory used `github.com/modelcontextprotocol/go-sdk v1.6.1` from `/Users/luca/go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp/protocol.go`.
- 2026-07-06 — Implementation owners inspected: `expr/mcp/mcp.go`, `dsl/mcp.go`, `codegen/mcp/mcp_types.go`, `codegen/mcp/adapter_generator.go`, `codegen/mcp/adapter_tools_jennifer.go`, `codegen/mcp/adapter_core_jennifer.go`, `codegen/mcp/sdk_server_file.go`, `integration_tests/fixtures/assistant/design/design.go`, and `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`.
- 2026-07-06 — Red DSL/expression contract tests added as `TestMCPToolDiscoveryMetadataOptions`; `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1` fails on missing `ToolTitle`, `ToolDiscoveryCategory`, `ToolDiscoveryTags`, `ToolDiscoveryKeywords`, and missing `ToolExpr` metadata fields.
- 2026-07-06 — Milestone 2 implemented: `ToolExpr` and MCP DSL now carry title/category/tags/keywords, generated `ToolInfo` includes `title`, `outputSchema`, and `_meta`, adapter data derives discovery `_meta` and object result output schemas, assistant fixture metadata was added and regenerated. Proofs passed: `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1`, `make regen-assistant-fixture`, `go test ./integration_tests/fixtures/assistant -run '^$' -count=1`, and `go test ./codegen/mcp -run 'TestGeneratedToolInfoIncludesMCPDiscoveryFields|TestGeneratedToolInfoConversionsPreserveDiscoveryFields|TestGeneratedSearchToolsHasOutputSchema' -count=1`.
- 2026-07-06 — Milestone 3 implemented: generated JSON-RPC adapters now expose compact authoritative `tools/list`, validate ToolSearch construction, reject direct hidden calls by default, invoke hidden targets through a private real-tool dispatcher, return ranked `search_tools` descriptors with metadata/schema controls, preserve proxy validation/error/context/interceptor behavior, and record `mcp.target_tool` telemetry on proxy calls. Proof passed: `go test ./codegen/mcp ./integration_tests/fixtures/assistant -run 'TestGeneratedToolInfoIncludesMCPDiscoveryFields|TestGeneratedToolInfoConversionsPreserveDiscoveryFields|TestGeneratedSearchToolsHasOutputSchema|TestGenerateSDKServerRendersToolSearchSyntheticTools|TestGeneratedAdapterToolSearch|TestGeneratedJSONRPCToolSearch|TestGeneratedSDKServerToolSearch' -count=1`.
- 2026-07-06 — Milestone 4 implemented: SDK-backed compact mode registers synthetic tools plus validated pins, sets full SDK Tool fields, invokes hidden tools through `call_tool`, rejects direct hidden calls, and fails construction when `AllowDirectHiddenCalls` is true. Proof included in the focused command above.
- 2026-07-06 — Milestone 5 implemented: `docs/runtime.md`, `docs/dsl.md`, `docs/mcp_sdk_server.md`, `.agents/skills/loom-mcp/SKILL.md`, and `.agents/skills/loom-mcp/references/codegen-contracts.md` now describe progressive discovery. `rg -n "direct.*tools/call|search_tools|call_tool|ToolSearchOptions|large-catalog|progressive discovery" docs .agents/skills/loom-mcp` is aligned; surviving matches under `docs/plans/2026-03-30-mcp-protected-resource-discovery-design.md` are historical plan text and intentionally stale.
- 2026-07-06 — Milestone 6 review completed. Fresh reviewer found three blockers: omitted `search_tools` arguments failed despite an optional input schema; descriptor wording said Tool-shaped while implementation was compact custom-only; and final full gates were still open. The first was fixed by normalizing blank `search_tools` arguments to `{}` and adding adapter, JSON-RPC, and SDK regressions. The second was fixed by preserving MCP Tool-shaped descriptor fields (`inputSchema`, `outputSchema`, `_meta`, `annotations`, `icons`) while keeping extracted discovery fields. The third remains the planned Milestone 7 work. Rerun proofs passed: `go test ./codegen/mcp ./integration_tests/fixtures/assistant -run 'TestGeneratedToolInfoIncludesMCPDiscoveryFields|TestGeneratedToolInfoConversionsPreserveDiscoveryFields|TestGeneratedSearchToolsHasOutputSchema|TestGenerateSDKServerRendersToolSearchSyntheticTools|TestGeneratedAdapterToolSearch|TestGeneratedJSONRPCToolSearch|TestGeneratedSDKServerToolSearch' -count=1` and reviewer-scoped `git diff --check`.
- 2026-07-06 — Milestone 7 completed. Starting and ending Loom mode were remote `github.com/CaliLuke/loom v1.2.0` for root, fixture, and quickstart. Passed in order: `make loom-status`, `make loom-local`, `make verify-mcp-local`, `make loom-remote`, `make loom-status`, `go fmt ./...`, `make build`, `make lint`, `make test`, and `make itest`. Final status shows intended progressive discovery source/docs/generated/test changes plus intended untracked `PROGRESSIVE_TOOL_DISCOVERY_PLAN.md` and rendered `PROGRESSIVE_TOOL_DISCOVERY_PLAN.html`; unrelated untracked local dirt left untouched: `ADK_PRODUCT_GAPS.md` and `docs/plans/2026-07-06-memory-service-plan.md`.

## Milestones

### Milestone 1: Contract Inventory And Red Tests

Toc: Inventory

Goal: Establish exact owners and failing contract tests before implementation code changes.

Acceptance Criteria

- The executing agent has recited the workflow on the record before any code edits.
- This plan status records the target MCP contract from `third_party/modelcontextprotocol/docs/specification/2025-06-18/server/tools.mdx`, `third_party/modelcontextprotocol/docs/specification/2025-06-18/schema.mdx`, and the local `github.com/modelcontextprotocol/go-sdk v1.6.1` `mcp.Tool` type.
- `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1` fails because the new MCP discovery metadata DSL and expression fields do not exist.

Checklist

- [x] Read this plan end-to-end, read the linked skill at `/Users/luca/code/skills/skills/design-docs-execution-plans/SKILL.md`, then recite the workflow you will follow — milestone order, exit criteria, named commands in execution order, test-first rule, peer-review gate, commit/push handoff, and any inherited repo constraints. Do not edit code before this recital is on the record.
- [x] Read `.agents/skills/loom-mcp/SKILL.md` and `.agents/skills/loom-mcp/references/codegen-contracts.md`; record in this plan status that generated `gen/` files are regenerated, not hand-edited.
- [x] Inspect `third_party/modelcontextprotocol/docs/specification/2025-06-18/server/tools.mdx`, `third_party/modelcontextprotocol/docs/specification/2025-06-18/schema.mdx`, and `/Users/luca/go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp/protocol.go`; record in this plan status that `tools/list` and `tools/call` remain the only protocol methods, `Tool` includes `title`, `outputSchema`, and `_meta`, and tool-originated failures return `isError: true`.
- [x] Inspect `expr/mcp/mcp.go`, `dsl/mcp.go`, `codegen/mcp/mcp_types.go`, `codegen/mcp/adapter_generator.go`, `codegen/mcp/adapter_tools_jennifer.go`, `codegen/mcp/adapter_core_jennifer.go`, `codegen/mcp/sdk_server_file.go`, `integration_tests/fixtures/assistant/design/design.go`, and `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`; record these as the implementation owners in this plan status.
- [x] Add failing DSL and expression tests named `TestMCPToolDiscoveryMetadataOptions` proving `ToolTitle`, `ToolDiscoveryCategory`, `ToolDiscoveryTags`, and `ToolDiscoveryKeywords` populate MCP `ToolExpr` metadata.
- [x] Run `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and keep the failure output tied to the missing DSL and expression fields.

### Milestone 2: DSL And Generated Tool Shape

Toc: Tool Shape

Goal: Add first-class MCP discovery metadata and generate complete MCP Tool fields through JSON-RPC DTOs.

Acceptance Criteria

- `expr/mcp.ToolExpr` has `Title string`, `DiscoveryCategory string`, `DiscoveryTags []string`, and `DiscoveryKeywords []string`, and `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1` passes.
- Generator tests in `codegen/mcp` assert generated source contains `title`, `outputSchema`, `_meta`, synthetic `search_tools` outputSchema, plus conversion paths for generated service, JSON-RPC server, and JSON-RPC client Tool DTOs.
- `integration_tests/fixtures/assistant/gen/mcp_assistant/service.go`, `gen/jsonrpc/mcp_assistant/server/types.go`, and `gen/jsonrpc/mcp_assistant/client/types.go` regenerate with `title`, `outputSchema`, and `_meta` Tool fields matching MCP JSON names.

Checklist

- [x] Extend `expr/mcp/mcp.go` `ToolExpr` with `Title string`, `DiscoveryCategory string`, `DiscoveryTags []string`, and `DiscoveryKeywords []string`.
- [x] Add MCP tool option helpers in `dsl/mcp.go`: `ToolTitle`, `ToolDiscoveryCategory`, `ToolDiscoveryTags`, and `ToolDiscoveryKeywords`.
- [x] Run `go test ./dsl ./expr/mcp -run TestMCPToolDiscoveryMetadataOptions -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Add generator contract tests in `codegen/mcp` named `TestGeneratedToolInfoIncludesMCPDiscoveryFields`, `TestGeneratedToolInfoConversionsPreserveDiscoveryFields`, and `TestGeneratedSearchToolsHasOutputSchema`.
- [x] Update `codegen/mcp/mcp_types.go` so `buildToolInfoType` emits optional `title`, `outputSchema`, and `_meta` fields in addition to name, description, inputSchema, annotations, and icons.
- [x] Extend `codegen/mcp/adapter_generator.go` `ToolAdapter` with `Title`, `DiscoveryCategory`, `DiscoveryTags`, `DiscoveryKeywords`, `MetaJSON`, and `OutputSchema`.
- [x] Generate `ToolAdapter.Title` from `ToolExpr.Title` with `codegen/naming.HumanizeTitle(tool.Name)` as the fallback.
- [x] Generate `ToolAdapter.MetaJSON` with `_meta["com.github.caliluke.loom-mcp/discovery"]` containing category, tags, and keywords, and never generate `io.modelcontextprotocol/*` custom keys.
- [x] Generate `ToolAdapter.OutputSchema` with `shared.ToJSONSchema(tool.Method.Result)` only when the result attribute unwraps to `*expr.Object`, including named result types backed by objects; omit `outputSchema` for nil results and primitive, array, map, and union result shapes.
- [x] Update `codegen/mcp/adapter_tools_jennifer.go` `toolInfoValue`, `toolSearchToolInfo`, and `toolCallProxyToolInfo` so real and synthetic ToolInfo values set title, input schema, output schema, annotations, `_meta`, and icons consistently.
- [x] Update `integration_tests/fixtures/assistant/design/design.go` to declare metadata on `analyze_sentiment`, `extract_keywords`, and `search` with `ToolTitle`, `ToolDiscoveryCategory`, `ToolDiscoveryTags`, and `ToolDiscoveryKeywords`.
- [x] Run `make regen-assistant-fixture` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test ./integration_tests/fixtures/assistant -run '^$' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` to prove regenerated service, server, and client DTOs compile before runtime behavior tests.
- [x] Run `go test ./codegen/mcp -run 'TestGeneratedToolInfoIncludesMCPDiscoveryFields|TestGeneratedToolInfoConversionsPreserveDiscoveryFields|TestGeneratedSearchToolsHasOutputSchema' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 3: JSON-RPC Progressive Discovery

Toc: JSON-RPC

Goal: Replace the old regex-first adapter search with compact JSON-RPC progressive discovery and default-hidden direct-call behavior.

Acceptance Criteria

- `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go` passes named direct-adapter tests for disabled full list, compact public list, validated pins, collision panics, direct hidden-call protocol errors, proxy invocation through the private real-tool execution path, ranking, invalid input, text guidance, structured content, synthetic `search_tools` outputSchema, and proxy-preserved execution path.
- `integration_tests/fixtures/assistant/mcp_generated_jsonrpc_tool_search_test.go` passes generated JSON-RPC server/client tests for compact list wire shape, direct hidden-call protocol error, `call_tool` hidden invocation, plain `query` search, and `search_tools` structured descriptors.
- `search_tools` returns useful text with `invoke: call_tool name="<tool>"`, structured content with `tools`, `total_matches`, `truncated`, and the supplied `query` or `pattern` field, and `isError: true` for invalid regex or query-plus-pattern input.

Checklist

- [x] Add red fixture tests in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`: `TestGeneratedAdapterToolSearchListsFullCatalogWhenDisabled`, `TestGeneratedAdapterToolSearchCompactsPublicCatalog`, `TestGeneratedAdapterToolSearchRejectsUnknownAlwaysVisibleTool`, `TestGeneratedAdapterToolSearchPanicsOnSyntheticNameCollision`, and `TestGeneratedAdapterToolSearchOverrideNames`.
- [x] Rewrite existing `TestGeneratedAdapterToolSearchDoesNotBlockDirectToolCalls` in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go` into tests named `TestGeneratedAdapterToolSearchRejectsDirectHiddenCallsByDefault` and `TestGeneratedAdapterToolSearchAllowsDirectHiddenCallsWhenCompatEnabled`.
- [x] Add red fixture tests in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`: `TestGeneratedAdapterToolSearchAlwaysVisibleToolRemainsDirectlyCallable`, `TestGeneratedAdapterToolSearchCallToolInvokesHiddenTool`, and `TestGeneratedAdapterToolSearchCallToolInvokesHiddenToolDespiteDirectHiddenGate`.
- [x] Add red fixture tests in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`: `TestGeneratedAdapterToolSearchSearchesQueryText`, `TestGeneratedAdapterToolSearchRanksBeforeLimiting`, `TestGeneratedAdapterToolSearchFiltersByCategory`, `TestGeneratedAdapterToolSearchFiltersByTags`, `TestGeneratedAdapterToolSearchOmitsSchemasByDefault`, `TestGeneratedAdapterToolSearchIncludesSchemasWhenRequested`, `TestGeneratedAdapterToolSearchRejectsQueryAndPattern`, `TestGeneratedAdapterToolSearchRejectsInvalidRegex`, `TestGeneratedAdapterToolSearchReturnsModelReadableText`, `TestGeneratedAdapterToolSearchReturnsStructuredDescriptors`, `TestGeneratedAdapterToolSearchStructuredContentIncludesPattern`, and `TestGeneratedAdapterToolSearchSyntheticToolHasOutputSchema`.
- [x] Add red fixture tests in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`: `TestGeneratedAdapterToolSearchProxyPreservesToolContext`, `TestGeneratedAdapterToolSearchProxyPreservesValidationErrors`, `TestGeneratedAdapterToolSearchProxyPreservesErrorMapping`, `TestGeneratedAdapterToolSearchProxyRecordsProxyAndTargetTelemetryAttributes`, and `TestGeneratedAdapterToolSearchProxyPreservesRequestContext`.
- [x] Update or add a generated JSON-RPC test helper in `integration_tests/fixtures/assistant/mcp_generated_server_test_helpers.go` that accepts `*mcpassistant.MCPAdapterOptions` and starts the generated JSON-RPC server with `ToolSearch` enabled for compact-mode tests.
- [x] Add red generated JSON-RPC fixture tests in new file `integration_tests/fixtures/assistant/mcp_generated_jsonrpc_tool_search_test.go`: `TestGeneratedJSONRPCToolSearchUsesCompactCatalog`, `TestGeneratedJSONRPCToolSearchRejectsDirectHiddenCalls`, `TestGeneratedJSONRPCToolSearchCallToolInvokesHiddenTool`, `TestGeneratedJSONRPCToolSearchSearchesQueryText`, `TestGeneratedJSONRPCToolSearchStructuredContentIncludesPattern`, and `TestGeneratedJSONRPCToolSearchReturnsStructuredDescriptors`.
- [x] Update `ToolSearchOptions` in `codegen/mcp/adapter_core_jennifer.go` with `AllowDirectHiddenCalls bool` documented as a compatibility option outside the compact public discovery contract.
- [x] Add generated construction-time validation in `codegen/mcp/adapter_core_jennifer.go` so `NewMCPAdapter` panics when synthetic names match each other, collide with generated real tools, or `AlwaysVisible` names do not match generated real tools.
- [x] Update `toolSearchPayload` in `codegen/mcp/adapter_tools_jennifer.go` to include `Query string`, `Pattern string`, `MaxResults *int`, `IncludeSchemas bool`, `Category string`, and `Tags []string`.
- [x] Add helper generation in `codegen/mcp/adapter_tools_jennifer.go` for extracting parameter names and parameter descriptions from decoded JSON schema objects.
- [x] Implement deterministic ranking in `codegen/mcp/adapter_tools_jennifer.go`: exact name, name prefix or substring, title, tag/category/keyword, description, input parameter name, input schema or parameter description, then generated catalog order.
- [x] Update `handleSearchTools` in `codegen/mcp/adapter_tools_jennifer.go` to reject `query` plus `pattern`, reject invalid regex with `isError: true`, rank before applying `max_results`, omit schemas when `include_schemas` is false, and build text with `invoke: call_tool name="<tool>"`.
- [x] Split generated public and private tool dispatch in `codegen/mcp/adapter_tools_jennifer.go`: top-level public `ToolsCall` applies the compact hidden-tool gate, while a private real-tool execution helper dispatches generated real tools without re-entering that public gate.
- [x] Update `toolsCallHandler` in `codegen/mcp/adapter_tools_jennifer.go` so compact mode returns a protocol-level unknown tool error for direct public calls to hidden generated tools when `AllowDirectHiddenCalls` is false, while pinned `AlwaysVisible` tools remain directly callable.
- [x] Update `handleCallToolProxy` in `codegen/mcp/adapter_tools_jennifer.go` so synthetic targets and invalid target names return `isError: true`, and valid target calls use the private real-tool execution helper while preserving generated validation, interceptors, auth context, request context, and error mapping.
- [x] Update telemetry in `codegen/mcp/adapter_tools_jennifer.go` so `call_tool` proxy calls record both `mcp.tool=call_tool` for the public MCP call and `mcp.target_tool=<hidden-name>` for the proxied generated target; make `TestGeneratedAdapterToolSearchProxyRecordsProxyAndTargetTelemetryAttributes` assert both attributes.
- [x] Run `make regen-assistant-fixture` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test ./integration_tests/fixtures/assistant -run 'TestGeneratedAdapterToolSearch|TestGeneratedJSONRPCToolSearch' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 4: SDK Progressive Discovery

Toc: SDK

Goal: Make SDK-backed servers expose the same compact public discovery contract as generated JSON-RPC adapters.

Acceptance Criteria

- SDK server tests prove `SDKServerOptions.Adapter.ToolSearch` makes SDK `tools/list` return synthetic tools plus validated pins, not every generated real tool.
- SDK server tests prove SDK `tools/call` rejects direct hidden tools by default and invokes hidden real tools through `call_tool`.
- SDK server construction fails fast when `SDKServerOptions.Adapter.ToolSearch.AllowDirectHiddenCalls` is true, because unregistered SDK tools cannot be directly invoked while preserving compact authoritative `tools/list`.

Checklist

- [x] Add SDK fixture tests in new file `integration_tests/fixtures/assistant/mcp_generated_server_tool_search_test.go` named `TestGeneratedSDKServerToolSearchUsesCompactCatalog`, `TestGeneratedSDKServerToolSearchRejectsDirectHiddenCalls`, `TestGeneratedSDKServerToolSearchRejectsDirectHiddenCompatOption`, and `TestGeneratedSDKServerToolSearchCallToolInvokesHiddenTool`.
- [x] Add a generator test in `codegen/mcp` named `TestGenerateSDKServerRendersToolSearchSyntheticTools`.
- [x] Update `codegen/mcp/sdk_server_file.go` `registerSDKTools` generation so SDK servers register synthetic `search_tools` and `call_tool` plus `AlwaysVisible` real tools when `adapter.toolSearchEnabled()` is true.
- [x] Update `codegen/mcp/sdk_server_file.go` server construction so SDK compact mode rejects `AllowDirectHiddenCalls` with an actionable construction error.
- [x] Update `codegen/mcp/sdk_server_file.go` tool registration so SDK-backed servers set `mcpsdk.Tool.Title`, `OutputSchema`, embedded `Meta`, `Annotations`, `InputSchema`, `Name`, and `Icons`.
- [x] Run `make regen-assistant-fixture` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test ./codegen/mcp ./integration_tests/fixtures/assistant -run 'TestGenerateSDKServerRendersToolSearchSyntheticTools|TestGeneratedSDKServerToolSearch' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 5: Docs And Skill Contract

Toc: Docs

Goal: Update public docs and repo-local guidance so downstream servers can adopt generated progressive discovery without hand-written catalogs.

Acceptance Criteria

- `docs/runtime.md`, `docs/dsl.md`, and `docs/mcp_sdk_server.md` describe compact `tools/list` as the authoritative public surface, `call_tool` as the invocation path for hidden tools, validated `AlwaysVisible` pins, JSON-RPC direct hidden calls as an explicit compatibility option, and SDK direct hidden compatibility as unsupported in compact mode.
- `.agents/skills/loom-mcp/SKILL.md` and `.agents/skills/loom-mcp/references/codegen-contracts.md` record the progressive discovery contract and no longer state that hidden real tools remain directly callable by default.
- `rg -n "direct.*tools/call|search_tools|call_tool|ToolSearchOptions|large-catalog|progressive discovery" docs .agents/skills/loom-mcp` has each surviving non-archived match aligned with the new contract.

Checklist

- [x] Update `docs/runtime.md` section `Generated MCP tool search` to describe opt-in progressive discovery, compact authoritative `tools/list`, `query` and `pattern` input, category and tag filters, include_schemas behavior, structured content, text invocation guidance, collision validation, validated pins, JSON-RPC `AllowDirectHiddenCalls`, and SDK rejection of direct-hidden compatibility.
- [x] Update `docs/mcp_sdk_server.md` so `SDKServerOptions.Adapter.ToolSearch` describes compact SDK `tools/list`, synthetic search and call tools, and construction failure when `AllowDirectHiddenCalls` is true.
- [x] Update `docs/dsl.md` large-catalog discovery text so downstream users configure `MCPAdapterOptions.ToolSearch`, declare discovery metadata with MCP tool options, and invoke hidden tools through `call_tool`.
- [x] Update `.agents/skills/loom-mcp/SKILL.md` current product rules so future agents preserve compact authoritative `tools/list` semantics and do not re-enable direct hidden calls by default.
- [x] Update `.agents/skills/loom-mcp/references/codegen-contracts.md` with the generated Tool field and SDK parity contract for progressive discovery.
- [x] Run `rg -n "direct.*tools/call|search_tools|call_tool|ToolSearchOptions|large-catalog|progressive discovery" docs .agents/skills/loom-mcp` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and record archived historical docs under `docs/plans/` as intentionally stale when their text is historical.

### Milestone 6: Peer Review Reconciliation

Toc: Review

Goal: Get a fresh design/code review before final full gates and reconcile every blocker.

Acceptance Criteria

- A fresh reviewer inspects this plan and the changed code paths and returns no unresolved sequencing, design, implementation-owner, or proof-command blockers.
- This plan's status section records each reviewer blocker as applied, rejected with reason, or absent.
- Every code or plan change made after peer review has its smallest named proof command rerun before final full verification.

Checklist

- [x] Ask a fresh reviewer to inspect `PROGRESSIVE_TOOL_DISCOVERY_PLAN.md`, `expr/mcp/mcp.go`, `dsl/mcp.go`, `codegen/mcp/mcp_types.go`, `codegen/mcp/adapter_generator.go`, `codegen/mcp/adapter_tools_jennifer.go`, `codegen/mcp/adapter_core_jennifer.go`, `codegen/mcp/sdk_server_file.go`, `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`, `integration_tests/fixtures/assistant/mcp_generated_jsonrpc_tool_search_test.go`, `integration_tests/fixtures/assistant/mcp_generated_server_tool_search_test.go`, and docs changed in Milestone 5; require critique only.
- [x] Reconcile every reviewer blocker in code, tests, docs, or this plan.
- [x] Rerun the smallest proof command named by each reconciled blocker.
- [x] Re-render this tracker with `python3 /Users/luca/code/skills/skills/design-docs-execution-plans/render_plan.py PROGRESSIVE_TOOL_DISCOVERY_PLAN.md`.

### Milestone 7: Full Verification And Handoff

Toc: Handoff

Goal: Finish with intentional Loom mode, full repo gates, and a clear handoff state that preserves unrelated local dirt.

Acceptance Criteria

- `make loom-status`, `make loom-local`, `make verify-mcp-local`, `make loom-remote`, `make build`, `make lint`, `make test`, and `make itest` all pass from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- Final `git status --short --branch` records intended progressive discovery changes separately from pre-existing unrelated files.
- Commit and push remain out of scope until the user asks for them explicitly.

Checklist

- [x] Run `make loom-status` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and record the starting Loom mode.
- [x] Run `make loom-local` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make verify-mcp-local` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make loom-remote` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make loom-status` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and record that remote mode was restored before CI-facing gates.
- [x] Run `go fmt ./...` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make build` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make lint` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make test` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make itest` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `git status --short --branch` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and record intended progressive discovery files separately from unrelated local dirt.
- [x] Leave commit and push for a separate explicit user request.
