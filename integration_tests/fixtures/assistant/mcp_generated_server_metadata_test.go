package assistantapi

import (
	"context"
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
