# Temporal Setup

Temporal-backed workflow recovery for loom-mcp runs, with explicit persistence
for the runtime state that is not part of Temporal workflow history.

## Overview

Temporal backs agent runs as workflows and tool calls as activities:

- workflow history is durable and replayable
- planner and tool activities use bounded attempt policies
- a restarted worker replays workflow history and resumes outstanding work

`runtime.WithEngine(temporalEng)` makes workflow execution durable. It does not
make every runtime store durable. Without explicit options, `runtime.New`
creates process-local in-memory `runlog.Store` and `session.Store` values. Those
defaults are useful for development, but a restarted process loses its local
run-introspection record and session metadata even though Temporal can still
resume the workflow.

## How Durability Works

| Component | Role | Owner and durability |
| --- | --- | --- |
| Workflow state | Live run orchestration, ledger, awaits | Temporal event history; replayed after worker restart |
| Planner/tool activity | Nondeterministic model or tool work | Temporal records completed attempts; failed or unacknowledged attempts may retry |
| `runlog.Store` | Canonical append-only introspection record | In memory by default; use a shared persistent adapter such as `features/runlog/mongo` |
| `session.Store` | Session lifecycle and run metadata | In memory by default; use a shared persistent adapter such as `features/session/mongo` |
| `memory.Store` | Derived per-run transcript projection | Best-effort and in memory by default; configure separately when the projection must survive restart |
| `stream.Sink` | Live client delivery | Separate delivery contract; Temporal history is not a replayable UI stream |

Temporal does not make activity side effects exactly once. A completed activity
recorded in history is not rerun during workflow replay, but an activity can be
retried if the worker fails after the external side effect and before Temporal
records successful completion. Tool implementations still need idempotency keys
or another retry-safe contract for externally visible effects.

## What Survives Failures

| Failure scenario | Temporal protects | Additional configuration required |
| --- | --- | --- |
| Worker process crashes | Workflow state replays and outstanding work resumes | Persistent runlog/session stores preserve application-visible metadata |
| Tool activity times out | The configured activity attempt policy applies | The tool's external side effects must be retry-safe |
| Temporal connection is interrupted | The worker reconnects and replays history | Use normal Temporal deployment and worker-draining practices |
| Worker is replaced during deploy | Another worker on the same task queue can continue | Deploy compatible workflow code and keep shared stores reachable |
| UI consumer disconnects | Nothing: workflow execution continues | Use a stream implementation with the delivery/replay behavior your UI requires |

## Installation

### Option 1: Docker (Development)

--- CODE ---
docker run --rm -d --name temporal-dev -p 7233:7233 temporalio/auto-setup:latest
--- END CODE ---

### Option 2: Temporalite (Development)

--- CODE ---
go install go.temporal.io/server/cmd/temporalite@latest
temporalite start
--- END CODE ---

### Option 3: Temporal Cloud

Use Temporal Cloud and configure client credentials.

### Option 4: Self-Hosted

Use Docker Compose or Kubernetes depending on your ops baseline.

## Runtime Configuration

loom-mcp uses the `Engine` interface. Swap engines without changing planner code.

In-memory:

--- CODE ---
// Default: no external dependencies
rt := runtime.New()
--- END CODE ---

Temporal with process-local stores (workflow durability only):

--- CODE ---
import (
    "log"

    runtimeTemporal "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
    "go.temporal.io/sdk/client"

    // Generated specs package from the generated agent
    specs "github.com/example/module/gen/orchestrator/agents/chat/specs"
)

temporalEng, err := runtimeTemporal.NewWorker(runtimeTemporal.Options{
    ClientOptions: &client.Options{
        HostPort:  "127.0.0.1:7233",
        Namespace: "default",
        // Required: enforce loom-mcp's workflow boundary contract.
        // Tool results, server-data, and UI artifacts cross boundaries as canonical JSON bytes
        // (api.ToolEvent/api.ToolArtifact).
        DataConverter: runtimeTemporal.NewAgentDataConverter(specs.Spec),
    },
    WorkerOptions: runtimeTemporal.WorkerOptions{
        TaskQueue: "orchestrator.chat",
    },
})
if err != nil {
    panic(err)
}
defer func() {
    if err := temporalEng.Close(); err != nil {
        log.Printf("close Temporal engine: %v", err)
    }
}()

// Runlog and session data are process-local in this configuration.
rt := runtime.New(runtime.WithEngine(temporalEng))
--- END CODE ---

Use that form only when losing local introspection and session metadata at
process restart is acceptable.

### Production worker with shared stores

The following construction uses the repository's Mongo adapters for the two
stores required by the runtime lifecycle. Replace the generated `specs` import
with the aggregate specs package for the agent hosted by the worker.

The session store writes run metadata through multi-document transactions, so
its MongoDB deployment must be a replica set (MongoDB 4.0 or later) or a sharded
cluster (MongoDB 4.2 or later). `sessionclient.New` rejects a standalone `mongod`
with an error that names the requirement. The URI below therefore targets a
replica set; a local `mongod` qualifies once it is started with `--replSet` and
initialised. Name the member explicitly when initialising it:

--- CODE ---
rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:27017"}]})
--- END CODE ---

A bare `rs.initiate()` writes the machine's hostname into the configuration
instead. The driver then replaces the `127.0.0.1` seed with that hostname, and
if the worker cannot reach it, construction fails with a server-selection
timeout. The timeout names the unreachable address but says nothing about the
transaction requirement, because no server was ever inspected.

--- CODE ---
import (
    "context"
    "errors"
    "fmt"

    runlogmongo "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo"
    runlogclient "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo/clients/mongo"
    sessionmongo "github.com/CaliLuke/loom-mcp/v2/features/session/mongo"
    sessionclient "github.com/CaliLuke/loom-mcp/v2/features/session/mongo/clients/mongo"
    runtimeTemporal "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
    mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
    mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
    temporalclient "go.temporal.io/sdk/client"

    specs "github.com/example/module/gen/orchestrator/agents/chat/specs"
)

