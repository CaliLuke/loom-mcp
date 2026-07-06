package completion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type testCompletionResult struct {
	AssistantText string `json:"assistant_text"`
}

type recordingCompletionClient struct {
	request   *model.Request
	response  *model.Response
	streamer  model.Streamer
	err       error
	streamErr error
}

type recvResult struct {
	chunk model.Chunk
	err   error
}

type scriptedStreamer struct {
	metadata map[string]any
	results  []recvResult
	index    int
}

func testCompletionSpec() Spec[testCompletionResult] {
	return Spec[testCompletionResult]{
		Name:        "draft_from_transcript",
		Description: "Synthesize task draft",
		Result: tools.TypeSpec{
			Name:   "DraftFromTranscriptResult",
			Schema: []byte(`{"type":"object","required":["assistant_text"]}`),
		},
		Codec: tools.JSONCodec[testCompletionResult]{
			ToJSON: func(value testCompletionResult) ([]byte, error) {
				return json.Marshal(value)
			},
			FromJSON: func(data []byte) (testCompletionResult, error) {
				var out testCompletionResult
				err := json.Unmarshal(data, &out)
				return out, err
			},
		},
	}
}

func (c *recordingCompletionClient) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	c.request = req
	return c.response, c.err
}

func (c *recordingCompletionClient) Stream(_ context.Context, req *model.Request) (model.Streamer, error) {
	c.request = req
	return c.streamer, c.streamErr
}

func (s *scriptedStreamer) Recv() (model.Chunk, error) {
	if s.index >= len(s.results) {
		return model.Chunk{}, io.EOF
	}
	result := s.results[s.index]
	s.index++
	return result.chunk, result.err
}

func (s *scriptedStreamer) Close() error {
	return nil
}

func (s *scriptedStreamer) Metadata() map[string]any {
	return s.metadata
}

func TestCompleteSetsStructuredOutputAndDecodesTypedValue(t *testing.T) {
	client := &recordingCompletionClient{
		response: &model.Response{
			Content: []model.Message{{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ThinkingPart{Text: "internal"},
					model.TextPart{Text: `{"assistant_text":"created a draft"}`},
				},
			}},
		},
	}
	req := &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "create a task"}},
		}},
	}

	resp, err := Complete(context.Background(), client, req, testCompletionSpec())
	require.NoError(t, err)
	require.Equal(t, testCompletionResult{AssistantText: "created a draft"}, resp.Value)
	require.NotNil(t, client.request.StructuredOutput)
	require.Equal(t, "draft_from_transcript", client.request.StructuredOutput.Name)
	require.JSONEq(t, `{"type":"object","required":["assistant_text"]}`, string(client.request.StructuredOutput.Schema))
	require.Nil(t, req.StructuredOutput)
}

func TestCompleteRejectsStreamingRequests(t *testing.T) {
	_, err := Complete(
		context.Background(),
		&recordingCompletionClient{},
		&model.Request{Stream: true},
		testCompletionSpec(),
	)
	require.ErrorContains(t, err, "does not support streaming")
}

func TestCompleteRejectsTools(t *testing.T) {
	_, err := Complete(
		context.Background(),
		&recordingCompletionClient{},
		&model.Request{
			Tools: []*model.ToolDefinition{{
				Name:        "lookup",
				Description: "Search",
				InputSchema: rawjson.Message(`{"type":"object"}`),
			}},
		},
		testCompletionSpec(),
	)
	require.ErrorContains(t, err, "does not allow tool definitions")
}

func TestStreamSetsStructuredOutputAndEnablesStreaming(t *testing.T) {
	client := &recordingCompletionClient{streamer: &scriptedStreamer{}}
	req := &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "create a task"}},
		}},
	}

	stream, err := Stream(context.Background(), client, req, testCompletionSpec())
	require.NoError(t, err)
	require.NotNil(t, stream)
	require.True(t, client.request.Stream)
	require.NotNil(t, client.request.StructuredOutput)
	require.Equal(t, "draft_from_transcript", client.request.StructuredOutput.Name)
	require.False(t, req.Stream)
	require.Nil(t, req.StructuredOutput)
}

