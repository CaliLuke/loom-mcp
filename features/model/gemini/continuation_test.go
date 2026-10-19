package gemini_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestClientRejectsContinuationThroughSDK(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"unfinished"}]},"finishReason":"CONTINUATION","continuationToken":"dG9rZW4="}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	sdk, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey: "test", Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	})
	require.NoError(t, err)
	provider, err := geminimodel.New(geminimodel.Options{Client: sdk.Models, DefaultModel: "gemini-test"})
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)

	response, err := client.Complete(t.Context(), continuationRequest())

	assert.Nil(t, response)
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, model.OutputValidationResponseShape, rejection.Kind())
	require.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
	assert.Equal(t, &model.TokenUsage{Model: "gemini-test", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, rejection.Usage())
	assert.True(t, rejection.Evidence().Present)
	assert.Equal(t, int32(1), calls.Load())
	assert.NotContains(t, err.Error(), "unfinished")
}

func TestClientRejectsAnyContinuationCandidate(t *testing.T) {
	paused := &genai.Candidate{
		FinishReason: genai.FinishReasonContinuation,
		Content: &genai.Content{Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
			Name: "partial_tool", Args: map[string]any{"incomplete": true},
		}}}},
	}
	stopped := &genai.Candidate{FinishReason: genai.FinishReasonStop, Content: &genai.Content{Parts: []*genai.Part{{Text: "complete"}}}}
	limited := &genai.Candidate{FinishReason: genai.FinishReasonMaxTokens}
	for _, tc := range []struct {
		name       string
		candidates []*genai.Candidate
	}{
		{"missing_token", []*genai.Candidate{paused}},
		{"first", []*genai.Candidate{paused, stopped}},
		{"last_after_nil", []*genai.Candidate{nil, stopped, paused}},
		{"with_output_limit", []*genai.Candidate{limited, paused}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := geminimodel.New(geminimodel.Options{
				Client: &mockModelsClient{response: &genai.GenerateContentResponse{Candidates: tc.candidates}}, DefaultModel: "gemini-test",
			})
			require.NoError(t, err)

			response, err := provider.Complete(t.Context(), continuationRequest())

			assert.Nil(t, response)
			var rejection *model.OutputValidationError
			require.ErrorAs(t, err, &rejection)
			assert.Equal(t, model.OutputValidationResponseShape, rejection.Kind())
			require.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
			assert.NotContains(t, err.Error(), "partial_tool")
		})
	}
}

func TestClientContinuationDoesNotRetainInvalidUsage(t *testing.T) {
	provider, err := geminimodel.New(geminimodel.Options{
		Client: &mockModelsClient{response: &genai.GenerateContentResponse{
			Candidates:    []*genai.Candidate{{FinishReason: genai.FinishReasonContinuation}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: -1},
		}}, DefaultModel: "gemini-test",
	})
	require.NoError(t, err)

	response, err := provider.Complete(t.Context(), continuationRequest())

	assert.Nil(t, response)
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Nil(t, rejection.Usage())
}

func continuationRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{
		Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "question"}},
	}}}
}
