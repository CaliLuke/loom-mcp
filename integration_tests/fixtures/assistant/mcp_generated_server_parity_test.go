package assistantapi

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedAdapterAgainstGeneratedServerReturnsRetryPrompt(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	out, err := newAdapterEndpoints(t, server).ExecuteCode(context.Background(), invalidExecuteCodePayload())
	require.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Redo the operation now with valid parameters.")
	assert.Contains(t, err.Error(), `"enum":["python","javascript"]`)
}

func TestGeneratedCallerMatchesRuntimeHTTPCaller(t *testing.T) {
	t.Parallel()

	jsonrpcServer := newGeneratedJSONRPCServer(t)
	defer jsonrpcServer.Close()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	generatedCaller := newGeneratedCallerFromServer(t, jsonrpcServer.URL)
	runtimeSession := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", nil)
	runtimeCaller := mcpruntime.NewSessionCaller(runtimeSession, nil)
	defer func() {
		require.NoError(t, runtimeCaller.Close())
	}()

	req := mcpruntime.CallRequest{
		Tool:    "analyze_sentiment",
		Payload: json.RawMessage(`{"text":"I love parity checks"}`),
	}

	generatedResp, err := generatedCaller.CallTool(context.Background(), req)
	require.NoError(t, err)

	runtimeResp, err := runtimeCaller.CallTool(context.Background(), req)
	require.NoError(t, err)

	require.JSONEq(t, string(generatedResp.Result), string(runtimeResp.Result))
	require.JSONEq(t, string(generatedResp.Structured), string(runtimeResp.Structured))
}

func TestGeneratedSDKServerAdvertisesUnionToolSchema(t *testing.T) {
	t.Parallel()

	session := connectSDKSessionToServer(t, newGeneratedSDKServerURL(t), nil)
	defer func() {
		require.NoError(t, session.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	toolsResult, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	idx := slices.IndexFunc(toolsResult.Tools, func(tool *sdkmcp.Tool) bool {
		return tool != nil && tool.Name == "dispatch_action"
	})
	require.NotEqual(t, -1, idx, "dispatch_action tool must be listed")

	tool := toolsResult.Tools[idx]
	schema, ok := tool.InputSchema.(map[string]any)
	require.True(t, ok, "tool input schema must decode to a JSON object")

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "tool schema must advertise top-level properties")

	requestSchema, ok := properties["request"].(map[string]any)
	require.True(t, ok, "union field schema must be present")
	assert.Equal(t, "object", requestSchema["type"])

	discriminator, ok := requestSchema["discriminator"].(map[string]any)
	require.True(t, ok, "union schema must advertise a discriminator")
	assert.Equal(t, "action", discriminator["propertyName"])

	oneOf, ok := requestSchema["oneOf"].([]any)
	require.True(t, ok, "union schema must advertise variants")
	require.Len(t, oneOf, 2)

	actions := make(map[string]map[string]any, len(oneOf))
	for _, variant := range oneOf {
		variantSchema, ok := variant.(map[string]any)
		require.True(t, ok)
		variantProperties, ok := variantSchema["properties"].(map[string]any)
		require.True(t, ok)
		actionSchema, ok := variantProperties["action"].(map[string]any)
		require.True(t, ok)
		enumValues, ok := actionSchema["enum"].([]any)
		require.True(t, ok)
		require.Len(t, enumValues, 1)
		actionName, ok := enumValues[0].(string)
		require.True(t, ok)
		actions[actionName] = variantSchema
	}

	listVariant, ok := actions["ListAction"]
	require.True(t, ok, "ListAction variant must be advertised")
	createVariant, ok := actions["CreateAction"]
	require.True(t, ok, "CreateAction variant must be advertised")

	listProperties := listVariant["properties"].(map[string]any)
	listValueSchema := listProperties["value"].(map[string]any)
	listValueProperties := listValueSchema["properties"].(map[string]any)
	assert.Contains(t, listValueProperties, "limit")

	createProperties := createVariant["properties"].(map[string]any)
	createValueSchema := createProperties["value"].(map[string]any)
	createValueProperties := createValueSchema["properties"].(map[string]any)
	assert.Contains(t, createValueProperties, "name")
	assert.Equal(t, []any{"name"}, createValueSchema["required"])
}

func newGeneratedSDKServerURL(t *testing.T) string {
	t.Helper()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	t.Cleanup(sdkHTTPServer.Close)
	return sdkHTTPServer.URL + "/rpc"
}
