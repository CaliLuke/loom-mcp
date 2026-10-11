package telemetry_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestNewTracer(t *testing.T) {
	t.Parallel()
	global := otel.GetTracerProvider()
	one, two := tracetest.NewSpanRecorder(), tracetest.NewSpanRecorder()
	p1 := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(one))
	p2 := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(two))
	t.Cleanup(func() {
		assert.NoError(t, p1.Shutdown(context.Background()))
		assert.NoError(t, p2.Shutdown(context.Background()))
	})
	ctx, parent := p1.Tracer("test").Start(t.Context(), "parent")
	ctx, child := telemetry.NewTracer(p1).Start(ctx, "child")
	require.True(t, trace.SpanContextFromContext(ctx).IsValid())
	child.End()
	parent.End()
	_, other := telemetry.NewTracer(p2).Start(t.Context(), "other")
	other.End()
	require.Len(t, one.Ended(), 2)
	require.Len(t, two.Ended(), 1)
	assert.Equal(t, one.Ended()[0].SpanContext().TraceID(), one.Ended()[1].SpanContext().TraceID())
	assert.Equal(t, "other", two.Ended()[0].Name())
	assert.Equal(t, global, otel.GetTracerProvider())
}
