package sdkbridge

import (
	"encoding/json/v2"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceQueryRejectsNumbersOutsideGeneratedGoTypes(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		field       mcpruntime.QueryField
	}{
		{"negative unsigned", "-1", mcpruntime.QueryField{Unsigned: true, Bits: 64}},
		{"unsigned overflow", "256", mcpruntime.QueryField{Unsigned: true, Bits: 8}},
		{"unsigned fraction", "1.5", mcpruntime.QueryField{Unsigned: true, Bits: 64}},
		{"signed fraction", "1.5", mcpruntime.QueryField{Bits: 64}},
		{"bare minus", "-", mcpruntime.QueryField{Bits: 64}},
		{"empty", "", mcpruntime.QueryField{Bits: 64}},
		{"signed overflow", "128", mcpruntime.QueryField{Bits: 8}},
		{"float overflow", "1e100", mcpruntime.QueryField{Float: true, Bits: 32}},
		{"invalid float", "number", mcpruntime.QueryField{Float: true, Bits: 64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := ResourceQueryJSONTyped("urn:records?count="+tc.value, map[string]mcpruntime.QueryField{"count": tc.field}, "")
			require.Error(t, err)
			assert.Nil(t, encoded)
		})
	}
}

func TestResourceQueryRejectsInvalidSchemaWithoutReturningUncheckedValues(t *testing.T) {
	for _, schema := range []string{`{`, `{"type":"unknown"}`, `{"$ref":"#/missing"}`} {
		for range 2 {
			encoded, err := ResourceQueryJSONTyped("urn:records?count=1", nil, schema)
			require.ErrorContains(t, err, "invalid resource query")
			assert.Nil(t, encoded, "cached schema errors must remain failures")
		}
	}
}

func TestMetadataAcceptsSDKAndEncodedRepresentations(t *testing.T) {
	for _, value := range []any{mcp.Meta{"trace": "abc"}, map[string]any{"trace": "abc"}, []byte(`{"trace":"abc"}`)} {
		metadata, err := DecodeMeta(value)
		require.NoError(t, err)
		encoded, err := json.Marshal(metadata)
		require.NoError(t, err)
		assert.JSONEq(t, `{"trace":"abc"}`, string(encoded))
	}
	metadata, err := DecodeMeta(1)
	require.ErrorContains(t, err, "unsupported MCP metadata")
	assert.Nil(t, metadata)
}
