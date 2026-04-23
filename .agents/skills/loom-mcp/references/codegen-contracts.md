# Codegen Contracts

Use this file when editing DSL, generators, generated helpers, or MCP codegen behavior.

## Design First

- The DSL in `design/*.go` is the only source of truth.
- Regenerate after design changes. Never patch generated output by hand.
- Keep business logic in non-generated packages.
- Use import paths with generation commands:
  - `loom gen <module>/design`
  - `loom example <module>/design`

## Generated Surface

- `loom gen` emits tool specs, codecs, workflow/runtime registration helpers, and `AGENTS_QUICKSTART.md`.
- `loom example` emits application-owned scaffold under `internal/agents/`.
- Disable generated quickstart docs from the DSL only when that surface is intentionally undesired.

## Partial Evaluation

- Evaluate static information at generation time.
- Do not generate runtime loops over known collections.
- Do not generate runtime conditionals for compile-time-known cases.
- Prefer small runtime libraries configured by generated data over duplicating near-identical generated logic.

## Type References

- Always derive type names and refs through `NameScope` helpers.
- Prefer `GoTypeRef` and `GoFullTypeRef` over string concatenation.
- Preserve original attributes so locator metadata remains intact.
- Let the shared type system own pointer and value semantics. Do not force pointer mode outside transport-validation cases.
- Use `codegen.GoTransform(...)` with proper conversion contexts instead of post-processing emitted code.

## Generator Editing Rules

- Edit generators by section and guard early.
- Keep template indentation readable without shifting Go code to match template directives.
- Do not rely on example-specific aliases or hard-coded package names.
- Use `codegen/pathutil.go` helpers for generated path rewrites.
- Use `updateHeader`-style header/import rewrites instead of manual string surgery when moving generated transport code.

## MCP Generator Rules

- Treat MCP as a transport layered on service designs.
- Compose on the existing codegen pipeline rather than forking transport stacks.
- Keep MCP file layout aligned with current repository conventions.
- Reuse generated encoding/decoding for payload and result transforms.
- Prefer minimal post-processing over handwritten alternative generators.
- For `OneOf(...)` unions, preserve explicit discriminator tags from
  `Meta("oneof:type:tag", "...")` across MCP schemas, agent tool schemas,
  and generated union helpers. Do not fall back to derived type names when an
  explicit tag is present.

## Validation And Contracts

- Put validation in the DSL.
- Service internals should trust validated payloads and generated contracts.
- Avoid defensive guards for evaluated design invariants in DSL and codegen packages.
- Fail fast when invariant holders are broken; do not add catch-all fallbacks.

## Where To Verify

- `DESIGN.md`
- `docs/dsl.md`
- `codegen/`
- `dsl/`
- `expr/`
