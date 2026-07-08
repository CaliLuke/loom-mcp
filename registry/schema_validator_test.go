package registry

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaValidatorCompiledSchemaCacheResetsAtLimit(t *testing.T) {
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

	require.Len(t, validator.compiled, 1)
	assert.Nil(t, validator.compiled[firstDigest])
	assert.NotNil(t, validator.compiled[overflowDigest])
}

func TestSchemaValidatorValidatePayloadRecompilesAfterCacheReset(t *testing.T) {
	validator := newSchemaValidator()
	schemaBytes := uniqueObjectSchema(0)
	payloadJSON := []byte(`{"value":0}`)

	require.NoError(t, validator.ValidatePayload(schemaBytes, payloadJSON))

	for i := 1; i <= maxCompiledSchemaCacheEntries; i++ {
		_, err := validator.compiledSchema(uniqueObjectSchema(i))
		require.NoError(t, err)
	}

	require.Len(t, validator.compiled, 1)

	require.NoError(t, validator.ValidatePayload(schemaBytes, payloadJSON))
	assert.Len(t, validator.compiled, 2)
}

func uniqueObjectSchema(value int) []byte {
	return []byte(fmt.Sprintf(`{"type":"object","properties":{"value":{"const":%d}},"required":["value"],"additionalProperties":false}`, value))
}
