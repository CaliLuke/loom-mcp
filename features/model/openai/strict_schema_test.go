package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

func TestProjectStrictSchemaRequiresAllPropertiesAndClosesObjects(t *testing.T) {
	got, err := projectStrictSchema(rawjson.Message(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"count": {"type": "integer", "format": "int64"},
			"nested": {
				"type": "object",
				"properties": {
					"label": {"type": "string"}
				}
			}
		},
		"required": ["title"],
		"$schema": "https://json-schema.org/draft/2020-12/schema"
	}`))

	require.NoError(t, err)
	require.JSONEq(t, `{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"count": {"type": ["integer", "null"]},
			"nested": {
				"type": ["object", "null"],
				"properties": {
					"label": {"type": ["string", "null"]}
				},
				"required": ["label"],
				"additionalProperties": false
			}
		},
		"required": ["count", "nested", "title"],
		"additionalProperties": false
	}`, mustMarshalJSON(t, got))
}

func TestProjectStrictSchemaRejectsOpenObject(t *testing.T) {
	_, err := projectStrictSchema(rawjson.Message(`{"type":"object","additionalProperties":true}`))
	require.ErrorContains(t, err, "open object")
}

func TestCanonicalizeStrictPayloadDropsProjectedNulls(t *testing.T) {
	got, err := canonicalizeStrictPayload(
		rawjson.Message(`{
			"type": "object",
			"properties": {
				"title": {"type": "string"},
				"count": {"type": "integer"},
				"maybe": {"type": ["string", "null"]},
				"nested": {
					"type": "object",
					"properties": {
						"label": {"type": "string"}
					}
				}
			},
			"required": ["title"]
		}`),
		rawjson.Message(`{"title":"ok","count":null,"maybe":null,"nested":{"label":null}}`),
	)

	require.NoError(t, err)
	require.JSONEq(t, `{"title":"ok","maybe":null,"nested":{}}`, string(got))
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
