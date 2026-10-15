package executor

import (
	"encoding/json"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorReturnsDecodedResultAndPrivateSidecar(t *testing.T) {
	total := 7
	message := toolregistry.NewToolResultMessage(testRegistrationTokenA, "use", json.RawMessage(`{"value":42}`))
	message.Bounds = &agent.Bounds{Returned: 1, Total: &total, Truncated: true}
	message.ServerData = []*toolregistry.ServerDataItem{{Kind: "audit", Audience: "server", Data: json.RawMessage(`{"source":"db"}`)}}
	spec := &tools.ToolSpec{Name: "docs.read", Toolset: "docs", Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
	out, err := executeRegistryResultMessage(t, "use", "call", message, spec)
	require.NoError(t, err)
	require.Nil(t, out.ToolResult.Error)
	assert.Equal(t, map[string]any{"value": float64(42)}, out.ToolResult.Result)
	require.NotNil(t, out.ToolResult.Bounds)
	assert.Equal(t, 7, *out.ToolResult.Bounds.Total)
	assert.JSONEq(t, `[{"kind":"audit","audience":"server","data":{"source":"db"}}]`, string(out.ToolResult.ServerData))
}

func TestExecutorResultCodecFailureDropsBoundsAndPrivateData(t *testing.T) {
	message := toolregistry.NewToolResultMessage(testRegistrationTokenA, "use", json.RawMessage(`{"value":42}`))
	message.Bounds = &agent.Bounds{Returned: 1}
	message.ServerData = []*toolregistry.ServerDataItem{{Kind: "audit", Audience: "server", Data: json.RawMessage(`{}`)}}
	codec := tools.AnyJSONCodec
	codec.FromJSON = func([]byte) (any, error) {
		return nil, assert.AnError
	}
	out, err := executeRegistryResultMessage(t, "use", "call", message, &tools.ToolSpec{Name: "docs.read", Toolset: "docs", Result: tools.TypeSpec{Codec: codec}})
	require.NoError(t, err)
	require.NotNil(t, out.ToolResult.Error)
	assert.Contains(t, out.ToolResult.Error.Message, "registered schema")
	assert.Nil(t, out.ToolResult.Bounds)
	assert.Empty(t, out.ToolResult.ServerData)
	assert.Nil(t, out.ToolResult.Result)
}

func TestExecutorMapsTerminalRegistryErrorsToRetryGuidance(t *testing.T) {
	for _, tc := range []struct {
		code   string
		reason planner.RetryReason
	}{
		{toolregistry.ToolErrorCodeStaleRegistration, planner.RetryReasonToolUnavailable},
		{"service_unavailable", planner.RetryReasonToolUnavailable},
		{"rate_limited", planner.RetryReasonRateLimited},
		{"timeout", planner.RetryReasonTimeout},
		{"application_failure", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			message := toolregistry.ToolResultMessage{RegistrationToken: testRegistrationTokenA, ToolUseID: "use", Error: &toolregistry.ToolError{Code: tc.code, Message: "try an appropriate recovery"}}
			out, err := executeRegistryResultMessage(t, "use", "call", message, &tools.ToolSpec{Name: "docs.read", Toolset: "docs"})
			require.NoError(t, err)
			require.NotNil(t, out.ToolResult.Error)
			assert.Equal(t, "try an appropriate recovery", out.ToolResult.Error.Message)
			if tc.reason == "" {
				assert.Nil(t, out.ToolResult.RetryHint)
				return
			}
			require.NotNil(t, out.ToolResult.RetryHint)
			assert.Equal(t, tc.reason, out.ToolResult.RetryHint.Reason)
			assert.Equal(t, tools.Ident("docs.read"), out.ToolResult.RetryHint.Tool)
		})
	}
}
