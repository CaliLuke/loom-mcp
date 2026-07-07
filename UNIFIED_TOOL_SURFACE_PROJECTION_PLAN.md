# Unified Tool Surface Projection Plan

Current contract inventory: `dsl/tool.go` routes `Tool(...)` into either `expr/agent.ToolExpr` for toolset tools or `expr/mcp.ToolExpr` for method-level MCP tools; method-level MCP metadata helpers live in `dsl/mcp.go`; `expr/agent/tool.go` owns `BindTo(...)`, `Inject(...)`, `ServerData(...)`, `Confirmation(...)`, `ResultReminder(...)`, and `BoundedResult(...)` validation; `expr/mcp/root.go` owns MCP service lookup and `MCPExpr.Name` lookup through `ServiceMCP(service, toolset)`; `codegen/agent/data.go`, `data_toolsets.go`, `agent_render_sections.go`, `specs_builder_*`, `generate_toolset_specs.go`, `generate_toolset_transforms.go`, and `service_executor_render.go` own runtime specs, codecs, transforms, generated toolset packages, and method-backed executor generation; `codegen/mcp/generate.go`, `adapter_generator.go`, `adapter_tools_jennifer.go`, `sdk_server_file.go`, `register_file.go`, and `client_caller_file.go` own MCP `ToolInfo`, `tools/list`, `tools/call`, SDK parity, registration, caller parity, and progressive discovery; `integration_tests/fixtures/agent_features` is the generated acceptance fixture for agent/runtime features and `integration_tests/fixtures/assistant` is the generated acceptance fixture for MCP adapters.

The chosen v1 design is a design-owned surface set on toolset tools, with `Expose(AgentRuntime, MCPSurface)` and `MCPPlacement(service, mcpServer)` projecting only method-backed `BindTo(...)` tools into MCP. The DSL spelling uses `MCPSurface` because `dsl.MCP(...)` already names the service-level MCP configuration function; the evaluated surface value remains `"mcp"`, and `mcpServer` resolves to the existing `MCPExpr.Name` value, such as `assistant-mcp`. Default behavior stays unchanged: toolset tools remain agent-runtime only, method-level `Tool(...)` remains MCP only, deployment still chooses what to register, and projected MCP tools route through the same exported generated dispatcher used by runtime method-backed execution.

## Status

