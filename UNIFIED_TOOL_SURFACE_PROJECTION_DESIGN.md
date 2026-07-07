# Unified Tool Surface Projection

## Summary

Loom should introduce a design-owned universal tool contract that can be projected onto internal agent runtime tools, MCP tools, or both without duplicating schemas or implementation bindings. The authored contract remains the source of truth; runtime and MCP generators derive their surface-specific registrations from that contract. Existing `Tool(...)` behavior remains backward compatible unless a tool opts into explicit surface projection metadata.

## Problem Statement

Today Loom has separate declaration paths for model-facing internal tools and MCP-exposed tools. A `Tool(...)` inside `Toolset(...)` declares an agent/runtime tool. A `Tool(...)` inside a service `Method(...)` marks that method as an MCP tool. This split keeps the current system clear, but it creates duplication when one capability should be available to both internal planners and MCP clients.

The visible symptom is an exposure-policy question, but the real design gap is deeper: the framework lacks one canonical tool contract that owns payload schema, result schema, metadata, confirmation policy, implementation binding, and allowed surfaces. Without that contract, dual exposure risks schema drift, duplicated adapter logic, and accidental MCP exposure through the wrong declaration path.

## Goals

- Define one design-owned tool contract that can be projected to multiple surfaces.
- Preserve existing `Toolset(...)`, `Use(...)`, `BindTo(...)`, and method-level MCP `Tool(...)` behavior by default.
- Let authors explicitly declare the surfaces a tool may appear on.
- Generate runtime `tool_specs.Specs` and MCP `tools/list` schemas from the same payload/result contract.
- Route dual-exposed method-backed tools through one bound service implementation.
- Keep deployment-time registration decisions separate from design-time exposure permission.
- Fail fast during design evaluation when a requested projection lacks a valid implementation route.
- Limit v1 MCP projection to method-backed `Toolset(...)` tools that do not declare runtime-only features.

## Non-Goals

- Do not replace the existing runtime `ToolsetRegistration` primitive.
- Do not remove method-level MCP `Tool(...)` declarations.
- Do not change `FromMCP`, `FromRegistry`, `FromSkills`, `FromArtifacts`, or `FromMemory` semantics.
- Do not make every runtime tool automatically MCP-visible.
- Do not use `docs.json` as a runtime schema source.
- Do not invent a second execution bridge when `BindTo(...)` already identifies the service implementation.
- Do not require deployment code to register every surface that the design permits.
- Do not support MCP-only `Toolset(...)` tools in v1.
- Do not support MCP projection for `Confirmation(...)`, `Inject(...)`, `ServerData(...)`, `ResultReminder(...)`, or `BoundedResult(...)` in v1.

## Current State

Loom currently treats tool declarations as context-sensitive. Inside `Toolset(...)`, `Tool(...)` contributes model-facing tools to generated agent/runtime registration. Inside a Goa service `Method(...)` with `MCP(...)` enabled, `Tool(...)` marks the method as an MCP protocol tool exposed through generated adapters.

Runtime tool execution is owned by `runtime/agent/runtime.ToolsetRegistration`. Generated agent code registers toolsets with the runtime, and the runtime uses generated `tool_specs.Specs` plus generated codecs as the schema source of truth for payload and result validation.

MCP exposure is currently service/method-oriented. Generated MCP adapters expose method-level tools through `tools/list` and `tools/call`, preserve MCP fields such as `name`, `title`, `description`, `inputSchema`, `outputSchema`, `annotations`, `_meta`, and `icons`, and can opt into compact progressive discovery with `MCPAdapterOptions.ToolSearch`.

`BindTo(...)` already bridges a toolset tool to a service method implementation. Codegen analyzes the tool payload/result shapes and emits transforms when the tool contract and bound method contract are compatible. This makes method-backed `Toolset(...)` tools the natural first-class owner for dual projection because the framework can point both runtime and MCP calls at the same service method.

## Proposed Design

Introduce a canonical evaluated tool contract in the design model. The contract represents an authored callable capability independent of any one surface. A contract owns:

- toolset and tool identity
- name, title, description, tags, icons, annotations, and metadata
- payload schema and result schema
- server-injected fields and server-only data rules
- confirmation policy
- bounded-result policy and result reminders
- implementation binding, such as `BindTo(...)`, provider source, or specialized runtime provider
- allowed surfaces

Surface projection becomes a generator concern. A tool contract with the agent runtime surface generates runtime `tool_specs.Specs`, codecs, and `ToolsetRegistration` helpers. A tool contract with the MCP surface generates MCP `ToolInfo`, schema projection, and `tools/call` routing. A dual-surface tool generates both projections from one contract.

The proposed DSL shape is:

```go
var GraphTools = Toolset("graph", func() {
    Tool("search_graph", "Search graph content", func() {
        Args(SearchGraphPayload)
        Return(SearchGraphResult)
        BindTo("graph", "Search")
        Expose(AgentRuntime, MCP)
        MCPPlacement("graph", "graph-mcp")
    })
})
```

