package assistantapi

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type validatingProjectedService struct {
	assistant.Service
	calls atomic.Int32
}

func TestProjectedMCPToolValidatesBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"direct", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			service := &validatingProjectedService{Service: NewAssistant()}
			opts := &mcpassistant.SDKServerOptions{PromptProvider: promptProvider{}, RequestStateKey: []byte(testRequestStateKey)}
			if mode == "proxy" {
				opts.Adapter = &mcpassistant.MCPAdapterOptions{ToolSearch: &mcpassistant.ToolSearchOptions{}}
			}
			server, err := mcpassistant.NewSDKServer(service, withTestRuntimeCORS(t, opts))
			require.NoError(t, err)
			httpServer := httptest.NewServer(server.Handler)
			t.Cleanup(httpServer.Close)
			session := connectSDKSessionToServer(t, httpServer.URL, nil)
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			for _, tt := range []struct {
				name      string
				arguments map[string]any
				valid     bool
			}{
				{name: "unknown field", arguments: map[string]any{"query": "test", "unexpected": 1}},
				{name: "missing required", arguments: map[string]any{}},
				{name: "wrong type", arguments: map[string]any{"query": 1}},
				{name: "omitted optional fields", arguments: map[string]any{"query": "test"}, valid: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					params := &sdkmcp.CallToolParams{Name: "projected_lookup_tool", Arguments: tt.arguments}
					if mode == "proxy" {
						params.Name = "call_tool"
						params.Arguments = map[string]any{"name": "projected_lookup_tool", "arguments": tt.arguments}
					}
					result, err := session.CallTool(t.Context(), params)
					require.NoError(t, err)
					assert.Equal(t, !tt.valid, result.IsError)
					calls := service.calls.Swap(0)
					if tt.valid {
						assert.Equal(t, int32(1), calls)
					} else {
						assert.Zero(t, calls, "invalid input must not reach the service")
					}
				})
			}
		})
	}
}

func (s *validatingProjectedService) ProjectedLookup(ctx context.Context, payload *assistant.ProjectedLookupPayload) (*assistant.ProjectedLookupResult, error) {
	s.calls.Add(1)
	return s.Service.ProjectedLookup(ctx, payload)
}
