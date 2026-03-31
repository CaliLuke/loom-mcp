---
name: new-mcp-feature-development
description: Default workflow for adding or catching up MCP protocol features in loom-mcp. Use this when implementing a new MCP spec surface, bringing Loom behavior up to the latest bundled MCP spec, or adding client-visible MCP compliance coverage.
---
# new-mcp-feature-development

Use this skill for any new MCP protocol feature or spec catch-up work in this repo.

This skill is the default workflow for MCP feature development. It is intentionally stricter than normal feature work because MCP changes are client-visible protocol contracts. The job is not done when the framework "mostly works" or when internal tests pass. The job is done when a real client validates the actual generated framework surface and the validation is green.

## Non-Negotiables

- Start from the bundled MCP spec under `third_party/modelcontextprotocol/docs/specification`.
- Check the latest official MCP SDK behavior and types before deciding what the contract is.
- Add a minimal generated fixture first. Do not validate against a handwritten compatibility shim.
- Add a real client-vs-framework validation test before implementing the framework change.
- Let the validation test fail first. Do not weaken it to match current Loom behavior.
- Keep working until the validation is green. Do not skip, quarantine, xfail, or defer the failing compliance test.
- Keep the resulting validation checked in as the permanent regression harness for that MCP feature.
- If SDK decoding hides wire mismatches, add raw protocol assertions alongside SDK assertions.

## Default Workflow

1. Read the latest relevant bundled MCP spec section and note the client-visible contract.
2. Check the latest official MCP SDK and confirm the typed surface the client sees.
3. Identify the exact behavior to validate:
   - initialization capability shape
   - request and response payload shape
   - notifications
   - pagination
   - completion
   - error behavior
4. Create a minimal generated fixture that exposes Loom's current behavior for just that feature.
5. Add a real client-driven validation test against that fixture.
6. Run the validation and let it go red.
7. Implement framework changes in DSL, codegen, runtime, or transports until the validation goes green.
8. Keep the validation in the main suite as the permanent regression test for that feature.

## Fixture Rules

- Prefer a new minimal fixture over extending a large existing fixture when the feature can be isolated cleanly.
- The fixture must be generated through the normal Loom design/codegen path.
- The fixture must expose the actual current framework behavior, not a hand-shaped approximation of the target spec.
- Keep the fixture focused on one protocol surface so failures stay easy to interpret.
- If the feature touches multiple related MCP surfaces, keep the fixture minimal but complete enough to cover the user-visible contract.

## Validation Rules

- Validate with the latest official MCP SDK first whenever the feature is client-visible through that SDK.
- Add raw JSON-RPC or streamable HTTP checks when the SDK:
  - fills defaults
  - drops unknown fields
  - normalizes wire shapes
  - hides transport-level mismatches
- Failure messages must identify:
  - the MCP method or capability under test
  - the latest expected behavior
  - the actual Loom behavior observed
- Do not write assertions that simply freeze current Loom output when the purpose of the test is spec catch-up.
- Do not convert a compliance failure into a "known mismatch" pass condition.

## Forbidden Shortcuts

- No handwritten fake server as the primary validation target.
- No compatibility shim that makes the test green without changing Loom's real generated/framework output.
- No skipped failing compliance tests.
- No TODO guards that suppress failures until "later".
- No partial landing that leaves the real client validation red.
- No papering over SDK/spec mismatches in the test instead of fixing Loom.

## Prompt Feature Application

The first consumer of this workflow is prompt catch-up work.

For prompts, the validation harness should:

- use a new minimal prompt-focused generated fixture
- use the latest official MCP Go SDK plus raw MCP requests
- validate:
  - `initialize`
  - `prompts/list`
  - `prompts/get`
  - `notifications/prompts/list_changed`
  - `completion/complete`
- start red
- remain in the suite as the permanent regression harness once Loom catches up

## Verification Ladder

1. Use the bundled MCP spec as the source of truth.
2. Run the new feature-specific validation test first and iterate on it.
3. Regenerate fixtures when their design changes.
4. Run `make verify-mcp-local`.
5. Run `go test ./...`.
6. Run the full repo verification flow before calling the feature done.

MCP feature work is not complete until the client-facing validation harness is green.

## References

- `references/workflow.md`: decision-complete workflow for MCP feature development
- `references/checklist.md`: implementation and verification checklist
- `.agents/skills/loom-mcp/SKILL.md`: general `loom-mcp` framework guidance
