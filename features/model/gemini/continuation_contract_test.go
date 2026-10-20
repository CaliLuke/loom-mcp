package gemini_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestClientBoundedContinuationThroughSDK(t *testing.T) {
	for _, backend := range []genai.Backend{genai.BackendGeminiAPI, genai.BackendVertexAI} {
		for _, count := range []int{2, 3} {
			t.Run(fmt.Sprintf("backend_%d/hops_%d", backend, count), func(t *testing.T) {
				hops := []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token1", "first"), continuationHop(genai.FinishReasonStop, "", "last")}
				if count == 3 {
					hops = []continuationHTTPHop{hops[0], continuationHop(genai.FinishReasonContinuation, "token2", "middle"), hops[1]}
				}
				client, script := newContinuationClient(t, count, backend, hops, nil)
				req := continuationRequest()
				req.MaxTokens = 64
				response, err := client.Complete(t.Context(), req)
				require.NoError(t, err)
				assert.Equal(t, model.RequestInputUsage{Present: true, InputTokens: 10}, response.RequestInputUsage)
				assert.Equal(t, model.TokenUsage{Model: "gemini-test", InputTokens: 8 * count, CacheReadTokens: 2 * count, OutputTokens: 3 * count, TotalTokens: 13 * count}, response.Usage)
				require.Len(t, response.Content, 1)
				want := []model.Part{model.TextPart{Text: "first"}, model.TextPart{Text: "last"}}
				if count == 3 {
					want = []model.Part{want[0], model.TextPart{Text: "middle"}, want[1]}
				}
				assert.Equal(t, want, response.Content[0].Parts)
				assert.Equal(t, "STOP", response.StopReason)
				requests := script.captured()
				require.Len(t, requests, count)
				assert.NotContains(t, requests[0], "continuationToken")
				for i, body := range requests[1:] {
					assert.Equal(t, base64.StdEncoding.EncodeToString(hops[i].response.Candidates[0].ContinuationToken), body["continuationToken"])
					delete(body, "continuationToken")
					assert.Equal(t, requests[0], body)
				}
				assert.Equal(t, "question", req.Messages[0].Parts[0].(model.TextPart).Text)
			})
		}
	}
}

// The real SDK must not consume extra hops beneath the adapter's request budget.
func TestClientContinuationBudgetOwnsSDKHops(t *testing.T) {
	for _, backend := range []genai.Backend{genai.BackendGeminiAPI, genai.BackendVertexAI} {
		for _, limit := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("backend_%d/limit_%d", backend, limit), func(t *testing.T) {
				hops := make([]continuationHTTPHop, 4)
				for i := range hops {
					hops[i] = continuationHop(genai.FinishReasonContinuation, fmt.Sprintf("token-%d", i), "private-partial")
				}
				client, script := newContinuationClient(t, limit, backend, hops, nil)
				response, err := client.Complete(t.Context(), continuationRequest())
				assert.Nil(t, response)
				require.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
				assert.Len(t, script.captured(), max(1, limit))
				var rejection *model.OutputValidationError
				require.ErrorAs(t, err, &rejection)
				require.NotNil(t, rejection.Usage())
				assert.Equal(t, 13*max(1, limit), rejection.Usage().TotalTokens)
			})
		}
	}
}