func newProductionRuntime(ctx context.Context) (*runtime.Runtime, func(context.Context) error, error) {
    rawMongo, err := mongodriver.Connect(
        mongooptions.Client().ApplyURI("mongodb://127.0.0.1:27017/?replicaSet=rs0"),
    )
    if err != nil {
        return nil, nil, fmt.Errorf("connect to MongoDB: %w", err)
    }
    closeMongo := func() error {
        return rawMongo.Disconnect(ctx)
    }

    runlogClient, err := runlogclient.New(runlogclient.Options{
        Client:   rawMongo,
        Database: "loom",
    })
    if err != nil {
        return nil, nil, errors.Join(
            fmt.Errorf("create runlog client: %w", err),
            closeMongo(),
        )
    }
    runlogStore, err := runlogmongo.NewStore(runlogClient)
    if err != nil {
        return nil, nil, errors.Join(
            fmt.Errorf("create runlog store: %w", err),
            closeMongo(),
        )
    }

    sessionClient, err := sessionclient.New(sessionclient.Options{
        Client:   rawMongo,
        Database: "loom",
    })
    if err != nil {
        return nil, nil, errors.Join(
            fmt.Errorf("create session client: %w", err),
            closeMongo(),
        )
    }
    sessionStore, err := sessionmongo.NewStore(sessionClient)
    if err != nil {
        return nil, nil, errors.Join(
            fmt.Errorf("create session store: %w", err),
            closeMongo(),
        )
    }

    temporalEng, err := runtimeTemporal.NewWorker(runtimeTemporal.Options{
        ClientOptions: &temporalclient.Options{
            HostPort:      "127.0.0.1:7233",
            Namespace:     "default",
            DataConverter: runtimeTemporal.NewAgentDataConverter(specs.Spec),
        },
        WorkerOptions: runtimeTemporal.WorkerOptions{
            TaskQueue: "orchestrator.chat",
        },
    })
    if err != nil {
        return nil, nil, errors.Join(
            fmt.Errorf("create Temporal worker: %w", err),
            closeMongo(),
        )
    }

    rt := runtime.New(
        runtime.WithEngine(temporalEng),
        runtime.WithRunEventStore(runlogStore),
        runtime.WithSessionStore(sessionStore),
    )
    cleanup := func(ctx context.Context) error {
        return errors.Join(temporalEng.Close(), rawMongo.Disconnect(ctx))
    }
    return rt, cleanup, nil
}
--- END CODE ---

Register toolsets and agents on `rt`, then call `rt.Seal(ctx)` before accepting
traffic. Every worker and client-only runtime that reads or writes session/run
metadata must use the same persistent store deployment. A client-only process
uses `runtimeTemporal.NewClient` instead of `NewWorker`, but should receive the
same `WithRunEventStore` and `WithSessionStore` options.

If the application relies on the derived `memory.Store` projection across
processes or restarts, also construct `features/memory/mongo` and pass it with
`runtime.WithMemoryStore`. Runlog persistence does not automatically rebuild
that projection. Configure a shared stream separately when clients need
cross-worker delivery; workflow history is not a substitute for stream replay.

## Activity attempts and timeouts

Generated planner and tool registrations carry bounded activity attempt
policies. Configure semantic planner and tool timeouts in the agent DSL:

--- CODE ---
Agent("chat", "Chat agent", func() {
    RunPolicy(func() {
        Timing(func() {
            Plan("45s")
            Tools("2m")
        })
    })
})
--- END CODE ---

`temporal.Options.ActivityDefaults` configures Temporal-specific queue-wait and
heartbeat-liveness limits; it does not replace the runtime's semantic attempt
budgets. Treat every planner and tool activity as retryable.

## Worker Setup

Workers poll task queues and execute workflows/activities for registered agents.
Use `NewWorker`, register all local agents/toolsets, and call `Runtime.Seal` to
start polling. Use `NewClient` only in processes that submit, query, signal, or
cancel workflows without registering local workflow/activity implementations.

## Restart and recovery verification

Verify the complete deployment boundary, not only that Temporal reports a
workflow as open:

1. Start a run with a known run ID and a deliberately slow, retry-safe tool.
2. Terminate the worker while the run is active, then start a fresh worker with
   the same Temporal namespace/task queue and the same Mongo database.
3. Confirm the run reaches a terminal Temporal status through
   `rt.Engine.QueryRunStatus(ctx, runID)`.
4. Read `rt.ListRunEvents(ctx, runID, "", limit)` from the restarted process and
   confirm the canonical runlog contains the run lifecycle through completion.
5. Read `rt.SessionStore.LoadRun(ctx, runID)` and
   `rt.SessionStore.ListRunsBySession(...)` and confirm the run remains attached
   to its session with the terminal status.
6. If persistent memory or cross-worker streaming is configured, verify those
   projections independently. Their success is not implied by Temporal or by a
   complete runlog.

A green recovery test proves different things at each layer: Temporal proves
workflow continuation, Mongo runlog proves durable introspection, Mongo session
storage proves durable session/run metadata, and the chosen stream or memory
adapter proves only its own projection contract.

## Best Practices

- Use separate environments (`dev`, `staging`, `prod`) for namespace scoping.
- Make planner and tool side effects retry-safe; do not assume exactly-once activity execution.
- Balance activity timeout values for reliability vs. failure detection speed.
- Share persistent runlog/session stores across workers and caller processes.
- Test store availability and restart recovery before deploying workflow changes.
- Use Temporal Cloud if you want hosted durability operations.
