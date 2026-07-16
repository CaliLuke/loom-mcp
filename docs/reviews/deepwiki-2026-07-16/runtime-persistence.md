# DeepWiki runtime and persistence delta review

Review date: 2026-07-16
DeepWiki source revision: `dd837947` (indexed 2026-07-15)
Workspace reviewed: `067b8ad` (`fix: close DeepWiki audit gaps`)

## Scope and method

Read each runtime/planner/event/policy/telemetry page (4 through 4.7) and each
persistence/Pulse page (8 through 8.2) at the URLs below. Compared their claims
with the current implementation, `docs/runtime.md`, and the repo-local
`loom-mcp` skill plus its runtime and memory references. This is a **delta** to
the 2026-07-15 review: it records what remains inaccurate in the indexed wiki,
and whether the documentation/skill/product follow-up is already complete at
HEAD. No product changes were made in this review.

## Result

The current user documentation and runtime skill cover the high-risk contracts
identified by the DeepWiki audit. The main discrepancy is index freshness:
DeepWiki describes the 15 July implementation, while the 16 July commit
intentionally changed several of those contracts. Do not use the current
DeepWiki pages as normative operational guidance until they are re-indexed.

| Area | Local docs and skill | Indexed DeepWiki | Action |
| --- | --- | --- | --- |
| Planner retries, registration, and active-time budgets | Accurate | Uses pre-fix/ambiguous wording | Re-index and retain the current contract text. |
| Authority, memory, and Mongo persistence | Accurate | Describes the retired single-document memory write path | Re-index urgently. |
| Pulse delivery | Accurate | Omits the durable manual-ack path | Re-index; keep auto-ack labelled UI-only. |
| Runtime telemetry | Accurate | Conflates trace domains and legacy telemetry names | Re-index and verify dashboards against current names. |

## Page-by-page log

