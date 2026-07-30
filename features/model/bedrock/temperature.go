package bedrock

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

func traceTemperatureOmitted(ctx context.Context, modelID string, requested float32) {
	trace.SpanFromContext(ctx).SetAttributes(
		telemetry.GenAITemperatureOmittedAttrs(modelID, float64(requested))...,
	)
}
