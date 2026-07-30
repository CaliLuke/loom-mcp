package bedrock

import (
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestEncodeOutputConfigStructuredOutput(t *testing.T) {
	cfg, err := encodeOutputConfig(&model.StructuredOutput{
		Name:   "draft",
		Schema: []byte(`{"type":"object","required":["title"]}`),
	})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.TextFormat)
	require.Equal(t, brtypes.OutputFormatTypeJsonSchema, cfg.TextFormat.Type)

	member, ok := cfg.TextFormat.Structure.(*brtypes.OutputFormatStructureMemberJsonSchema)
	require.True(t, ok)
	require.NotNil(t, member.Value.Name)
	require.Equal(t, "draft", *member.Value.Name)
	require.NotNil(t, member.Value.Schema)
	require.JSONEq(t, `{"type":"object","required":["title"],"additionalProperties":false}`, *member.Value.Schema)
}

func TestEncodeOutputConfigRejectsMissingSchema(t *testing.T) {
	_, err := encodeOutputConfig(&model.StructuredOutput{Name: "draft"})
	require.ErrorContains(t, err, "requires a schema")
}

func TestEncodeOutputConfigNormalizesStructuredOutputSchemaForBedrock(t *testing.T) {
	cfg, err := encodeOutputConfig(&model.StructuredOutput{
		Schema: []byte(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"properties": {
				"count": {
					"type": "integer",
					"format": "int64",
					"minimum": 1
				},
				"metadata": {
					"type": "object",
					"properties": {
						"label": {
							"type": "string",
							"maxLength": 20
						}
					}
				},
				"name": {
					"type": "string",
					"format": "uuid",
					"minLength": 1
				},
				"items": {
					"type": "array",
					"items": {
						"type": "string",
						"maxLength": 10
					},
					"minItems": 2
				}
			},
			"required": ["count"]
		}`),
	})
	require.NoError(t, err)

	member, ok := cfg.TextFormat.Structure.(*brtypes.OutputFormatStructureMemberJsonSchema)
	require.True(t, ok)
	require.JSONEq(t, `{
		"type": "object",
		"properties": {
			"count": {
				"type": "integer"
			},
			"metadata": {
				"type": "object",
				"properties": {
					"label": {
						"type": "string"
					}
				},
				"additionalProperties": false
			},
			"name": {
				"type": "string",
				"format": "uuid"
			},
			"items": {
				"type": "array",
				"items": {
					"type": "string"
				}
			}
		},
		"required": ["count"],
		"additionalProperties": false
	}`, *member.Value.Schema)
}

func TestEncodeOutputConfigRejectsAdditionalPropertiesSchema(t *testing.T) {
	_, err := encodeOutputConfig(&model.StructuredOutput{
		Schema: []byte(`{
			"type": "object",
			"additionalProperties": {
				"type": "string"
			}
		}`),
	})
	require.ErrorContains(t, err, "additionalProperties")
	require.ErrorContains(t, err, "$")
}

func TestEncodeOutputConfigPreservesEnumAndConstDataValues(t *testing.T) {
	cfg, err := encodeOutputConfig(&model.StructuredOutput{
		Schema: []byte(`{
			"type": "object",
			"properties": {
				"salutation": {
					"enum": [{
						"title": "Mr",
						"default": true,
						"type": "object",
						"pattern": "literal",
						"minimum": 1
					}]
				},
				"fixed": {
					"const": {
						"title": "fixed",
						"default": false,
						"type": "object"
					}
				},
				"choice": {
					"oneOf": [{
						"type": "object",
						"title": "branch",
						"properties": {
							"value": {"type": "string", "pattern": "schema-only"}
						}
					}]
				}
			}
		}`),
	})
	require.NoError(t, err)

	member, ok := cfg.TextFormat.Structure.(*brtypes.OutputFormatStructureMemberJsonSchema)
	require.True(t, ok)
	require.JSONEq(t, `{
		"type": "object",
		"properties": {
			"salutation": {
				"enum": [{
					"title": "Mr",
					"default": true,
					"type": "object",
					"pattern": "literal",
					"minimum": 1
				}]
			},
			"fixed": {
				"const": {
					"title": "fixed",
					"default": false,
					"type": "object"
				}
			},
			"choice": {
				"oneOf": [{
					"type": "object",
					"properties": {
						"value": {"type": "string"}
					},
					"additionalProperties": false
				}]
			}
		},
		"additionalProperties": false
	}`, *member.Value.Schema)
}
