package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoerceQueryCoversScalarRepeatedAndOverflowForms(t *testing.T) {
	timestamp := "2026-07-12T10:11:12.123456789Z"
	got := CoerceQuery(map[string][]string{
		"empty":     {""},
		"true":      {"TRUE"},
		"false":     {"false"},
		"zero":      {"0"},
		"one":       {"1"},
		"negative":  {"-7"},
		"float":     {"1.25"},
		"exponent":  {"2e3"},
		"timestamp": {timestamp},
		"overflow":  {"9223372036854775808"},
		"text":      {"12px"},
		"repeated":  {"1", "true", "text"},
		"absent":    nil,
	})

	parsedTime, err := time.Parse(time.RFC3339Nano, timestamp)
	require.NoError(t, err)
	assert.Empty(t, got["empty"])
	assert.Equal(t, true, got["true"])
	assert.Equal(t, false, got["false"])
	assert.Equal(t, int64(0), got["zero"])
	assert.Equal(t, int64(1), got["one"])
	assert.Equal(t, int64(-7), got["negative"])
	assert.InDelta(t, 1.25, got["float"], 0)
	assert.InDelta(t, 2000.0, got["exponent"], 0)
	assert.Equal(t, parsedTime, got["timestamp"])
	assert.Equal(t, "9223372036854775808", got["overflow"])
	assert.Equal(t, "12px", got["text"])
	assert.Equal(t, []any{int64(1), true, "text"}, got["repeated"])
	assert.Equal(t, []any{}, got["absent"])
}
func TestCoerceQueryTypedPreservesDeclaredShapes(t *testing.T) {
	timestamp := "2024-01-01T00:00:00Z"
	got := CoerceQueryTyped(map[string][]string{
		"query":   {"true"},
		"name":    {"42"},
		"when":    {timestamp},
		"tags":    {"1", "false", timestamp},
		"single":  {"true"},
		"numbers": {"42"},
		"limit":   {"42"},
		"maximum": {"18446744073709551615"},
		"uints":   {"0", "18446744073709551615"},
	}, map[string]QueryField{
		"name":    {String: true},
		"query":   {String: true},
		"single":  {String: true, Repeated: true},
		"tags":    {String: true, Repeated: true},
		"when":    {String: true},
		"numbers": {Repeated: true},
		"maximum": {Unsigned: true},
		"uints":   {Unsigned: true, Repeated: true},
	})

	assert.Equal(t, "true", got["query"])
	assert.Equal(t, "42", got["name"])
	assert.Equal(t, timestamp, got["when"])
	assert.Equal(t, []any{"1", "false", timestamp}, got["tags"])
	assert.Equal(t, []any{"true"}, got["single"])
	assert.Equal(t, []any{int64(42)}, got["numbers"])
	assert.Equal(t, int64(42), got["limit"])
	assert.Equal(t, uint64(18446744073709551615), got["maximum"])
	assert.Equal(t, []any{uint64(0), uint64(18446744073709551615)}, got["uints"])
}
func TestCoerceQueryTypedPreservesFloat32JSON(t *testing.T) {
	got := CoerceQueryTyped(map[string][]string{
		"scalar":   {"1.2"},
		"repeated": {"1.2"},
	}, map[string]QueryField{
		"scalar":   {Float: true, Bits: 32},
		"repeated": {Float: true, Bits: 32, Repeated: true},
		"default":  {Float: true, Bits: 32, DefaultValues: []string{"1.2"}},
	})

	require.IsType(t, float32(0), got["scalar"])
	require.IsType(t, float32(0), got["repeated"].([]any)[0])
	require.IsType(t, float32(0), got["default"])
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"default":1.2,"repeated":[1.2],"scalar":1.2}`, string(encoded))
}
func TestQueryShapeDetectorsRejectAmbiguousForms(t *testing.T) {
	for _, value := range []string{"", "-", "+1", "1.0", "1e2", " 1"} {
		assert.False(t, looksIntegral(value), value)
	}
	for _, value := range []string{"1.0", "1e2", "1E2"} {
		assert.True(t, looksFloat(value), value)
	}
	for _, value := range []string{"", "1", "plain"} {
		assert.False(t, looksFloat(value), value)
	}
}
