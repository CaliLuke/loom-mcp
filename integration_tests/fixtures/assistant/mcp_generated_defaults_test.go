package assistantapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	mcpdefaults "example.com/assistant/gen/mcp_payload_defaults"
	defaults "example.com/assistant/gen/payload_defaults"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultSearchService struct {
	mu    sync.Mutex
	calls []*defaults.SearchPayload
}

func TestGeneratedMCPMethodPayloadDefaults(t *testing.T) {
	service := &defaultSearchService{}
	policy := testRuntimeCORSPolicy(t)
	server, err := mcpdefaults.NewSDKServer(service, &mcpdefaults.SDKServerOptions{
		RuntimeCORS: &policy, RequestStateKey: []byte(testRequestStateKey),
	})
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
		zero      bool
	}{
		{"absent defaults", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "choice": map[string]any{"text": "needle"}}, "preference_list": []any{map[string]any{"required_label": "present"}}, "preference_map": map[string]any{"first": map[string]any{"required_label": "present"}}}, true, false},
		{"supplied zeros and empty collection", map[string]any{"query": "test", "weight": 0, "preferences": map[string]any{"required_label": "present", "weight": 0, "enabled": false, "label": "", "tags": []string{}, "nullable_weight": nil, "custom_weight": 0, "choice": map[string]any{"text": "needle", "weight": 0}}}, true, true},
		{"absent optional object", map[string]any{"query": "test"}, true, false},
		{"count union branch", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "choice": map[string]any{"count": 2}}}, true, false},
		{"required default does not admit absence", map[string]any{"query": "test", "preferences": map[string]any{}}, false, false},
		{"nested unknown field", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "unknown": 1}}, false, false},
		{"nested wrong type", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "weight": "bad"}}, false, false},
		{"untagged ambiguous", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "choice": map[string]any{"text": "x", "count": 1}}}, false, false},
		{"untagged no match", map[string]any{"query": "test", "preferences": map[string]any{"required_label": "present", "choice": map[string]any{}}}, false, false},
		{"nonnullable null", map[string]any{"query": "test", "weight": nil}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "search", Arguments: tt.arguments})
			require.NoError(t, err)
			calls := service.takeCalls()
			if !tt.valid {
				assert.True(t, result.IsError)
				assert.Empty(t, calls)
				require.NotEmpty(t, result.Content)
				text, ok := result.Content[0].(*sdkmcp.TextContent)
				require.True(t, ok)
				assert.Contains(t, text.Text, "[invalid_params]")
				return
			}
			assert.False(t, result.IsError)
			require.Len(t, calls, 1)
			payload := calls[0]
			if tt.name == "absent optional object" {
				assert.Nil(t, payload.Preferences)
				assert.Nil(t, payload.PreferenceList)
				assert.Nil(t, payload.PreferenceMap)
				assert.Equal(t, 0.75, payload.Weight)
				return
			}
			require.NotNil(t, payload.Preferences)
			if tt.name == "count union branch" {
				branch, selected := payload.Preferences.Choice.AsSearchCountOption()
				require.True(t, selected)
				assert.Equal(t, 2, branch.Count)
				assert.Equal(t, 5.5, branch.Weight)
				return
			}
			if tt.zero {
				assert.Zero(t, payload.Weight)
				assert.Zero(t, payload.Preferences.Weight)
				assert.False(t, payload.Preferences.Enabled)
				assert.Empty(t, payload.Preferences.Label)
				assert.NotNil(t, payload.Preferences.Tags)
				assert.Empty(t, payload.Preferences.Tags)
				assert.True(t, payload.Preferences.NullableWeight.IsNull())
				assert.Zero(t, payload.Preferences.CustomWeight)
				branch, selected := payload.Preferences.Choice.AsSearchTextOption()
				require.True(t, selected)
				assert.Zero(t, branch.Weight)
				return
			}
			branch, selected := payload.Preferences.Choice.AsSearchTextOption()
			require.True(t, selected)
			assert.Equal(t, 4.5, branch.Weight)
			assert.Equal(t, 0.75, payload.Weight)
			assert.EqualValues(t, 1.5, payload.Preferences.Weight)
			assert.True(t, payload.Preferences.Enabled)
			assert.Equal(t, "standard", payload.Preferences.Label)
			assert.EqualValues(t, []string{"base"}, payload.Preferences.Tags)
			value, ok := payload.Preferences.NullableWeight.Value()
			assert.True(t, ok)
			assert.Equal(t, 2.5, value)
			assert.Equal(t, float32(3.5), payload.Preferences.CustomWeight)
			require.Len(t, payload.PreferenceList, 1)
			assert.EqualValues(t, 1.5, payload.PreferenceList[0].Weight)
			require.Contains(t, payload.PreferenceMap, "first")
			assert.EqualValues(t, 1.5, payload.PreferenceMap["first"].Weight)
		})
	}
}

func (s *defaultSearchService) Search(_ context.Context, payload *defaults.SearchPayload) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, payload)
	return "matched", nil
}

func (s *defaultSearchService) takeCalls() []*defaults.SearchPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := s.calls
	s.calls = nil
	return calls
}
