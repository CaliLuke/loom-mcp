package sdkclient

import (
	"context"
	"errors"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithClientFeaturesIgnoresNilSession(t *testing.T) {
	t.Parallel()

	ctx := WithClientFeatures(context.Background(), nil)

	_, hasElicitor := mcpruntime.ElicitorFromContext(ctx)
	_, hasSampler := mcpruntime.SamplerFromContext(ctx)
	_, hasRootLister := mcpruntime.RootListerFromContext(ctx)
	_, hasProgressReporter := mcpruntime.ProgressReporterFromContext(ctx)
	require.False(t, hasElicitor)
	require.False(t, hasSampler)
	require.False(t, hasRootLister)
	require.False(t, hasProgressReporter)
}

func TestClientFeatureAdaptersUseOfficialSDKSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	implementation := &mcp.Implementation{Name: "sdkclient-test", Version: "1.0.0"}

	var gotElicit *mcp.ElicitParams
	var gotSample *mcp.CreateMessageParams
	progress := make(chan *mcp.ProgressNotificationParams, 1)
	client := mcp.NewClient(implementation, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			gotElicit = req.Params
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"project": "loom"}}, nil
		},
		CreateMessageHandler: func(_ context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			gotSample = req.Params
			return &mcp.CreateMessageResult{
				Role:       "assistant",
				Content:    &mcp.TextContent{Text: "sampled"},
				Model:      "test-model",
				StopReason: "endTurn",
			}, nil
		},
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progress <- req.Params
		},
	})
	client.AddRoots(&mcp.Root{URI: "file:///workspace", Name: "workspace"})
	server := mcp.NewServer(implementation, nil)
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientSession.Close()) })

	featureCtx := WithClientFeatures(ctx, serverSession)
	elicitor, ok := mcpruntime.ElicitorFromContext(featureCtx)
	require.True(t, ok)
	result, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{
		ElicitationID:   "elicit-1",
		Message:         "Choose a project",
		Mode:            "form",
		RequestedSchema: map[string]any{"type": "object"},
	})
	require.NoError(t, err)
	assert.Equal(t, "accept", result.Action)
	assert.Equal(t, "loom", result.Content["project"])
	require.NotNil(t, gotElicit)
	assert.Equal(t, "elicit-1", gotElicit.ElicitationID)
	assert.Equal(t, "Choose a project", gotElicit.Message)
	assert.Equal(t, "form", gotElicit.Mode)

	sampler, ok := mcpruntime.SamplerFromContext(featureCtx)
	require.True(t, ok)
	sample, err := sampler.Sample(ctx, mcpruntime.SampleRequest{
		Messages:      []mcpruntime.SampleMessage{{Role: "user", Text: "hello"}},
		SystemPrompt:  "Be concise",
		MaxTokens:     64,
		StopSequences: []string{"stop"},
		Temperature:   0.25,
		Metadata:      map[string]any{"trace": "trace-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, &mcpruntime.SampleResult{Role: "assistant", Text: "sampled", Model: "test-model", StopReason: "endTurn"}, sample)
	require.Len(t, gotSample.Messages, 1)
	assert.Equal(t, mcp.Role("user"), gotSample.Messages[0].Role)
	assert.Equal(t, "hello", gotSample.Messages[0].Content.(*mcp.TextContent).Text)
	assert.Equal(t, "Be concise", gotSample.SystemPrompt)
	assert.Equal(t, int64(64), gotSample.MaxTokens)

	rootLister, ok := mcpruntime.RootListerFromContext(featureCtx)
	require.True(t, ok)
	roots, err := rootLister.ListRoots(ctx)
	require.NoError(t, err)
	assert.Equal(t, []mcpruntime.Root{{URI: "file:///workspace", Name: "workspace"}}, roots)

	reporter, ok := mcpruntime.ProgressReporterFromContext(featureCtx)
	require.True(t, ok)
	require.NoError(t, reporter.ReportProgress(ctx, "progress-1", mcpruntime.ProgressUpdate{Progress: 1, Total: 3, Message: "started"}))
	gotProgress := <-progress
	assert.Equal(t, "progress-1", gotProgress.ProgressToken)
	assert.InDelta(t, 1.0, gotProgress.Progress, 0)
	assert.InDelta(t, 3.0, gotProgress.Total, 0)
	assert.Equal(t, "started", gotProgress.Message)
}

func TestClientFeatureAdaptersFailClosedWithoutSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := (sessionElicitor{}).Elicit(ctx, mcpruntime.ElicitRequest{})
	require.ErrorIs(t, err, mcpruntime.ErrElicitorUnavailable)
	_, err = (sessionSampler{}).Sample(ctx, mcpruntime.SampleRequest{})
	require.ErrorIs(t, err, mcpruntime.ErrSamplerUnavailable)
	_, err = (sessionRootLister{}).ListRoots(ctx)
	require.ErrorIs(t, err, mcpruntime.ErrRootListerUnavailable)
	err = (sessionProgressReporter{}).ReportProgress(ctx, nil, mcpruntime.ProgressUpdate{})
	require.ErrorIs(t, err, mcpruntime.ErrProgressReporterUnavailable)
}

func TestClientFeatureAdapterPropagatesClientErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("client rejected sampling")
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	implementation := &mcp.Implementation{Name: "sdkclient-error-test", Version: "1.0.0"}
	client := mcp.NewClient(implementation, &mcp.ClientOptions{
		CreateMessageHandler: func(context.Context, *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return nil, wantErr
		},
	})
	serverSession, err := mcp.NewServer(implementation, nil).Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientSession.Close()) })

	_, err = (sessionSampler{session: serverSession}).Sample(ctx, mcpruntime.SampleRequest{MaxTokens: 1})
	require.ErrorContains(t, err, wantErr.Error())
}
