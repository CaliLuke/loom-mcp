# Repository Guidelines

## Working Style

- Plan before acting. For work touching 1-2 files, state a short plan and implement. For 3+ files, write a step-by-step plan first.
- Read before editing. Search the repo and current docs before changing code.
- Fix root causes. Do not ship local workarounds.
- Keep status updates short during multi-step work.
- Prefer less code, strong contracts, and fail-fast behavior over defensive fallbacks.
- When framework-specific `loom-mcp` guidance is needed, use the repo-local skill at `.agents/skills/loom-mcp/SKILL.md` instead of expanding this file again.

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

## loom-mcp Repo Rules

- Design packages are the source of truth. This includes top-level `design/*.go`, service-local design packages such as `registry/design`, and fixture designs such as `integration_tests/fixtures/assistant/design`.
- Never edit generated `gen/` files by hand.
- Put validations in the Goa DSL, not in service internals.
- Use Goa import paths for generator commands, not filesystem paths.
- After design changes, regenerate with `loom gen <module-import-path>/design`.
- Run `loom example <module-import-path>/design` only when new scaffold files are intentionally desired.
- Prefer repo `make` targets for standard generation flows:
  - `make gen-registry` for `registry/design`
  - `make regen-assistant-fixture` for `integration_tests/fixtures/assistant/design`
- User-facing DSL, runtime, or codegen changes must also update the repo docs under `docs/` and any corresponding external docs set that publishes this project.
- The upstream Model Context Protocol reference lives in the submodule at `third_party/modelcontextprotocol`; the canonical spec folder for this repo is `third_party/modelcontextprotocol/docs/specification`.
- Keep repo-local skills current with the product. Update the skill files directly rather than adding sidecar delta documents.
- When bumping the forked `github.com/CaliLuke/loom` replace, do not assume `main` or the default branch. This repo is currently tracking the fork branch `openapi-3.1`. Resolve the freshest relevant fork commit from actual refs and timestamps, then pin that exact pseudo-version in `go.mod`.
- Do not use web search to verify whether a release exists. Check releases, tags, or published module versions from the authoritative source directly, such as `git ls-remote`, `gh release view`, or `go list -m -versions`.
- Use local Loom checkout mode for iterative development and the pinned remote fork for CI. The standard toggle is `make loom-local` for local iteration and `make loom-remote` before CI-facing verification or commits. `make loom-status` shows the current mode.
- After switching to local mode, use `make verify-mcp-local` for the default MCP fixture/framework verification ladder.
- Use `make regen-assistant-fixture` when the assistant MCP fixture design changes so generated churn is intentional and reproducible.
- The canonical local core checkout for this repo is `/Users/luca/code/loom-mono/loom`. If local mode points somewhere else, treat that as drift and correct it before interpreting test results.
- Do not compensate in `loom-mcp` for upstream `loom` regressions. Bump, verify, and if the new upstream commit breaks this repo, stop and return concrete upstream tickets instead of shipping local workarounds.

## Testing

- Write fast, deterministic, table-driven tests in `*_test.go`.
- Name tests `TestXxx`.
- Prefer `testify/assert`; use `testify/require` only when the test cannot continue after a failure.
- Treat pre-commit and commit-time hooks as mandatory. If a hook fails for any reason, fix the problem before committing; do not bypass the hook, do not use `--no-verify`, and do not classify the failure as unrelated debt to avoid fixing it.
- When bumping frameworks or dependencies, or when doing refactors or general improvement work, run the full verification suite rather than only targeted package checks. At minimum this means `make lint`, `make test`, and `make itest` before calling the work done.
- Normal verification flow:
  1. `make lint`
  2. fix issues
  3. `make test`
  4. `make itest` when integration behavior changed
- For local Loom framework validation, verify in this order:
  1. Choose Loom source intentionally: `make loom-local` for iterative local feedback, `make loom-remote` for CI/parity checks.
  2. Regenerate all affected outputs intentionally:
     - run `make gen-registry` when registry design or upstream generation behavior changed
     - run `make regen-assistant-fixture` when the assistant fixture design or upstream generation behavior changed
  3. Run `make verify-mcp-local`.
  4. Run the full repo suite: `make lint`, `make test`, and `make itest`.
- If a new `loom` fork bump breaks only integration semantics, do not patch around it in `loom-mcp`; capture the failing scenarios and return upstream tickets with exact scenario file references.

## Safety

| Action | Policy |
|--------|--------|
| `git clean/stash/reset/checkout` | FORBIDDEN |
| `go clean -cache` | FORBIDDEN during normal work |
| edit `gen/` directly | FORBIDDEN |
| new dependencies | explain why first |
| changes touching 3+ files | describe the plan first |

## Repo Skills

- Use `.agents/skills/loom-mcp/SKILL.md` for `loom-mcp` runtime, codegen, MCP, planner, and agent-as-tool guidance.
- For any new MCP protocol feature or spec catch-up work, use `.agents/skills/new-mcp-feature-development/SKILL.md` as the default workflow. MCP feature work must start with a real client-vs-framework validation test and proceed red-green until fully green.
- Use `.agents/skills/loom-mcp-release/SKILL.md` when cutting, tagging, pushing, or verifying a `loom-mcp` release from finished code.
- Use `.agents/skills/mcp-tool-design/SKILL.md` when shaping MCP-facing tool contracts.
