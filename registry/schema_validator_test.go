package registry

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaValidatorCompiledSchemaCacheEvictsOldestAtLimit(t *testing.T) {
	validator := newSchemaValidator()

	for i := range maxCompiledSchemaCacheEntries {
		_, err := validator.compiledSchema(uniqueObjectSchema(i))
		require.NoError(t, err)
	}

	require.Len(t, validator.compiled, maxCompiledSchemaCacheEntries)
	firstDigest := schemaDigest(uniqueObjectSchema(0))
	overflowSchema := uniqueObjectSchema(maxCompiledSchemaCacheEntries)
	overflowDigest := schemaDigest(overflowSchema)

	_, err := validator.compiledSchema(overflowSchema)
	require.NoError(t, err)

	require.Len(t, validator.compiled, maxCompiledSchemaCacheEntries)
	assert.Nil(t, validator.compiled[firstDigest])
	assert.NotNil(t, validator.compiled[overflowDigest])
	assert.Equal(t, schemaDigest(uniqueObjectSchema(1)), validator.compiledOrder[0])
}

func TestSchemaValidatorValidatePayloadRecompilesAfterEviction(t *testing.T) {
	validator := newSchemaValidator()
	schemaBytes := uniqueObjectSchema(0)
	payloadJSON := []byte(`{"value":0}`)

	require.NoError(t, validator.ValidatePayload(schemaBytes, payloadJSON))

	for i := 1; i <= maxCompiledSchemaCacheEntries; i++ {
		_, err := validator.compiledSchema(uniqueObjectSchema(i))
		require.NoError(t, err)
	}

	require.Len(t, validator.compiled, maxCompiledSchemaCacheEntries)
	assert.Nil(t, validator.compiled[schemaDigest(schemaBytes)])

	require.NoError(t, validator.ValidatePayload(schemaBytes, payloadJSON))
	assert.Len(t, validator.compiled, maxCompiledSchemaCacheEntries)
	assert.NotNil(t, validator.compiled[schemaDigest(schemaBytes)])
}

func TestSchemaValidatorPreservesIntegerPrecision(t *testing.T) {
	validator := newSchemaValidator()
	const exact = `9007199254740993`

	for _, tc := range []struct {
		name    string
		schema  string
		valid   string
		invalid string
	}{
		{
			name:    "minimum",
			schema:  `{"type":"integer","minimum":` + exact + `}`,
			valid:   exact,
			invalid: `9007199254740992`,
		},
		{
			name:    "enum",
			schema:  `{"type":"integer","enum":[` + exact + `]}`,
			valid:   exact,
			invalid: `9007199254740992`,
		},
		{
			name:    "const",
			schema:  `{"type":"integer","const":` + exact + `}`,
			valid:   exact,
			invalid: `9007199254740992`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validator.ValidatePayload([]byte(tc.schema), []byte(tc.valid)))
			require.Error(t, validator.ValidatePayload([]byte(tc.schema), []byte(tc.invalid)))
		})
	}
}

func TestSchemaValidatorRejectsInvalidJSONBoundaries(t *testing.T) {
	validator := newSchemaValidator()
	for _, raw := range []string{
		`{"type":"object","properties":{"value":{"type":"integer"}},"value":1,"value":2}`,
		`{"type":"integer"} {}`,
		"{\"type\":\"string\",\"description\":\"\xff\"}",
		`{"type":"integer","minimum":`,
	} {
		_, err := validator.compiledSchema([]byte(raw))
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`1 2`, "\xff", `{"n":1,"n":2}`, `{"n":`} {
		err := validator.ValidatePayload([]byte(`{"type":"number"}`), []byte(raw))
		require.Error(t, err, raw)
	}
}

func uniqueObjectSchema(value int) []byte {
	return []byte(fmt.Sprintf(`{"type":"object","properties":{"value":{"const":%d}},"required":["value"],"additionalProperties":false}`, value))
}
