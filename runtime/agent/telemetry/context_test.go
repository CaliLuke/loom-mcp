package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

type contextKey string

func TestMergeContextPreservesDestinationAndRehydratesTelemetry(t *testing.T) {
	member, err := baggage.NewMember("tenant", "acme")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
		Remote:  true,
	})
	base := trace.ContextWithSpanContext(baggage.ContextWithBaggage(context.Background(), bag), spanContext)
	destination := context.WithValue(context.Background(), contextKey("owner"), "workflow")

	merged := MergeContext(destination, base)

	assert.Equal(t, "workflow", merged.Value(contextKey("owner")))
	assert.Equal(t, "acme", baggage.FromContext(merged).Member("tenant").Value())
	assert.Equal(t, spanContext, trace.SpanContextFromContext(merged))
}

func TestMergeContextHandlesNilContextsAndEmptyMetadata(t *testing.T) {
	destination := context.WithValue(context.Background(), contextKey("owner"), "workflow")
	assert.Same(t, destination, MergeContext(destination, nil))

	//lint:ignore SA1012 Nil destination is an explicit MergeContext contract.
	merged := MergeContext(nil, context.Background())
	assert.NotNil(t, merged)
	assert.Zero(t, baggage.FromContext(merged).Len())
	assert.False(t, trace.SpanContextFromContext(merged).IsValid())
}
