package completion

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type testCompletionResult struct {
	AssistantText string `json:"assistant_text"`
}

type recordingCompletionClient struct {
	request   *model.Request
	response  *model.Response
	streamer  model.ValidatedStreamer
	err       error
	streamErr error
}

type recvResult struct {
	chunk model.Chunk
	err   error
}

type scriptedStreamer struct {
	response      *model.Response
	results       []recvResult
	index         int
	closeErr      error
	closeCalls    int
	finalizeCalls int
	finalizeInput error
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

func (c *recordingCompletionClient) Stream(_ context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	c.request = req
	return c.streamer, c.streamErr
}

func (s *scriptedStreamer) Recv() (model.Chunk, error) {
	if s.index >= len(s.results) {
		return nil, io.EOF
	}
	result := s.results[s.index]
	s.index++
	return result.chunk, result.err
}

func (s *scriptedStreamer) Close() error {
	s.closeCalls++
	return s.closeErr
}

func (s *scriptedStreamer) Response() *model.Response {
	return s.response
}

func (s *scriptedStreamer) Finalize(primaryErr error) error {
	s.finalizeCalls++
	s.finalizeInput = primaryErr
	return errors.Join(primaryErr, s.Close())
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

func TestCompletionStreamDelegatesCanonicalLifecycle(t *testing.T) {
	primaryErr := errors.New("processing failed")
	closeErr := errors.New("provider close failed")
	response := &model.Response{StopReason: "end_turn"}
	upstream := &scriptedStreamer{
		response: response,
		closeErr: closeErr,
	}
	stream := newCompletionStream(upstream, "draft_from_transcript")

	require.Same(t, response, stream.Response())
	err := stream.Finalize(primaryErr)
	require.ErrorIs(t, err, primaryErr)
	require.ErrorIs(t, err, closeErr)
	require.Equal(t, primaryErr, upstream.finalizeInput)
	require.Equal(t, 1, upstream.finalizeCalls)
	require.Equal(t, 1, upstream.closeCalls)
}

func TestStreamEnforcesCanonicalCompletionContract(t *testing.T) {
	upstream := &scriptedStreamer{
		results: []recvResult{
			{
				chunk: model.CompletionDeltaChunk{
					Delta: model.CompletionDelta{
						Name:  "draft_from_transcript",
						Delta: `{"assistant_text":"draft`,
					},
				},
			},
			{
				chunk: model.CompletionChunk{
					Completion: model.Completion{
						Name:    "draft_from_transcript",
						Payload: rawjson.Message(`{"assistant_text":"created a draft"}`),
					},
				},
			},
			{chunk: model.StopChunk{Reason: "stop"}},
		},
	}
	stream, err := Stream(context.Background(), &recordingCompletionClient{streamer: upstream}, &model.Request{}, testCompletionSpec())
	require.NoError(t, err)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.IsType(t, model.CompletionDeltaChunk{}, chunk)
	chunk, err = stream.Recv()
	require.NoError(t, err)
	value, ok, err := DecodeChunk(chunk, testCompletionSpec())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testCompletionResult{AssistantText: "created a draft"}, value)
	chunk, err = stream.Recv()
	require.NoError(t, err)
	require.IsType(t, model.StopChunk{}, chunk)
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
			streamer: &scriptedStreamer{results: []recvResult{{chunk: model.StopChunk{}}}},
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
			streamer: &scriptedStreamer{results: []recvResult{{chunk: model.TextChunk{}}}},
		},
		&model.Request{},
		testCompletionSpec(),
	)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorContains(t, err, "unexpected model.TextChunk chunk")
}

func TestDecodeChunkIgnoresPreviewAndDecodesFinalCompletion(t *testing.T) {
	_, ok, err := DecodeChunk(model.CompletionDeltaChunk{
		Delta: model.CompletionDelta{
			Name:  "draft_from_transcript",
			Delta: `{"assistant_text":"draft"}`,
		},
	}, testCompletionSpec())
	require.NoError(t, err)
	require.False(t, ok)

	value, ok, err := DecodeChunk(model.CompletionChunk{
		Completion: model.Completion{
			Name:    "draft_from_transcript",
			Payload: rawjson.Message(`{"assistant_text":"created a draft"}`),
		},
	}, testCompletionSpec())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testCompletionResult{AssistantText: "created a draft"}, value)
}

func TestDecodeChunkRejectsWrongCompletionName(t *testing.T) {
	_, _, err := DecodeChunk(model.CompletionChunk{
		Completion: model.Completion{
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
