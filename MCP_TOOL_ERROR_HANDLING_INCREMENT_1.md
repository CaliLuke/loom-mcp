# MCP Tool Error Handling Increment 1

## What changed

This increment changes generated MCP adapters so **service/tool execution failures no longer escape as transport errors**.

Before:
- tool method returned an error
- generated adapter converted it into JSON-RPC / transport failure
- client lost the MCP `isError` result shape
- remedy-style guidance was not delivered through the tool result

Now:
- generated adapter sends a final `tools/call` result
- result sets `isError: true`
- content is compact plain text
- Loom remedy metadata is preserved when present
- generated MCP callers convert remote `isError: true` results back into normal Go errors for typed adapters and runtime consumers
- successful non-string tool results now emit compact plain text in `content` and keep the semantic object in `structuredContent`
- generated validation recoveries are now more specific:
  - missing fields say which field to include
  - enum errors list allowed values
  - action/value wrapper payloads suggest the missing nested `value: {}`

## New text shape

Tool execution errors now render as:

```text
[code] safe message
Recovery: retry or correction hint
```

If no retry hint exists:

```text
[code] safe message
```

Code resolution order:
1. `loom.ErrorRemedyCode(err)`
2. Loom error name
3. status-based fallback (`invalid_params`, `not_found`, `internal_error`)
4. `internal_error`

Message resolution:
1. `loom.ErrorSafeMessage(err)`
2. fallback `"Tool execution failed."`

Recovery resolution:
1. `loom.ErrorRetryHint(err)`

## Scope of this increment

Fixed:
- tool/service execution failures routed through generated `sendToolError`
- payload decode and validation failures for generated tool handlers now route through the same `isError: true` text result path
- validation recoveries are no longer limited to the vague fallback `Retry the tool call with arguments that match the tool schema.`
- generated MCP callers now interpret `isError: true` remote responses as errors instead of trying to decode the error text as a success body
- generated successful tool results no longer need JSON text blobs to preserve machine-readable structure

Not fixed yet:
- broader contract cleanup in consuming repos still needs a follow-up bump/regeneration pass
- resources and notification/event payloads still use JSON text in some paths and need a later pass if the same token-light policy should apply outside tool results

## Files changed

- `codegen/mcp/templates/adapter_core.go.tpl`
- `codegen/mcp/templates/adapter_tools.go.tpl`
- `codegen/mcp/client_caller_file.go`
- `codegen/mcp/mcp_types.go`
- `codegen/mcp/sdk_server_file.go`
- `runtime/mcp/caller.go`
- `runtime/mcp/caller_test.go`
- focused regression test under `integration_tests/fixtures/assistant/`

## Why this is framework-safe

This change uses Loom’s generic error helpers:
- `ErrorRemedyCode`
- `ErrorSafeMessage`
- `ErrorRetryHint`
- `ErrorStatusCode`

So the behavior is **not Auto-K specific**. Any Loom service that returns Loom errors or errors mapped into Loom-compatible errors will benefit.
