package assistantapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mcpjsonrpcserver "example.com/assistant/gen/jsonrpc/mcp_assistant/server"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
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
	skillResource := findResourceByURI(t, resources.Resources, "skill://code-review/SKILL.md")
	assert.Equal(t, "code-review", skillResource.Name)
	assert.Equal(t, "Review code changes for correctness and maintainability.", skillResource.Description)
	assert.Equal(t, "code-review", nestedSDKMetaString(t, skillResource.Meta, "skill", "id"))

	skillContent, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "skill://code-review/SKILL.md"})
	require.NoError(t, err)
	require.Len(t, skillContent.Contents, 1)
	require.Contains(t, skillContent.Contents[0].Text, "# Code Review")
	assert.Equal(t, "code-review", nestedSDKMetaString(t, skillContent.Contents[0].Meta, "skill", "id"))

	manifestContent, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "skill://code-review/_manifest"})
	require.NoError(t, err)
	require.Len(t, manifestContent.Contents, 1)
	require.Contains(t, manifestContent.Contents[0].Text, `"path":"SKILL.md"`)
	require.Contains(t, manifestContent.Contents[0].Text, `"path":"reference.md"`)

	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	staticPrompt := findPromptByName(t, prompts.Prompts, "code_review")
	require.Len(t, staticPrompt.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/code-review.svg", staticPrompt.Icons[0].Source)

	dynamicPrompt := findPromptByName(t, prompts.Prompts, "contextual_prompts")
	require.Len(t, dynamicPrompt.Icons, 1)
	assert.Equal(t, "https://assistant.example.com/icons/contextual-prompts.png", dynamicPrompt.Icons[0].Source)
}

func TestGeneratedAdapterEnforcesSkillResourcePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      *mcpassistant.MCPAdapterOptions
		configure func(context.Context) context.Context
		wantError bool
	}{
		{
			name:      "unlisted skill URI is denied",
			opts:      &mcpassistant.MCPAdapterOptions{AllowedResourceURIs: []string{"doc://list"}},
			wantError: true,
		},
		{
			name: "skill URI prefix allows supporting files",
			opts: &mcpassistant.MCPAdapterOptions{AllowedResourceURIs: []string{"skill://code-review/"}},
		},
		{
			name: "adapter skill name allows supporting files",
			opts: &mcpassistant.MCPAdapterOptions{AllowedResourceNames: []string{"code-review"}},
		},
		{
			name:      "adapter skill name deny takes precedence",
			opts:      &mcpassistant.MCPAdapterOptions{DeniedResourceNames: []string{"code-review"}},
			wantError: true,
		},
		{
			name: "request skill name allows supporting files",
			configure: func(ctx context.Context) context.Context {
				return mcpruntime.WithAllowedResourceNames(ctx, "code-review")
			},
		},
		{
			name: "unknown request allow name denies skill",
			configure: func(ctx context.Context) context.Context {
				return mcpruntime.WithAllowedResourceNames(ctx, "documents")
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, test.opts)
			ctx := context.Background()
			if test.configure != nil {
				ctx = test.configure(ctx)
			}
			_, err := adapter.Initialize(ctx, &mcpassistant.InitializePayload{ProtocolVersion: "2025-06-18"})
			require.NoError(t, err)

			result, err := adapter.ResourcesRead(ctx, &mcpassistant.ResourcesReadPayload{URI: "skill://code-review/reference.md"})
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.Contents, 1)
			assert.Equal(t, "skill://code-review/reference.md", result.Contents[0].URI)
		})
	}
}

