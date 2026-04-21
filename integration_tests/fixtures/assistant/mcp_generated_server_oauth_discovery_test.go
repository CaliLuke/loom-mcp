package assistantapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerServesProtectedResourceMetadataAtPathSuffixedURL(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	doc := fetchOAuthMetadata(t, sdkHTTPServer.URL+"/.well-known/oauth-protected-resource/rpc")

	assert.Equal(t, "https://api.example.com/mcp", doc["resource"])
	assert.Equal(t, []any{"https://auth.example.com"}, doc["authorization_servers"])
	assert.Equal(t, []any{"read", "write"}, doc["scopes_supported"])
	assert.Equal(t, []any{"header"}, doc["bearer_methods_supported"])
	assert.Equal(t, "https://docs.example.com/mcp-auth", doc["resource_documentation"])
}

func TestGeneratedSDKServerServesProtectedResourceMetadataAtRootAlias(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	root := fetchOAuthMetadata(t, sdkHTTPServer.URL+"/.well-known/oauth-protected-resource")
	suffixed := fetchOAuthMetadata(t, sdkHTTPServer.URL+"/.well-known/oauth-protected-resource/rpc")

	assert.Equal(t, root, suffixed, "root alias and path-suffixed metadata must return the same document")
}

func TestGeneratedSDKServerProtectedResourceMetadataSetsCacheHeaders(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		sdkHTTPServer.URL+"/.well-known/oauth-protected-resource/rpc",
		nil,
	)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "max-age=3600", resp.Header.Get("Cache-Control"))
}

func fetchOAuthMetadata(t *testing.T, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	require.Equal(t, http.StatusOK, resp.StatusCode, "metadata endpoint must return 200")

	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	return doc
}
