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
- `loom example` emits application-owned scaffolds under
  `internal/agents/<service>/`. Bootstrap, planner, and executor paths include
  the service name so same-named agents in different services cannot collide.
  Every scaffold uses `SkipExist`; later runs preserve application changes.
- Disable generated quickstart docs from the DSL only when that surface is intentionally undesired.

## Partial Evaluation

- Evaluate static information at generation time.
- Do not generate runtime loops over known collections.
- Do not generate runtime conditionals for compile-time-known cases.
- Prefer small runtime libraries configured by generated data over duplicating near-identical generated logic.
- Use `json.Deterministic(true)` for every generation-time JSON value embedded
  in generated Go source, including schemas, examples, annotations, discovery
  metadata, and recovery hints. Do not alter runtime JSON behavior to stabilize
  source generation.

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
- Own every cross-generator behavior through stable section identifiers and
  evaluated generator data. MCP mount, handler, endpoint initialization, SSE,
  and client-constructor behavior are loom-mcp-owned sections with exact
  cardinality checks. Missing or duplicate upstream sections must fail
  generation.
- Never inspect, parse, or mutate rendered Go source to extend another
  generator. Replace a named section or emit a separate owned section instead.
- High-level generation contract tests must run preparation in production order
  before rendering. Combined agent/MCP tests must also generate the core
  transport files and both plugin outputs before claiming that generated code
  compiles. Direct `Generate` calls are reserved for intentional generator-seam
  tests.

## MCP Generator Rules

- Treat MCP as a transport layered on service designs.
- Compose on the existing codegen pipeline rather than forking transport stacks.
- Keep MCP file layout aligned with current repository conventions.
- Reuse generated encoding/decoding for payload and result transforms.
- Prefer loom-mcp-owned sections around the upstream generator over
  post-processing or handwritten alternative transport stacks.
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
- Generated retry and repair examples must use the same canonical union-aware
  synthesizer as adapter recovery hints. Every emitted example must validate
  against the input schema it accompanies, including discriminator envelopes.
- Generated MCP `ToolInfo` surfaces must preserve MCP Tool fields across the
  service, adapter, local-provider, and SDK paths: `name`, `title`,
  `description`, `inputSchema`, `outputSchema`, `annotations`, `_meta`, and
  `icons`.
- The official MCP Go SDK owns protocol versions and transport behavior. Do not
  require or synthesize MCP `JSONRPC` declarations. Keep explicit non-MCP
  `JSONRPC` transports unchanged.
- The shared `runtime/mcp/sdkbridge` selects the POST response type before the
  SDK. SSE is the default. `StreamableHTTP.JSONResponse` selects JSON. Return
  HTTP 406 when the request does not accept the selected type. If the request
  accepts only that type, add both MCP media types only to a cloned SDK request.
  Do not modify the original request.
- The shared `runtime/mcp/sdkbridge` installs receiving middleware that
  converts untyped adapter errors into typed JSON-RPC errors. Preserve existing
  typed SDK errors. Map invalid parameters and missing resources to `-32602`.
  Map internal and unknown errors to `-32603` with only declared safe messages.
  The bridge rejects request envelopes with an explicit null `id` before SDK
  dispatch, returning HTTP 400 with JSON-RPC `-32600`. Do not parse or rewrite
  response bodies for SDK session errors.
- Generated SDK files contain service descriptors, typed dispatch closures, and
  result conversion. The bridge owns registration loops, common options, HTTP
  plumbing, sessions, subscriptions, request context, CORS, and observation.
  Generated configuration uses a literal compatibility version. Construction
  fails when it differs from `sdkbridge.CompatibilityVersion`.
- MCP generation emits only the minimal service types, `MCPAdapter`,
  local-provider registration, OAuth discovery, prompt provider, and
  `SDKServer`. Absence tests must reject generated native MCP clients, servers,
  protocol-version files, batching, SSE extensions, cancellation registries,
  session stores, and broadcasters.
- The shared SDK bridge installs the
  `runtime/mcp/sdkclient.WithClientFeatures` adapter in request contexts.
  Service code can then issue elicitation through official multi-round-trip
  `InputRequests` and `InputResponses`. Generated tool, prompt, and resource
  closures preserve typed payload and result conversion. The bounded and
  versioned request state carries issued input contracts, the exact pending
  round, and prior client responses.
  Encrypt and authenticate every round with AES-GCM. Bind the state to the
  original MCP method
  and logical parameters, and plumb the generated server's stable 32-byte
  `RequestStateKey` into the adapter. Endpoint replicas must share that key.
  Protected responses remain client assertions and must not drive
  authorization.
  One runtime elicitation call produces one input request, and multi-step flows
  require a `2026-07-28` client. Official modern streamable HTTP is stateless
  and sessionless, with one POST per retry. Preserve input-required errors
  through adapter safe-error mapping, and reject wrong response types, invalid
  actions, response IDs outside the pending round, changed input contracts on
  re-entry, and response/state limit violations. Preserve invalid-client-input
  markers through tool dispatch so validation failures remain protocol errors
  rather than tool-level `isError` results. Sampling and roots are
  deprecated in MCP `2026-07-28` and are not installed as runtime client
  features. Do not duplicate SDK request/response or request-state conversion
  in generated code.
- The shared SDK bridge preserves `_meta.progressToken` in request context.
  Thus, `runtime/mcp.ReportProgress` can send the original token.
- The bridge response observer implements `Unwrap() http.ResponseWriter`.
  Thus, `http.ResponseController` can use optional transport interfaces.