func TestGeneratedSDKServerTrimsResourcePolicyHeaderNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		headers   map[string]string
		wantError bool
	}{
		{
			name:    "allow list resolves spaced name",
			headers: map[string]string{"x-mcp-allow-names": "documents, code-review"},
		},
		{
			name:      "deny list resolves spaced name",
			headers:   map[string]string{"x-mcp-deny-names": "documents, code-review"},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, sdkHTTPServer := newGeneratedSDKServer(t)
			defer sdkHTTPServer.Close()

			session := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", test.headers)
			defer func() {
				require.NoError(t, session.Close())
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "skill://code-review/SKILL.md"})
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.Contents, 1)
			assert.Equal(t, "skill://code-review/SKILL.md", result.Contents[0].URI)
		})
	}
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

	capabilities := nestedMap(t, initResult, "capabilities")
	experimental := nestedMap(t, capabilities, "experimental")
	loomMCP := nestedMap(t, experimental, "loom-mcp")
	events := nestedMap(t, loomMCP, "events")
	assert.Equal(t, true, events["stream"])
	assert.Equal(t, "events/stream", events["method"])
	assert.Equal(t, []any{"notify_status_update"}, nestedSlice(t, events, "notifications"))

	toolsResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "tools/list", map[string]any{})
	tools := nestedSlice(t, toolsResult, "tools")
	tool := findMapByStringField(t, tools, "name", "analyze_sentiment")
	require.Len(t, nestedSlice(t, tool, "icons"), 1)

	resourcesResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "resources/list", map[string]any{})
	resources := nestedSlice(t, resourcesResult, "resources")
	resource := findMapByStringField(t, resources, "uri", "doc://list")
	require.Len(t, nestedSlice(t, resource, "icons"), 1)
	skillResource := findMapByStringField(t, resources, "uri", "skill://code-review/SKILL.md")
	assert.Equal(t, "code-review", skillResource["name"])
	assert.Equal(t, "Review code changes for correctness and maintainability.", skillResource["description"])
	assert.Equal(t, "code-review", nestedMap(t, nestedMap(t, skillResource, "_meta"), "skill")["id"])

	skillReadResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "resources/read", map[string]any{
		"uri": "skill://code-review/reference.md",
	})
	contents := nestedSlice(t, skillReadResult, "contents")
	require.Len(t, contents, 1)
	content, ok := contents[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "skill://code-review/reference.md", content["uri"])
	assert.Contains(t, content["text"], "Prioritize concrete bugs")
	assert.Equal(t, "code-review", nestedMap(t, nestedMap(t, content, "_meta"), "skill")["id"])

	promptsResult := rawJSONRPCResult(t, ctx, server.URL+"/rpc", sessionID, "prompts/list", map[string]any{})
	prompts := nestedSlice(t, promptsResult, "prompts")
	prompt := findMapByStringField(t, prompts, "name", "code_review")
	require.Len(t, nestedSlice(t, prompt, "icons"), 1)
}

