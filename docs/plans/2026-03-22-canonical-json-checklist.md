# Runtime Audit Checklist

This document tracks the March 22 runtime audit follow-up so the thread survives compaction.

## Scope

- Completed batch: canonical JSON boundary strictness in `runtime/mcp`
- Remaining hotspots:
  - `runtime/toolregistry/executor/executor.go`
  - `runtime/agent/runtime/runtime_options.go`
  - `runtime/agent/memory/event_data.go`
- Goal: close the remaining audit findings while reducing complexity in the touched seams.

## Batch 1: Stop Silent Map-Key Data Loss

- [x] Capture the concrete finding and affected file
- [x] Read the current runtime/MCP contract and existing package tests
- [x] Add direct tests for canonical JSON marshalling behavior
- [x] Change map normalization to reject unsupported key kinds instead of skipping entries
- [x] Verify `go test ./runtime/mcp`
- [x] Verify `go test ./...`
- [x] Reassess follow-up cleanup in `runtime/mcp/runtime.go`

## Follow-Up Queue

- [x] Decide whether canonical JSON should support text-marshaled map keys or keep the stricter string-only contract
- [x] Review `MarshalCanonicalJSON` and `UnmarshalCanonicalJSON` for other silent coercions or stdlib divergences
- [x] Split `runtime/mcp/runtime.go` if the boundary logic remains too broad after behavior fixes

## Batch 2: Remaining Audit Findings

- [x] Expand the tracked scope beyond the canonical JSON batch
- [x] Stop treating result-stream destroy failures as authoritative after a valid tool result has already been acknowledged
- [x] Add a focused executor regression test for the destroy-failure success path
- [x] Persist `ServerData`, `Telemetry`, and `RetryHint` in memory-backed tool-result events
- [x] Extract tool-result memory projection helpers so runtime subscriber logic is smaller and clearer
- [x] Reject fractional float-backed values when decoding persisted integer or duration fields
- [x] Add direct persistence/decode tests for the stricter numeric boundary
- [x] Verify focused runtime, memory, and executor package tests
- [x] Verify `go test ./...`
- [x] Verify `make lint`
- [x] Verify `make test`
- [x] Verify `make itest`

## Notes

- Repo guidance for this batch: prefer less code and strong contracts over defensive fallback behavior.
- This is refactor work only. No intended external behavior change beyond replacing silent loss or truncation with explicit persistence or explicit errors.
- Decision: keep the stricter string-only map-key contract. Named string aliases are accepted; text-marshaled non-string keys are rejected.
- Review result: no other silent data-loss path in the canonical codec warranted an immediate fix. The remaining divergences are strict failures or explicit normalization choices.
- Tool-result memory persistence now keeps server-only payloads, telemetry, and retry guidance instead of projecting them away during hook handling.
- Persisted numeric event fields accept float-backed values only when they are mathematically integral.
- Registry result-stream destroy failures are now logged as cleanup failures after a successful acknowledgment rather than overriding a valid tool result.
