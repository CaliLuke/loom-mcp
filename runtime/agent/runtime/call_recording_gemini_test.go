package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type pausedGeminiModels struct{}

func TestCallRecordingGeminiContinuationRetainsUsage(t *testing.T) {
	provider, err := gemini.New(gemini.Options{Client: pausedGeminiModels{}, DefaultModel: "gemini-test"})
	require.NoError(t, err)
	validated, err := model.NewClient(provider)
	require.NoError(t, err)
	store := inmem.New()
	recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
	client := &callRecordingClient{inner: validated, recording: recording}
	ctx := recordingCallContext(1)

	response, err := client.Complete(ctx, &model.Request{Messages: []*model.Message{{
		Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "question"}},
	}}})

	assert.Nil(t, response)
	require.ErrorIs(t, err, gemini.ErrContinuationUnsupported)
	require.NoError(t, recording.Finish(ctx))
	calls, err := store.ListCalls(ctx, "root", "run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, runstore.CallFailed, calls[0].State)
	completion, err := callrecord.DecodeCompletion(calls[0].Completion)
	require.NoError(t, err)
	assert.Nil(t, completion.Response)
	assert.Equal(t, model.TokenUsage{Model: "gemini-test", InputTokens: 8, CacheReadTokens: 2, OutputTokens: 5, TotalTokens: 15}, completion.Usage)
	require.NotNil(t, completion.Facts)
	assert.Equal(t, contextmeasure.OutcomeError, completion.Facts.Outcome)
	assert.Equal(t, 10, completion.Facts.InputTokens)
	require.NotNil(t, completion.Error)
	assert.NotContains(t, completion.Error.Message, "unfinished")
}

func (pausedGeminiModels) GenerateContent(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			FinishReason: genai.FinishReasonContinuation,
			Content:      &genai.Content{Parts: []*genai.Part{{Text: "unfinished"}}},
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 10, CachedContentTokenCount: 2, CandidatesTokenCount: 5, TotalTokenCount: 15,
		},
	}, nil
}