func TestGeneratedJSONRPCServerValidatesProtocolVersionHeader(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, _ := rawInitializeResult(t, ctx, server.URL)
	require.NotEmpty(t, sessionID)

	for _, tc := range []struct {
		name    string
		header  string
		status  int
		message string
	}{
		{
			name:   "missing uses compatibility fallback",
			status: http.StatusOK,
		},
		{
			name:    "unsupported",
			header:  "2099-01-01",
			status:  http.StatusBadRequest,
			message: `Unsupported MCP-Protocol-Version header "2099-01-01"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      tc.name + "-1",
				"method":  "tools/list",
				"params":  map[string]any{},
			})
			require.NoError(t, err)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/rpc", strings.NewReader(string(body)))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
			if tc.header != "" {
				req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, tc.header)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tc.status, resp.StatusCode)
			if tc.status == http.StatusOK {
				return
			}

			var envelope struct {
				ID    json.RawMessage `json:"id"`
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
			require.JSONEq(t, `"`+tc.name+`-1"`, string(envelope.ID),
				"MCP protocol-version errors must echo the readable JSON-RPC request ID")
			assert.Equal(t, -32602, envelope.Error.Code)
			assert.Equal(t, tc.message, envelope.Error.Message)
		})
	}
}

func TestGeneratedJSONRPCServerRejectsOversizedRequestBody(t *testing.T) {
	previousLimit := mcpjsonrpcserver.MCPMaxRequestBodyBytes
	mcpjsonrpcserver.MCPMaxRequestBodyBytes = 256
	t.Cleanup(func() {
		mcpjsonrpcserver.MCPMaxRequestBodyBytes = previousLimit
	})

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "oversized-1",
		"method":  "tools/list",
		"params":  map[string]any{"padding": strings.Repeat("x", 512)},
	})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/rpc", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)

	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(responseBody), "request body too large")
}

func TestGeneratedJSONRPCServerAcceptsNotificationsAndResponses(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, _ := rawInitializeResult(t, ctx, server.URL)
	require.NotEmpty(t, sessionID)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{
			name: "initialized notification",
			body: map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/initialized",
			},
		},
		{
			name: "cancelled notification",
			body: map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/cancelled",
				"params": map[string]any{
					"requestId": "tool-1",
					"reason":    "client cancelled",
				},
			},
		},
		{
			name: "response",
			body: map[string]any{
				"jsonrpc": "2.0",
				"id":      "server-request-1",
				"result":  map[string]any{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.body)
			require.NoError(t, err)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/rpc", strings.NewReader(string(body)))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
			req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusAccepted, resp.StatusCode)

			data, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Empty(t, data)
		})
	}
}

func TestGeneratedJSONRPCServerCancellationStopsMatchingRequest(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	canceled := make(chan error, 1)
	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolCallInterceptors: []mcpassistant.ToolCallInterceptor{
			func(ctx context.Context, _ mcpassistant.ToolCallInterceptorInfo, _ *mcpassistant.ToolsCallPayload, _ mcpassistant.ToolsCallServerStream, _ mcpassistant.ToolCallHandler) (bool, error) {
				close(started)
				<-ctx.Done()
				canceled <- ctx.Err()
				return true, ctx.Err()
			},
		},
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, _ := rawInitializeResult(t, ctx, server.URL)
	require.NotEmpty(t, sessionID)

	toolBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "search_records",
			"arguments": map[string]any{},
		},
	})
	require.NoError(t, err)
	toolReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/rpc", strings.NewReader(string(toolBody)))
	require.NoError(t, err)
	toolReq.Header.Set("Accept", "application/json, text/event-stream")
	toolReq.Header.Set("Content-Type", "application/json")
	toolReq.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	toolReq.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

	toolDone := make(chan error, 1)
	go func() {
		resp, doErr := http.DefaultClient.Do(toolReq)
		if doErr == nil {
			_, readErr := io.ReadAll(resp.Body)
			closeErr := resp.Body.Close()
			doErr = readErr
			if doErr == nil {
				doErr = closeErr
			}
		}
		toolDone <- doErr
	}()

	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("timed out waiting for tools/call to start")
	}

	cancelBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params": map[string]any{
			"requestId": 7,
			"reason":    "client cancelled",
		},
	})
	require.NoError(t, err)
	cancelReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/rpc", strings.NewReader(string(cancelBody)))
	require.NoError(t, err)
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelReq.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	cancelReq.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

	cancelResp, err := http.DefaultClient.Do(cancelReq)
	require.NoError(t, err)
	defer cancelResp.Body.Close()
	require.Equal(t, http.StatusAccepted, cancelResp.StatusCode)

	select {
	case err := <-canceled:
		assert.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("timed out waiting for notifications/cancelled to cancel tools/call")
	}

	select {
	case err := <-toolDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for cancelled tools/call to return")
	}
}

func TestGeneratedJSONRPCServerEnforcesStreamableHTTPSessions(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	request := func(method string, sessionID string) (int, string) {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"jsonrpc":"2.0","id":"list-1","method":"tools/list","params":{}}`)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+"/rpc", body)
		require.NoError(t, err)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		if sessionID != "" {
			req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
			req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(responseBody)
	}

	status, body := request(http.MethodPost, "stale-after-restart")
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, body, "Invalid or expired session ID")

	sessionID, _ := rawInitializeResult(t, ctx, server.URL)
	require.NotEmpty(t, sessionID)

	status, _ = request(http.MethodPost, sessionID)
	assert.Equal(t, http.StatusOK, status)

	status, body = request(http.MethodPost, "")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, "Missing session ID")

	status, _ = request(http.MethodDelete, sessionID)
	assert.Equal(t, http.StatusOK, status)

	status, body = request(http.MethodPost, sessionID)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, body, "Invalid or expired session ID")

	status, _ = request(http.MethodDelete, sessionID)
	assert.Equal(t, http.StatusNotFound, status)
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
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

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

func nestedSDKMetaString(t *testing.T, meta sdkmcp.Meta, keys ...string) string {
	t.Helper()
	var current any = map[string]any(meta)
	for _, key := range keys {
		m, ok := current.(map[string]any)
		require.True(t, ok, "expected nested meta map at %q", key)
		current = m[key]
	}
	value, ok := current.(string)
	require.True(t, ok)
	return value
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
