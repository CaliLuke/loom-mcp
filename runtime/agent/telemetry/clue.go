package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/CaliLuke/loom/clue/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type (
	// ClueLogger wraps github.com/CaliLuke/loom/clue/log for runtime logging.
	ClueLogger struct{}

	// ClueMetrics wraps OTEL metrics for runtime instrumentation.
	ClueMetrics struct {
		meter metric.Meter
	}

	// ClueTracer wraps OTEL tracing for runtime tracing.
	ClueTracer struct {
		tracer trace.Tracer
	}

	// clueSpan wraps an OTEL trace span.
	clueSpan struct {
		span trace.Span
	}
)

const clueMessageKey = "msg"

// NewClueLogger constructs a Logger that delegates to github.com/CaliLuke/loom/clue/log.
// The logger reads formatting and debug settings from the context (set via
// log.Context and log.WithFormat/log.WithDebug).
func NewClueLogger() Logger {
	return ClueLogger{}
}

// NewClueMetrics constructs a Metrics recorder that delegates to OTEL metrics.
// Uses the global MeterProvider; configure it via otel.SetMeterProvider before
// invoking runtime methods (typically done via clue.ConfigureOpenTelemetry).
func NewClueMetrics() Metrics {
	meter := otel.Meter("github.com/CaliLuke/loom-mcp/v2/agents/runtime")
	return &ClueMetrics{meter: meter}
}

// NewClueTracer constructs a Tracer that delegates to OTEL tracing.
// Uses the global TracerProvider; configure it via otel.SetTracerProvider before
// invoking runtime methods (typically done via clue.ConfigureOpenTelemetry or
// environment variables like OTEL_EXPORTER_OTLP_ENDPOINT).
func NewClueTracer() Tracer {
	tracer := otel.Tracer("github.com/CaliLuke/loom-mcp/v2/agents/runtime")
	return &ClueTracer{tracer: tracer}
}

// Debug emits a debug-level log message with structured key-value pairs.
func (ClueLogger) Debug(ctx context.Context, msg string, keyvals ...any) {
	fielders := append([]log.Fielder{log.KV{K: clueMessageKey, V: msg}}, kvSliceToClue(keyvals)...)
	log.Debug(ctx, fielders...)
}

// Info emits an info-level log message with structured key-value pairs.
func (ClueLogger) Info(ctx context.Context, msg string, keyvals ...any) {
	fielders := append([]log.Fielder{log.KV{K: clueMessageKey, V: msg}}, kvSliceToClue(keyvals)...)
	log.Info(ctx, fielders...)
}

// Warn emits a warning-level log message with structured key-value pairs.
func (ClueLogger) Warn(ctx context.Context, msg string, keyvals ...any) {
	clueKVs := kvSliceToClue(keyvals)
	fielders := make([]log.Fielder, 0, 2+len(clueKVs))
	fielders = append(fielders, log.KV{K: clueMessageKey, V: msg}, log.KV{K: "severity", V: "warning"})
	fielders = append(fielders, clueKVs...)
	log.Warn(ctx, fielders...)
}

// Error emits an error-level log message with structured key-value pairs.
func (ClueLogger) Error(ctx context.Context, msg string, keyvals ...any) {
	fielders := append([]log.Fielder{log.KV{K: clueMessageKey, V: msg}}, kvSliceToClue(keyvals)...)
	log.Error(ctx, nil, fielders...)
}

// IncCounter increments a counter metric by the given value.
func (m *ClueMetrics) IncCounter(name string, value float64, tags ...string) {
	counter, err := m.meter.Float64Counter(name)
	if err != nil {
		return
	}
	counter.Add(context.Background(), value, metric.WithAttributes(tagsToAttrs(tags)...))
}

// RecordTimer records a duration histogram/timer metric.
func (m *ClueMetrics) RecordTimer(name string, duration time.Duration, tags ...string) {
	histogram, err := m.meter.Float64Histogram(name)
	if err != nil {
		return
	}
	histogram.Record(context.Background(), duration.Seconds(), metric.WithAttributes(tagsToAttrs(tags)...))
}

