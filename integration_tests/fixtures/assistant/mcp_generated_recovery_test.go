package assistantapi

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedRecoveryExampleSatisfiesNumericConstraints(t *testing.T) {
	session := connectSDKSessionToServer(t, newGeneratedSDKServerURL(t), nil)
	t.Cleanup(func() {
		require.NoError(t, session.Close())
	})
	catalog, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tt := range []struct {
		name  string
		limit float64
	}{
		{"search", 50},
		{"search_records", 10},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
				Name: tt.name, Arguments: map[string]any{"unknown": true},
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.NotEmpty(t, result.Content)
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			require.True(t, ok)
			_, example, found := strings.Cut(content.Text, "Example: ")
			require.True(t, found, content.Text)
			var arguments map[string]any
			require.NoError(t, json.Unmarshal([]byte(example), &arguments))
			assert.Equal(t, tt.limit, arguments["limit"])

			index := slices.IndexFunc(catalog.Tools, func(tool *sdkmcp.Tool) bool {
				return tool.Name == tt.name
			})
			require.NotEqual(t, -1, index)
			compiler := jsonschema.NewCompiler()
			require.NoError(t, compiler.AddResource("schema.json", catalog.Tools[index].InputSchema))
			schema, err := compiler.Compile("schema.json")
			require.NoError(t, err)
			assert.NoError(t, schema.Validate(arguments))

			hint := mcpassistant.AssistantAssistantMcpToolsetRetryHint(tools.Ident(tt.name), &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid input"})
			require.NotNil(t, hint)
			_, repairExample, found := strings.Cut(hint.Message, "Example params: ")
			require.True(t, found, hint.Message)
			assert.JSONEq(t, example, repairExample)

			retried, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: tt.name, Arguments: arguments})
			require.NoError(t, err)
			assert.False(t, retried.IsError, "%+v", retried.Content)
		})
	}
}
