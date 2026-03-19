---
name: goa-ai
description: Build and maintain Goa-AI services and framework code in Go. Use this skill when the task involves Goa-AI DSL, `goa gen`, generated `gen/` code, runtime/planner behavior, agent-as-tool, MCP integration, codegen internals, or refactoring a repo with a `design` package.
---
# Goa-AI

Use this skill for Goa-AI work in this repo. Keep `AGENTS.md` short and keep framework-specific guidance here and in the files under `references/`.

## Non-Negotiables

- Treat `design/*.go` as the source of truth.
- Regenerate after every design change with `goa gen <module-import-path>/design`.
- Never hand-edit generated `gen/` files.
- Implement business logic in non-generated files.
- Use Go import paths for Goa commands, not filesystem paths.
- Commit generated code; do not rely on CI to regenerate it.
- Keep this skill current with the product. Update `SKILL.md` and the reference files directly instead of writing sidecar delta docs.

## Default Workflow

1. Detect the Goa-AI surface: `go.mod`, `design/`, DSL imports, `codegen/`, `runtime/`, or generated `gen/`.
2. Decide whether the task is DSL/codegen/runtime/application code.
3. Edit the DSL first when the contract changed.
4. Regenerate with `goa gen <module>/design`.
5. Run `goa example <module>/design` only when scaffold output is intentionally required.
6. Implement or refactor non-generated logic.
7. Verify with formatting, lint, and relevant tests.

## Current Product Rules

- Runtime planners have two streaming modes only:
  - use `PlannerContext.ModelClient(id)` and drain the decorated stream yourself, or
  - use `planner.ConsumeStream` with a raw client.
- Agent-as-tool runs as a real child workflow. Parent and child are linked by `ChildRunLinked`, and parent tool results carry `RunLink`.
- Stream visibility is profile-driven. Child runs are linked, not flattened, by default.
- Runtime schemas come from generated `tool_specs.Specs` and codecs, not `docs.json`.
- MCP is a two-way bridge:
  - consume external MCP servers through `runtime/mcp` callers,
  - expose Goa services as MCP servers through generated adapters and registrations.
- Codegen should use partial evaluation and Goa `NameScope` helpers rather than string surgery or runtime branching over static structure.
- DSL/codegen/runtime internals should trust Goa invariants and fail fast instead of adding speculative fallback paths.

## Command Reminders

```bash
go install goa.design/goa/v3/cmd/goa@latest
goa version
goa gen <module-import-path>/design
goa example <module-import-path>/design
```

- Correct: `goa gen example.com/myapi/design`
- Incorrect: `goa gen ./design`

## References

- `references/repo-map.md`: source routing for repo docs and packages
- `references/runtime-contracts.md`: current runtime, planner streaming, stream profile, and agent-as-tool rules
- `references/codegen-contracts.md`: current DSL/codegen/type-ref/MCP generation rules
- `references/user-guides/runtime.md`: broader runtime narrative and examples
- `references/user-guides/toolsets.md`: toolset behavior, retry hints, injected fields, executors
- `references/user-guides/composition.md`: agent composition and child-run UX
- `references/user-guides/mcp-integration.md`: long-form MCP overview and caller examples
- `references/user-guides/testing.md`: testing patterns
- `references/user-guides/production.md`: production operations and deployment patterns

## Selection Rules

- Start with `references/runtime-contracts.md` or `references/codegen-contracts.md`, depending on the task.
- Use `references/repo-map.md` to jump into the right repo docs or packages.
- Use the `references/user-guides/*.md` files only when the contract files and repo docs are not enough.
- When docs and code disagree, trust the current repo code and update the skill/reference files accordingly.
