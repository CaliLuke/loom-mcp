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

### MongoDB deployment requirement

`features/session/mongo` requires a replica set (MongoDB 4.0 or later) or a
sharded cluster (MongoDB 4.2 or later). `UpsertRun` and `LinkChildRun` write
through multi-document transactions, which a standalone `mongod` does not
support. The client checks the deployment at construction and returns an error
naming the requirement, so an unsupported deployment fails at startup rather
than at the first run write. A local `mongod` satisfies the requirement as a
single-node replica set: start it with `--replSet` and name the member
explicitly when you initialise it.

```javascript
rs.initiate({ _id: "rs0", members: [{ _id: 0, host: "127.0.0.1:27017" }] })
```

A bare `rs.initiate()` records the machine's hostname instead. The driver then
replaces the seed from the connection string with that hostname, and a client
that cannot reach it fails with a server-selection timeout.

The check covers the whole client, not only the transactional methods, so a
consumer that merely reads sessions and runs is refused on a standalone `mongod`
as well. That is deliberate: any holder of the client can reach `UpsertRun`, and
a startup error is easier to act on than a write that fails later.

The other Mongo-backed feature stores write one document at a time and never
open a transaction, so they run against a standalone `mongod`.
