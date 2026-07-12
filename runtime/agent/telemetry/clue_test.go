package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestClueMetricsRecordsAllInstrumentKinds(t *testing.T) {
	previous := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	metrics := NewClueMetrics()
	metrics.IncCounter("requests", 2, "service", "planner")
	metrics.RecordTimer("latency", 250*time.Millisecond, "service", "planner")
	metrics.RecordGauge("queue_depth", 7, "service", "planner")

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))
	names := make(map[string]struct{})
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			names[metric.Name] = struct{}{}
		}
	}
	assert.Contains(t, names, "requests")
	assert.Contains(t, names, "latency")
	assert.Contains(t, names, "queue_depth_gauge")
}

func TestClueTracerRecordsSpanContract(t *testing.T) {
	previous := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	tracer := NewClueTracer()
	ctx, span := tracer.Start(context.Background(), "planner.stream")
	span.SetAttributes(attribute.String("agent", "assistant"))
	span.AddEvent("chunk", "index", 3, "final", true)
	span.SetStatus(codes.Error, "failed")
	span.RecordError(errors.New("boom"))
	span.End()

	current := tracer.Span(ctx)
	require.NotNil(t, current)

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, "planner.stream", ended[0].Name())
	assert.Equal(t, codes.Error, ended[0].Status().Code)
	assert.Equal(t, "failed", ended[0].Status().Description)
	assert.Len(t, ended[0].Events(), 2)
	assert.Contains(t, attrMap(ended[0].Attributes()), attribute.Key("agent"))
}

func TestClueLoggerAndAttributeConversions(t *testing.T) {
	logger := NewClueLogger()
	ctx := context.Background()
	logger.Debug(ctx, "debug", "key", "value")
	logger.Info(ctx, "info", "key", "value")
	logger.Warn(ctx, "warn", "key", "value")
	logger.Error(ctx, "error", "key", "value")

	attrs := attrMap(kvSliceToAttrs([]any{
		"string", "value",
		"int", 2,
		"int64", int64(3),
		"float", 4.5,
		"bool", true,
		"unknown", []string{"value"},
		7, "ignored-key",
		"odd",
	}))
	assert.Equal(t, "value", attrs["string"].AsString())
	assert.Equal(t, int64(2), attrs["int"].AsInt64())
	assert.Equal(t, int64(3), attrs["int64"].AsInt64())
	assert.InEpsilon(t, 4.5, attrs["float"].AsFloat64(), 0.0001)
	assert.True(t, attrs["bool"].AsBool())
	assert.Empty(t, attrs["unknown"].AsString())
	assert.Empty(t, attrs["odd"].AsString())

	tags := attrMap(tagsToAttrs([]string{"service", "planner", "orphan"}))
	assert.Equal(t, "planner", tags["service"].AsString())
	assert.Empty(t, tags["orphan"].AsString())
	assert.Len(t, kvSliceToClue([]any{"key", "value", 7, "ignored", "odd"}), 2)
}