- 2026-07-07 — Plan created from `UNIFIED_TOOL_SURFACE_PROJECTION_DESIGN.md` after inspecting the current DSL, expression, agent codegen, MCP codegen, fixture, docs, and repo skill owners.
- 2026-07-07 — Fresh review pass found blockers in skill path, dual-use DSL options, cross-root validation ordering, collision coverage, nested fixture commands, dispatcher API specificity, MCP adapter bridge data, and fixture implementation wiring; plan updated to resolve each blocker.
- 2026-07-07 — Focused readiness re-check found no remaining blockers and marked the plan ready to execute. Remaining risk is implementation complexity, not plan actionability.
- 2026-07-07 — Execution started. Workflow recited before edits. Read `/Users/luca/code/skills/skills/execution-plans/SKILL.md`, `.agents/skills/loom-mcp/SKILL.md`, and `.agents/skills/loom-mcp/references/codegen-contracts.md`; design packages remain source of truth, `gen/` will be regenerated rather than hand-edited, and full framework gates remain mandatory.
- 2026-07-07 — Inventory command confirmed scoped owners: `dsl/tool.go` owns `Tool`, parsing, `BindTo`, `Inject`, `ServerData`, `ResultReminder`, and `BoundedResult`; `dsl/mcp.go` owns service-level `MCP` and method MCP metadata options; `expr/agent/tool.go` owns tool binding and local validation; `expr/agent/root.go` owns root cross-toolset validation; `expr/mcp/root.go` owns `ServiceMCP`; `expr/mcp/mcp.go` owns MCP tool validation and `ToolSearch`; `codegen/agent/*` owns `ToolsetRegistration`, tool specs, transforms, and service executors; `codegen/mcp/*` owns adapter catalogs, tool calls, SDK server, registration, client caller, and progressive discovery; `integration_tests/fixtures/agent_features` and `integration_tests/fixtures/assistant` remain the generated acceptance fixtures.
- 2026-07-07 — Red contract proof ran: `go test ./dsl ./expr/agent -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation' -count=1` fails only on missing projection API symbols and fields: `Expose`, `MCPSurface`, `AgentRuntime`, `MCPPlacement`, `ToolSurface`, `ToolSurfaceAgentRuntime`, `ToolSurfaceMCP`, `ToolMCPPlacementExpr`, `ToolExpr.Surfaces`, and `ToolExpr.MCPPlacement`.
- 2026-07-07 — Milestone 2 implemented. Exact proof passed: `go test ./dsl ./expr/agent ./expr/mcp -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation|TestMCPToolValidation' -count=1`.
- 2026-07-07 — Milestone 3 implemented. Exact proof passed: `go test ./codegen/agent ./codegen/mcp -run 'TestUnifiedToolSurfaceProjectionData|TestUnifiedToolSurfaceNoExposureCompatibility' -count=1`.
- 2026-07-07 — Milestone 4 started. Generated provider packages now emit per-tool `<Tool>DispatchOptions` and `Dispatch<Tool>Method(...)`; focused proof passed for the provider dispatcher shape and existing executor hook surface: `go test ./codegen/agent/tests -run 'TestGeneratedMethodBackedDispatcher|TestGeneratedMethodBackedDispatcherPreservesExecutorHooks' -count=1`.
- 2026-07-07 — Reviewer feedback recorded. Milestone 4 remains in progress because generated runtime executors still own payload mapping, label injection, direct method calls, result mapping, bounds, and server-data projection; the dispatcher-preservation test must assert generated executors call `Dispatch<Tool>Method(...)`; MCP projection inventory exists but is not wired into MCP adapter generation; the Milestone 4 package proof command is corrected to `go test ./codegen/agent/tests ...`.
- 2026-07-07 — Reviewer Milestone 4 generator blockers addressed. Generated runtime executors now delegate method-backed calls to `Dispatch<Tool>Method(...)`, and the generated dispatcher owns payload decode, payload mapping, label injection, interceptors, direct method calls, result mapping, bounds projection, retry hints, and server-data projection. Exact proof passed: `go test ./codegen/agent/tests -run 'TestGeneratedMethodBackedDispatcher|TestGeneratedMethodBackedDispatcherPreservesExecutorHooks' -count=1`; broader proof passed with DSL/eval/projection/bounds/server-data tests: `go test ./dsl ./expr/agent ./expr/mcp ./codegen/agent ./codegen/mcp ./codegen/agent/tests -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation|TestMCPToolValidation|TestUnifiedToolSurfaceProjectionData|TestUnifiedToolSurfaceNoExposureCompatibility|TestGeneratedMethodBackedDispatcher|TestGeneratedMethodBackedDispatcherPreservesExecutorHooks|TestGolden_BoundedResult_UsesBoundsSpecAndProjection|TestGolden_ServerData_UsesGeneratedCodec' -count=1`. Milestone 4 remains in progress until the generated `agent_features` fixture proof is added; Milestone 5 MCP catalog/call/search wiring remains not started.
- 2026-07-07 — Fresh agent review cleared the generator/runtime executor slice to continue. The reviewer confirmed executor delegation, dispatcher ownership, real hook-preservation assertions, Milestone 4 still in-progress status, and Milestone 5 still unchecked status; both reviewer-run proof commands passed.
- 2026-07-07 — Milestone 4 fixture proof completed. Added a runtime-only method-backed `workflow.method_echo` tool in `integration_tests/fixtures/agent_features/design/design.go`, regenerated with `make regen-agent-feature-fixture`, fixed the generated executor handoff to pass `json.RawMessage(call.Payload)` into the dispatcher, and proved execution through `rt.ExecuteToolActivity`. Exact proof passed: `go test -C ./integration_tests/fixtures/agent_features . -run TestAgentFeatureMethodBackedDispatcher -count=1`; full fixture package also passed: `go test -C ./integration_tests/fixtures/agent_features . -count=1`.
- 2026-07-07 — Milestone 5 completed. Projected assistant tool `projected_lookup_tool` is generated from the agent toolset into the MCP adapter catalog, uses generated toolset specs for MCP `ToolInfo` schemas, routes `tools/call` through `DispatchProjectedLookupToolMethod`, and participates in adapter, JSON-RPC, and SDK progressive discovery. Exact proofs passed: `go test ./codegen/mcp -run TestUnifiedToolSurfaceProjectionData -count=1` and `go test -C ./integration_tests/fixtures/assistant . -run 'TestProjectedTool|TestGeneratedAdapterToolSearch|TestGeneratedJSONRPCToolSearch|TestGeneratedSDKServerToolSearch' -count=1`.
- 2026-07-07 — Milestone 6 docs and skill updates completed before review. `docs/dsl.md` owns DSL spelling, default exposure, projection example, method-level MCP separation, and v1 invalid combinations; `docs/runtime.md` owns shared dispatcher behavior, design-time exposure vs deployment-time registration, and compact discovery runtime behavior; `docs/mcp_sdk_server.md` owns SDK compact catalog behavior for projected tools; `.agents/skills/loom-mcp/SKILL.md` owns repo-local product rules; `.agents/skills/loom-mcp/references/codegen-contracts.md` owns generator rules; `.agents/skills/loom-mcp/references/user-guides/toolsets.md` owns quick user-guide wording. Required search also finds unrelated `cors.Expose("X-Time")` examples in `.agents/skills/loom-mcp/references/user-guides/http-guide.md`, which are CORS docs and not tool projection.
- 2026-07-07 — Fresh reviewer found two P2 tracker/docs honesty blockers before full gates: Milestone 5 overclaimed projected MCP annotations, `_meta`, icons, and metadata; `.agents/skills/loom-mcp/references/codegen-contracts.md` overclaimed provider-loop dispatcher ownership. Reconciled by narrowing Milestone 5 to projected `ToolInfo` name, title, description, and generated-spec schemas, and by limiting dispatcher ownership guidance to runtime service executors and projected MCP calls.
- 2026-07-07 — Focused reviewer re-check cleared the reconciliations. The reviewer found no remaining issues, confirmed the Milestone 5 wording now matches projected `ToolInfo` name/title/description and generated-spec schemas, confirmed codegen-contract dispatcher wording now excludes registry provider loops, and returned `CLEARED TO CONTINUE to full verification`.
- 2026-07-07 — Full verification started. `make loom-status` reports remote Loom mode for root, fixture, and quickstart before local validation.
- 2026-07-07 — Local Loom validation passed: `make loom-local` then `make verify-mcp-local` completed successfully for assistant fixture, agent_features fixture, and `integration_tests/framework`. `make loom-remote` restored remote mode; `make loom-status` confirms root, fixture, and quickstart are back on `github.com/CaliLuke/loom v1.2.0 (remote)`.
- 2026-07-07 — Full repo gates passed after lint fixes and MCP-only generation compatibility fix: `go fmt ./...`, `make build`, `make lint`, `make test`, and `make itest`.
- 2026-07-07 — Final `git status --short --branch` reports `main...origin/main` with intended unified projection changes across `.agents/skills/loom-mcp`, `docs`, `dsl`, `expr`, `codegen/agent`, `codegen/mcp`, generated `agent_features` and `assistant` fixtures, and the `UNIFIED_TOOL_SURFACE_PROJECTION_*` tracker/design artifacts. No unrelated local dirt was observed in the final status output. Commit and push remain out of scope.
- 2026-07-07 — Follow-up review fixes applied. Projected MCP calls now branch for no-payload bound service methods, generated runtime service executors and provider adapters handle no-payload method-backed tools, SDK static tool registration uses generated toolset specs for projected schemas, and `TestProjectedToolRuntimeAndMCPSchemasMatch` now compares MCP schemas against generated runtime spec schemas including output schema. Added `projected_status_tool` to the assistant fixture to prove no-payload projected execution.
- 2026-07-07 — Follow-up verification passed: focused codegen/fixture tests, `make lint`, `make test`, assistant fixture package tests, `make itest`, and `git diff --check`.
- 2026-07-07 — Follow-up schema parity correction applied. Projected `ToolInfo.InputSchema` now always uses the generated toolset payload spec, including no-payload tools such as `projected_status_tool`; the parity test now asserts both input and output schemas for `projected_lookup_tool` and `projected_status_tool`.

