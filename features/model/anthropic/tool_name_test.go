package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

func TestSanitizeToolName(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		want      string
		wantLen   int
	}{
		{
			name:      "allowed name at boundary",
			canonical: strings.Repeat("a", 64),
			want:      strings.Repeat("a", 64),
			wantLen:   64,
		},
		{
			name:      "name beyond boundary is hashed",
			canonical: strings.Repeat("a", 65),
			want:      strings.Repeat("a", 55) + "_635361c4",
			wantLen:   64,
		},
		{
			name:      "unicode is replaced before byte truncation",
			canonical: strings.Repeat("界", 65),
			want:      strings.Repeat("_", 55) + "_5de5f765",
			wantLen:   64,
		},
		{
			name:      "toolset suffix remains concise",
			canonical: "atlas.read.read_lookup",
			want:      "lookup",
			wantLen:   6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeToolName(tt.canonical)
			assert.Equal(t, tt.want, got)
			assert.Len(t, got, tt.wantLen)
			assert.True(t, isProviderSafeToolName(got))
			assert.Equal(t, got, sanitizeToolName(tt.canonical), "mapping must be deterministic")
		})
	}
}

func TestEncodeToolsLongNameRoundTrip(t *testing.T) {
	canonical := "tools." + strings.Repeat("long_name_", 8)
	definitions := []*model.ToolDefinition{
		{
			Name:        canonical,
			Description: "long-name tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}

	encoded, canonicalToProvider, providerToCanonical, err := encodeTools(context.Background(), definitions)
	require.NoError(t, err)
	require.Len(t, encoded, 1)
	providerName := canonicalToProvider[canonical]
	assert.Len(t, providerName, anthropicToolNameMaxBytes)
	assert.Equal(t, canonical, providerToCanonical[providerName])

	response, err := translateResponse(&sdk.Message{
		Content: []sdk.ContentBlockUnion{
			{
				Type:  anthropicContentTypeToolUse,
				Name:  providerName,
				ID:    "tool-1",
				Input: json.RawMessage(`{"value":1}`),
			},
		},
	}, providerToCanonical, newToolUseIDCodec(), "claude", model.ModelClassDefault)
	require.NoError(t, err)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, canonical, string(response.ToolCalls[0].Name))
}

func TestEncodeToolsRejectsProviderNameCollision(t *testing.T) {
	definitions := []*model.ToolDefinition{
		{Name: "alpha.lookup", Description: "first"},
		{Name: "beta.lookup", Description: "second"},
	}

	_, _, _, err := encodeTools(context.Background(), definitions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `sanitizes to "lookup" which collides with "alpha.lookup"`)
}

func TestSanitizeToolNameHashesCanonicalIdentity(t *testing.T) {
	base := strings.Repeat("lookup_", 10)
	first := sanitizeToolName("alpha." + base)
	second := sanitizeToolName("beta." + base)

	assert.NotEqual(t, first, second)
	assert.Len(t, first, anthropicToolNameMaxBytes)
	assert.Len(t, second, anthropicToolNameMaxBytes)
}
