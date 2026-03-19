# Repository Guidelines

## Working Style

- Plan before acting. For work touching 1-2 files, state a short plan and implement. For 3+ files, write a step-by-step plan first.
- Read before editing. Search the repo and current docs before changing code.
- Fix root causes. Do not ship local workarounds.
- Keep status updates short during multi-step work.
- Prefer less code, strong contracts, and fail-fast behavior over defensive fallbacks.
- When framework-specific Goa-AI guidance is needed, use the repo-local skill at `.agents/skills/goa-ai/SKILL.md` instead of expanding this file again.

## Go Style

- Use Go 1.24+ and format with `go fmt ./...`.
- Group stdlib imports separately. Let `gofmt` order them.
- Use `lower_snake_case.go` filenames and keep files reasonably small; split proactively when a file starts carrying multiple concerns.
- Exported identifiers require GoDoc. Non-trivial helpers should have brief contract comments.
- Prefer `any` over `interface{}` in new code.
- Keep signatures on one line when they fit; only wrap genuinely long signatures.
- Never ignore errors or discard them with `_`.
- Use `len(x) == 0` directly for slices and maps; do not guard `len` with nil checks.
- Use multi-line blocks. Do not write single-line `if`, `for`, `switch`, or `func` bodies.

## File Organization

Order declarations as:

1. Types
2. Constants
3. Variables
4. Public functions
5. Public methods
6. Private functions
7. Private methods

Within each section, place main logic first and helpers last.

## Goa-AI Repo Rules

- `design/*.go` is the source of truth.
- Never edit generated `gen/` files by hand.
- Put validations in the Goa DSL, not in service internals.
- Use Goa import paths for generator commands, not filesystem paths.
- After design changes, regenerate with `goa gen <module-import-path>/design`.
- Run `goa example <module-import-path>/design` only when new scaffold files are intentionally desired.
- User-facing DSL, runtime, or codegen changes must also update the goa.design docs under `content/en/docs/2-goa-ai/`.
- Keep repo-local skills current with the product. Update the skill files directly rather than adding sidecar delta documents.

## Testing

- Write fast, deterministic, table-driven tests in `*_test.go`.
- Name tests `TestXxx`.
- Prefer `testify/assert`; use `testify/require` only when the test cannot continue after a failure.
- Normal verification flow:
  1. `make lint`
  2. fix issues
  3. `make test`
  4. `make itest` when integration behavior changed

## Safety

| Action | Policy |
|--------|--------|
| `git clean/stash/reset/checkout` | FORBIDDEN |
| `go clean -cache` | FORBIDDEN during normal work |
| edit `gen/` directly | FORBIDDEN |
| new dependencies | explain why first |
| changes touching 3+ files | describe the plan first |

## Repo Skills

- Use `.agents/skills/goa-ai/SKILL.md` for Goa-AI runtime, codegen, MCP, planner, and agent-as-tool guidance.
- Use `.agents/skills/mcp-tool-design/SKILL.md` when shaping MCP-facing tool contracts.
