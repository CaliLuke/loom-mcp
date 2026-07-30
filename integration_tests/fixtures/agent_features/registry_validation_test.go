package agentfeatures_test

import (
	"context"
	"testing"

	registryvalidation "example.com/agentfeatures/gen/features/toolsets/registry_validation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type registryValidationClient struct{}

func (registryValidationClient) GetToolset(context.Context, string) (*registryvalidation.ToolsetSchema, error) {
	return &registryvalidation.ToolsetSchema{
		Name: "validation-tools",
		Tools: []registryvalidation.ToolSchema{
			{
				Name: "validate",
				PayloadSchema: []byte(`{
					"type": "object",
					"properties": {
						"profile": {"$ref": "#/$defs/Profile"},
						"label": {"type": "string", "minLength": 2, "maxLength": 2}
					},
					"required": ["profile", "label"],
					"additionalProperties": false,
					"$defs": {
						"Profile": {
							"type": "object",
							"properties": {
								"name": {"type": "string", "minLength": 2}
							},
							"required": ["name"],
							"additionalProperties": false
						}
					}
				}`),
			},
			{
				Name:          "broken_ref",
				PayloadSchema: []byte(`{"$ref":"#/$defs/Missing","$defs":{}}`),
			},
		},
	}, nil
}

func TestGeneratedRegistryValidatorResolvesRefsAndCountsUnicodeCodePoints(t *testing.T) {
	require.NoError(t, registryvalidation.DiscoverAndPopulate(context.Background(), registryValidationClient{}))

	require.NoError(t, registryvalidation.ValidatePayload(tools.Ident("validate"), map[string]any{
		"profile": map[string]any{"name": "éx"},
		"label":   "你好",
	}))

	err := registryvalidation.ValidatePayload(tools.Ident("validate"), map[string]any{
		"profile": map[string]any{"name": "x"},
		"label":   "你好",
	})
	require.ErrorContains(t, err, "profile.name")
	require.ErrorContains(t, err, "less than minimum 2")

	err = registryvalidation.ValidatePayload(tools.Ident("validate"), map[string]any{
		"profile": map[string]any{"name": "valid"},
		"label":   "界",
	})
	require.ErrorContains(t, err, "label")
	require.ErrorContains(t, err, "less than minimum 2")

	err = registryvalidation.ValidatePayload(tools.Ident("broken_ref"), map[string]any{})
	require.ErrorContains(t, err, "unresolved local schema reference")
}
