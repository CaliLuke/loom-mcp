package runtime

import (
	"context"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	runtimeRunStartedMetric      = "loom_mcp.runtime.run.started"
	runtimeRunCompletedMetric    = "loom_mcp.runtime.run.completed"
	runtimePlannerAttemptsMetric = "loom_mcp.runtime.planner.attempts"
	runtimePlannerDurationMetric = "loom_mcp.runtime.planner.duration"
	runtimeToolCompletedMetric   = "loom_mcp.runtime.tool.completed"
	runtimeToolDurationMetric    = "loom_mcp.runtime.tool.duration"
	runtimeMetricTagAgent        = "agent"
	runtimeMetricTagOperation    = "operation"
	runtimeMetricTagStatus       = "status"
	runtimeTelemetryStatusError  = "error"
)

func (r *Runtime) recordPlannerAttempt(span telemetry.Span, agentID agent.Ident, operation string, runCtx run.Context, duration time.Duration, err error) {
	status := telemetryStatus(err)
	tags := []string{runtimeMetricTagAgent, string(agentID), runtimeMetricTagOperation, operation, runtimeMetricTagStatus, status}
	metrics := r.runtimeMetrics()
	metrics.IncCounter(runtimePlannerAttemptsMetric, 1, tags...)
	metrics.RecordTimer(runtimePlannerDurationMetric, duration, tags...)
	span.SetAttributes(
		attribute.String("loom_mcp.agent_id", string(agentID)),
		attribute.String("loom_mcp.run_id", runCtx.RunID),
		attribute.String("loom_mcp.session_id", runCtx.SessionID),
		attribute.String("loom_mcp.turn_id", runCtx.TurnID),
		attribute.String("loom_mcp.planner.operation", operation),
	)
	setRuntimeSpanStatus(span, err)
}

func (r *Runtime) recordCanonicalEventTelemetry(ctx context.Context, evt hooks.Event) {
	metrics := r.runtimeMetrics()
	switch e := evt.(type) {
	case *hooks.RunStartedEvent:
		metrics.IncCounter(runtimeRunStartedMetric, 1, runtimeMetricTagAgent, e.AgentID())
	case *hooks.RunCompletedEvent:
		metrics.IncCounter(runtimeRunCompletedMetric, 1, runtimeMetricTagAgent, e.AgentID(), runtimeMetricTagStatus, e.Status)
	case *hooks.ToolResultReceivedEvent:
		r.recordToolResultTelemetry(ctx, e)
	}
}

func (r *Runtime) recordToolResultTelemetry(ctx context.Context, evt *hooks.ToolResultReceivedEvent) {
	duration := evt.Duration
	if duration < 0 {
		duration = 0
	}
	var toolErr error
	if evt.Error != nil {
		toolErr = evt.Error
	}
	status := telemetryStatus(toolErr)
	tags := []string{runtimeMetricTagAgent, evt.AgentID(), defaultToolName, r.metricToolName(evt.ToolName), runtimeMetricTagStatus, status}
	metrics := r.runtimeMetrics()
	metrics.IncCounter(runtimeToolCompletedMetric, 1, tags...)
	metrics.RecordTimer(runtimeToolDurationMetric, duration, tags...)

	end := time.UnixMilli(evt.Timestamp())
	_, span := r.runtimeTracer().Start(
		ctx,
		"tool.execute",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(end.Add(-duration)),
	)
	span.SetAttributes(
		attribute.String("loom_mcp.agent_id", evt.AgentID()),
		attribute.String("loom_mcp.run_id", evt.RunID()),
		attribute.String("loom_mcp.session_id", evt.SessionID()),
		attribute.String("loom_mcp.turn_id", evt.TurnID()),
		attribute.String("loom_mcp.tool.name", string(evt.ToolName)),
		attribute.String("loom_mcp.tool_call_id", evt.ToolCallID),
		attribute.Int64("loom_mcp.tool.duration_ms", duration.Milliseconds()),
	)
	setRuntimeSpanStatus(span, toolErr)
	span.End(trace.WithTimestamp(end))
}

func (r *Runtime) runtimeMetrics() telemetry.Metrics {
	if r.metrics != nil {
		return r.metrics
	}
	return telemetry.NoopMetrics{}
}

func (r *Runtime) runtimeTracer() telemetry.Tracer {
	if r.tracer != nil {
		return r.tracer
	}
	return telemetry.NoopTracer{}
}

func (r *Runtime) metricToolName(name tools.Ident) string {
	if _, ok := r.toolSpec(name); ok {
		return string(name)
	}
	return unknownID
}

func telemetryStatus(err error) string {
	if err != nil {
		return runtimeTelemetryStatusError
	}
	return runStatusSuccess
}

func setRuntimeSpanStatus(span telemetry.Span, err error) {
	if err == nil {
		span.SetStatus(codes.Ok, "ok")
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "failed")
}
