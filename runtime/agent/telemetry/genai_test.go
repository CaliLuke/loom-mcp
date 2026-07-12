package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestGenAITemperatureOmittedAttrs(t *testing.T) {
	attrs := attrMap(GenAITemperatureOmittedAttrs("claude-sonnet-5", 0.7))

	assert.InEpsilon(t, 0.7, attrs[AttrGenAIRequestTemperature].AsFloat64(), 0.0001)
	assert.True(t, attrs[AttrGenAIRequestTemperatureOmitted].AsBool())
	assert.Equal(t, "claude-sonnet-5", attrs[AttrGenAIRequestModel].AsString())
}

func TestGenAIUsageAttrs(t *testing.T) {
	attrs := attrMap(GenAIUsageAttrs(10, 20, 3, 4))

	assert.Equal(t, int64(10), attrs[AttrGenAIUsageInputTokens].AsInt64())
	assert.Equal(t, int64(20), attrs[AttrGenAIUsageOutputTokens].AsInt64())
	assert.Equal(t, int64(3), attrs[AttrGenAIUsageCacheReadTokens].AsInt64())
	assert.Equal(t, int64(4), attrs[AttrGenAIUsageCacheCreationTokens].AsInt64())
}

func attrMap(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	values := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	return values
}
