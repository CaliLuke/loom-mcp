package assistantapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mcpruntime "goa.design/goa-ai/runtime/mcp"
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
