package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentComponents(t *testing.T) {
	cases := []struct {
		name        string
		ident       Ident
		wantToolset string
		wantTool    string
	}{
		{name: "canonical", ident: "helpers.search", wantToolset: "helpers", wantTool: "search"},
		{name: "nested namespace", ident: "company.helpers.search", wantToolset: "company.helpers", wantTool: "search"},
		{name: "unqualified", ident: "search", wantTool: "search"},
		{name: "empty", ident: ""},
		{name: "trailing separator", ident: "helpers.", wantToolset: "helpers"},
		{name: "leading separator", ident: ".search", wantTool: "search"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, string(tc.ident), tc.ident.String())
			assert.Equal(t, tc.wantToolset, tc.ident.Toolset())
			assert.Equal(t, tc.wantTool, tc.ident.Tool())
		})
	}
}

func TestIdempotencyScopeFromTags(t *testing.T) {
	cases := []struct {
		name      string
		tags      []string
		wantScope IdempotencyScope
		wantFound bool
		wantError string
	}{
		{name: "none", tags: []string{"team=search"}},
		{name: "transcript", tags: []string{"team=search", TagIdempotencyTranscript}, wantScope: IdempotencyScopeTranscript, wantFound: true},
		{name: "unknown", tags: []string{"loom-mcp.idempotency=request"}, wantError: "tools: unknown idempotency scope \"request\""},
		{name: "empty", tags: []string{"loom-mcp.idempotency="}, wantError: "tools: unknown idempotency scope \"\""},
		{name: "duplicate", tags: []string{TagIdempotencyTranscript, TagIdempotencyTranscript}, wantError: "tools: multiple idempotency tags"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, found, err := IdempotencyScopeFromTags(tc.tags)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.False(t, found)
				assert.Empty(t, scope)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantScope, scope)
			assert.Equal(t, tc.wantFound, found)
		})
	}
}

func TestAnyJSONCodec(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		value, err := AnyJSONCodec.FromJSON(nil)
		require.NoError(t, err)
		assert.Nil(t, value)
	})

	t.Run("round trip object", func(t *testing.T) {
		value, err := AnyJSONCodec.FromJSON([]byte("{\"name\":\"loom\",\"count\":2}"))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"name": "loom", "count": float64(2)}, value)

		encoded, err := AnyJSONCodec.ToJSON(value)
		require.NoError(t, err)
		assert.JSONEq(t, "{\"name\":\"loom\",\"count\":2}", string(encoded))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := AnyJSONCodec.FromJSON([]byte("{"))
		require.Error(t, err)
	})

	t.Run("unsupported value", func(t *testing.T) {
		_, err := AnyJSONCodec.ToJSON(make(chan int))
		require.Error(t, err)
	})
}
