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
- Preserve custom union envelope keys from `Meta("oneof:type:field", "...")`
  and `Meta("oneof:value:field", "...")` in schemas, examples, validation,
  and generated marshal/unmarshal methods.
- Generated union decoders must return structured validation errors for invalid
  discriminators and missing nested union values. Invalid discriminators should
  use the allowed enum values; missing `value` envelopes should use the generated
  missing-field error path, not raw JSON decoder failures.
- Generated MCP `ToolInfo` surfaces must preserve MCP Tool fields across service,
  JSON-RPC server, JSON-RPC client, and SDK paths: `name`, `title`,
  `description`, `inputSchema`, `outputSchema`, `annotations`, `_meta`, and
  `icons`.
- Method-backed toolset tools may project into MCP only when the evaluated
  design exposes both `AgentRuntime` and `MCPSurface` and places the tool with
  `MCPPlacement(service, mcpServer)`. Codegen should trust validation for
  same-service placement, resolved MCP server names, and catalog collision
  checks, while still failing fast if adapter catalog merging sees a duplicate.
- Generated toolset owner packages must own method-backed execution through
  exported `Dispatch<Tool>Method(...)` helpers. Generated runtime service
  executors and projected MCP `tools/call` cases should call those dispatchers
  rather than duplicating payload/result transforms, injection, bounds,
  server-data projection, retry hints, or tool-error mapping. Registry provider
  loops still use their generated provider adapter path unless that path is
  explicitly unified in a later milestone.
- Generated tool specs must keep transport/public payload shapes distinct from
  advertised model-facing shapes. `Inject(...)` fields remain in generated
  public payload structs, codecs, validation, and method dispatch payloads, but
  must be removed from `ToolSpec.Payload.Schema`, `ExampleJSON`, and
  `ExampleInput`, including the advertised `required` list.
- `toolEntry.ConstName` is the authoritative unique Go identifier for a tool
  after specs construction. Agent-level aggregators, typed aliases, codecs, and
  call builders must reuse it rather than recomputing `Goify(tool.Name)`.
- Registry-backed generated specs are refreshable only before runtime
  registration. `Specs()` returns a locked snapshot and `FreezeSpecs()` is the
  mandatory registration boundary; discovery after freezing is an error.
- Projected MCP `ToolInfo` schemas must come from the generated toolset
  `tools.ToolSpec` payload and result schemas, not from service-method-only
  schema extraction. This keeps runtime specs, JSON-RPC adapters, and SDK
  servers on one schema contract.
- Progressive tool discovery is opt-in through `MCPAdapterOptions.ToolSearch`.
  When enabled, generated `tools/list` is the compact authoritative public
  catalog: `search_tools`, `call_tool`, and validated `AlwaysVisible` pins.
  Search descriptors carry discovery metadata and omit schemas unless requested.
  `search_tools` must normalize snake_case names into searchable words, rank
  natural-language token overlap, use `github.com/sahilm/fuzzy` only for the
  name/title fuzzy tier, support DSL/runtime tuning through `ToolSearch(...)`
  and generated `ToolSearchOptions`, explain `why_matched`, and include exact
  `call_tool` JSON examples in both text guidance and structured descriptor
  fields. `ToolDiscoveryCallTemplateArg` may add exemplar optional arguments to
  those examples, but must not change validation semantics. Exact or near-exact
  name/title matches should suppress weak broad matches by default. Hidden real
  tools are called through `call_tool`; direct hidden JSON-RPC calls require
  `AllowDirectHiddenCalls`, and SDK compact mode must reject that option because
  unregistered SDK tools cannot be directly invoked. Projected MCP tools must follow the same `AlwaysVisible`,
  `search_tools`, and `call_tool` behavior as method-level MCP tools.

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
