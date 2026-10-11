package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingRecordingClient struct {
	entered chan struct{}
}

type blockingRecordingStream struct {
	*recordingTestStream
	ctx     context.Context
	entered chan struct{}
}

func TestCallRecordingAbandonUnblocksReceive(t *testing.T) {
	store := inmem.New()
	recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
	entered := make(chan struct{})
	client := &callRecordingClient{inner: blockingRecordingClient{entered: entered}, recording: recording}
	ctx := recordingCallContext(1)
	stream, err := client.Stream(ctx, &model.Request{})
	require.NoError(t, err)
	received := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		received <- err
	}()
	<-entered
	require.NoError(t, recording.Finish(ctx))
	require.ErrorIs(t, <-received, context.Canceled)
	calls, err := store.ListCalls(ctx, "root", "run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, runstore.CallFailed, calls[0].State)
	completion, err := callrecord.DecodeCompletion(calls[0].Completion)
	require.NoError(t, err)
	require.NotNil(t, completion.Error)
	assert.Equal(t, "stream_abandoned", completion.Error.Kind)
}

func (c blockingRecordingClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return &model.Response{}, nil
}

func (c blockingRecordingClient) Stream(ctx context.Context, _ *model.Request) (model.ValidatedStreamer, error) {
	return &blockingRecordingStream{recordingTestStream: &recordingTestStream{}, ctx: ctx, entered: c.entered}, nil
}

func (s *blockingRecordingStream) Recv() (model.Chunk, error) {
	close(s.entered)
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
