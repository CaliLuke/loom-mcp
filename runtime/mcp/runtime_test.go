package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type namedString string

type textMapKey struct {
	value string
}

func (k textMapKey) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler requires the error result.
	return []byte(k.value), nil
}

func TestMarshalCanonicalJSONRejectsUnsupportedMapKeyKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
	}{
		{
			name:  "int keys",
			input: map[int]string{1: "one"},
		},
		{
			name:  "text marshaler keys",
			input: map[textMapKey]string{{value: "one"}: "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := MarshalCanonicalJSON(tt.input)
			require.Error(t, err)
			require.Contains(t, err.Error(), "unsupported map key type")
		})
	}
}

func TestMarshalCanonicalJSONAcceptsStringMapKeys(t *testing.T) {
	t.Parallel()

	data, err := MarshalCanonicalJSON(map[string]any{
		"count": int64(2),
		"name":  "ok",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"count":2,"name":"ok"}`, string(data))
}

func TestMarshalCanonicalJSONAcceptsNamedStringMapKeys(t *testing.T) {
	t.Parallel()

	data, err := MarshalCanonicalJSON(map[namedString]string{
		namedString("name"): "ok",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"ok"}`, string(data))
}
