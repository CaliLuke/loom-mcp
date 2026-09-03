# Internal Tool Registry

The registry is a Redis/Pulse-backed catalog and gateway for discovering and invoking toolsets across process boundaries. It is separate from the in-process prompt registry.

## Architecture

- A Pulse replicated map stores the shared toolset catalog.
- A second replicated map stores provider health.
- A Pulse pool coordinates health work across registry nodes.
- Request and per-call result streams route tool invocations.
- Generated providers in the toolset-owning service decode payloads, call bound service methods, and encode results.

Nodes using the same `Config.Name` and Redis instance form one logical registry. The catalog is not backed by a pluggable memory/Mongo `Store`; the replicated map is the current catalog authority.

The client-side `runtime/registry.Manager.Search` and semantic
`SearchClient.Search` share one concurrent registry fan-out implementation and
the same partial-failure rule. Successful registries contribute results when
some peers fail; an error is returned only when every selected registry fails.
The richer client then applies semantic-to-keyword fallback and filters. It
orders results by descending relevance, then origin and ID, before applying the
result limit. Do not reintroduce the removed registry `Store` model in wiring or
documentation.
## Configuration

```go
r, err := registry.New(ctx, registry.Config{
    Redis:               redisClient,
    Name:                "production-tools",
    Logger:              logger,
    PingInterval:        10 * time.Second,
    MissedPingThreshold: 3,
    ResultStreamTTL:     15 * time.Minute,
})
if err != nil {
    return err
}
defer func() {
    if closeErr := r.Close(context.Background()); closeErr != nil {
        log.Printf("close registry: %v", closeErr)
    }
}()

return r.Run(ctx, ":9090")
```

Current fields:

| Field | Contract |
| --- | --- |
| `Redis` | Required Redis client used by Pulse |
| `Name` | Cluster/resource namespace; defaults to `registry` |
| `Logger` | Optional health-tracker logger |
| `PingInterval` | Optional positive override; default is 10 seconds |
| `MissedPingThreshold` | Optional positive override; default is 3 |
| `ResultStreamTTL` | TTL for per-call result streams/mappings; default is 15 minutes |
| `PoolNodeOptions` | Additional Pulse pool-node options |

Negative durations/thresholds fail validation. Zero selects the default.

## Tool call flow

1. Validate the payload against the generated tool schema.
2. Reject calls to unhealthy toolsets immediately.
3. Create a per-call result stream and store its mapping with a TTL.
4. Publish the request to the provider stream.
5. Wait for the matching result or the request deadline.

Provider health is established through ping/pong traffic. Run the generated provider loop in the process that owns the bound service implementation; registering catalog metadata alone does not execute tools.

The deterministic stream families are derived from canonical toolset and tool-use identities. Treat their concrete names as internal protocol details unless integrating the provider package itself.

## Agent consumption

Declare the registry and the remote toolset in design:

```go
var ToolsRegistry = Registry("tools")

var Inventory = Toolset(
    "inventory",
    FromRegistry(ToolsRegistry, "inventory"),
)
```

Use the generated agent registration helpers and runtime registry client. Do not reconstruct remote tool schemas manually; generated specs and the registry catalog are the contract.

## Operations

- Use a distinct `Name` per environment or isolation boundary.
- Set `ResultStreamTTL` above the longest allowed tool execution plus delivery margin.
- Monitor provider health transitions, call latency, timeouts, and stale result streams.
- Apply authentication and network policy at the gRPC/service boundary; Redis coordination is not an authorization layer.
- Close registry resources during graceful shutdown.

## Verification

Registry changes require focused package tests and Redis-backed integration coverage. When generated registry design changes, run `make gen-registry`, then `make verify-mcp-local`, `make lint`, `make test`, and `make itest`.