## Milestones

### Milestone 1: Inventory And Red Contracts

Toc: Inventory

Goal: Put the workflow, exact current owners, and failing projection contract tests on the record before implementation edits.

Acceptance Criteria

- The executing agent has recited the workflow on the record before any code edits.
- This plan status records the current-contract inventory command output and confirms generated `gen/` files will be regenerated rather than hand-edited.
- `go test ./dsl ./expr/agent -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation' -count=1` fails only because the new `ToolSurface`, `Expose`, `MCPPlacement`, and projected-tool validation symbols do not exist.

Checklist

- [x] Read this plan end-to-end, read the linked skill at `/Users/luca/code/skills/skills/execution-plans/SKILL.md`, then recite the workflow you will follow — milestone order, exit criteria, named commands in execution order, test-first rule, peer-review gate, commit/push handoff, and any inherited repo constraints. Do not edit code before this recital is on the record.
- [x] Read `.agents/skills/loom-mcp/SKILL.md` and `.agents/skills/loom-mcp/references/codegen-contracts.md`; add a dated `## Status` entry confirming design packages are source of truth, `gen/` is regenerated, and full gates are mandatory for this framework-scale change.
- [x] Run `rg -n "func Tool\\(|func ToolTitle|type ToolExpr|func MCP\\(|ServiceMCP|ToolsetRegistration|buildToolAdapters|serviceExecutorFiles|toolSpecsSection|buildToolSpecsData|generate_toolset_transforms|ToolSearch|BindTo\\(|Confirmation\\(|Inject\\(|ServerData\\(|ResultReminder\\(|BoundedResult\\(" dsl expr codegen runtime integration_tests/fixtures -g'*.go'` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`; add a dated `## Status` entry naming the concrete owners that remain in scope.
- [x] Add compile-failing DSL and expression tests in `dsl/tool_test.go` and new file `expr/agent/tool_projection_test.go` named `TestToolSurfaceProjectionDSL` and `TestToolSurfaceProjectionValidation`.
- [x] Run `go test ./dsl ./expr/agent -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and keep the failure tied to missing projection symbols.

### Milestone 2: DSL And Evaluation Contract

Toc: DSL

Goal: Add design-time surface and MCP placement metadata with v1 fail-fast validation.

Acceptance Criteria

- `expr/agent.ToolExpr` stores a deterministic surface set and optional MCP placement, and omitted `Expose(...)` on toolset tools evaluates as `AgentRuntime`.
- Validation rejects empty or duplicate surfaces, `Expose(MCPSurface)` without `AgentRuntime` on toolset tools, method-level agent projection, missing placement, inert placement, unresolved placement, cross-service placement, projected-vs-method and projected-vs-projected name collisions, unbound MCP projection, ownerless one-arg `BindTo(...)`, and v1 runtime-only feature combinations.
- `go test ./dsl ./expr/agent ./expr/mcp -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation|TestMCPToolValidation' -count=1` passes.

