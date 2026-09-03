# Memory, Run Logs, and Live State

loom-mcp has several related stores with different authority and reliability. Do not collapse them into one “transcript” abstraction.

## Authority model

| Surface | Purpose | Reliability/authority |
| --- | --- | --- |
| Workflow state/ledger | Live deterministic planner and tool state | Execution authority while the run is active |
| `runlog.Store` | Append-only run introspection | Canonical durable event log; runtime append is fail-closed |
| `memory.Store` | Raw transcript-event snapshot/projection | Derived memory surface; persistence may be best-effort |
| `memory.Searcher` | Indexed lookup over raw transcript events | Query surface, not execution authority |
| `memory.Service` | Long-term, entry-shaped memory | Separate durable knowledge contract |
| Streams and hook bus | Live UI/observer delivery | Observability; not a substitute for run-log durability |

Replay/resume comes from workflow state. Reconstruct introspection from the run log. Use memory only through the contract required by the feature; do not treat a memory snapshot as the canonical workflow ledger.

## Raw transcript memory

`memory.Store` provides:

```go
type Store interface {
    LoadRun(ctx context.Context, agentID, runID string) (Snapshot, error)
    AppendEvents(ctx context.Context, agentID, runID string, events ...Event) error
}
```

Events carry a typed `EventType`, timestamp, typed data payload, and labels. Construct/decode event data with the helpers in `runtime/agent/memory/event_data.go`; avoid reaching into unversioned map keys.

Use `memory.CloneEvent` when an in-process component retains or returns an
event. It copies `Labels` and the canonical mutable values in `Data`.

The Mongo adapter stores new appends as immutable documents in a companion
events collection instead of growing one run document indefinitely. Reads
load any legacy single-document history plus append buckets ordered by
`created_at`, `_id`, then stable-sort all events by timestamp. Equal timestamps
keep legacy events before new buckets and retain deterministic bucket order.
Keep both collections while legacy histories remain in service; a customized
legacy collection named `X` defaults its companion to `X_events`.

`memory.Searcher` adds indexed/cross-run raw-event lookup without changing the append/load contract:

```go
type Searcher interface {
    Query(ctx context.Context, query Query) (QueryResult, error)
}
```

The generated `FromMemory(...)` toolset distinguishes current-run transcript access from indexed transcript search. Indexed scope requires `runtime.WithMemorySearcher(...)`; the runtime returns an `unsupported_operation` retry hint if it is missing.

## Canonical run log

`runlog.Store` appends immutable hook-shaped events and lists them with opaque cursors. Append must be idempotent on `(run_id, event_key)`:

- an exact replay returns the existing ID with `Inserted=false`;
- a conflicting event body for the same key fails loudly;
- event IDs remain store-owned ordered cursors.

Run-log failure is not silently converted into a successful event publication. This keeps durable introspection aligned with execution.

## Long-term memory

`memory.Service` is an entry-shaped knowledge service, separate from raw transcripts:

- `IngestRun` and `IngestEvents` derive entries from completed or incremental transcript material;
- `PutEntry` stores direct application knowledge;
- `Search` returns ranked hits in a resolved scope.

Applications provide `memory.ScopeResolver` so namespace, user identity, and visibility are runtime-owned. Model tool payloads must not choose tenant/user scope or expose raw source references and storage metadata.

Generated `FromMemory(MemoryLongTerm(), ...)` registration passes the runtime memory service and resolver. The model-facing tool exposes bounded search inputs/results only.

## Planner preload

Memory preload is opt-in in `RunPolicy`:

- transcript preload fills `PlanInput.PreloadedMemory` / `PlanResumeInput.PreloadedMemory`;
- long-term preload fills `PreloadedMemoryEntries` using the latest history-filtered user text.

Preload does not replace normal planner history. Use `memory.FormatEntriesForPrompt(...)` when a planner needs prompt text from long-term entries.

## Operational guidance

- Keep event payloads bounded and preserve typed thinking/tool data needed for provider round trips.
- Scope every query by the strongest available agent/run/session/user boundary.
- Treat cursors and long-term source references as opaque.
- Monitor run-log append failures separately from best-effort memory projection failures.
- Use durable backends for production introspection and long-term knowledge; in-memory implementations are for tests and local development.
- Apply retention, redaction, and access-control policy independently to run logs, raw transcripts, and long-term entries.

See `docs/runtime.md` for event publication order, preload behavior, generated memory toolsets, and runtime options.
