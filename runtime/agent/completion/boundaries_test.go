package completion

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionRejectsInvalidConfigurationBeforeProvider(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.Request, *Spec[testCompletionResult])
		want   string
	}{
		{"existing structured output", func(r *model.Request, _ *Spec[testCompletionResult]) {
			r.StructuredOutput = &model.StructuredOutput{Name: "existing"}
		}, "cannot override"},
		{"tool choice", func(r *model.Request, _ *Spec[testCompletionResult]) {
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeAuto}
		}, "tool choice"},
		{"missing name", func(_ *model.Request, s *Spec[testCompletionResult]) {
			s.Name = ""
		}, "name is required"},
		{"missing decoder", func(_ *model.Request, s *Spec[testCompletionResult]) {
			s.Codec.FromJSON = nil
		}, "decoder"},
		{"missing schema", func(_ *model.Request, s *Spec[testCompletionResult]) {
			s.Result.Schema = nil
		}, "schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, spec := &model.Request{}, testCompletionSpec()
			tt.change(req, &spec)
			client := &recordingCompletionClient{}
			_, err := Complete(t.Context(), client, req, spec)
			require.ErrorContains(t, err, tt.want)
			_, err = Stream(t.Context(), client, req, spec)
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, client.request)
		})
	}
	_, err := Complete(t.Context(), nil, &model.Request{}, testCompletionSpec())
	require.ErrorContains(t, err, "client is required")
	_, err = Stream(t.Context(), nil, &model.Request{}, testCompletionSpec())
	require.ErrorContains(t, err, "client is required")
	client := &recordingCompletionClient{}
	_, err = Complete(t.Context(), client, nil, testCompletionSpec())
	require.ErrorContains(t, err, "request is required")
	_, err = Stream(t.Context(), client, nil, testCompletionSpec())
	require.ErrorContains(t, err, "request is required")
	_, err = Stream(t.Context(), client, &model.Request{}, testCompletionSpec())
	assert.ErrorContains(t, err, "stream is nil")
}

func TestCompletionResponseRejectsNoncanonicalPayloads(t *testing.T) {
	tests := []struct {
		name     string
		response *model.Response
		want     string
	}{
		{"nil", nil, "response is nil"},
		{"empty", &model.Response{}, "exactly 1"},
		{"multiple messages", &model.Response{Content: []model.Message{{}, {}}}, "exactly 1"},
		{"user role", &model.Response{Content: []model.Message{{Role: model.ConversationRoleUser}}}, "unexpected"},
		{"empty text", completionResponse(model.TextPart{Text: " "}), "did not contain"},
		{"thinking only", completionResponse(model.ThinkingPart{Text: "private"}, model.CacheCheckpointPart{}), "did not contain"},
		{"image", completionResponse(model.ImagePart{Format: "png", Bytes: []byte("image")}), "unsupported response part"},
		{"two texts", completionResponse(model.TextPart{Text: "{}"}, model.TextPart{Text: "{}"}), "multiple content parts"},
		{"two citations", completionResponse(model.CitationsPart{Text: "{}"}, model.CitationsPart{Text: "{}"}), "multiple content parts"},
		{"malformed json", completionResponse(model.TextPart{Text: "{"}), "decode completion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &recordingCompletionClient{response: tt.response}
			result, err := Complete(t.Context(), client, &model.Request{}, testCompletionSpec())
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, result)
		})
	}
	result, err := DecodeResponse(completionResponse(model.CitationsPart{Text: `{"assistant_text":"grounded"}`}, model.CacheCheckpointPart{}), testCompletionSpec())
	require.NoError(t, err)
	assert.Equal(t, "grounded", result.AssistantText)
}

func TestCompletionStreamRejectsInvalidFinalOrdering(t *testing.T) {
	final := model.CompletionChunk{Completion: model.Completion{Name: "draft_from_transcript", Payload: []byte(`{"assistant_text":"ok"}`)}}
	tests := []struct {
		name   string
		chunks []model.Chunk
		want   string
	}{
		{"empty payload", []model.Chunk{model.CompletionChunk{Completion: model.Completion{Name: "draft_from_transcript"}}}, "empty canonical"},
		{"duplicate final", []model.Chunk{final, final}, "multiple canonical"},
		{"preview after final", []model.Chunk{final, model.CompletionDeltaChunk{Delta: model.CompletionDelta{Name: "draft_from_transcript", Delta: "{"}}}, "after final"},
		{"wrong preview", []model.Chunk{model.CompletionDeltaChunk{Delta: model.CompletionDelta{Name: "other", Delta: "{"}}}, "delta for"},
		{"wrong final", []model.Chunk{model.CompletionChunk{Completion: model.Completion{Name: "other", Payload: []byte("{}")}}}, "completion for"},
		{"nil chunk", []model.Chunk{nil}, "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &scriptedStreamer{}
			for _, chunk := range tt.chunks {
				upstream.results = append(upstream.results, recvResult{chunk: chunk})
			}
			stream, err := Stream(t.Context(), &recordingCompletionClient{streamer: upstream}, &model.Request{}, testCompletionSpec())
			require.NoError(t, err)
			for range len(tt.chunks) - 1 {
				_, err = stream.Recv()
				require.NoError(t, err)
			}
			_, err = stream.Recv()
			require.ErrorContains(t, err, tt.want)
			require.NoError(t, stream.Close())
			assert.Equal(t, 1, upstream.closeCalls)
		})
	}
	_, ok, err := DecodeChunk(model.CompletionChunk{Completion: model.Completion{Name: "draft_from_transcript", Payload: []byte("{")}}, testCompletionSpec())
	assert.False(t, ok)
	assert.ErrorContains(t, err, "decode completion")
}

func completionResponse(parts ...model.Part) *model.Response {
	return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: parts}}}
}
