package anthropic

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

func traceTemperatureOmitted(ctx context.Context, modelID string, requested float64) {
	trace.SpanFromContext(ctx).SetAttributes(
		telemetry.GenAITemperatureOmittedAttrs(modelID, requested)...,
	)
}
