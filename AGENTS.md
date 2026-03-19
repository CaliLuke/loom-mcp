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
- When bumping the forked `goa.design/goa/v3` replace, do not assume `main` or the default branch. This repo is currently tracking the fork branch `openapi-3.1`. Resolve the freshest relevant fork commit from actual refs and timestamps, then pin that exact pseudo-version in `go.mod`.
- Use local Goa checkout mode for iterative development and the pinned remote fork for CI. The standard toggle is `make goa-local` for local iteration and `make goa-remote` before CI-facing verification or commits. `make goa-status` shows the current mode.
- The canonical local core checkout for this repo is `/Users/luca/code/goa-light`. If local mode points somewhere else, treat that as drift and correct it before interpreting test results.
- Do not compensate in `goa-ai` for upstream `goa-light` or fork regressions. Bump, verify, and if the new upstream commit breaks this repo, stop and return concrete upstream tickets instead of shipping local workarounds.

## Testing

- Write fast, deterministic, table-driven tests in `*_test.go`.
- Name tests `TestXxx`.
- Prefer `testify/assert`; use `testify/require` only when the test cannot continue after a failure.
- Normal verification flow:
  1. `make lint`
  2. fix issues
  3. `make test`
  4. `make itest` when integration behavior changed
- For the MCP fixture and integration harness, verify in this order:
  0. Choose Goa source intentionally: `make goa-local` for iterative local feedback, `make goa-remote` for CI/parity checks
  1. `go test -C integration_tests/fixtures/assistant ./...`
  2. `go test ./integration_tests/framework -count=1`
  3. `go test ./...`
- If a new `goa` fork bump breaks only integration semantics, do not patch around it in `goa-ai`; capture the failing scenarios and return upstream tickets with exact scenario file references.

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