Checklist

- [x] Add `ToolSurfaceAgentRuntime` and `ToolSurfaceMCP` constants to `expr/agent/tool.go`; store `Surfaces []ToolSurface` and `MCPPlacement *ToolMCPPlacementExpr` on `ToolExpr`.
- [x] Add `ToolMCPPlacementExpr` to `expr/agent/tool.go` with `Service` and `MCPServer` fields.
- [x] Add DSL aliases `type ToolSurface = agent.ToolSurface`, `const AgentRuntime = agent.ToolSurfaceAgentRuntime`, and `const MCPSurface = agent.ToolSurfaceMCP` in `dsl/tool.go`; do not add a DSL constant named `MCP` because it conflicts with the existing `MCP(...)` function.
- [x] Add `Expose(surfaces ...ToolSurface) func(*mcp.ToolExpr)` and `MCPPlacement(service string, mcpServer string) func(*mcp.ToolExpr)` to `dsl/tool.go`; each helper must mutate the current `*agent.ToolExpr` when evaluated inside a toolset DSL block and return a method-level MCP option when passed to `Tool(...)` inside a Goa method.
- [x] Extend `expr/mcp.ToolExpr` in `expr/mcp/mcp.go` with projection-option bookkeeping fields used only to reject method-level `Expose(AgentRuntime)` and `MCPPlacement(...)`; keep the fields string/slice based so `expr/mcp` does not import `expr/agent`.
- [x] Extend `parseToolArgs` in `dsl/tool.go` so method-level `Tool(...)` receives projection options without breaking existing `ToolTitle`, `ToolDiscoveryCategory`, `ToolDiscoveryTags`, `ToolDiscoveryKeywords`, and `ToolIcons` options from `dsl/mcp.go`.
- [x] Add validation helpers in `expr/agent/tool.go` that default missing toolset surfaces to `AgentRuntime`, reject duplicate surfaces, and reject `MCPPlacement(...)` unless MCP is exposed.
- [x] Update `expr/agent.RootExpr.DependsOn()` to include `expr/mcp.Root`, then add cross-root validation in `expr/agent/root.go` using `expr/mcp.Root.ServiceMCP(service, mcpServer)` so `MCPPlacement(service, mcpServer)` resolves to an existing `MCPExpr.Name` and the placement service matches the resolved `BindTo(...)` service in v1.
- [x] Reject projected one-arg `BindTo(method)` when neither an owning agent service nor a source service is available, so placement service matching has one concrete source of truth.
- [x] Add collision validation in `expr/agent/root.go` so a projected tool name cannot duplicate any existing method-level MCP catalog name or another projected tool name in the same placement server.
- [x] Add v1 validation in `expr/agent/tool.go` rejecting MCP projection when the tool declares `Confirmation(...)`, `Inject(...)`, `ServerData(...)`, `ResultReminder(...)`, or `BoundedResult(...)`.
- [x] Add method-level validation in `expr/mcp/mcp.go` rejecting `Expose(AgentRuntime)` and `MCPPlacement(...)` on method-level MCP tools.
- [x] Run `go test ./dsl ./expr/agent ./expr/mcp -run 'TestToolSurfaceProjectionDSL|TestToolSurfaceProjectionValidation|TestMCPToolValidation' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 3: Canonical Tool Data And Compatibility

Toc: Contract

Goal: Carry evaluated surface metadata through agent and MCP generator data while proving no-exposure designs keep their generated contract.

Acceptance Criteria

- `codegen/agent.ToolData` and MCP adapter data can identify projected method-backed tools, their placement MCP server, their shared specs package, and their bound service method without duplicating schema ownership.
- `TestUnifiedToolSurfaceNoExposureCompatibility` in `codegen/mcp/contract_test.go` renders generated source for a no-exposure design and proves existing runtime toolset output and method-level MCP output remain byte-for-byte equal to the captured pre-projection source.
- `go test ./codegen/agent ./codegen/mcp -run 'TestUnifiedToolSurfaceProjectionData|TestUnifiedToolSurfaceNoExposureCompatibility' -count=1` passes.

Checklist

- [x] Extend `codegen/agent/data.go` `ToolData` with `Surfaces`, `MCPPlacementService`, `MCPPlacementServer`, and `MCPProjected` fields.
- [x] Populate projection fields in `codegen/agent/data_toolsets.go` from `expr/agent.ToolExpr` after method binding has resolved.
- [x] Add a projection inventory helper in `codegen/mcp/generate.go` that reads `expr/agent.Root` from `[]eval.Root` and returns only `Toolset(...)` tools placed into the current MCP service and `MCPExpr.Name`.
- [x] Add `ProjectedToolAdapter` ownership data in `codegen/mcp/adapter_generator.go` that references the owning generated toolset specs package and bound method metadata instead of rebuilding schemas from `expr/mcp.ToolExpr`.
- [x] Add `TestUnifiedToolSurfaceProjectionData` in `codegen/mcp/contract_test.go` proving projected tool data names the source toolset, source tool, placement service, placement MCP server, specs package, and bound method.
- [x] Add `TestUnifiedToolSurfaceNoExposureCompatibility` in `codegen/mcp/contract_test.go` with captured rendered source strings for one runtime-only toolset tool and one method-level MCP tool; assert omitted `Expose(...)` keeps both generated outputs byte-for-byte stable.
- [x] Run `go test ./codegen/agent ./codegen/mcp -run 'TestUnifiedToolSurfaceProjectionData|TestUnifiedToolSurfaceNoExposureCompatibility' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 4: Shared Method-Backed Dispatcher

