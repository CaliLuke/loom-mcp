package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateJSONSchemaRejectsInvalidAndDuplicateSchemaJSON(t *testing.T) {
	tests := []struct {
		name   string
		schema string
	}{
		{name: "invalid JSON", schema: `{"type":`},
		{name: "duplicate members", schema: `{"type":"string","type":"integer"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateJSONSchema([]byte(test.schema), "value")
			require.Error(t, err)
		})
	}
}

func TestValidateJSONSchemaPreservesExactNumbers(t *testing.T) {
	const schema = `{"type":"integer","minimum":9007199254740993}`

	require.NoError(t, ValidateJSONSchema([]byte(schema), int64(9007199254740993)))
	err := ValidateJSONSchema([]byte(schema), int64(9007199254740992))
	require.Error(t, err)
}

func TestValidateJSONSchemaRejectsExternalReferences(t *testing.T) {
	err := ValidateJSONSchema([]byte(`{"$ref":"https://example.invalid/schema.json"}`), map[string]any{})
	require.ErrorContains(t, err, "external schema reference")
}
