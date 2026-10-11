# loom-mcp Repo Map

Use this map to choose the smallest authoritative source for the task at hand.

## Start Here

- `README.md`: current product framing, quick start, agent composition, MCP overview, stream profiles.
- `DESIGN.md`: plugin architecture, generated surface, MCP/codegen intent, quickstart output.
- `docs/agent_testing.md`: compiled application wiring, selection and request-fixture recipes.
- `docs/runtime.md`: runtime contracts, plan/execute/resume loop, streaming, tool execution, prompt/runtime features.
- `docs/dsl.md`: DSL surface and generated helper semantics.
- `docs/glossary.md`: canonical distinctions for state, delivery, skills, MCP, registry, and generated ownership.
- `quickstart/AGENTS_QUICKSTART.md`: generated developer quickstart and wiring examples.

## Source Directories

- `agenttest/`: in-memory harness, scripted models, strict stubs and standalone decisions.
- `expect/`: typed expectations and sealed evidence subjects.
- `trajectory/`: record-derived causal steps, rendering and versioned JSON.
- `dsl/`: loom-mcp DSL surface.
- `expr/`: design expression model.
- `codegen/`: generator internals and templates.
- `runtime/agent/runtime/`: runtime orchestration, planner loop, agent-as-tool, result materialization.
- `runtime/agent/stream/`: stream events and profiles.
- `runtime/mcp/`: MCP callers and transport integration.
- `features/model/`: model-provider adapters, conformance tests, and client middleware.
- `features/memory/` and `features/runlog/`: production persistence adapters.
- `features/stream/`: Pulse-backed streaming clients and integrations.
- `registry/`: internal tool registry implementation.
- `integration_tests/`: end-to-end scenarios and fixtures.
- `eval/`: immutable datasets, strict JSONL, scenario validation and single-attempt execution.

## Skill Reference Files

- `references/agent-testing.md`: first stop for application agent/tool tests.
- `references/runtime-contracts.md`: current runtime, planner streaming, agent-as-tool, and stream behavior.
- `references/codegen-contracts.md`: current DSL/codegen/MCP generation rules.
- `references/user-guides/*.md`: secondary long-form guides. Use these after the contract files and repo docs when you need broader narrative or examples.

## Suggested Lookup Flow

1. Start with `SKILL.md` for routing.
2. Open `references/runtime-contracts.md` or `references/codegen-contracts.md` for repo-specific rules.
3. Confirm behavior in repo docs (`README.md`, `DESIGN.md`, `docs/*.md`).
4. Check source packages when the docs are incomplete or you are changing internals.
5. Edit DSL first, regenerate, then implement non-generated logic.

## Useful Search Commands

```bash
rg -n "Agent\\(|Toolset\\(|FromMCP\\(|MCP\\(|Export\\(" dsl design docs README.md
rg -n "ConsumeStream|ModelClient\\(|ChildRunLinked|StreamProfile|RegisterToolset|NewRegistration" runtime docs README.md quickstart
rg -n "RunProviderConformance|ErrRateLimited|ToolName|TokenCounter" features/model runtime/agent/model docs/runtime.md
rg -n "tool_specs\\.Specs|GoFullTypeRef|GoTransform|pathutil|updateHeader" codegen runtime dsl expr
```