Toc: Dispatcher

Goal: Generate one exported dispatcher for method-backed toolset execution and use it from runtime registration before MCP projection depends on it.

Acceptance Criteria

- Generated toolset owner packages contain exported dispatcher helpers for method-backed tools that own payload decode, tool-to-method transform, method call hook, method-to-tool transform, bounds, and server-data projection.
- Runtime service executors in generated agent packages call the exported dispatcher instead of duplicating the default `BindTo(...)` transform path.
- `go test ./codegen/agent/tests -run TestGeneratedMethodBackedDispatcher -count=1` and `go test -C ./integration_tests/fixtures/agent_features . -run TestAgentFeatureMethodBackedDispatcher -count=1` pass after `make regen-agent-feature-fixture`.

Checklist

- [x] Add red generator tests in `codegen/agent` named `TestGeneratedMethodBackedDispatcher` that assert generated toolset owner packages export `Dispatch<ToolName>Method(ctx context.Context, meta *runtime.ToolCallMeta, raw json.RawMessage, labels map[string]string, opts <ToolName>DispatchOptions) (*planner.ToolResult, error)` for method-backed tools.
- [x] Extend `codegen/agent/generate_toolset_specs.go`, `codegen/agent/agent_render_sections.go`, and `codegen/agent/specs_builder_*` so each `ToolData.IsMethodBacked` tool emits `<ToolName>DispatchOptions` with `Call func(context.Context, any) (any, error)`, `MapPayload func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)`, `MapResult func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)`, and `Injectors []func(context.Context, any, *runtime.ToolCallMeta) error`.
- [x] Move the default payload transform, result transform, bounds projection, and server-data projection logic from `codegen/agent/service_executor_render.go` into generated dispatcher calls for runtime-only method-backed tools as well as projected tools.
- [x] Update `codegen/agent/service_executor_render.go` so generated runtime executors call the exported dispatcher while preserving `WithPayloadMapper`, `WithResultMapper`, and `WithInterceptors` semantics.
- [x] Add `TestGeneratedMethodBackedDispatcherPreservesExecutorHooks` in `codegen/agent` proving generated source still applies `WithPayloadMapper`, `WithResultMapper`, and `WithInterceptors` around the dispatcher call.
- [x] Extend `integration_tests/fixtures/agent_features/design/design.go` with a concrete service method, payload, result, and method-backed toolset tool that remains runtime-only by default.
- [x] Update `integration_tests/fixtures/agent_features/agent_features_test.go` with generated `RegisterUsedToolsets` wiring and the generated workflow service executor required by the new method-backed tool, then assert execution through `rt.ExecuteToolActivity`.
- [x] Run `make regen-agent-feature-fixture` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Add `TestAgentFeatureMethodBackedDispatcher` in `integration_tests/fixtures/agent_features/agent_features_test.go` proving runtime execution reaches the shared dispatcher path.
- [x] Run `go test ./codegen/agent/tests -run 'TestGeneratedMethodBackedDispatcher|TestGeneratedMethodBackedDispatcherPreservesExecutorHooks' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test -C ./integration_tests/fixtures/agent_features . -run TestAgentFeatureMethodBackedDispatcher -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 5: MCP Projection And Discovery

Toc: MCP

Goal: Project eligible toolset tools into generated MCP adapters and route calls through the shared dispatcher.

Acceptance Criteria

- Regenerated assistant MCP adapters list projected tools in `tools/list` with `name`, `title`, `description`, `inputSchema`, and `outputSchema`, with schemas derived from the same generated toolset specs contract as runtime `tool_specs.Specs`.
- Generated JSON-RPC MCP `tools/call` paths execute projected tools through the shared dispatcher and adapt the returned `planner.ToolResult` into MCP `ToolsCallResult`; SDK calls continue to route through `MCPAdapter.ToolsCall`.
- Projected tools participate in `MCPAdapterOptions.ToolSearch` `AlwaysVisible`, hidden search, and `call_tool` behavior like method-level MCP tools.

Checklist

- [x] Add red MCP fixture tests in `integration_tests/fixtures/assistant/mcp_generated_server_parity_test.go` named `TestProjectedToolRuntimeAndMCPSchemasMatch` and `TestProjectedToolMCPCallUsesSharedDispatcher`.
- [x] Add red progressive discovery tests in `integration_tests/fixtures/assistant/mcp_generated_tool_search_test.go`, `mcp_generated_jsonrpc_tool_search_test.go`, and `mcp_generated_server_tool_search_test.go` proving projected tools support `AlwaysVisible`, hidden search, and `call_tool`.
- [x] Extend `integration_tests/fixtures/assistant/design/design.go` with a new non-colliding service method `projected_lookup`, payload, result, assistant agent, and method-backed `Toolset(...)` tool named `projected_lookup_tool` that declares `BindTo("assistant", "projected_lookup")`, `Expose(AgentRuntime, MCPSurface)`, and `MCPPlacement("assistant", "assistant-mcp")`.
- [x] Implement `projected_lookup` in `integration_tests/fixtures/assistant/assistant.go` so generated JSON-RPC and SDK fixture tests can prove execution rather than only catalog shape.
- [x] Update `codegen/mcp/adapter_generator.go` so `buildToolAdapters` merges method-level MCP tools and projected toolset tools in deterministic catalog order and fails on any collision missed by validation.
- [x] Add projected-tool adapter data fields distinguishing method-level MCP tools from projected toolset tools, including specs import alias, qualified runtime tool name, dispatcher function name, and bound service method caller.
- [x] Update `codegen/mcp/adapter_tools_jennifer.go` so projected tool `ToolInfo` schema values are emitted from generated toolset specs rather than service-method-only schema extraction.
- [x] Update `codegen/mcp/adapter_tools_jennifer.go` so projected `tools/call` cases invoke the exported dispatcher, convert `planner.ToolResult.Result` into MCP text and `StructuredContent`, preserve tool errors as `isError: true`, and do not duplicate `BindTo(...)` transforms.
- [x] Update `codegen/mcp/sdk_server_file.go` only as needed to prove projected tools are included in `adapter.generatedToolCatalog()`, SDK `ListTools`, SDK compact `AlwaysVisible`, and SDK `call_tool`; SDK calls must continue routing through `MCPAdapter.ToolsCall`.
- [x] Run `make regen-assistant-fixture` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test ./codegen/mcp -run TestUnifiedToolSurfaceProjectionData -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `go test -C ./integration_tests/fixtures/assistant . -run 'TestProjectedTool|TestGeneratedAdapterToolSearch|TestGeneratedJSONRPCToolSearch|TestGeneratedSDKServerToolSearch' -count=1` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 6: Docs, Skills, And Review

Toc: Docs

Goal: Update the public contract and get a fresh review before full repo gates.

Acceptance Criteria

- `docs/dsl.md`, `docs/runtime.md`, `docs/mcp_sdk_server.md`, `.agents/skills/loom-mcp/SKILL.md`, and `.agents/skills/loom-mcp/references/codegen-contracts.md` describe `Expose(...)`, `MCPPlacement(...)`, v1 limitations, shared dispatcher ownership, default backward compatibility, and projected-tool progressive discovery behavior.
- A fresh reviewer inspects this plan and the named code paths, and this plan status records every blocker as applied, rejected with reason, or absent.
- `python3 /Users/luca/code/skills/skills/execution-plans/render_plan.py UNIFIED_TOOL_SURFACE_PROJECTION_PLAN.md` succeeds after review reconciliation.

Checklist

- [x] Update `docs/dsl.md` with the `Toolset(...)` projection DSL, method-level MCP default behavior, and v1 invalid combinations.
- [x] Update `docs/runtime.md` with the shared generated dispatcher route for dual-exposed method-backed tools and the rule that deployment-time runtime registration remains separate from design-time exposure permission.
- [x] Update `docs/mcp_sdk_server.md` with projected-tool JSON-RPC and SDK discovery semantics under `MCPAdapterOptions.ToolSearch`.
- [x] Update `.agents/skills/loom-mcp/SKILL.md` current product rules with the unified tool surface projection contract.
- [x] Update `.agents/skills/loom-mcp/references/codegen-contracts.md` with generated dispatcher ownership, projected MCP schema parity, and codegen placement rules.
- [x] Run `rg -n "Expose\\(|MCPPlacement|unified tool|projected tool|Toolset\\(.*MCP|shared dispatcher|ToolSearch" docs .agents/skills/loom-mcp` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and add a dated `## Status` entry assigning every surviving doc and skill match.
- [x] Ask a fresh reviewer to inspect `UNIFIED_TOOL_SURFACE_PROJECTION_PLAN.md`, `dsl/tool.go`, `dsl/mcp.go`, `expr/agent/tool.go`, `expr/agent/root.go`, `expr/mcp/mcp.go`, `expr/mcp/root.go`, `codegen/agent/data.go`, `codegen/agent/data_toolsets.go`, `codegen/agent/agent_render_sections.go`, `codegen/agent/generate_toolset_specs.go`, `codegen/agent/generate_toolset_transforms.go`, `codegen/agent/service_executor_render.go`, `codegen/mcp/generate.go`, `codegen/mcp/adapter_generator.go`, `codegen/mcp/adapter_tools_jennifer.go`, `codegen/mcp/sdk_server_file.go`, `codegen/mcp/register_file.go`, `codegen/mcp/client_caller_file.go`, `integration_tests/fixtures/agent_features`, `integration_tests/fixtures/assistant`, `docs/dsl.md`, `docs/runtime.md`, `docs/mcp_sdk_server.md`, and `.agents/skills/loom-mcp`; require critique only.
- [x] Reconcile every reviewer blocker in the owning artifact named by the blocker.
- [x] Rerun the smallest proof command named by each reconciled blocker.
- [x] Run `python3 /Users/luca/code/skills/skills/execution-plans/render_plan.py UNIFIED_TOOL_SURFACE_PROJECTION_PLAN.md` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.

### Milestone 7: Full Verification And Handoff

Toc: Handoff

Goal: Finish with local Loom validation, CI-facing remote mode, full repo gates, and a clean handoff record.

Acceptance Criteria

- `make loom-status`, `make loom-local`, `make verify-mcp-local`, `make loom-remote`, `make build`, `make lint`, `make test`, and `make itest` all pass from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- Final `git status --short --branch` records intended source, docs, skill, generated fixture, plan, and tracker changes separately from unrelated local dirt.
- Commit and push remain out of scope until the user asks for them explicitly.

Checklist

- [x] Run `make loom-status` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and add a dated `## Status` entry with the starting Loom mode.
- [x] Run `make loom-local` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make verify-mcp-local` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make loom-remote` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make loom-status` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and add a dated `## Status` entry confirming remote mode was restored.
- [x] Run `go fmt ./...` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make build` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make lint` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make test` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `make itest` from `/Users/luca/code/my-tools/loom-mono/loom-mcp`.
- [x] Run `git status --short --branch` from `/Users/luca/code/my-tools/loom-mono/loom-mcp` and add a dated `## Status` entry separating intended changes from unrelated local dirt.
- [x] Leave commit and push for a separate explicit user request.
