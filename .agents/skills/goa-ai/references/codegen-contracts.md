# Codegen Contracts

Use this file when editing DSL, generators, generated helpers, or MCP codegen behavior.

## Design First

- The DSL in `design/*.go` is the only source of truth.
- Regenerate after design changes. Never patch generated output by hand.
- Keep business logic in non-generated packages.
- Use import paths with Goa commands:
  - `goa gen <module>/design`
  - `goa example <module>/design`

## Generated Surface

- `goa gen` emits tool specs, codecs, workflow/runtime registration helpers, and `AGENTS_QUICKSTART.md`.
- `goa example` emits application-owned scaffold under `internal/agents/`.
- Disable generated quickstart docs from the DSL only when that surface is intentionally undesired.

## Partial Evaluation

- Evaluate static information at generation time.
- Do not generate runtime loops over known collections.
- Do not generate runtime conditionals for compile-time-known cases.
- Prefer small runtime libraries configured by generated data over duplicating near-identical generated logic.

## Type References

- Always derive type names and refs through Goa `NameScope` helpers.
- Prefer `GoTypeRef` and `GoFullTypeRef` over string concatenation.
- Preserve original attributes so locator metadata remains intact.
- Let Goa own pointer and value semantics. Do not force pointer mode outside transport-validation cases.
- Use `codegen.GoTransform(...)` with proper conversion contexts instead of post-processing emitted code.

## Generator Editing Rules

- Edit generators by section and guard early.
- Keep template indentation readable without shifting Go code to match template directives.
- Do not rely on example-specific aliases or hard-coded package names.
- Use `codegen/pathutil.go` helpers for generated path rewrites.
- Use `updateHeader`-style header/import rewrites instead of manual string surgery when moving generated transport code.

## MCP Generator Rules

- Treat MCP as a transport layered on Goa services.
- Compose on Goa codegen rather than forking transport stacks.
- Keep MCP file layout aligned with Goa conventions.
- Reuse Goa encoding/decoding for payload and result transforms.
- Prefer minimal post-processing over handwritten alternative generators.

## Validation And Contracts

- Put validation in the DSL.
- Service internals should trust validated payloads and generated contracts.
- Avoid defensive guards for Goa invariants in DSL and codegen packages.
- Fail fast when invariant holders are broken; do not add catch-all fallbacks.

## Where To Verify

- `DESIGN.md`
- `docs/dsl.md`
- `codegen/`
- `dsl/`
- `expr/`
