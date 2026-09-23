package codegen

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom/expr"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalRecoveryNumericConstraints(t *testing.T) {
	for _, tt := range []struct {
		name string
		attr *expr.AttributeExpr
		want string
	}{
		{"collection default", &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50, Validation: &expr.ValidationExpr{Minimum: new(1.0), Maximum: new(200.0)}}, `50`},
		{"history default", &expr.AttributeExpr{Type: expr.Int, DefaultValue: 10, Validation: &expr.ValidationExpr{Minimum: new(1.0), Maximum: new(200.0)}}, `10`},
		{"example before default", &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50, UserExamples: []*expr.ExampleExpr{{Value: 25}}, Validation: &expr.ValidationExpr{Minimum: new(1.0), Maximum: new(200.0)}}, `25`},
		{"invalid example", &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50, UserExamples: []*expr.ExampleExpr{{Value: 0}}, Validation: &expr.ValidationExpr{Minimum: new(1.0)}}, `50`},
		{"invalid default", &expr.AttributeExpr{Type: expr.Int, DefaultValue: 0, Validation: &expr.ValidationExpr{Minimum: new(1.0)}}, `1`},
		{"enum", &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{Values: []any{5, 10}}}, `5`},
		{"positive integer", &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{Minimum: new(1.2)}}, `2`},
		{"negative integer", &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{Maximum: new(-1.2)}}, `-2`},
		{"exclusive integer minimum", &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(2.0)}}, `3`},
		{"exclusive integer maximum", &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{ExclusiveMaximum: new(-2.0)}}, `-3`},
		{"fractional range", &expr.AttributeExpr{Type: expr.Float64, Validation: &expr.ValidationExpr{Minimum: new(0.25), Maximum: new(0.5)}}, `0.25`},
		{"exclusive float", &expr.AttributeExpr{Type: expr.Float64, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(1.0), Maximum: new(2.0)}}, `1.0000000000000002`},
		{"exclusive float32", &expr.AttributeExpr{Type: expr.Float32, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(1.0), Maximum: new(2.0)}}, `1.0000001192092896`},
		{"fractional float32 minimum", &expr.AttributeExpr{Type: expr.Float32, Validation: &expr.ValidationExpr{Minimum: new(0.1000000009)}}, `0.10000000149011612`},
		{"fractional exclusive float32 minimum", &expr.AttributeExpr{Type: expr.Float32, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(0.1)}}, `0.10000000149011612`},
		{"exclusive large integer", &expr.AttributeExpr{Type: expr.Int64, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(9007199254740992.0)}}, `9007199254740993`},
		{"decimal unsigned minimum", &expr.AttributeExpr{Type: expr.UInt64, Validation: &expr.ValidationExpr{Minimum: new(1.0000000000000023e19)}}, `10000000000000023000`},
		{"decimal unsigned exclusive minimum", &expr.AttributeExpr{Type: expr.UInt64, Validation: &expr.ValidationExpr{ExclusiveMinimum: new(1.0000000000000023e19)}}, `10000000000000023001`},
		{"decimal signed maximum", &expr.AttributeExpr{Type: expr.Int64, Validation: &expr.ValidationExpr{Maximum: new(-9.000000000000001e18)}}, `-9000000000000001000`},
		{"unsigned", &expr.AttributeExpr{Type: expr.UInt64, Validation: &expr.ValidationExpr{Minimum: new(3.0)}}, `3`},
		{"exact unsigned default", &expr.AttributeExpr{Type: expr.UInt64, DefaultValue: uint64(18446744073709551615)}, `18446744073709551615`},
		{"alias occurrence bounds", &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: "Limit", AttributeExpr: &expr.AttributeExpr{Type: expr.Int, Validation: &expr.ValidationExpr{Minimum: new(1.0)}}}, Validation: &expr.ValidationExpr{Minimum: new(10.0)}}, `10`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := &expr.AttributeExpr{
				Type:       &expr.Object{{Name: "limit", Attribute: tt.attr}},
				Validation: &expr.ValidationExpr{Required: []string{"limit"}},
			}
			example := synthesizeCanonicalExample(payload)
			assert.Equal(t, `{"limit":`+tt.want+`}`, example)
			assertRecoverySchemaValid(t, payload, example)
			assert.Equal(t, example, synthesizeCanonicalExample(payload))
		})
	}
}

func TestCanonicalRecoveryAuthoredPayload(t *testing.T) {
	for _, tt := range []struct {
		name    string
		example map[string]any
		want    string
	}{
		{"valid object example", map[string]any{"limit": 25}, `{"limit":25}`},
		{"invalid object example", map[string]any{"limit": 0}, `{"limit":50}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := &expr.AttributeExpr{
				Type:         &expr.Object{{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50, Validation: &expr.ValidationExpr{Minimum: new(1.0)}}}},
				UserExamples: []*expr.ExampleExpr{{Value: tt.example}},
			}
			example := synthesizeCanonicalExample(payload)
			assert.Equal(t, tt.want, example)
			assertRecoverySchemaValid(t, payload, example)
		})
	}
}

func TestCanonicalRecoveryUnionDefaults(t *testing.T) {
	union := &expr.Union{TypeName: "Action", Values: []*expr.NamedAttributeExpr{
		{Name: "list", Attribute: &expr.AttributeExpr{Type: &expr.Object{{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50, Validation: &expr.ValidationExpr{Minimum: new(1.0), Maximum: new(200.0)}}}}}},
		{Name: "history", Attribute: &expr.AttributeExpr{Type: &expr.Object{{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, DefaultValue: 10, Validation: &expr.ValidationExpr{Minimum: new(1.0), Maximum: new(200.0)}}}}}},
	}}
	payload := &expr.AttributeExpr{Type: &expr.Object{{Name: "request", Attribute: &expr.AttributeExpr{Type: union}}}, Validation: &expr.ValidationExpr{Required: []string{"request"}}}
	assertRecoverySchemaValid(t, payload, synthesizeCanonicalExample(payload))
	for _, example := range tagExamples(payload, "request", union) {
		assertRecoverySchemaValid(t, payload, example)
	}
}

func assertRecoverySchemaValid(t *testing.T, attr *expr.AttributeExpr, example string) {
	t.Helper()
	schemaJSON, err := expr.InlineJSONSchema(attr)
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaJSON)))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("schema.json", document))
	schema, err := compiler.Compile("schema.json")
	require.NoError(t, err)
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(example))
	require.NoError(t, err)
	assert.NoError(t, schema.Validate(value), example)
}
