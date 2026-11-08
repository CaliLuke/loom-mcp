package codegen_test

const toolCustomizationRuntimeTest = `package fmcp_test

import (
	"testing"

	docs "example.com/fmcp/gen/alpha/toolsets/docs"
	"github.com/stretchr/testify/require"
)

func TestCustomizedToolRequiredness(t *testing.T) {
	_, err := docs.UnmarshalStorePayload([]byte("{\"id\":\"one\"}"))
	require.Error(t, err)
	payload, err := docs.UnmarshalStorePayload([]byte("{\"id\":\"one\",\"title\":\"hello\"}"))
	require.NoError(t, err)
	require.Equal(t, "hello", payload.Title)
	result, err := docs.UnmarshalStoreResult([]byte("{\"id\":\"one\"}"))
	require.NoError(t, err)
	require.Nil(t, result.Title)
}
`
