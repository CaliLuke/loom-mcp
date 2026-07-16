# loom-mcp quickstart — run and verify

Use `quickstart/README.md` as the canonical runnable walkthrough. Generated
package names, config fields, and executor wiring are design-specific, so this
skill reference intentionally does not duplicate a standalone application.

The stable flow is:

1. Generate from the module import path with `loom gen <module>/design`.
2. Implement application-owned planners and tool executors outside `gen/`.
3. Register models, agents, and generated toolset executors on one runtime.
4. Create a session, then use the generated agent `NewClient(rt)` to submit the
   run.
5. Run the generated package tests and one end-to-end tool execution.

Inside this repository, use the checked-in quickstart and full verification
ladder:

```bash
make regen-quickstart
go test -C ./quickstart ./... -count=1
make verify-mcp-local
make lint
make test
make itest
```

Do not invent generic generated imports such as `gen/demo/agents/assistant` or
generic `Executor` config fields. Read the generated `AGENTS_QUICKSTART.md` and
agent config for the current design; method-backed toolsets are wired through
generated `RegisterUsedToolsets(..., With<Toolset>Executor(...))` helpers.