func TestClientContinuationRejectsUnsafeChains(t *testing.T) {
	paused := func(token string) continuationHTTPHop {
		return continuationHop(genai.FinishReasonContinuation, token, "private-partial")
	}
	mixed := paused("token")
	mixed.response.Candidates = append(mixed.response.Candidates, continuationHop(genai.FinishReasonStop, "", "private-stop").response.Candidates[0])
	mixedReverse := paused("token")
	mixedReverse.response.Candidates = append([]*genai.Candidate{mixed.response.Candidates[1]}, mixedReverse.response.Candidates...)
	changedIndex := continuationHop(genai.FinishReasonStop, "", "private-last")
	changedIndex.response.Candidates[0].Index = 1
	missingUsage := paused("token")
	missingUsage.response.UsageMetadata = nil
	badUsage := continuationHop(genai.FinishReasonStop, "", "private-last")
	badUsage.response.UsageMetadata.TotalTokenCount = 1
	noCandidates := continuationHTTPHop{response: &genai.GenerateContentResponse{UsageMetadata: paused("x").response.UsageMetadata}}
	blocked := continuationHop(genai.FinishReasonStop, "", "private-last")
	blocked.response.PromptFeedback = &genai.GenerateContentResponsePromptFeedback{}
	for _, tt := range []struct {
		name      string
		limit     int
		hops      []continuationHTTPHop
		calls     int
		usageHops int
	}{
		{"default", 0, []continuationHTTPHop{paused("token")}, 1, 1},
		{"initial only", 1, []continuationHTTPHop{paused("token")}, 1, 1},
		{"fresh token bound", 2, []continuationHTTPHop{paused("one"), paused("two"), paused("three")}, 2, 2},
		{"repeat", 4, []continuationHTTPHop{paused("one"), paused("one")}, 2, 2},
		{"cycle", 4, []continuationHTTPHop{paused("one"), paused("two"), paused("one")}, 3, 3},
		{"missing token", 3, []continuationHTTPHop{paused("")}, 1, 1},
		{"mixed first", 3, []continuationHTTPHop{mixed}, 1, 1},
		{"mixed last", 3, []continuationHTTPHop{mixedReverse}, 1, 1},
		{"changed index", 3, []continuationHTTPHop{paused("one"), changedIndex}, 2, 2},
		{"missing first usage", 3, []continuationHTTPHop{missingUsage}, 1, 0},
		{"missing later usage", 3, []continuationHTTPHop{paused("one"), missingUsage}, 2, 1},
		{"malformed later usage", 3, []continuationHTTPHop{paused("one"), badUsage}, 2, 1},
		{"no final candidates", 3, []continuationHTTPHop{paused("one"), noCandidates}, 2, 2},
		{"final prompt feedback", 3, []continuationHTTPHop{paused("one"), blocked}, 2, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, script := newContinuationClient(t, tt.limit, genai.BackendGeminiAPI, tt.hops, nil)
			response, err := client.Complete(t.Context(), continuationRequest())
			assert.Nil(t, response)
			require.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
			var rejection *model.OutputValidationError
			require.ErrorAs(t, err, &rejection)
			assert.NotContains(t, err.Error(), "private-")
			assert.Len(t, script.captured(), tt.calls)
			if tt.usageHops == 0 {
				assert.Nil(t, rejection.Usage())
			} else {
				require.NotNil(t, rejection.Usage())
				assert.Equal(t, 13*tt.usageHops, rejection.Usage().TotalTokens)
			}
		})
	}
}

func TestClientContinuationRetainsUsageOnLaterFailure(t *testing.T) {
	for _, status := range []int{400, 429} {
		client, script := newContinuationClient(t, 3, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "private-token", "private-partial"), {status: status}}, nil)
		response, err := client.Complete(t.Context(), continuationRequest())
		assert.Nil(t, response)
		var rejection *model.OutputValidationError
		require.ErrorAs(t, err, &rejection)
		require.NotNil(t, rejection.Usage())
		assert.Equal(t, 13, rejection.Usage().TotalTokens)
		assert.Equal(t, model.RequestInputUsage{Present: true, InputTokens: 10}, rejection.Evidence().RequestInputUsage)
		var apiErr genai.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, status, apiErr.Code)
		assert.Equal(t, status == 429, errors.Is(err, model.ErrRateLimited))
		assert.Len(t, script.captured(), 2)
		assert.NotContains(t, err.Error(), "private-")
	}
}

func TestClientContinuationCancellationBetweenHops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, script := newContinuationClient(t, 3, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token", "private-partial")}, cancel)
	response, err := client.Complete(ctx, continuationRequest())
	assert.Nil(t, response)
	require.ErrorIs(t, err, context.Canceled)
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	require.NotNil(t, rejection.Usage())
	assert.Equal(t, 13, rejection.Usage().TotalTokens)
	assert.Len(t, script.captured(), 1)
}

func TestClientContinuationNegativeLimit(t *testing.T) {
	_, err := geminimodel.New(geminimodel.Options{Client: &mockModelsClient{}, DefaultModel: "gemini-test", MaxGenerationRequests: -1})
	require.ErrorContains(t, err, "generation requests")
}