// RecordGauge records a gauge metric value.
func (m *ClueMetrics) RecordGauge(name string, value float64, tags ...string) {
	// OTEL doesn't have synchronous gauges; use an observable gauge or histogram as fallback
	histogram, err := m.meter.Float64Histogram(name + "_gauge")
	if err != nil {
		return
	}
	histogram.Record(context.Background(), value, metric.WithAttributes(tagsToAttrs(tags)...))
}

// Start creates a new span with the given name and optional attributes, returning
// a new context and the span handle.
func (t *ClueTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span) {
	newCtx, span := t.tracer.Start(ctx, name, opts...)
	return newCtx, &clueSpan{span: span}
}

// Span retrieves the current span from the context.
func (t *ClueTracer) Span(ctx context.Context) Span {
	span := trace.SpanFromContext(ctx)
	return &clueSpan{span: span}
}

// End finalizes the span, optionally applying additional options.
func (s *clueSpan) End(opts ...trace.SpanEndOption) {
	s.span.End(opts...)
}

// AddEvent records a span event with the given name and attributes.
func (s *clueSpan) AddEvent(name string, attrs ...any) {
	s.span.AddEvent(name, trace.WithAttributes(kvSliceToAttrs(attrs)...))
}

// SetAttributes records attributes on the underlying OTEL span.
func (s *clueSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.span.SetAttributes(attrs...)
}

// SetStatus sets the span status code and description.
func (s *clueSpan) SetStatus(code codes.Code, description string) {
	s.span.SetStatus(code, description)
}

// RecordError records an error on the span with optional attributes.
func (s *clueSpan) RecordError(err error, opts ...trace.EventOption) {
	s.span.RecordError(err, opts...)
}

// kvSliceToClue converts variadic key-value pairs (k1, v1, k2, v2, ...) into
// Clue's log.Fielder slice. If the slice has an odd length, the last key is paired
// with nil. Keys are converted to strings.
func kvSliceToClue(keyvals []any) []log.Fielder {
	var fielders []log.Fielder
	for i := 0; i < len(keyvals); i += 2 {
		k := keyvals[i]
		var v any
		if i+1 < len(keyvals) {
			v = keyvals[i+1]
		}
		keyStr := fmt.Sprint(k)
		fielders = append(fielders, log.KV{K: keyStr, V: v})
	}
	return fielders
}

// tagsToAttrs converts tag strings (k1, v1, k2, v2, ...) into OTEL attributes
// for metrics dimensions. If the slice has an odd length, the last key is paired
// with an empty string.
func tagsToAttrs(tags []string) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for i := 0; i < len(tags); i += 2 {
		k := tags[i]
		v := ""
		if i+1 < len(tags) {
			v = tags[i+1]
		}
		attrs = append(attrs, attribute.String(k, v))
	}
	return attrs
}

// kvSliceToAttrs converts variadic key-value pairs (k1, v1, k2, v2, ...) into
// OTEL attributes for span events. If the slice has an odd length, the last key
// is paired with nil and converted to its string representation.
func kvSliceToAttrs(keyvals []any) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for i := 0; i < len(keyvals); i += 2 {
		k := keyvals[i]
		var v any
		if i+1 < len(keyvals) {
			v = keyvals[i+1]
		}
		keyStr := fmt.Sprint(k)
		// Convert value based on type
		switch val := v.(type) {
		case string:
			attrs = append(attrs, attribute.String(keyStr, val))
		case int:
			attrs = append(attrs, attribute.Int(keyStr, val))
		case int64:
			attrs = append(attrs, attribute.Int64(keyStr, val))
		case float64:
			attrs = append(attrs, attribute.Float64(keyStr, val))
		case bool:
			attrs = append(attrs, attribute.Bool(keyStr, val))
		default:
			// Fallback: convert to string
			attrs = append(attrs, attribute.String(keyStr, fmt.Sprint(v)))
		}
	}
	return attrs
}
