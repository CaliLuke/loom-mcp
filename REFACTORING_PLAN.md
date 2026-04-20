# Refactoring Plan

Open refactoring work in `loom-mcp`. Only trigger-based cleanup remains.

## Working Rules

- Keep refactoring and behavior changes separate.
- Change one hotspot at a time.
- Add or tighten tests before structural edits.
- Do not hand-edit generated `gen/` code.
- Treat `design/*.go` as the source of truth.
- When a design contract changes, regenerate intentionally and verify generated churn.
- Prefer smaller helpers, narrower contracts, and explicit ownership over fallback-heavy code.
- One task owns one hotspot and one clear outcome; touches at most 1-2 production files unless it is pure file-splitting.

## Repo Constraints

- Use `make loom-local` for iterative local feedback when core Loom integration is involved.
- Use `make loom-remote` before CI-facing verification or commit preparation when fork parity matters.
- Run `make regen-assistant-fixture` when the assistant fixture design changes.
- User-facing DSL, runtime, or codegen changes may also require docs updates under `content/en/docs/2-loom-mcp/`.
- Do not compensate in `loom-mcp` for upstream `loom` or fork regressions. Capture the failing scenario and stop.

---

## W5 Contract And Generation Boundary Cleanup (trigger-only)

Not a standalone starting point. Trigger only when a contract, DSL, codegen, or assistant fixture change is already in flight.

### Trigger conditions

- `design/*.go` changes
- `dsl/`, `expr/`, or `codegen/` changes
- assistant fixture design changes under `integration_tests/fixtures/assistant/`
- user-facing generated behavior changes that require docs sync

### Required batch shape

Each contract-boundary batch must name:

- handwritten source of truth
- generated outputs expected to change
- fixture regeneration requirement, if any
- docs that must be checked or updated

### Standard verification

- If fixture design changed: `make regen-assistant-fixture`, then `make verify-mcp-local`.
- If DSL/codegen changed: run the relevant generation command intentionally, then review generated churn.
- If user-facing behavior changed: update docs under `content/en/docs/2-loom-mcp/`.

### Exit criteria

- Contract interpretation is owned in fewer places.
- Regeneration steps are explicit and reproducible.
- Small schema fixes no longer require undocumented choreography.

---

## Safety Harness

Before every refactoring batch:

1. Write down the exact behavior that must not change.
2. Run the focused package command for the hotspot.
3. Add characterization coverage if the target behavior is not obvious from existing tests.
4. Keep the edit small enough that the same focused command can be rerun in under a minute.

Escalate only when the boundary requires it:

- Loom integration touched: `make loom-local`, then the focused command, then `make verify-mcp-local`.
- Assistant fixture design touched: `make regen-assistant-fixture`, then `make verify-mcp-local`.
- DSL/codegen/user-facing generated behavior touched: regenerate intentionally, review churn, update docs.
- CI-facing verification before merge: `make loom-remote`, `make lint`, `make test`, `make itest` when integration behavior changed.

## Stop Conditions

- If a refactoring batch starts forcing behavior changes, stop and split the work into a separate bug-fix or feature pass.
- If a shared abstraction adds more indirection than it removes, revert and choose a smaller local refactor.
- If upstream `loom` or fork behavior blocks cleanup, capture the exact failing scenario and stop instead of compensating locally.
