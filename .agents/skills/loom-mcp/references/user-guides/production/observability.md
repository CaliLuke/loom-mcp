# Production: Observability

Use this for Clue, tracing, metrics, logs, and health checks.

## Core Areas

1. Distributed tracing
2. Metrics
3. Logs

## Recommended Stack

Loom recommends Clue on top of OpenTelemetry for observability.

Typical setup includes:

- `clue.NewConfig(...)`
- `clue.ConfigureOpenTelemetry(...)`
- OTLP trace exporter
- OTLP metric exporter
- `log.Context(...)`

## Runtime Patterns

- Configure `runtime.WithMetrics(...)` and `runtime.WithTracer(...)`; nil
  options are no-ops.
- Use the stable semantic metrics documented in `docs/runtime.md`:
  `loom_mcp.runtime.run.started`, `run.completed`, `planner.attempts`,
  `planner.duration`, `tool.completed`, and `tool.duration` (all with the full
  `loom_mcp.runtime.` prefix).
- Treat planner metrics as activity-attempt measurements; retries can produce
  more than one attempt for a logical turn.
- Canonical run/tool counters are event-key deduplicated. Tool results emit
  `tool.execute` spans with correlation IDs on span attributes, not metric
  dimensions.
- The semantic runtime contract is engine-neutral. Temporal infrastructure
  telemetry is a separate trace domain whose activity roots link to the origin
  request rather than continuing one parent trace.
- Use structured request-scoped logs
- Expose health endpoints for critical dependencies

Generated MCP adapter metrics, SDK transport observers, Pulse streams, and the
local debug server are separate observability and delivery surfaces. Enabling
one does not enable the others.
