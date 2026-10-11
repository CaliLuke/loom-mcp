package agenttest

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type countedProvider struct {
	model.Provider
	stream *countedStream
}

type countedStream struct {
	model.Streamer
	closes int
}

func TestScriptStreamOrderAndParity(t *testing.T) {
	turn := Respond(Text("héllo"), RawToolCall("helpers.search", []byte(`{"query":"go"}`)), Thinking("reason"), Text("bye"), Usage(7, 3)).Chunked(2)
	client, script := ScriptClient(t, turn)
	stream, err := client.Stream(t.Context(), scriptRequest())
	require.NoError(t, err)
	assert.Nil(t, stream.Response())
	chunks, terminal := drainScriptStream(stream)
	require.ErrorIs(t, terminal, io.EOF)
	require.NoError(t, stream.Finalize(nil))
	require.NoError(t, stream.Finalize(nil))
	require.NoError(t, stream.Close())
	kinds := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		kinds = append(kinds, chunk.Kind())
	}
	assert.Equal(t, []string{model.ChunkTypeThinking, model.ChunkTypeText, model.ChunkTypeText, model.ChunkTypeText, model.ChunkTypeText, model.ChunkTypeToolCall, model.ChunkTypeUsage, model.ChunkTypeStop}, kinds)
	assert.True(t, script.Requests()[0].Stream)
	unary, unaryScript := ScriptClient(t, turn)
	response, err := unary.Complete(t.Context(), scriptRequest())
	require.NoError(t, err)
	assert.Equal(t, response, stream.Response())
	assert.False(t, unaryScript.Requests()[0].Stream)
}

func TestScriptStreamFailuresAndCleanup(t *testing.T) {
	failure := errors.New("receive failure")
	for _, tc := range []struct {
		name   string
		turn   Turn
		cancel bool
		want   error
		chunks int
	}{
		{"after zero", Respond(Text("hello")).FailStreamAfter(0, failure), false, failure, 0},
		{"after two", Respond(Text("hello")).Chunked(3).FailStreamAfter(2, failure), false, failure, 2},
		{"after all chunks", Respond(Text("hello")).FailStreamAfter(3, failure), false, failure, 2},
		{"canceled", Respond(Text("hello")), true, context.Canceled, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := NewScript(tc.turn)
			require.NoError(t, err)
			provider := &countedProvider{Provider: script}
			client, err := model.NewClient(provider)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := client.Stream(ctx, scriptRequest())
			require.NoError(t, err)
			if tc.cancel {
				cancel()
			}
			chunks, terminal := drainScriptStream(stream)
			require.ErrorIs(t, terminal, tc.want)
			assert.Len(t, chunks, tc.chunks)
			assert.Nil(t, stream.Response())
			require.ErrorIs(t, stream.Finalize(terminal), tc.want)
			require.ErrorIs(t, stream.Finalize(terminal), tc.want)
			require.NoError(t, stream.Close())
			assert.Equal(t, 1, provider.stream.closes)
			require.NoError(t, script.Err())
		})
	}
}

func TestScriptMalformedTurnRejectedForCompleteAndStream(t *testing.T) {
	turn := Respond(Text("x"), StopReason("  "))
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{"complete", func() error {
			script, err := NewScript(turn)
			if err != nil {
				return err
			}
			response, err := script.Client().Complete(t.Context(), scriptRequest())
			assert.Nil(t, response)
			return err
		}},
		{"stream", func() error {
			script, err := NewScript(turn)
			if err != nil {
				return err
			}
			stream, err := script.Client().Stream(t.Context(), scriptRequest())
			if err != nil {
				return err
			}
			chunks, terminal := drainScriptStream(stream)
			assert.NotEmpty(t, chunks)
			return stream.Finalize(terminal)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			require.ErrorContains(t, err, "turn 1")
			require.ErrorContains(t, err, "invalid stop chunk shape")
			var rejection *model.OutputValidationError
			require.NotErrorAs(t, err, &rejection, "malformed turns must fail before invoking the client")
		})
	}
}

func TestScriptRejectsUnreachableStreamFailure(t *testing.T) {
	failure := errors.New("unreachable receive failure")
	for _, tc := range []struct {
		name  string
		turn  Turn
		count string
	}{
		{"unfragmented", Respond(Text("x")).FailStreamAfter(4, failure), "3 chunks"},
		{"fragmented", Respond(Thinking("reason"), Text("x"), RawToolCall("helpers.search", []byte(`{"query":"go"}`))).Chunked(2).FailStreamAfter(7, failure), "6 chunks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := NewScript(Respond(Text("first")), tc.turn)
			assert.Nil(t, script)
			require.ErrorContains(t, err, "turn 2")
			require.ErrorContains(t, err, tc.count)
		})
	}
}

func TestScriptStreamSetupFailure(t *testing.T) {
	failure := errors.New("setup failure")
	client, script := ScriptClient(t, Fail(failure))
	stream, err := client.Stream(t.Context(), scriptRequest())
	assert.Nil(t, stream)
	require.ErrorIs(t, err, failure)
	require.NoError(t, script.Err())
}

func TestScriptRawStreamCancellationAndTerminalResponse(t *testing.T) {
	script, err := NewScript(Respond(Text("ok")))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := script.Stream(ctx, scriptRequest())
	require.NoError(t, err)
	cancel()
	chunk, err := stream.Recv()
	assert.Nil(t, chunk)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, stream.Response())
	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close())
}

func TestScriptStreamSuccessfulCleanupOnce(t *testing.T) {
	script, err := NewScript(Respond(Text("ok")))
	require.NoError(t, err)
	provider := &countedProvider{Provider: script}
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	stream, err := client.Stream(t.Context(), scriptRequest())
	require.NoError(t, err)
	chunks, terminal := drainScriptStream(stream)
	assert.NotEmpty(t, chunks)
	require.ErrorIs(t, terminal, io.EOF)
	require.NoError(t, stream.Finalize(nil))
	require.NoError(t, stream.Finalize(nil))
	require.NoError(t, stream.Close())
	assert.Equal(t, 1, provider.stream.closes)
}

func drainScriptStream(stream model.Streamer) ([]model.Chunk, error) {
	var chunks []model.Chunk
	for {
		chunk, err := stream.Recv()
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
}

func (p *countedProvider) Stream(ctx context.Context, request *model.Request) (model.Streamer, error) {
	stream, err := p.Provider.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	p.stream = &countedStream{Streamer: stream}
	return p.stream, nil
}

func (s *countedStream) Close() error {
	s.closes++
	return s.Streamer.Close()
}
