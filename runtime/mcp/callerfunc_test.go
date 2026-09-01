package mcp

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallerFuncDelegatesRequestContextResponseAndError(t *testing.T) {
	type contextKey string
	wantErr := errors.New("call failed")
	wantResponse := CallResponse{Result: jsontext.Value(`{"ok":true}`)}
	wantRequest := CallRequest{Tool: "search"}
	caller := CallerFunc(func(ctx context.Context, req CallRequest) (CallResponse, error) {
		assert.Equal(t, "trace-1", ctx.Value(contextKey("trace")))
		assert.Equal(t, wantRequest, req)
		return wantResponse, wantErr
	})

	got, err := caller.CallTool(context.WithValue(context.Background(), contextKey("trace"), "trace-1"), wantRequest)
	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, wantResponse, got)
}
