# MCP Feature Workflow

Use this workflow when adding or catching up any MCP protocol feature in `loom-mcp`.

## Goal

Validate the real Loom MCP surface with a real client first, then implement until that validation is green.

## Process

1. Read the bundled MCP spec

- Start in `third_party/modelcontextprotocol/docs/specification`.
- Use the latest spec version relevant to the feature unless the task explicitly targets an older protocol version.
- Read the normative behavior, examples, capability shape, and related utility sections such as pagination or completion.

2. Confirm the latest official SDK surface

- Check the latest official MCP SDK version and typed API.
- Record the client-visible types and methods the validation must exercise.
- If the SDK and raw spec differ in emphasis, validate both:
  - typed SDK behavior for what clients actually consume
  - raw wire shape for fields or semantics the SDK may hide

3. Define the validation contract

- List the exact client-visible requirements:
  - capability advertisement
  - method availability
  - request params
  - response fields
  - notification shape
  - error behavior
  - pagination
  - completion
- Keep this list small, concrete, and testable.

4. Build a minimal generated fixture

- Prefer a new minimal fixture instead of expanding a broad one.
- The fixture must be generated via Loom's normal design and generation path.
- The fixture should expose current Loom behavior for the feature and as little else as possible.
- Do not handcraft a "target spec" server just to make the client test easy.

5. Add the client-driven validation test

- Use the latest official SDK for the main validation path.
- Add raw protocol checks for fields or transport details the SDK may normalize away.
- Structure the test as focused subtests so multiple mismatches are visible in a single run.
- Write failure messages that explain the spec/SDK expectation and the actual Loom behavior.

6. Run the test red

- The first meaningful state of the validation test should be failing against current Loom behavior.
- Do not change the assertions to make the test pass prematurely.
- Do not skip the test.

7. Implement Loom changes

- Update the appropriate surface:
  - DSL when the contract changes
  - codegen when generated protocol output is wrong or incomplete
  - runtime/transports when behavior or transport semantics are wrong
- Regenerate generated artifacts whenever design or codegen changes require it.
- Keep rerunning the feature validation until it is green.

8. Promote the validation to the permanent regression harness

- Leave the feature validation test checked in.
- Keep it in the normal suite once the feature is green.
- Future regressions in that MCP surface should fail the same client-driven validation first.

## Decision Rules

- If the feature is client-visible, the client validation is authoritative.
- If a handwritten fake server and the generated Loom surface disagree, trust the generated Loom surface and fix Loom, not the test target.
- If the latest SDK exposes a richer surface than Loom currently generates, the test should assert the richer surface and stay red until Loom matches it.
- If the spec requires wire behavior that the SDK does not directly expose, add raw transport assertions rather than dropping the check.

## Prompt Example

For prompt catch-up work, the minimal validation target should cover:

- `initialize` capability advertisement for prompts and related utilities
- `prompts/list`
- `prompts/get`
- `notifications/prompts/list_changed`
- `completion/complete` for prompt arguments

Use both:

- the latest official MCP Go SDK for typed client checks
- raw JSON-RPC or streamable HTTP calls for exact wire-shape checks
