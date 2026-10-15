package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPOAuthOptionsPreserveNormalizedResourceContract(t *testing.T) {
	runMCPDSL(t, func() {
		API("test", func() {
		})
		Service("protected", func() {
			MCP("protected", "1", OAuth(nil, AuthorizationServer(" https://auth.example.com "), AuthorizationServer("https://other.example.com"), OAuthScope(" read ", " Read documents "), ResourceIdentifier(" https://api.example.com/mcp "), BearerMethodsSupported(" header ", " ", "body"), ResourceDocumentationURL(" https://api.example.com/docs "), TrustProxyHeaders()))
		})
	})
	actual := mcpexpr.Root.MCPServers["protected"].OAuth
	require.NotNil(t, actual)
	assert.Equal(t, []string{"https://auth.example.com", "https://other.example.com"}, actual.AuthorizationServers)
	require.Len(t, actual.Scopes, 1)
	assert.Equal(t, "read", actual.Scopes[0].Name)
	assert.Equal(t, "Read documents", actual.Scopes[0].Description)
	assert.Equal(t, "https://api.example.com/mcp", actual.ResourceIdentifier)
	assert.Equal(t, []string{"header", "body"}, actual.BearerMethodsSupported)
	assert.Equal(t, "https://api.example.com/docs", actual.ResourceDocumentationURL)
	assert.True(t, actual.TrustProxyHeaders)
}

func TestMCPDiscoveryAndWatchOptionsSurviveEvaluation(t *testing.T) {
	runMCPDSL(t, func() {
		API("test", func() {
		})
		Service("catalog", func() {
			MCP("catalog", "1")
			Method("lookup", func() {
				Payload(func() {
					Attribute("query", String)
				})
				Result(String)
				Tool("lookup", "Find documents", ToolDiscoveryCallTemplateArg(" query ", "example"), ToolDiscoveryCallTemplateArg(" ", "ignored"), ToolDiscoveryCallTemplateArg("limit", 10))
			})
			Method("status", func() {
				Result(String)
				WatchableResource("status", "status://current", "text/plain", nil, ResourceIcons(Icon("https://example.com/icon.png")))
			})
		})
	})
	server := mcpexpr.Root.MCPServers["catalog"]
	require.Len(t, server.Tools, 1)
	assert.Equal(t, map[string]any{"query": "example", "limit": 10}, server.Tools[0].DiscoveryCallTemplateArgs)
	require.Len(t, server.Resources, 1)
	assert.True(t, server.Resources[0].Watchable)
	require.Len(t, server.Resources[0].Icons, 1)
	assert.Equal(t, "https://example.com/icon.png", server.Resources[0].Icons[0].Source)
}

func TestMCPResourcesAndPromptsRequireServerDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		declare func()
	}{
		{"resource", func() {
			Method("read", func() {
				Resource("doc", "doc://read", "text/plain")
			})
		}},
		{"watchable", func() {
			Method("read", func() {
				WatchableResource("doc", "doc://read", "text/plain")
			})
		}},
		{"prompt", func() {
			StaticPrompt("greeting", "hello", "user", "Hello")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runInvalidMCPDSL(t, func() {
				API("test", func() {
				})
				Service("missing", tc.declare)
			})
			assert.Contains(t, err, "MCP")
		})
	}
}

func TestMCPStaticPromptRejectsUnsupportedArgument(t *testing.T) {
	err := runInvalidMCPDSL(t, func() {
		API("test", func() {
		})
		Service("prompts", func() {
			MCP("prompts", "1")
			StaticPrompt("greeting", "hello", 42)
		})
	})
	assert.Contains(t, err, "string or MCP prompt option")
}
