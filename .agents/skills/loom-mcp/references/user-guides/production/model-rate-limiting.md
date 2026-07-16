# Model Rate Limiting

`features/model/middleware.AdaptiveRateLimiter` applies a token-estimating AIMD limiter at the provider-client boundary. Use one limiter per independently limited provider/model quota.

## Local usage

```go
limiter := middleware.NewAdaptiveRateLimiter(
    ctx,
    nil,    // no replicated map: process-local coordination
    "",     // key is unused in local mode
    60_000, // initial tokens per minute
    120_000,
)

client := limiter.Middleware()(providerClient)
```

Register the wrapped client with the runtime, not the underlying provider
client. The middleware preserves `model.TokenCounter` when the provider
implements it and preserves the absence of that optional interface otherwise.
Admission always uses estimated input tokens; exact counting remains available
to other runtime policies only when the provider supports it.

If `initialTPM <= 0`, the limiter starts at 60,000 TPM. If `maxTPM` is zero or below the initial value, it is clamped to the initial value.

## Cluster coordination

Join one Pulse replicated map per coordination domain and share a stable key for replicas consuming the same quota:

```go
limits, err := rmap.Join(ctx, "model-rate-limits", redisClient)
if err != nil {
    return err
}
defer limits.Close()

limiter := middleware.NewAdaptiveRateLimiter(
    ctx,
    limits,
    "anthropic:production",
    60_000,
    120_000,
)
```

Backoff/probe updates use compare-and-set operations on the shared value, and subscribers reconcile local refill rates from map updates. If the map or key is absent, the limiter remains process-local.

## Admission and adaptation

The limiter estimates input tokens with `model.TokenEstimator`, waits for capacity, and then calls the provider.
It does not reserve output tokens, so size the TPM limits and burst for expected
response volume as well as prompt size.

| Outcome | Adjustment |
| --- | --- |
| Successful `Complete` | Add 5% of initial TPM, capped at `maxTPM` |
| `Complete` returns `model.ErrRateLimited` | Halve TPM, floored at 10% of initial (minimum 1) |
| Stream setup returns `model.ErrRateLimited` | Back off immediately |
| Stream reaches `io.EOF` | Probe once |
| Stream `Recv` returns `model.ErrRateLimited` | Back off once |
| Ordinary error or early `Close` | No adjustment |

A successful stream setup is not proof that the provider completed the request. The wrapper observes only the first terminal `Recv` outcome, so a receive-time 429 is not misclassified as success and repeated terminal calls cannot adjust the budget twice.

The token-bucket burst stays pinned at `maxTPM`; adaptation changes only the refill rate. A request estimated above that burst can never be admitted and fails fast with `middleware.ErrRequestTooLarge`. Reduce the request or raise the configured maximum.

## Provider requirement

Adaptive backoff depends on providers normalizing quota failures to `model.ErrRateLimited`, including errors returned while receiving a stream. Provider conformance tests must cover both setup-time and receive-time rate limits. Preserve the underlying provider error in the chain for diagnostics.

## Operational guidance

- Start below the documented provider quota and leave recovery headroom in `maxTPM`.
- Use separate keys for quotas that are actually independent; share a key across all replicas consuming one quota.
- Bound request contexts so queued calls can be canceled.
- Monitor admission wait time, `ErrRequestTooLarge`, provider rate-limit errors, and upstream request latency.
- Treat the shared map as coordination state, not a billing or exact token-accounting ledger.
- Close the replicated map and cancel the limiter context during shutdown.

Run `go test -race ./features/model/middleware` after middleware changes, followed by the repository verification ladder for framework work.
