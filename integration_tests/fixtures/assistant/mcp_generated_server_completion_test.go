package assistantapi

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerCompletesPromptArguments(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	session := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", nil)
	defer func() {
		require.NoError(t, session.Close())
	}()

	require.NotNil(t, session.InitializeResult().Capabilities.Completions)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.Complete(ctx, &sdkmcp.CompleteParams{
		Ref: &sdkmcp.CompleteReference{
			Type: "ref/prompt",
			Name: "figma_implementation_prompt",
		},
		Argument: sdkmcp.CompleteParamsArgument{
			Name:  "framework",
			Value: "re",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"react"}, result.Completion.Values)
	require.Equal(t, 1, result.Completion.Total)
	require.False(t, result.Completion.HasMore)
}