The default for `Toolset(...)` tools remains agent runtime only:

```go
Tool("work_plan", "Maintain a session-scoped task ledger", func() {
    Args(WorkPlanPayload)
    Return(WorkPlanResult)
})
```

The default for method-level MCP tools remains unchanged:

```go
Method("status", func() {
    Result(StatusResult)
    Tool("external_status", "Expose service status")
})
```

The universal model can eventually support MCP-only tools, but v1 does not. V1 supports only method-backed `Toolset(...)` tools that declare `Expose(AgentRuntime, MCP)`, `BindTo(...)`, and `MCPPlacement(...)`. A `Toolset(...)` tool that declares `Expose(MCP)` without a `BindTo(...)` route, without MCP placement, or without `AgentRuntime` is invalid in v1.

MCP placement is explicit because current MCP generation is service/method-oriented. A projected tool must name the service and MCP suite that own its generated MCP catalog entry. In v1, the placement service must match the `BindTo(...)` service so the generated MCP adapter already receives the same service implementation it needs to call. Cross-service placement is future work because it requires generated MCP adapter constructor changes and dependency wiring for additional bound service implementations. The target service must declare the named `MCP(...)` suite. The default projected MCP tool name is the authored tool name. If that name collides with a method-level MCP tool or another projected tool in the target suite, design evaluation fails. A future aliasing DSL can relax this rule without changing the canonical contract model.

Dual-surface execution must share a generated binding dispatcher. Codegen should emit one exported dispatcher function for the `BindTo(...)` tool under the generated toolset package and have both the runtime `ToolsetRegistration` helper and the MCP adapter call that dispatcher. The function is exported because generated MCP adapter packages may not be colocated with generated toolset packages. The dispatcher owns payload decode, generated transforms into the bound service method, bound method invocation, result transform, and result encode. The MCP adapter must not generate an independent copy of those steps.

This follows the same architectural shape as mature MCP-first frameworks: one component model, then surface-specific projection, visibility filtering, and protocol conversion. Loom should adapt that pattern to its design-first and generated-code architecture rather than copying a runtime-only registry.

## API / Contract Changes

Add design-time surface values:

```go
const (
    AgentRuntime ToolSurface = "agent_runtime"
    MCP          ToolSurface = "mcp"
)
```

Add a `Tool(...)` child DSL function:

```go
Expose(surfaces ...ToolSurface)
```

Add a `Tool(...)` child DSL function for MCP ownership:

```go
MCPPlacement(service string, suite string)
```

`Expose(...)` declares the surfaces the authored tool may be projected onto. Empty or omitted exposure preserves the existing context default:

- `Tool(...)` inside `Toolset(...)`: `Expose(AgentRuntime)`
- `Tool(...)` inside service `Method(...)`: `Expose(MCP)`

`MCPPlacement(...)` is meaningful only for `Toolset(...)` tools that declare `Expose(MCP)`. It is invalid without MCP exposure and invalid on method-level `Tool(...)` declarations in v1.

The evaluated expression model should add a surface set to the canonical tool expression rather than storing one-off booleans on runtime or MCP adapters. Codegen should consume that surface set to decide which projections to emit.

Generated runtime contracts:

- Agent-runtime projection continues to emit `tool_specs.Specs` and registration helpers.
- Existing runtime `ToolsetRegistration` remains the execution container.
- Dual-exposed tools use the same generated payload/result codecs as internal-only tools.
- Method-backed dual-exposed tools call the shared generated binding dispatcher rather than an MCP-specific duplicate of the `BindTo(...)` transform path.

Generated MCP contracts:

- MCP projection emits `ToolInfo` entries for projected toolset tools.
- MCP `inputSchema` and `outputSchema` are generated from the same contract as runtime `tool_specs.Specs`.
- MCP calls for `BindTo(...)` dual-exposed tools route through the shared generated binding dispatcher used by the runtime registration helper.
- Compact tool discovery treats projected MCP tools as ordinary MCP tools, including `ToolSearchOptions.AlwaysVisible`, hidden-tool search, and `call_tool` behavior.
- The projected MCP tool is emitted into the `MCPPlacement(...)` target service and suite.

Validation changes:

- `Expose(...)` rejects an empty surface list.
- Duplicate surfaces in one declaration are rejected.
- Unknown surfaces are rejected.
- `Expose(MCP)` on a `Toolset(...)` tool without a supported implementation route is rejected.
- `Expose(MCP)` on a `Toolset(...)` tool without `Expose(AgentRuntime)` is rejected in v1.
- `Expose(MCP)` on a `Toolset(...)` tool without `MCPPlacement(...)` is rejected in v1.
- `MCPPlacement(...)` without `Expose(MCP)` is rejected in v1.
- `MCPPlacement(...)` inside a service `Method(...)` is rejected in v1.
- `MCPPlacement(...)` names must resolve to an existing service and MCP suite.
- `MCPPlacement(...)` service must match the `BindTo(...)` service in v1.
- `Expose(AgentRuntime)` inside a service `Method(...)` is rejected in v1.
- `Expose(MCP)` with `Confirmation(...)`, `Inject(...)`, `ServerData(...)`, `ResultReminder(...)`, or `BoundedResult(...)` is rejected in v1.
- Dual projection requires payload/result contracts compatible with both runtime specs and MCP schema constraints.
- Name collisions in the generated MCP tool catalog are rejected unless an explicit future aliasing rule is added.

