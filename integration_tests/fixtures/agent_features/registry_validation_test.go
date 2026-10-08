package agentfeatures_test

import (
	"context"
	"strings"
	"testing"

	registryvalidation "example.com/agentfeatures/gen/features/toolsets/registry_validation"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
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

type registrySchemaClient struct {
	schema []byte
}

func (client registrySchemaClient) GetToolset(context.Context, string) (*registryvalidation.ToolsetSchema, error) {
	return &registryvalidation.ToolsetSchema{
		Name: "validation-tools",
		Tools: []registryvalidation.ToolSchema{{
			Name:          "validate",
			PayloadSchema: client.schema,
			ResultSchema:  client.schema,
		}},
	}, nil
}

func TestGeneratedRegistryValidatorAppliesCompleteSchemaConstraints(t *testing.T) {
	tests := []struct {
		name       string
		schema     string
		invalid    any
		valid      []any
		path       string
		value      any
		diagnostic bool
		errorCount int
	}{
		{
			name:    "schema-valued additionalProperties",
			schema:  `{"type":"object","additionalProperties":{"type":"integer","minimum":10}}`,
			invalid: map[string]any{"extra": 1},
			valid:   []any{map[string]any{"extra": 10}},
		},
		{
			name:    "type array",
			schema:  `{"type":["string","null"]}`,
			invalid: 42,
			valid:   []any{"text", nil},
		},
		{
			name:       "allOf required",
			schema:     `{"type":"object","allOf":[{"required":["x"]}]}`,
			invalid:    map[string]any{},
			valid:      []any{map[string]any{"x": true}},
			path:       "x",
			value:      nil,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "nested required",
			schema:     `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["name"]}}}}`,
			invalid:    map[string]any{"items": []any{map[string]any{}}},
			valid:      []any{map[string]any{"items": []any{map[string]any{"name": "ok"}}}},
			path:       "items[0].name",
			value:      nil,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "additionalProperties false at root",
			schema:     `{"type":"object","additionalProperties":false}`,
			invalid:    map[string]any{"extra": "bad"},
			valid:      []any{map[string]any{}},
			path:       "extra",
			value:      "bad",
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "additionalProperties false nested",
			schema:     `{"type":"object","properties":{"a":{"type":"object","additionalProperties":false}}}`,
			invalid:    map[string]any{"a": map[string]any{"extra": "bad"}},
			valid:      []any{map[string]any{"a": map[string]any{}}},
			path:       "a.extra",
			value:      "bad",
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "minimum item count",
			schema:     `{"type":"object","properties":{"items":{"type":"array","minItems":2}}}`,
			invalid:    map[string]any{"items": []any{"one"}},
			valid:      []any{map[string]any{"items": []any{"one", "two"}}},
			path:       "items",
			value:      nil,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "maximum item count",
			schema:     `{"type":"object","properties":{"items":{"type":"array","maxItems":1}}}`,
			invalid:    map[string]any{"items": []any{"one", "two"}},
			valid:      []any{map[string]any{"items": []any{"one"}}},
			path:       "items",
			value:      nil,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "oneOf mismatch",
			schema:     `{"oneOf":[{"type":"string"},{"type":"integer"}]}`,
			invalid:    true,
			valid:      []any{"text", 1},
			path:       "",
			value:      true,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:       "anyOf mismatch",
			schema:     `{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}`,
			invalid:    map[string]any{"value": true},
			valid:      []any{map[string]any{"value": "text"}, map[string]any{"value": 1}},
			path:       "value",
			value:      true,
			diagnostic: true,
			errorCount: 1,
		},
		{
			name:    "slash in property name",
			schema:  `{"type":"object","properties":{"a/b":{"type":"integer"}}}`,
			invalid: map[string]any{"a/b": "bad"},
			valid:   []any{map[string]any{"a/b": 10}},
			path:    "a/b",
			value:   "bad",
		},
		{
			name:    "numeric object property name",
			schema:  `{"type":"object","properties":{"0":{"type":"integer"}}}`,
			invalid: map[string]any{"0": "bad"},
			valid:   []any{map[string]any{"0": 10}},
			path:    "0",
			value:   "bad",
		},
		{
			name:    "nested array object path and value",
			schema:  `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"count":{"type":"integer"}}}}}}`,
			invalid: map[string]any{"items": []any{map[string]any{"count": "bad"}}},
			valid:   []any{map[string]any{"items": []any{map[string]any{"count": 10}}}},
			path:    "items[0].count",
			value:   "bad",
		},
		{
			name:    "exact integer bounds",
			schema:  `{"type":"integer","minimum":9007199254740993}`,
			invalid: int64(9007199254740992),
			valid:   []any{int64(9007199254740993)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, registryvalidation.DiscoverAndPopulate(context.Background(), registrySchemaClient{schema: []byte(test.schema)}))
			payloadErr := registryvalidation.ValidatePayload(tools.Ident("validate"), test.invalid)
			resultErr := registryvalidation.ValidateResult(tools.Ident("validate"), test.invalid)
			require.Error(t, payloadErr)
			require.Error(t, resultErr)
			var structuredErr *registryvalidation.SchemaValidationErrors
			require.ErrorAs(t, payloadErr, &structuredErr)
			require.NotEmpty(t, structuredErr.Errors)
			if test.diagnostic || test.path != "" {
				require.Equal(t, test.path, structuredErr.Errors[0].Path)
				require.Equal(t, test.value, structuredErr.Errors[0].Value)
			}
			if test.errorCount > 0 {
				require.Len(t, structuredErr.Errors, test.errorCount)
			}
			structuredErr = nil
			require.ErrorAs(t, resultErr, &structuredErr)
			require.NotEmpty(t, structuredErr.Errors)
			if test.errorCount > 0 {
				require.Len(t, structuredErr.Errors, test.errorCount)
			}
			if test.diagnostic || test.path != "" {
				require.Equal(t, test.path, structuredErr.Errors[0].Path)
				require.Equal(t, test.value, structuredErr.Errors[0].Value)
			}
			for _, valid := range test.valid {
				require.NoError(t, registryvalidation.ValidatePayload(tools.Ident("validate"), valid))
				require.NoError(t, registryvalidation.ValidateResult(tools.Ident("validate"), valid))
			}
		})
	}
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
	require.ErrorContains(t, err, "minLength")
	require.ErrorContains(t, err, "want 2")

	err = registryvalidation.ValidatePayload(tools.Ident("validate"), map[string]any{
		"profile": map[string]any{"name": "valid"},
		"label":   "界",
	})
	require.ErrorContains(t, err, "label")
	require.ErrorContains(t, err, "minLength")
	require.ErrorContains(t, err, "want 2")

	err = registryvalidation.ValidatePayload(tools.Ident("broken_ref"), map[string]any{})
	require.ErrorContains(t, err, "json-pointer")
	require.ErrorContains(t, err, "Missing")
}

func TestGeneratedRegistryProviderUsesSafeValidationText(t *testing.T) {
	t.Parallel()

	const toolUseID = "generated-validation-use"
	provider := workflow.NewProvider(&methodBackedFeatureService{})
	result, err := provider.HandleToolCall(
		toolregistry.WithToolUseID(context.Background(), toolUseID),
		toolregistry.ToolCallMessage{
			RegistrationToken: strings.Repeat("a", 64),
			ToolUseID:         toolUseID,
			Tool:              workflow.MethodEcho,
			Payload:           []byte(`{}`),
			Meta:              &toolregistry.ToolCallMeta{RunID: "run", ToolCallID: "call"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result.Error)
	require.Equal(t, "invalid_arguments", result.Error.Code)
	require.Equal(t, "tool arguments failed validation", result.Error.Message)
	require.Equal(t, []*tools.FieldIssue{{Field: "topic", Constraint: "missing_field"}}, result.Error.Issues)
}