| ID | DeepWiki page | Priority | Discrepancy and repository evidence | Recommended action / status |
| --- | --- | --- | --- | --- |
| R-01 | [4 Runtime Architecture](https://deepwiki.com/CaliLuke/loom-mcp/4-runtime-architecture) | P1 | “Replay-safe” is easy to read as exactly-once execution. Planner and tool activity attempts can repeat after a late failure; the live workflow is durable, but effects still need idempotency. `runtime/agent/planner/planner.go:59-67`; `runtime/agent/runtime/planner_activities.go:30-69`. | **Resolved locally.** `docs/runtime.md` and `references/runtime-contracts.md` distinguish logical turns from attempts. Re-index DeepWiki. |
| R-02 | [4.1 Runtime Coordinator & Registration](https://deepwiki.com/CaliLuke/loom-mcp/4.1-runtime-coordinator-and-registration) | P2 | The page says registration is sealed but does not make the full boundary operational: model registration also closes after `Seal` or first submitted run, and replacement runtime is required for model rotation. `runtime/agent/runtime/runtime_registration.go:25-53`; `runtime-contracts.md` “Registration Lifecycle”. | **Resolved locally.** Retain the skill rule; re-index. |
| R-03 | [4.2 Workflow Engine & Execution Loop](https://deepwiki.com/CaliLuke/loom-mcp/4.2-workflow-engine-and-execution-loop) | P1 | The page says `PlanStart` occurs once. It is once per *logical turn*, not necessarily once per activity attempt. It also presents `TimeBudget` as elapsed duration, while clarification/confirmation/typed-input/external waits pause active-time accounting. `planner.go:59-67`; `runtime-contracts.md` “Planner Activity Retries” and “Time Budgets”. | **Resolved locally.** Re-index; preserve this wording in all generated user guides. |
| R-04 | [4.3 Planner Interface & Workflow Graphs](https://deepwiki.com/CaliLuke/loom-mcp/4.3-planner-interface-and-workflow-graphs) | P1 | Repeats the exactly-once `PlanStart` claim. It also describes `ConsumeStream` without the critical two-path rule: a runtime-decorated client must be drained directly, while `ConsumeStream` is for a raw client; mixing them double-emits chunks. `runtime-contracts.md` “Planner Streaming”. | **Resolved locally.** Re-index. A future regression test should keep docs/skill examples from combining the two streaming paths. |
| R-05 | [4.4 Hooks, Streaming & Event Pipeline](https://deepwiki.com/CaliLuke/loom-mcp/4.4-hooks-streaming-and-event-pipeline) | P1 | Calling `EventKey` exact-once delivery is overbroad. Workflow state is execution authority; `runlog.Store` is fail-closed canonical introspection; streams, hooks, and memory have distinct projection/delivery contracts. `runtime-contracts.md` “Event Authority And Reliability”. | **Resolved locally.** Re-index. Keep `EventKey` documented as an idempotency key, not a general delivery guarantee. |
| R-06 | [4.5 Memory, Transcript & Session Management](https://deepwiki.com/CaliLuke/loom-mcp/4.5-memory-transcript-and-session-management) | P1 | It conflates `memory.Searcher` (indexed raw event search) with entry-shaped long-term `memory.Service`, and claims `RunEventStore` can rebuild the transcript/deterministically resume a run. `memory.Store` is derived raw events; runlog is hook-shaped introspection; workflow state is live authority. `references/user-guides/memory.md`; `runtime-contracts.md` “Event Authority And Reliability”. | **Resolved locally.** Re-index. The local memory guide is now the correct source for user-facing authority boundaries. |
| R-07 | [4.6 Policy Enforcement & Human-in-the-Loop](https://deepwiki.com/CaliLuke/loom-mcp/4.6-policy-enforcement-and-human-in-the-loop) | P1 | It implies `CapsState` enforces time. `CapsState.ExpiresAt` is source-compatible but ignored; deterministic `TimeBudget` deadlines enforce active runtime work. A clarification pause is also not a `RetryHint`. `runtime/agent/policy/policy.go:148-155`; `runtime-contracts.md` “Time Budgets”. | **Resolved locally.** Re-index. |
| R-08 | [4.7 Telemetry & Observability](https://deepwiki.com/CaliLuke/loom-mcp/4.7-telemetry-and-observability) | P1 | It describes a continuous Temporal trace and calls the debug server “Pulse.” Temporal activity spans are new roots linked to the scheduling span; the debug HTTP server, Pulse stream sink, generated MCP telemetry, and transport observer are separate surfaces. Current runtime metric names are `loom_mcp.runtime.*`, and `tool.execute` is emitted from newly inserted canonical events. `runtime/agent/runtime/runtime_observability.go:18-92`; `runtime-contracts.md` “Runtime Observability”. | **Resolved locally.** Re-index and update any dashboard/query references to the current metric names. |
| P-01 | [8 Persistence & Infrastructure](https://deepwiki.com/CaliLuke/loom-mcp/8-persistence-and-infrastructure-features) | P1 | The table labels memory as a generic snapshot store and does not say which stores are authoritative or durable after a Temporal worker replacement. Temporal history does not make the default runlog/session stores persistent. `docs/runtime.md` production configuration; `references/user-guides/memory.md` authority table. | **Resolved locally.** Re-index. |
| P-02 | [8.1 MongoDB Persistence](https://deepwiki.com/CaliLuke/loom-mcp/8.1-mongodb-persistence-layer) | P0 | The page says memory uses `$push` into one `events` array. Current Mongo memory writes immutable buckets into a companion events collection, reads the legacy document for compatibility, and stable-sorts combined events. Restoring `$push` would reintroduce unbounded-document growth. `features/memory/mongo/clients/mongo/client.go:31-58,102-165`; `references/user-guides/memory.md`. | **Resolved locally; DeepWiki is materially stale.** Re-index before using the page for operations or new adapters. |
| P-03 | [8.2 Pulse Streaming](https://deepwiki.com/CaliLuke/loom-mcp/8.2-pulse-streaming-infrastructure) | P1 | The page's poison-message description applies only to `Subscribe` (auto-ack UI fanout). Durable consumers must use `SubscribeManual`, persist idempotently by `EventKey`, then call `Delivery.Ack`; malformed manual payloads remain pending until the application dead-letters them. `features/stream/pulse/subscriber.go:160-305`; `runtime-contracts.md` “Event Authority And Reliability”. | **Resolved locally.** Re-index and label `Subscribe` explicitly as non-durable UI convenience. |

## Improvements still worth planning

| Priority | Improvement | Why it helps |
| --- | --- | --- |
| P1 | Add a release/CI check that records the source commit used by published DeepWiki and flags it when it falls behind `main`. | This review found that code-derived documentation can be dangerously authoritative-looking when its index revision is behind a contract-changing commit. |
| P2 | Provide a production-readiness validator or explicit startup warning for a Temporal runtime using default in-memory `runlog.Store` or `session.Store`. | The docs clearly explain the split, but an executable guard would prevent the common “Temporal means all state is durable” deployment error. It should be opt-in/warn-only unless a strict production mode is introduced. |
| P3 | Offer a small supported dead-letter/commit helper for Pulse manual deliveries. | The current API correctly keeps DLQ ownership with applications; a helper could standardize raw-payload capture, idempotency-key commit, and post-commit acknowledgement without weakening that contract. |

## Documentation and skill assessment

No new local documentation or skill edit is needed for this area at HEAD. The
following current sources already contain the corrective contracts:

- `docs/runtime.md` — production persistence, registration, planner retries,
  time budgets, event pipeline, and semantic telemetry.
- `.agents/skills/loom-mcp/references/runtime-contracts.md` — compact
  implementer rules for retries, streaming, authority, Pulse, Temporal, and
  telemetry.
- `.agents/skills/loom-mcp/references/user-guides/memory.md` — the authority
  model, Mongo migration/read behavior, and long-term-memory separation.

## Coverage checklist

- [x] 4 Runtime Architecture
- [x] 4.1 Runtime Coordinator & Registration
- [x] 4.2 Workflow Engine & Execution Loop
- [x] 4.3 Planner Interface & Workflow Graphs
- [x] 4.4 Hooks, Streaming & Event Pipeline
- [x] 4.5 Memory, Transcript & Session Management
- [x] 4.6 Policy Enforcement & Human-in-the-Loop
- [x] 4.7 Telemetry & Observability
- [x] 8 Persistence & Infrastructure Features
- [x] 8.1 MongoDB Persistence Layer
- [x] 8.2 Pulse Streaming Infrastructure
