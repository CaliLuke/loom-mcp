package assistantapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	assistant "example.com/assistant/gen/assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerClosedLoopFigmaFlow(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	session := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", map[string]string{
		"x-mcp-allow-names": "figma_design_system",
	})
	defer func() {
		require.NoError(t, session.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	toolResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "generate_dpi_spec",
		Arguments: map[string]any{
			"screen_title":      "Checkout",
			"platform":          "ios",
			"density":           "comfortable",
			"primary_cta":       "Pay now",
			"sections":          []string{"hero", "summary", "payment_form", "trust_bar"},
			"include_dev_notes": true,
		},
	})
	require.NoError(t, err)
	require.Len(t, toolResult.Content, 1)

	_, ok := toolResult.Content[0].(*sdkmcp.TextContent)
	require.True(t, ok)

	structured, err := json.Marshal(toolResult.StructuredContent)
	require.NoError(t, err)

	var spec assistant.DPISpec
	require.NoError(t, json.Unmarshal(structured, &spec))
	assert.Equal(t, "Checkout", spec.ScreenTitle)
	assert.Equal(t, "ios", spec.Platform)
	assert.Equal(t, 390, spec.Viewport.Width)
	if assert.Len(t, spec.Sections, 4) {
		assert.Equal(t, "HeroCard", spec.Sections[0].Component)
	}
	assert.Equal(t, "figma://design-system/mobile-checkout", spec.DesignTokensURI)

	resource, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "figma://design-system/mobile-checkout",
	})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)

	var designSystem assistant.DesignSystem
	require.NoError(t, json.Unmarshal([]byte(resource.Contents[0].Text), &designSystem))
	assert.Equal(t, "Mobile Commerce System", designSystem.Name)
	assert.Equal(t, "2026.03", designSystem.Version)
	assert.Contains(t, designSystem.Tokens.Colors, "accent.brand=#1ABCFE")

	promptResult, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{
		Name: "figma_implementation_prompt",
		Arguments: map[string]string{
			"screen_title":      spec.ScreenTitle,
			"framework":         "react",
			"design_tokens_uri": spec.DesignTokensURI,
			"dpi_json":          string(structured),
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, promptResult.Messages)
	require.Equal(t, "Figma implementation handoff", promptResult.Description)
	assert.Contains(t, promptResult.Messages[0].Content.(*sdkmcp.TextContent).Text, "Checkout")
	assert.Contains(t, promptResult.Messages[0].Content.(*sdkmcp.TextContent).Text, "react")
	assert.Contains(t, promptResult.Messages[0].Content.(*sdkmcp.TextContent).Text, "figma://design-system/mobile-checkout")
	assert.Contains(t, promptResult.Messages[0].Content.(*sdkmcp.TextContent).Text, "Pay now")
}
