package assistantapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errorOwnershipService struct {
	assistant.Service
	err error
}

func TestGeneratedMCPErrorResponseOwnership(t *testing.T) {
	missing := loom.WithErrorRemedy(loom.PermanentError("resource_not_found", "private missing"), &loom.ErrorRemedy{
		Code: "resource_not_found", SafeMessage: "Branch missing.", RetryHint: "branch-only recovery",
	})
	other := loom.WithErrorRemedy(loom.PermanentError("internal_error", "private other"), &loom.ErrorRemedy{
		Code: "internal_error", SafeMessage: "Other branch.", RetryHint: "other-only recovery",
	})
	for _, tt := range []struct {
		name         string
		err          error
		toolText     string
		resourceCode int64
	}{
		{"joined", errors.Join(missing, other), "[internal_error] internal server error", jsonrpc.CodeInternalError},
		{"reversed", errors.Join(other, missing), "[internal_error] internal server error", jsonrpc.CodeInternalError},
		{"wrapped", fmt.Errorf("private wrapper: %w", missing), "[resource_not_found] Branch missing.\nRecovery: branch-only recovery", jsonrpc.CodeInvalidParams},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &errorOwnershipService{Service: NewAssistant(), err: tt.err}
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
			result, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "test"}})
			require.NoError(t, err)
			assert.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			require.True(t, ok)
			assert.Equal(t, tt.toolText, content.Text)
			_, err = session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: "doc://list"})
			var rpcErr *jsonrpc.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, tt.resourceCode, rpcErr.Code)
			assert.NotContains(t, rpcErr.Message, "private")
			if tt.resourceCode == jsonrpc.CodeInternalError {
				assert.NotContains(t, rpcErr.Message, "Branch")
				assert.NotContains(t, rpcErr.Message, "branch")
			}
		})
	}
}

func (s *errorOwnershipService) Search(context.Context, *assistant.SearchPayload) (*assistant.SearchResult, error) {
	return nil, s.err
}

func (s *errorOwnershipService) ListDocuments(context.Context) (*assistant.Documents, error) {
	return nil, s.err
}
