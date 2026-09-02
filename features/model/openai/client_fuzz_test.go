package openai

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzParseToolArguments(f *testing.F) {
	f.Add(`{"query":"loom"}`)
	f.Add("")
	f.Add("{")
	f.Add(`[1,true,null]`)

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<20 {
			return
		}
		payload, err := parseToolArguments(raw)
		if raw == "" {
			require.NoError(t, err)
			require.Nil(t, payload)
			return
		}
		if !jsontext.Value([]byte(raw)).IsValid() {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, raw, string(payload))
	})
}
