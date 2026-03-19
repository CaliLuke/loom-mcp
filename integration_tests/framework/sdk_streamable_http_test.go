package framework

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedServerSupportsMultipleSDKStreamableHTTPSessions(t *testing.T) {
	t.Parallel()

	session1 := newIntegrationSDKSession(t, "itest-1")
	assertSDKAnalyzeSentimentCallWorks(t, session1)
	require.NoError(t, session1.Close())

	session2 := newIntegrationSDKSession(t, "itest-2")
	assertSDKAnalyzeSentimentCallWorks(t, session2)
	require.NoError(t, session2.Close())
}

func TestGeneratedServerSDKInitializeAndListCatalog(t *testing.T) {
	t.Parallel()

	session := newIntegrationSDKSession(t, "itest-catalog")
	defer func() {
		require.NoError(t, session.Close())
	}()

	initResult := session.InitializeResult()
	require.NotNil(t, initResult)
	assert.Equal(t, "2025-11-25", initResult.ProtocolVersion)
	require.NotNil(t, initResult.Capabilities)
	require.NotNil(t, initResult.Capabilities.Tools)
	require.NotNil(t, initResult.Capabilities.Resources)
	require.NotNil(t, initResult.Capabilities.Prompts)
	require.NotNil(t, initResult.ServerInfo)
	assert.Equal(t, "assistant-mcp", initResult.ServerInfo.Name)
	assert.Equal(t, "1.0.0", initResult.ServerInfo.Version)

	ctx, cancel := sdkTestContext(t)
	defer cancel()

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	analyzeTool := findToolByName(t, tools.Tools, "analyze_sentiment")
	require.Equal(t, "Analyze sentiment of text", analyzeTool.Description)

	schema, ok := analyzeTool.InputSchema.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, false, schema["additionalProperties"])

	required, ok := schema["required"].([]any)
	require.True(t, ok)
	require.Len(t, required, 1)
	assert.Equal(t, "text", required[0])

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	textField, ok := properties["text"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", textField["type"])

	resources, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, resources.Resources, 4)

	prompts, err := session.ListPrompts(ctx, &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, prompts.Prompts, 3)
}

func TestGeneratedServerSDKCallToolWorks(t *testing.T) {
	t.Parallel()

	session := newIntegrationSDKSession(t, "itest-tool-call")
	defer func() {
		require.NoError(t, session.Close())
	}()

	ctx, cancel := sdkTestContext(t)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_sentiment",
		Arguments: map[string]any{
			"text": "I love this generated SDK server.",
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)

	firstText, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.JSONEq(t, `{"sentiment":"positive"}`, firstText.Text)
}

func TestGeneratedServerSDKReadResourceAndGetPrompt(t *testing.T) {
	t.Parallel()

	session := newIntegrationSDKSession(t, "itest-resources-prompts")
	defer func() {
		require.NoError(t, session.Close())
	}()

	ctx, cancel := sdkTestContext(t)
	defer cancel()

	resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "system://info",
	})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)

	content := resource.Contents[0]
	assert.Equal(t, "system://info", content.URI)
	assert.Equal(t, "application/json", content.MIMEType)

	var body struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal([]byte(content.Text), &body))
	assert.Equal(t, "assistant-itest", body.Name)
	assert.Equal(t, "1.0.0", body.Version)

	staticPrompt, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "code_review",
		Arguments: map[string]string{
			"code": "func add(a, b int) int { return a + b }",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, staticPrompt.Messages)
	assert.Equal(t, "Code review guidance", staticPrompt.Description)

	prompts, err := session.ListPrompts(ctx, &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, prompts.Prompts, 3)
}

func TestGeneratedServerSDKClosedLoopFigmaFlow(t *testing.T) {
	t.Parallel()

	session := newIntegrationSDKSession(t, "itest-figma-loop")
	defer func() {
		require.NoError(t, session.Close())
	}()

	ctx, cancel := sdkTestContext(t)
	defer cancel()

	toolResult, err := session.CallTool(ctx, &mcp.CallToolParams{
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

	firstText, ok := toolResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var spec struct {
		ScreenTitle     string `json:"screen_title"`
		Platform        string `json:"platform"`
		DesignTokensURI string `json:"design_tokens_uri"`
		Sections        []struct {
			Component string `json:"component"`
		} `json:"sections"`
		PrimaryCTA struct {
			Label string `json:"label"`
		} `json:"primary_cta"`
	}
	require.NoError(t, json.Unmarshal([]byte(firstText.Text), &spec))
	assert.Equal(t, "Checkout", spec.ScreenTitle)
	assert.Equal(t, "ios", spec.Platform)
	assert.Equal(t, "figma://design-system/mobile-checkout", spec.DesignTokensURI)
	if assert.Len(t, spec.Sections, 4) {
		assert.Equal(t, "HeroCard", spec.Sections[0].Component)
	}
	assert.Equal(t, "Pay now", spec.PrimaryCTA.Label)

	resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "figma://design-system/mobile-checkout",
	})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	assert.Equal(t, "figma://design-system/mobile-checkout", resource.Contents[0].URI)
	assert.Contains(t, resource.Contents[0].Text, `"name":"Mobile Commerce System"`)
	assert.Contains(t, resource.Contents[0].Text, `"accent.brand=#1ABCFE"`)

	promptResult, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "figma_implementation_prompt",
		Arguments: map[string]string{
			"screen_title":      spec.ScreenTitle,
			"framework":         "react",
			"design_tokens_uri": spec.DesignTokensURI,
			"dpi_json":          firstText.Text,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Figma implementation handoff", promptResult.Description)
	require.NotEmpty(t, promptResult.Messages)
}

func newIntegrationSDKSession(t *testing.T, clientName string) *mcp.ClientSession {
	t.Helper()

	if !SupportsServer() {
		t.Skip("integration server not available; set TEST_SERVER_URL or restore the example directory")
	}

	r := NewRunner()
	r.skipGeneration = true
	require.NoError(t, r.startServer(t))
	t.Cleanup(r.stopServer)

	return connectSDKSession(t, r.baseURL.String()+"/rpc", clientName)
}

func connectSDKSession(t *testing.T, endpoint string, clientName string) *mcp.ClientSession {
	t.Helper()

	ctx, cancel := sdkTestContext(t)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    clientName,
		Version: "1.0.0",
	}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: headerRoundTripper{
				base: http.DefaultTransport,
				headers: map[string]string{
					"x-mcp-allow-names": "documents,system_info,conversation_history,figma_design_system",
				},
			},
		},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	return session
}

func assertSDKAnalyzeSentimentCallWorks(t *testing.T, session *mcp.ClientSession) {
	t.Helper()

	ctx, cancel := sdkTestContext(t)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_sentiment",
		Arguments: map[string]any{
			"text": "hello from sdk",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.Content)
}

func findToolByName(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()

	for _, tool := range tools {
		if tool != nil && tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func sdkTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 10*time.Second)
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := req.Clone(req.Context())
	for key, value := range rt.headers {
		cloned.Header.Set(key, value)
	}
	return base.RoundTrip(cloned)
}
