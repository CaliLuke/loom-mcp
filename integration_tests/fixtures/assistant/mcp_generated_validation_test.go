package assistantapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type validatingSearchService struct {
	assistant.Service
	mu    sync.Mutex
	calls []*assistant.SearchPayload
}

func TestGeneratedSDKServerValidatesNumericArgumentsBeforeDispatch(t *testing.T) {
	service := &validatingSearchService{Service: NewAssistant()}
	server, err := mcpassistant.NewSDKServer(service, withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{}, RequestStateKey: []byte(testRequestStateKey),
	}))
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler)
	t.Cleanup(httpServer.Close)
	session := connectSDKSessionToServer(t, httpServer.URL, nil)
	t.Cleanup(func() {
		require.NoError(t, session.Close())
	})

	for _, tt := range []struct {
		name      string
		arguments map[string]any
		valid     bool
		wantLimit int
	}{
		{"omitted", map[string]any{"query": "test"}, true, 50},
		{"zero", map[string]any{"query": "test", "limit": 0}, false, 0},
		{"negative", map[string]any{"query": "test", "limit": -1}, false, 0},
		{"above maximum", map[string]any{"query": "test", "limit": 201}, false, 0},
		{"null", map[string]any{"query": "test", "limit": nil}, false, 0},
		{"minimum", map[string]any{"query": "test", "limit": 1}, true, 1},
		{"maximum", map[string]any{"query": "test", "limit": 200}, true, 200},
		{"exclusive minimum", map[string]any{"query": "test", "ratio": 0}, false, 0},
		{"exclusive maximum", map[string]any{"query": "test", "ratio": 1}, false, 0},
		{"fractional value", map[string]any{"query": "test", "ratio": 0.5}, true, 50},
		{"optional nonnullable", map[string]any{"query": "test", "ratio": nil}, false, 0},
		{"explicit nullable", map[string]any{"query": "test", "nullable_limit": nil}, true, 50},
		{"nullable below minimum", map[string]any{"query": "test", "nullable_limit": 0}, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "search", Arguments: tt.arguments})
			require.NoError(t, err)
			calls := service.takeCalls()
			if tt.valid {
				assert.False(t, result.IsError)
				require.Len(t, calls, 1)
				assert.Equal(t, tt.wantLimit, calls[0].Limit)
				return
			}
			assert.True(t, result.IsError)
			assert.Empty(t, calls, "invalid input must not invoke the service")
			require.NotEmpty(t, result.Content)
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			require.True(t, ok)
			assert.Contains(t, content.Text, "[invalid_params]")
		})
	}
}

func (s *validatingSearchService) Search(_ context.Context, payload *assistant.SearchPayload) (*assistant.SearchResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, payload)
	s.mu.Unlock()
	return &assistant.SearchResult{Results: []string{"matched"}}, nil
}

func (s *validatingSearchService) takeCalls() []*assistant.SearchPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := s.calls
	s.calls = nil
	return calls
}
