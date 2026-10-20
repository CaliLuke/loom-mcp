package codegen_test

const untaggedUnionRuntimeTest = `package generated_test

import (
	"encoding/json/v2"
	"testing"

	"example.com/fmcp/gen/assistant/toolsets/actions"
	"github.com/stretchr/testify/require"
)

func TestUntaggedUnionPayload(t *testing.T) {
	for _, tc := range []struct {
		name string
		input string
		valid bool
	}{
		{"lookup", "{\"request\":{\"query\":\"needle\"}}", true},
		{"count", "{\"request\":{\"limit\":2}}", true},
		{"ambiguous", "{\"request\":{\"query\":\"needle\",\"limit\":2}}", false},
		{"no match", "{\"request\":{}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := actions.DispatchTool.Payload.FromJSON([]byte(tc.input))
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			encoded, err := actions.DispatchTool.Payload.ToJSON(payload)
			require.NoError(t, err)
			require.JSONEq(t, tc.input, string(encoded))
			var public actions.DispatchPayload
			require.NoError(t, json.Unmarshal([]byte(tc.input), &public))
			require.Equal(t, payload.Request.Kind(), public.Request.Kind())
		})
	}
}
`