## Detailed Behavior

Existing designs without `Expose(...)` generate byte-for-byte identical output. This is a hard compatibility rule and must be protected by a no-exposure golden fixture containing at least one current `Toolset(...)` tool and one current method-level MCP `Tool(...)`.

Default surfaces are derived from the declaration context when `Expose(...)` is omitted:

```go
func defaultSurfaces(tool) set[ToolSurface] {
    switch tool.Context {
    case ToolsetContext:
        return {AgentRuntime}
    case MethodContext:
        return {MCP}
    }
}
```

V1 validation is fail-fast. Unsupported combinations are design errors, not partially generated behavior:

```go
func validateProjection(tool) error {
    surfaces := tool.Expose
    if len(surfaces) == 0 {
        surfaces = defaultSurfaces(tool)
    }

    if tool.Has(MCPPlacement) && !surfaces.Has(MCP) {
        return error("MCPPlacement requires Expose(MCP)")
    }

    if tool.Context == MethodContext {
        if surfaces.Has(AgentRuntime) {
            return error("method-level AgentRuntime projection is unsupported in v1")
        }
        if tool.Has(MCPPlacement) {
            return error("method-level MCPPlacement is unsupported in v1")
        }
        return nil
    }

    if tool.Context == ToolsetContext && surfaces.Has(MCP) {
        if surfaces != {AgentRuntime, MCP} {
            return error("v1 MCP projection requires Expose(AgentRuntime, MCP)")
        }
        if !tool.Has(BindTo) {
            return error("v1 MCP projection requires BindTo")
        }
        if !tool.Has(MCPPlacement) {
            return error("v1 MCP projection requires MCPPlacement")
        }
        if !mcpSuiteExists(tool.MCPPlacement.Service, tool.MCPPlacement.Suite) {
            return error("MCPPlacement must resolve to an existing MCP suite")
        }
        if tool.MCPPlacement.Service != tool.BindTo.Service {
            return error("v1 MCPPlacement service must match BindTo service")
        }
        if mcpNameCollides(tool.MCPPlacement, tool.Name) {
            return error("projected MCP tool name collides in target suite")
        }
        if tool.HasAny(Confirmation, Inject, ServerData, ResultReminder, BoundedResult) {
            return error("runtime-only tool features are unsupported for MCP projection in v1")
        }
    }

    return nil
}
```

Projection is deterministic and surface-specific. Runtime registration never mutates the MCP catalog, and MCP placement never forces runtime registration at deployment time:

```go
func project(tool) {
    contract := canonicalToolContract(tool)

    if tool.Surfaces.Has(AgentRuntime) {
        emitRuntimeToolSpec(contract)
        emitRuntimeRegistration(contract, sharedDispatcher(contract))
    }

    if tool.Surfaces.Has(MCP) {
        emitMCPToolInfo(contract, tool.MCPPlacement)
        emitMCPCallRoute(contract, tool.MCPPlacement, sharedDispatcher(contract))
    }
}
```

The shared dispatcher is the only generated implementation route for dual-exposed `BindTo(...)` tools. It is exported from the generated toolset package so both runtime registration helpers and MCP adapter packages can call it:

```go
func DispatchSearchGraph(ctx context.Context, svc graph.Service, rawPayload []byte) ([]byte, error) {
    payload := decodeSearchGraphPayload(rawPayload)
    methodPayload := transformSearchGraphToSearchPayload(payload)
    methodResult := svc.Search(ctx, methodPayload)
    toolResult := transformSearchResultToSearchGraphResult(methodResult)
    return encodeSearchGraphResult(toolResult)
}
```

Projected tools behave like normal MCP tools in the target suite. They use the bare authored tool name, participate in `MCPAdapterOptions.ToolSearch`, can be listed through `AlwaysVisible`, can be hidden and discovered through `search_tools`, and can be invoked through `call_tool` under compact discovery.

The required fixture and test set is:

- no-exposure backward compatibility golden fixture with byte-for-byte unchanged generated output
- successful dual-exposed `BindTo(...)` fixture with `Expose(AgentRuntime, MCP)` and valid `MCPPlacement(...)`
- runtime spec versus MCP schema equivalence test for payload and result schemas
- runtime execution test proving runtime registration calls the shared exported dispatcher
- MCP `tools/call` test proving the generated MCP adapter calls the same shared exported dispatcher
- validation failure tests for MCP-only toolsets, method-level `Expose(AgentRuntime)`, missing placement, inert placement without `Expose(MCP)`, method-level placement, cross-service placement, and unsupported runtime-only features
- progressive discovery test proving projected tools participate in `AlwaysVisible`, hidden search, and `call_tool` like method-level MCP tools
