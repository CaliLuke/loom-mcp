package sdkbridge

import (
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolResultForProtocol preserves text while omitting structured values that
// older clients cannot receive. Never mutate a result retained by the caller.
func toolResultForProtocol(result *mcpsdk.CallToolResult, version string) (*mcpsdk.CallToolResult, error) {
	if result == nil || result.StructuredContent == nil || version >= "2026-07-28" {
		return result, nil
	}
	// Match CallToolResult's SDK wire marshaler, including typed nil containers.
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	if encoded[0] == '{' {
		return result, nil
	}
	projected := *result
	projected.StructuredContent = nil
	return &projected, nil
}
