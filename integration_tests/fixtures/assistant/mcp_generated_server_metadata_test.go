package assistantapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerExposesSEP973Metadata(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	session := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", nil)
	defer func() {
		require.NoError(t, session.Close())
	}()

	initResult := session.InitializeResult()
	require.NotNil(t, initResult)
	require.NotNil(t, initResult.ServerInfo)
	require.Equal(t, "https://assistant.example.com/docs", initResult.ServerInfo.WebsiteURL)
	require.Len(t, initResult.ServerInfo.Icons, 2)
	assert.Equal(t, "https://assistant.example.com/icons/server-light.png", initResult.ServerInfo.Icons[0].Source)
	assert.Equal(t, sdkmcp.IconThemeLight, initResult.ServerInfo.Icons[0].Theme)
	assert.Equal(t, []string{"48x48"}, initResult.ServerInfo.Icons[0].Sizes)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	tool := findToolByName(t, tools.Tools, "analyze_sentiment")
	require.Len(t, tool.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/analyze-sentiment.png", tool.Icons[0].Source)

	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	resource := findResourceByURI(t, resources.Resources, "doc://list")
	require.Len(t, resource.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/documents.png", resource.Icons[0].Source)

	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	staticPrompt := findPromptByName(t, prompts.Prompts, "code_review")
	require.Len(t, staticPrompt.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/code-review.svg", staticPrompt.Icons[0].Source)

	dynamicPrompt := findPromptByName(t, prompts.Prompts, "contextual_prompts")
	require.Len(t, dynamicPrompt.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/contextual-prompts.png", dynamicPrompt.Icons[0].Source)
}

func TestGeneratedJSONRPCServerExposesSEP973MetadataOnWire(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, initResult := rawInitializeResult(t, ctx, server.URL)
	require.NotEmpty(t, sessionID)

	serverInfo := nestedMap(t, initResult, "serverInfo")
	assert.Equal(t, "https://assistant.example.com/docs", serverInfo["websiteUrl"])
	serverIcons := nestedSlice(t, serverInfo, "icons")
	require.Len(t, serverIcons, 2)

	toolsResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "tools/list", map[string]any{})
	tools := nestedSlice(t, toolsResult, "tools")
	tool := findMapByStringField(t, tools, "name", "analyze_sentiment")
	require.Len(t, nestedSlice(t, tool, "icons"), 1)

	resourcesResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "resources/list", map[string]any{})
	resources := nestedSlice(t, resourcesResult, "resources")
	resource := findMapByStringField(t, resources, "uri", "doc://list")
	require.Len(t, nestedSlice(t, resource, "icons"), 1)

	promptsResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "prompts/list", map[string]any{})
	prompts := nestedSlice(t, promptsResult, "prompts")
	prompt := findMapByStringField(t, prompts, "name", "code_review")
	require.Len(t, nestedSlice(t, prompt, "icons"), 1)
}

func rawInitializeResult(t *testing.T, ctx context.Context, rawURL string) (string, map[string]any) {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "init-1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo": map[string]any{
				"name":       "metadata-test-client",
				"version":    "1.0.0",
				"websiteUrl": "https://client.example.com",
				"icons": []map[string]any{
					{
						"src":      "https://client.example.com/icons/client.png",
						"mimeType": "image/png",
						"sizes":    []string{"48x48"},
					},
				},
			},
		},
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL+"/rpc", strings.NewReader(string(body)))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	sessionID := resp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sessionID)

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	return sessionID, envelope.Result
}

func rawJSONRPCResult(t *testing.T, ctx context.Context, endpoint, sessionID, method string, params map[string]any) map[string]any {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      method + "-1",
		"method":  method,
		"params":  params,
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	return envelope.Result
}

func findToolByName(t *testing.T, tools []*sdkmcp.Tool, name string) *sdkmcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool != nil && tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func findResourceByURI(t *testing.T, resources []*sdkmcp.Resource, uri string) *sdkmcp.Resource {
	t.Helper()
	for _, resource := range resources {
		if resource != nil && resource.URI == uri {
			return resource
		}
	}
	t.Fatalf("resource %q not found", uri)
	return nil
}

func findPromptByName(t *testing.T, prompts []*sdkmcp.Prompt, name string) *sdkmcp.Prompt {
	t.Helper()
	for _, prompt := range prompts {
		if prompt != nil && prompt.Name == name {
			return prompt
		}
	}
	t.Fatalf("prompt %q not found", name)
	return nil
}

func nestedMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := m[key].(map[string]any)
	require.Truef(t, ok, "expected map at key %q, got %T", key, m[key])
	return value
}

func nestedSlice(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	value, ok := m[key].([]any)
	require.Truef(t, ok, "expected slice at key %q, got %T", key, m[key])
	return value
}

func findMapByStringField(t *testing.T, values []any, field, want string) map[string]any {
	t.Helper()
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := entry[field].(string); got == want {
			return entry
		}
	}
	t.Fatalf("entry with %s=%q not found", field, want)
	return nil
}
