package runtime

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (r *Runtime) recordContextCompactionTelemetry(ctx context.Context, event *hooks.ContextCompactionEvent) {
	end := time.UnixMilli(event.Timestamp())
	duration := time.Duration(max(event.Latency, 0))
	_, span := r.runtimeTracer().Start(ctx, "context.compact", trace.WithSpanKind(trace.SpanKindInternal), trace.WithTimestamp(end.Add(-duration)))
	outcome := "compacted"
	if event.Type() == hooks.ContextCompactionSkipped {
		outcome = "skipped"
	}
	checkpoint := ""
	if event.Checkpoint != nil {
		checkpoint = string(*event.Checkpoint)
	}
	span.SetAttributes(
		attribute.String("loom_mcp.agent_id", event.AgentID()), attribute.String("loom_mcp.run_id", event.RunID()), attribute.String("loom_mcp.session_id", event.SessionID()), attribute.String("loom_mcp.turn_id", event.TurnID()),
		attribute.String("loom_mcp.context.reason", event.Reason), attribute.String("loom_mcp.context.tier", event.Tier), attribute.String("loom_mcp.context.outcome", outcome), attribute.String("loom_mcp.context.kind", event.Kind),
		attribute.Int("loom_mcp.context.units", event.Units), attribute.Int("loom_mcp.context.calls", event.Calls), attribute.Int("loom_mcp.context.before", event.Before), attribute.Int("loom_mcp.context.after", event.After), attribute.String("loom_mcp.context.checkpoint", checkpoint), attribute.String("loom_mcp.context.fingerprint", event.Fingerprint), attribute.Int("loom_mcp.context.step", event.Step), attribute.String("loom_mcp.context.model", event.Model), attribute.Int("loom_mcp.context.chunks", event.Chunks), attribute.Int("loom_mcp.context.effective_window", event.EffectiveWindow), attribute.Bool("loom_mcp.context.pretend", event.Pretend),
		attribute.Int("loom_mcp.context.usage.input_tokens", event.Usage.InputTokens), attribute.Int("loom_mcp.context.usage.output_tokens", event.Usage.OutputTokens), attribute.Int("loom_mcp.context.usage.total_tokens", event.Usage.TotalTokens), attribute.Int("loom_mcp.context.usage.cache_read_tokens", event.Usage.CacheReadTokens), attribute.Int("loom_mcp.context.usage.cache_write_tokens", event.Usage.CacheWriteTokens),
	)
	span.End(trace.WithTimestamp(end))
}
