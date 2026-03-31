# MCP Feature Checklist

Use this checklist while implementing a new MCP feature or a spec catch-up.

## Before Coding

- Identify the latest bundled spec section under `third_party/modelcontextprotocol/docs/specification`.
- Check the latest official MCP SDK version and the client-visible types involved.
- Write down the exact client-visible requirements to validate.
- Choose or create a minimal generated fixture for the feature.

## Validation First

- Add a real client-vs-framework validation test.
- Use the latest official SDK.
- Add raw wire assertions where SDK decoding hides mismatches.
- Run the test and confirm it fails for the right reason.

## Implementation

- Change DSL first when the protocol contract or generated surface should change.
- Regenerate code after design changes.
- Update codegen/runtime/transport behavior as needed.
- Keep rerunning the feature validation until it is green.

## Do Not Do This

- Do not validate primarily against a handwritten fake server.
- Do not weaken assertions to match Loom's current incorrect behavior.
- Do not skip, xfail, or quarantine the failing compliance test.
- Do not land partial support while the real client validation is still red.

## Verification

- Run the feature-specific validation first.
- Regenerate fixtures intentionally when their design changes.
- Run `make verify-mcp-local`.
- Run `go test ./...`.
- Run the full repo verification flow before considering the work complete.

## Exit Criteria

- The real client validation is green.
- The generated/framework MCP surface matches the intended spec/SDK contract.
- The validation remains checked in as the permanent regression harness.
