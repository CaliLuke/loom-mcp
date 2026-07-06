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

func attrMap(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	values := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	return values
}
