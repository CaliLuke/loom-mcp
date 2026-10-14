package longrun_test

import (
	"strings"
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cProfileExactRolesAndCapabilities(t *testing.T) {
	for _, test := range []struct {
		configuration, raw, normalized string
		imageLimit, documentLimit      int
	}{
		{"offline", "reader", "reader", 1 << 20, 1 << 20},
		{"anthropic", "claude-sonnet-4-20250514", "claude-sonnet-4-20250514", 5_000_000, 24_000_000},
		{"bedrock-anthropic", "us.anthropic.claude-sonnet-4-20250514-v1:0", "anthropic.claude-sonnet-4-20250514-v1:0", 3_750_000, 4_500_000},
		{"openai", "gpt-4.1-2025-04-14", "gpt-4.1-2025-04-14", 0, 0},
		{"gemini", "gemini-2.5-pro", "gemini-2.5-pro", 0, 0},
	} {
		t.Run(test.configuration, func(t *testing.T) {
			profileEnvironment(t, test.configuration, test.raw)
			profile, err := longrun.ResolveProfile(test.configuration)
			require.NoError(t, err)
			assert.Equal(t, 1, profile.Version)
			assert.Equal(t, test.configuration, profile.Configuration)
			require.Len(t, profile.Roles, 3)
			assert.Equal(t, test.raw, profile.Roles["planner"].Raw)
			assert.Equal(t, test.normalized, profile.Roles["planner"].Normalized)
			assert.Equal(t, test.configuration, profile.Roles["planner"].Provider)
			configuration, err := longrun.ProfileConfiguration(profile, fake.Faults{})
			require.NoError(t, err)
			require.NotNil(t, configuration.Planner)
			client, err := configuration.Planner(t.Context())
			require.NoError(t, err)
			info, ok := client.(model.ProviderConfigurationInfo)
			require.True(t, ok)
			assert.Equal(t, test.configuration, info.ProviderConfiguration(t.Context(), nil))
			parts, ok := client.(model.ToolResultPartsInfo)
			require.True(t, ok)
			support := parts.ToolResultParts(t.Context(), nil)
			assert.Equal(t, test.imageLimit, support.MaxImageBytes)
			assert.Equal(t, test.documentLimit, support.MaxDocumentBytes)
			limits, ok := client.(model.ContextInfo)
			require.True(t, ok)
			_, raw, known := limits.ContextLimits(t.Context(), nil)
			assert.True(t, known)
			assert.Equal(t, test.raw, raw)
			if len(support.ImageFormats) > 0 {
				support.ImageFormats[0] = "changed"
				assert.NotContains(t, parts.ToolResultParts(t.Context(), nil).ImageFormats, model.ImageFormat("changed"))
			}
			summary, err := configuration.Summariser(t.Context())
			require.NoError(t, err)
			summaryInfo, ok := summary.(model.ProviderConfigurationInfo)
			require.True(t, ok, "summary rejection decorator must preserve profile identity")
			assert.Equal(t, test.configuration, summaryInfo.ProviderConfiguration(t.Context(), nil))
		})
	}
}

func TestK19cProfileRejectsUnknownOrMissingRoles(t *testing.T) {
	_, err := longrun.ResolveProfile("unknown")
	require.Error(t, err)
	profileEnvironment(t, "anthropic", "")
	_, err = longrun.ResolveProfile("anthropic")
	require.Error(t, err)
	_, err = longrun.ProfileConfiguration(longrun.ConfigurationProfile{}, fake.Faults{})
	require.Error(t, err)
}

func profileEnvironment(t *testing.T, configuration, name string) {
	t.Helper()
	for _, role := range []string{"planner", "planner_small", "summariser"} {
		t.Setenv("LONGRUN_"+strings.ToUpper(strings.ReplaceAll(configuration, "-", "_"))+"_"+strings.ToUpper(role), name)
	}
}
