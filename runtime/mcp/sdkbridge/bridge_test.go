package sdkbridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServerRejectsGeneratedRuntimeVersionMismatch(t *testing.T) {
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion + 1,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
	})

	require.ErrorContains(t, err, "MCP SDK bridge compatibility mismatch: generated version 2, runtime version 1")
	assert.Nil(t, server)
}

func TestNewServerRejectsInvalidSessionBeforeSDKDispatch(t *testing.T) {
	errInvalidSession := errors.New("invalid session")
	asserted := false
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Sessions: SessionHooks{
			AssertPrincipal: func(_ context.Context, sessionID string) error {
				asserted = true
				assert.Equal(t, "missing", sessionID)
				return errInvalidSession
			},
			IsInvalidSessionID: func(err error) bool {
				return errors.Is(err, errInvalidSession)
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
	req.Header.Set(mcpruntime.HeaderKeySessionID, "missing")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.True(t, asserted)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "invalid session")
}

func TestNewServerReportsGeneratedDescriptorFailure(t *testing.T) {
	descriptorErr := errors.New("catalog unavailable")
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Tools: func() ([]ToolBinding, error) {
			return nil, descriptorErr
		},
	})

	require.ErrorContains(t, err, "load MCP SDK tool bindings: catalog unavailable")
	assert.Nil(t, server)
}

func TestToolHandlerAcceptsNilSDKRequest(t *testing.T) {
	called := false
	handler := ToolHandler(HandlerContext{}, func(ctx context.Context, request ToolRequest) (*mcpsdk.CallToolResult, error) {
		called = true
		assert.Empty(t, request.Name)
		assert.Empty(t, request.Arguments)
		assert.NotNil(t, request.Bind(nil))
		return &mcpsdk.CallToolResult{}, nil
	})

	result, err := handler(context.Background(), nil)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, called)
}

func TestToolHandlerPreservesProgressTokenAfterPayloadBinding(t *testing.T) {
	handler := ToolHandler(HandlerContext{}, func(ctx context.Context, request ToolRequest) (*mcpsdk.CallToolResult, error) {
		bound := request.Bind(struct{}{})
		token, ok := mcpruntime.ProgressTokenFromContext(bound)
		require.True(t, ok)
		assert.Equal(t, "progress-1", token)
		return &mcpsdk.CallToolResult{}, nil
	})
	req := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Meta: mcpsdk.Meta{"progressToken": "progress-1"}}}

	_, err := handler(context.Background(), req)

	require.NoError(t, err)
}