func TestStreamEnforcesCanonicalCompletionContract(t *testing.T) {
	upstream := &scriptedStreamer{
		metadata: map[string]any{"provider": "test"},
		results: []recvResult{
			{
				chunk: model.Chunk{
					Type: model.ChunkTypeCompletionDelta,
					CompletionDelta: &model.CompletionDelta{
						Name:  "draft_from_transcript",
						Delta: `{"assistant_text":"draft`,
					},
				},
			},
			{
				chunk: model.Chunk{
					Type: model.ChunkTypeCompletion,
					Completion: &model.Completion{
						Name:    "draft_from_transcript",
						Payload: rawjson.Message(`{"assistant_text":"created a draft"}`),
					},
				},
			},
			{chunk: model.Chunk{Type: model.ChunkTypeStop, StopReason: "stop"}},
		},
	}
	stream, err := Stream(context.Background(), &recordingCompletionClient{streamer: upstream}, &model.Request{}, testCompletionSpec())
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, model.ChunkTypeCompletionDelta, chunk.Type)
	chunk, err = stream.Recv()
	require.NoError(t, err)
	value, ok, err := DecodeChunk(chunk, testCompletionSpec())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testCompletionResult{AssistantText: "created a draft"}, value)
	chunk, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, model.ChunkTypeStop, chunk.Type)
	require.Equal(t, map[string]any{"provider": "test"}, stream.Metadata())
}

func TestStreamRejectsEOFBeforeFinalCompletion(t *testing.T) {
	stream, err := Stream(
		context.Background(),
		&recordingCompletionClient{streamer: &scriptedStreamer{}},
		&model.Request{},
		testCompletionSpec(),
	)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorContains(t, err, "ended without canonical completion chunk")
}

func TestStreamRejectsStopBeforeFinalCompletion(t *testing.T) {
	stream, err := Stream(
		context.Background(),
		&recordingCompletionClient{
			streamer: &scriptedStreamer{results: []recvResult{{chunk: model.Chunk{Type: model.ChunkTypeStop}}}},
		},
		&model.Request{},
		testCompletionSpec(),
	)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorContains(t, err, "stopped before canonical completion chunk")
}

func TestStreamRejectsUnexpectedChunk(t *testing.T) {
	stream, err := Stream(
		context.Background(),
		&recordingCompletionClient{
			streamer: &scriptedStreamer{results: []recvResult{{chunk: model.Chunk{Type: model.ChunkTypeText}}}},
		},
		&model.Request{},
		testCompletionSpec(),
	)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorContains(t, err, `unexpected "text" chunk`)
}

func TestDecodeChunkIgnoresPreviewAndDecodesFinalCompletion(t *testing.T) {
	_, ok, err := DecodeChunk(model.Chunk{
		Type: model.ChunkTypeCompletionDelta,
		CompletionDelta: &model.CompletionDelta{
			Name:  "draft_from_transcript",
			Delta: `{"assistant_text":"draft"}`,
		},
	}, testCompletionSpec())
	require.NoError(t, err)
	require.False(t, ok)

	value, ok, err := DecodeChunk(model.Chunk{
		Type: model.ChunkTypeCompletion,
		Completion: &model.Completion{
			Name:    "draft_from_transcript",
			Payload: rawjson.Message(`{"assistant_text":"created a draft"}`),
		},
	}, testCompletionSpec())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testCompletionResult{AssistantText: "created a draft"}, value)
}

func TestDecodeChunkRejectsWrongCompletionName(t *testing.T) {
	_, _, err := DecodeChunk(model.Chunk{
		Type: model.ChunkTypeCompletion,
		Completion: &model.Completion{
			Name:    "other",
			Payload: rawjson.Message(`{"assistant_text":"created a draft"}`),
		},
	}, testCompletionSpec())
	require.ErrorContains(t, err, "does not match spec")
}

func TestDecodeResponseRejectsToolCalls(t *testing.T) {
	_, err := DecodeResponse(&model.Response{
		ToolCalls: []model.ToolCall{{Name: tools.Ident("lookup")}},
	}, testCompletionSpec())
	require.ErrorContains(t, err, "returned tool calls")
}

func TestStreamPropagatesUnderlyingErrors(t *testing.T) {
	want := errors.New("provider failed")
	stream, err := Stream(
		context.Background(),
		&recordingCompletionClient{
			streamer: &scriptedStreamer{results: []recvResult{{err: want}}},
		},
		&model.Request{},
		testCompletionSpec(),
	)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorIs(t, err, want)
}
