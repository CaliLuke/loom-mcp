# Toolsets

Use this guide for the current toolset model. Confirm exact DSL and generated names in `docs/dsl.md` and the generated `AGENTS_QUICKSTART.md` for the service you are wiring.

## Sources

| Source | Declaration | Execution |
| --- | --- | --- |
| Service-owned | `Toolset("name", func() { Tool(...) })` | Generated executor or generated method dispatcher for `BindTo(...)` |
| MCP | `Toolset(FromMCP(service, suite))` | Generated MCP registration backed by an `mcpruntime.Caller` |
| Internal registry | `Toolset("name", FromRegistry(registry, remoteToolset))` | Registry client/provider routing |
| Agent-as-tool | `UseAgentToolset(service, agent, export)` | A real linked child run |
| Skills | `Toolset(FromSkills(...))` | Generated `list_skills` and load tools |
| Artifacts | `Toolset(FromArtifacts(...))` | Runtime artifact store |
| Memory | `Toolset(FromMemory(...))` | Transcript, indexed transcript, or long-term memory service |

An agent consumes a toolset with `Use(...)`; an agent publishes a toolset to other agents with `Export(...)`.

## Generated ownership

Service-owned toolset code lives under:

```text
gen/<owner-service>/toolsets/<toolset>/
```

Import that owner-scoped package. Do not invent an agent-scoped toolset path and never edit generated files manually.

Generated agent packages expose the authoritative wiring helper:

```go
err := agentpkg.RegisterUsedToolsets(
    ctx,
    rt,
    agentpkg.WithProjectedExecutor(exec),
)
```

Use the generated `AGENTS_QUICKSTART.md` beside that package for the exact executor option names and transforms. Canonical tool IDs are `<toolset>.<tool>`; prefer generated constants over string literals.

## Method-backed tools

`BindTo(service, method)` keeps the tool schema in the agent design and maps it to a Loom service method. Code generation owns:

- model-facing payload/result types and JSON schemas;
- payload and result transforms;
- dispatch to the bound method;
- injection, server-data, confirmation, and bounded-result metadata where supported.

Keep validation in the design. Application code should implement the service behavior, not repeat generated boundary validation.

## Bounded results

`BoundedResult()` declares out-of-band bounds metadata. The authored semantic result must not define the reserved canonical fields. Executors return them through `planner.ToolResult.Bounds`:

```go
&agent.Bounds{
    Returned:       len(items),
    Total:          &total,
    Truncated:      truncated,
    NextCursor:     nextCursor,
    RefinementHint: "narrow the time range",
}
```

For cursor paging:

```go
BoundedResult(func() {
    Cursor("cursor")
    NextCursor("next_cursor")
})
```

Cursors are opaque. A caller fetching the next page must keep all other arguments unchanged.

## Model-facing versus server-facing data

- `Inject(...)` marks payload fields populated by the runtime/application and omitted from the model schema.
- `ServerData(...)` carries observer-facing data separately from the model result.
- `ResultHintTemplate(...)` adds a concise model-facing result hint.
- `ResultReminder(...)` adds a runtime-owned system reminder after the result.
- `Confirmation(...)` declares authorization requirements enforced by the runtime.
- `Idempotent()` currently emits metadata only; it does not add replay suppression by itself.

Projected MCP tools use `Expose(AgentRuntime, MCPSurface)` plus `MCPPlacement(...)`. The design rejects combinations unsupported by the v1 projected surface, including bounded results, confirmation, injection, server data, and result reminders.

## Verification

After a toolset design change:

1. Regenerate through the repository Make target or `loom gen <module-import-path>/design`.
2. Inspect the generated quickstart and tool schemas.
3. Run focused generator/runtime tests.
4. Run the repository verification ladder from the repo-local skill.