- Generated `SDKServerOptions` expose `TransportObserver transport.Observer`.
  The bridge installs `transport.HTTPMiddleware` around the SDK handler.
- Generated SDK servers always expose `SDKServerOptions.RuntimeCORS`. The
  bridge keeps default cross-origin protection unless the application changes it.
- Generated closures identify designed `WatchableResource` URIs. The bridge
  validates subscriptions and `SDKServer.ResourceUpdated(ctx, uri)` calls.
  Watchable resources are invalid with stateless Streamable HTTP.
- Generated adapters collect streaming tool and resource method values into one
  standard MCP result. Intermediate status uses `runtime/mcp.ReportProgress`.
- The shared SDK bridge binds each session to one verified principal. It checks
  the principal on POST, GET, and DELETE. Authentication middleware must wrap
  the generated handler.
- Dynamic-only MCP prompt services enable prompt capabilities during expression
  finalization so generated adapters and `loom example` scaffolds agree on the
  prompt-provider constructor contract.
- Generated MCP services retain their structural tools/list and tools/call
  methods and empty-catalog adapter helpers even when no tools are declared;
  prompt-only and resource-only servers must still compile without advertising
  the tools capability.
- `SkillDirectory` alone is a resource surface. It must trigger resource
  methods, resource types, initialize capabilities, adapter handlers, and SDK
  result conversions even when no method-backed resource is declared.
- Generated adapter options define the maximum resource grant. Request-scoped
  allowed resource names are an independent narrowing predicate and must never
  be unioned into that server grant. Server and request denies are additive and
  take precedence. Generated raw policy headers remain untrusted transport
  input, not proof of authentication or authorization.
- Generated `tools/call` adapters normalize omitted, whitespace-only, and JSON
  `null` arguments to `{}` before strict payload decoding.
- Optional arbitrary JSON in generated MCP service types uses
  `loom.Nullable[any]`; do not force those fields back to custom physical Go
  types. SDK, prompt-provider, interceptor, projected-tool, and local-provider
  boundaries must preserve contained `jsontext.Value` bytes and marshal other
  concrete values only when a raw JSON boundary requires them.
- Generated `MCPAdapter` types must satisfy their generated `Service` interface
  directly. Keep unary result and error signatures aligned, and emit a
  compile-time interface assertion in the adapter.
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
- Generated method dispatchers with injected fields must validate `MapPayload`
  output before field injection. Mapper errors, nil results, and wrong types
  become tool errors. The dispatcher must not panic.
- Projected rich tools use a feature-by-feature contract. For `Inject(...)`,
  generated MCP execution reads verified `ToolCallMeta` from
  `mcpruntime.WithProjectedToolCallMeta` and fails if it is absent. For
  `BoundedResult(...)`, encode the semantic result and runtime bounds with
  `runtime.EncodeCanonicalToolResult`. Return the same canonical JSON through
  MCP `structuredContent` and text content. Keep `ResultReminder(...)`
  agent-only. Reject `Confirmation(...)` and `ServerData(...)` during
  validation because MCP cannot preserve their authorization and privacy
  contracts.
- Generated tool specs must keep transport/public payload shapes distinct from
  advertised model-facing shapes. `Inject(...)` fields remain in generated
  public payload structs, codecs, validation, and method dispatch payloads, but
  must be removed from `ToolSpec.Payload.Schema`, `ExampleJSON`, and
  `ExampleInput`, including the advertised `required` list. Generated injection
  helpers must derive pointer-versus-value assignment from the prepared public
  payload attribute using the same default semantics as public type generation;
  tests for this contract must run `Prepare` before rendering.
- `toolEntry.ConstName` is the authoritative unique Go identifier for a tool
  after specs construction. Agent-level aggregators, typed aliases, codecs, and
  call builders must reuse it rather than recomputing `Goify(tool.Name)`.
- Registry-backed generated specs are refreshable only before runtime
  registration. `Specs()` returns a locked snapshot and `FreezeSpecs()` is the
  mandatory registration boundary; discovery after freezing is an error.
- Registry-backed generated schema validators resolve local JSON Pointer
  references against the root schema, fail closed on unsupported or unresolved
  references, and apply `minLength`/`maxLength` to Unicode code points rather
  than UTF-8 bytes.
- Generated registry clients expose
  `Capabilities(context.Context) (SearchCapabilities, error)`, preserve caller
  cancellation and trace context, and never conflate transport failures with
  remote capability values.
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
  fields. Narrow mode must remove weak fuzzy candidates whenever an exact,
  normalized, prefix, or contains name/title tier is present.
  `ToolDiscoveryCallTemplateArg` may add exemplar optional arguments to those
  examples, but must not change validation semantics. Hidden real
  tools are called through `call_tool`; direct hidden JSON-RPC calls require
  `AllowDirectHiddenCalls`, and SDK compact mode must reject that option because
  unregistered SDK tools cannot be directly invoked. Projected MCP tools must follow the same `AlwaysVisible`,
  `search_tools`, and `call_tool` behavior as method-level MCP tools.
- Generated MCP tool packages also emit an in-process progressive-discovery
  `ToolsetRegistration` constructor. It derives compact tool specs from the
  adapter's synthetic and visible catalogs, invokes search/proxy/real tools
  through the adapter's existing helpers and interceptors, and converts final
  structured/error results into planner-native results without protocol
  initialization, session state, JSON-RPC, or transport DTO work in application
  code.

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
