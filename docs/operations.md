# Registry and persistence operations

## Registry

The runtime registry is a discovery catalog, not a durable execution store.
Pulse's replicated map is the catalog authority; manager and client searches
fan out concurrently and can return partial results when one registry is
unavailable. Run provider loops in the process that owns the bound service
implementation so liveness traffic reflects executability, not merely catalog
metadata.

Configure Redis/Pulse ownership, cluster naming, stream retention, and
shutdown in the application that mounts the registry. Treat a registry result
as a candidate until its provider health and request execution succeed. See
the runtime registry guide for API wiring and retry-hint behavior.

## Persistence

| Surface | Default | Durable option | Contract |
| --- | --- | --- | --- |
| Workflow state | Engine memory | Temporal workflow history | Live execution authority; not an audit log. |
| Run log | In-memory | `runlog.Store` implementation | Canonical append-only introspection record; append failures fail closed. |
| Session metadata | In-memory | `session.Store` implementation | Required alongside persistent run logs for process-replacement lifecycle recovery. |
| Transcript memory | In-memory projection | Mongo transcript buckets | Derived raw events; not long-term memory or a run log. |
| Long-term memory | Application supplied | `memory.Service` implementation | Explicit durable `memory.Entry` values. |
| Pulse delivery | Best-effort subscriber | Manual acknowledgement | Durable consumers commit idempotently by `EventKey`, then acknowledge. |

Temporal persists workflow history only. A production Temporal worker that
leaves run log or session stores at their defaults loses those projections when
the process is replaced. Verify persistent stores and restart behavior as part
of deployment readiness.
