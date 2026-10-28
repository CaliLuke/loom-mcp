package sdkbridge

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolHandlerPreservesSharedStructuredResult(t *testing.T) {
	for _, tc := range []struct {
		name       string
		structured any
	}{
		{"array", jsontext.Value(`[{"id":"member"}]`)},
		{"nil map", map[string]any(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			structured := tc.structured
			retained := &mcpsdk.CallToolResult{
				Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: "complete result"}},
				StructuredContent: structured,
			}
			handler := ToolHandler(HandlerContext{}, func(context.Context, ToolRequest) (*mcpsdk.CallToolResult, error) {
				return retained, nil
			})
			for _, version := range []string{"2025-11-25", "2026-07-28"} {
				t.Run(version, func(t *testing.T) {
					result, err := handler(t.Context(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{
						Meta: mcpsdk.Meta{mcpsdk.MetaKeyProtocolVersion: version},
					}})
					require.NoError(t, err)
					require.NotNil(t, result)
					assert.Equal(t, retained.Content, result.Content)
					if version == "2025-11-25" {
						encoded, err := json.Marshal(result)
						require.NoError(t, err)
						var wire map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(encoded, &wire))
						assert.NotContains(t, wire, "structuredContent")
					} else {
						assert.Equal(t, structured, result.StructuredContent)
					}
					assert.Equal(t, structured, retained.StructuredContent)
				})
			}
		})
	}
}
