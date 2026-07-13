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

func uniqueObjectSchema(value int) []byte {
	return []byte(fmt.Sprintf(`{"type":"object","properties":{"value":{"const":%d}},"required":["value"],"additionalProperties":false}`, value))
}
