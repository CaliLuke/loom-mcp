package codegen

import (
	"testing"

	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalRecoveryUUIDConstraints(t *testing.T) {
	const pattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	const authored = "6f7d20d5-985b-4bbc-a07c-1abe9286b40e"
	for _, tc := range []struct {
		name    string
		example string
		named   bool
		enum    bool
	}{
		{"synthesized", "", false, false},
		{"authored", authored, false, false},
		{"invalid authored", "example", false, false},
		{"named occurrence", "", true, false},
		{"enum", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field := &expr.AttributeExpr{Type: expr.String, Validation: &expr.ValidationExpr{Format: expr.FormatUUID, Pattern: pattern}}
			if tc.example != "" {
				field.UserExamples = []*expr.ExampleExpr{{Value: tc.example}}
			}
			if tc.enum {
				field.Validation.Values = []any{authored}
			}
			if tc.named {
				field.Validation.Pattern = ""
				field = &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: "RequestKey", AttributeExpr: field}, Validation: &expr.ValidationExpr{Pattern: pattern}}
			}
			payload := &expr.AttributeExpr{Type: &expr.Object{{Name: "requestKey", Attribute: field}}, Validation: &expr.ValidationExpr{Required: []string{"requestKey"}}}
			example := synthesizeCanonicalExample(payload)
			require.NotEmpty(t, example)
			assertRecoverySchemaValid(t, payload, example)
			assert.Equal(t, example, synthesizeCanonicalExample(payload))
			if tc.example == authored || tc.enum {
				assert.JSONEq(t, `{"requestKey":"`+authored+`"}`, example)
			}
		})
	}
}

func TestCanonicalRecoveryOmitsUnsupportedStringConstraints(t *testing.T) {
	field := &expr.AttributeExpr{Type: expr.String, Validation: &expr.ValidationExpr{Format: expr.FormatUUID, Pattern: "^abcd"}}
	payload := &expr.AttributeExpr{Type: &expr.Object{{Name: "requestKey", Attribute: field}}, Validation: &expr.ValidationExpr{Required: []string{"requestKey"}}}
	assert.Empty(t, synthesizeCanonicalExample(payload))
}
