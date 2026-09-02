package gateway

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type emptyRemoteStream struct{}

func (emptyRemoteStream) Recv() (model.Chunk, error) {
	return model.Chunk{}, io.EOF
}

func (emptyRemoteStream) Close() error {
	return nil
}

func (emptyRemoteStream) Metadata() map[string]any {
	return nil
}

type remoteSequenceStream struct {
	chunks   []model.Chunk
	index    int
	terminal error
	closeErr error
	closed   int
}

func (s *remoteSequenceStream) Recv() (model.Chunk, error) {
	if s.index < len(s.chunks) {
		chunk := s.chunks[s.index]
		s.index++
		return chunk, nil
	}
	if s.terminal != nil {
		return model.Chunk{}, s.terminal
	}
	return model.Chunk{}, io.EOF
}

func (s *remoteSequenceStream) Close() error {
	s.closed++
	return s.closeErr
}

func (*remoteSequenceStream) Metadata() map[string]any {
	return nil
}

func TestRemoteClientKeepsPreTransportToolContract(t *testing.T) {
	client := NewRemoteClient(
		func(_ context.Context, request *model.Request) (*model.Response, error) {
			request.Tools = append(request.Tools, &model.ToolDefinition{
				Name:        "transport_injected",
				InputSchema: map[string]any{"type": "object"},
			})
			return &model.Response{
				ToolCalls: []model.ToolCall{{
					Name:    tools.Ident("transport_injected"),
					Payload: rawjson.Message(`{}`),
					ID:      "call-1",
				}},
				StopReason: "tool_use",
			}, nil
		},
		func(context.Context, *model.Request) (model.Streamer, error) {
			return emptyRemoteStream{}, nil
		},
	)

	_, err := client.Complete(context.Background(), &model.Request{
		Tools: []*model.ToolDefinition{{
			Name:        "advertised",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.Error(t, err)
	var validationErr *model.OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, model.OutputValidationToolIdentity, validationErr.Kind())
	assert.NotContains(t, err.Error(), "transport_injected")
}

func TestRemoteClientKeepsPreTransportStreamingToolContract(t *testing.T) {
	raw := &remoteSequenceStream{chunks: []model.Chunk{
		{Type: model.ChunkTypeToolCall, ToolCall: &model.ToolCall{Name: tools.Ident("transport_injected"), ID: "call-1", Payload: rawjson.Message(`{}`)}},
		{Type: model.ChunkTypeStop, StopReason: "tool_use"},
	}}
	client := NewRemoteClient(
		func(context.Context, *model.Request) (*model.Response, error) {
			return nil, errors.New("unexpected unary call")
		},
		func(_ context.Context, request *model.Request) (model.Streamer, error) {
			request.Tools = append(request.Tools, &model.ToolDefinition{Name: "transport_injected", InputSchema: map[string]any{"type": "object"}})
			return raw, nil
		},
	)
	stream, err := client.Stream(context.Background(), &model.Request{Tools: []*model.ToolDefinition{{
		Name: "advertised", InputSchema: map[string]any{"type": "object"},
	}}})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)
	var validationErr *model.OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, model.OutputValidationToolIdentity, validationErr.Kind())
	finalizeErr := stream.Finalize(err)
	require.ErrorAs(t, finalizeErr, &validationErr)
	assert.Equal(t, 1, raw.closed)
}

func TestRemoteClientRejectsWrappedEOFAndPreservesCloseFailure(t *testing.T) {
	wrappedEOF := errors.Join(io.EOF, errors.New("truncated transport"))
	closeErr := errors.New("transport close failed")
	raw := &remoteSequenceStream{terminal: wrappedEOF, closeErr: closeErr}
	client := NewRemoteClient(
		func(context.Context, *model.Request) (*model.Response, error) {
			return nil, errors.New("unexpected unary call")
		},
		func(context.Context, *model.Request) (model.Streamer, error) {
			return raw, nil
		},
	)
	stream, err := client.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Equal(t, wrappedEOF, err)
	finalizeErr := stream.Finalize(err)
	require.ErrorIs(t, finalizeErr, wrappedEOF)
	require.ErrorIs(t, finalizeErr, closeErr)
	assert.Equal(t, 1, raw.closed)
}

func TestRemoteClientPreservesConstructorCompatibilityAndReportsInvalidCallbacks(t *testing.T) {
	client := NewRemoteClient(nil, nil)
	_, err := client.Complete(context.Background(), &model.Request{})
	require.EqualError(t, err, "gateway: complete callback is required")
}
