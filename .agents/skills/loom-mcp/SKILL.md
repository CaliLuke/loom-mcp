---
name: loom-mcp
description: Build and maintain the loom-mcp repository and framework code in Go. Use this skill when the task involves the agent DSL, generated `gen/` code, runtime/planner behavior, agent-as-tool, MCP integration, codegen internals, or refactoring a repo with a `design` package.
---
# loom-mcp

Use this skill for `loom-mcp` work in this repo. Keep `AGENTS.md` short and keep framework-specific guidance here and in the files under `references/`.

## Non-Negotiables

- Treat `design/*.go` as the source of truth.
- Regenerate after every design change with `loom gen <module-import-path>/design`.
- Never hand-edit generated `gen/` files.
- Implement business logic in non-generated files.
- Use Go import paths for generation commands, not filesystem paths.
- Commit generated code; do not rely on CI to regenerate it.
- Keep this skill current with the product. Update `SKILL.md` and the reference files directly instead of writing sidecar delta docs.

## Default Workflow

1. Detect the `loom-mcp` surface: `go.mod`, `design/`, DSL imports, `codegen/`, `runtime/`, or generated `gen/`.
2. Decide whether the task is DSL/codegen/runtime/application code.
3. Edit the DSL first when the contract changed.
4. Regenerate with `loom gen <module>/design`.
5. Run `loom example <module>/design` only when scaffold output is intentionally required.
6. Implement or refactor non-generated logic.
7. Verify with formatting, lint, and relevant tests.

## Planning Quality Rules

- Multi-file or multi-layer plans must start with a current-contract inventory
  that names the existing structs, files, generated surfaces, and invariants the
  work will change.
- Plans must use concrete Loom DSL shapes, runtime type names, generated-code
  owners, test files, and proof commands. Avoid placeholder feature names once
  code inspection has revealed the owner.
- Contract tests may intentionally compile-fail before implementation, but the
  plan must mark them as contract tests and must not run package proof commands
  until the symbols those tests import have been added.
- Before accepting a plan, check for impossible or wrongly placed tests: planner
  behavior belongs in `runtime/agent/planner`, runtime context and model-client
  decoration belong in `runtime/agent/runtime`, MCP skill resources belong in
  `runtime/mcp/skills`, and model-facing skill tools belong in
  `runtime/agent/runtime`.
- Framework-scale DSL, codegen, runtime, or MCP feature work must include the
  full repo gates expected by AGENTS.md: `make lint`, `make test`, `make itest`,
  and `make verify-mcp-local`, with targeted package commands only as earlier
  red-green checks.
- When a model-facing agent feature crosses DSL, codegen, generated
  registration, and runtime behavior, extend
  `integration_tests/fixtures/agent_features` as the generated acceptance
  fixture in addition to focused package tests.

## Current Product Rules

- Runtime planners have two streaming modes only:
  - use `PlannerContext.ModelClient(id)` and drain the decorated stream yourself, or
  - use `planner.ConsumeStream` with a raw client.
- Agent-as-tool runs as a real child workflow. Parent and child are linked by `ChildRunLinked`, and parent tool results carry `RunLink`.
- Stream visibility is profile-driven. Child runs are linked, not flattened, by default.
- Runtime schemas come from generated `tool_specs.Specs` and codecs, not `docs.json`.
- Tool confirmation is runtime-owned and design-visible: declare
  `Confirmation(...)` in tool DSL for default approval requirements, or use
  `runtime.WithToolConfirmation(...)` for runtime overrides. The runtime emits
  `AwaitConfirmation`, resumes through `ProvideConfirmation`, records
  `ToolAuthorization`, executes only approved calls, and synthesizes
  schema-compliant denied results.
- MCP is a two-way bridge:
  - consume external MCP servers through `runtime/mcp` callers,
  - expose designed services as MCP servers through generated adapters and registrations.
- MCP metadata is design-owned. Implementation `WebsiteURL`/`ServerIcons` and
  list-surface `ToolIcons`/`ResourceIcons`/`PromptIcons`/`DynamicPromptIcons`
  should be declared in the DSL and allowed to flow through codegen into
  `initialize`, `tools/list`, `resources/list`, and `prompts/list`.
- MCP skill exposure is design-owned too: declare local agent skill roots with
  `SkillDirectory(...)`, then let generated JSON-RPC and SDK servers expose
  `skill://` entries through `resources/list` and `resources/read`.
- Model-facing skill exposure is design-owned through
  `Toolset(FromSkills(..., SkillPreload(...), SkillReload(...)))`; generated
  agent registration should wire these skills into `runtime/agent/runtime`
  skill tools rather than MCP resource handlers.
- Long-term memory is separate from transcript memory. `memory.Store` persists
  raw run events, `memory.Searcher` searches indexed raw events, and
  `memory.Service` stores/searches durable `memory.Entry` values. Expose
  long-term memory intentionally with `FromMemory(MemoryLongTerm(), ...)` and
  `PreloadLongTermMemory(...)`; do not overload transcript preload or
  `load_memory` with extracted facts.
- Local skills can declare structured `SKILL.md` frontmatter (`id`, `name`,
  `description`, `allowed_tools`, `preload`, `reload`). Duplicate IDs and
  unknown load modes are hard errors, not silent skips.
- Generated MCP adapters can opt into progressive discovery with
  `MCPAdapterOptions.ToolSearch`. Compact mode makes `tools/list` authoritative:
  it exposes `search_tools`, `call_tool`, and validated `AlwaysVisible` pins.
  Hidden real tools are invoked through `call_tool`; direct hidden `tools/call`
  is rejected by default, except for the explicit JSON-RPC compatibility option
  `AllowDirectHiddenCalls`. `search_tools` must support tokenized natural
  language queries, fuzzy name/title matching, exact-match narrowing, and DSL
  tuning through `ToolSearch(...)`; it must return exact `call_tool` JSON
  examples. `call_tool` schema text must require top-level `name` and
  `arguments` and warn not to use `args`. SDK compact mode must reject
  direct-hidden compatibility at construction.
- Generated SDK-backed MCP servers expose prompt argument completion for
  enum-backed dynamic prompt arguments and place a runtime elicitor in request
  contexts so service code can call `runtime/mcp.Elicit` during MCP calls.
- Codegen should use partial evaluation and `NameScope` helpers rather than string surgery or runtime branching over static structure.
- DSL/codegen/runtime internals should trust evaluated design invariants and fail fast instead of adding speculative fallback paths.

## Command Reminders

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.2.0
loom version
loom gen <module-import-path>/design
loom example <module-import-path>/design
```

- Correct: `loom gen example.com/myapi/design`
- Incorrect: `loom gen ./design`

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
