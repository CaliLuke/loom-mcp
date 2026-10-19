package gemini_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestClientContinuationInitialPromptAccounting(t *testing.T) {
	for _, initialZero := range []bool{false, true} {
		first := continuationHop(genai.FinishReasonContinuation, "token", "first")
		first.response.UsageMetadata.ToolUsePromptTokenCount = 7
		first.response.UsageMetadata.TotalTokenCount += 7
		wantInitial, wantInput, wantCache, wantTotal := 10, 23, 4, 33
		if initialZero {
			first.response.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{}
			wantInitial, wantInput, wantCache, wantTotal = 0, 8, 2, 13
		}
		client, _ := newContinuationClient(t, 2, genai.BackendGeminiAPI, []continuationHTTPHop{first, continuationHop(genai.FinishReasonStop, "", "last")}, nil)
		response, err := client.Complete(t.Context(), continuationRequest())
		require.NoError(t, err)
		assert.Equal(t, model.RequestInputUsage{Present: true, InputTokens: wantInitial}, response.RequestInputUsage)
		assert.Equal(t, wantInput, response.Usage.InputTokens)
		assert.Equal(t, wantCache, response.Usage.CacheReadTokens)
		assert.Equal(t, wantTotal, response.Usage.TotalTokens)
	}
}

func TestClientContinuationTerminalRejections(t *testing.T) {
	for _, reason := range []genai.FinishReason{genai.FinishReasonMaxTokens, genai.FinishReasonSafety} {
		client, script := newContinuationClient(t, 3, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token", "private-first"), continuationHop(reason, "never-follow", "private-last")}, nil)
		response, err := client.Complete(t.Context(), continuationRequest())
		assert.Nil(t, response)
		var rejection *model.OutputValidationError
		require.ErrorAs(t, err, &rejection)
		require.NotNil(t, rejection.Usage())
		assert.Equal(t, 26, rejection.Usage().TotalTokens)
		assert.Equal(t, 10, rejection.Evidence().RequestInputUsage.InputTokens)
		assert.Len(t, script.captured(), 2)
		assert.NotContains(t, err.Error(), "private-")
		if reason == genai.FinishReasonMaxTokens {
			assert.Equal(t, model.OutputValidationOutputBounds, rejection.Kind())
		} else {
			assert.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
		}
	}
}

func TestClientContinuationPrivateOutputBound(t *testing.T) {
	client, script := newContinuationClient(t, 3, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "private-token", strings.Repeat("x", 16<<20))}, nil)
	response, err := client.Complete(t.Context(), continuationRequest())
	assert.Nil(t, response)
	require.ErrorIs(t, err, geminimodel.ErrContinuationUnsupported)
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, model.OutputValidationOutputBounds, rejection.Kind())
	assert.Equal(t, 13, rejection.Usage().TotalTokens)
	assert.Len(t, script.captured(), 1)
	assert.Less(t, len(err.Error()), 512)
}

func TestClientContinuationTransportRetryPolicy(t *testing.T) {
	client, script := newContinuationClientWithRetries(t, 2, genai.BackendGeminiAPI, []continuationHTTPHop{{status: 500}, continuationHop(genai.FinishReasonContinuation, "token", "first"), continuationHop(genai.FinishReasonStop, "", "last")}, nil, 2)
	response, err := client.Complete(t.Context(), continuationRequest())
	require.NoError(t, err)
	assert.Equal(t, 26, response.Usage.TotalTokens)
	assert.Len(t, script.captured(), 3, "two generation requests can include an SDK transport retry")
}

func TestClientContinuationProviderOverflow(t *testing.T) {
	client, script := newContinuationClient(t, 3, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token", "first"), {status: 400, message: "The input token count exceeds the maximum number of tokens allowed 1048576."}}, nil)
	response, err := client.Complete(t.Context(), continuationRequest())
	assert.Nil(t, response)
	var overflow *model.ProviderError
	require.ErrorAs(t, err, &overflow)
	assert.Equal(t, model.ProviderErrorKindContextOverflow, overflow.Kind())
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, 13, rejection.Usage().TotalTokens)
	assert.Len(t, script.captured(), 2)
}

func TestClientContinuationThinkingAndToolParts(t *testing.T) {
	first := continuationHop(genai.FinishReasonContinuation, "token", "")
	first.response.Candidates[0].Content.Parts = []*genai.Part{{Thought: true, ThoughtSignature: []byte("signature")}}
	last := continuationHop(genai.FinishReasonStop, "", "")
	last.response.Candidates[0].Content.Parts = []*genai.Part{{Text: "after"}, {FunctionCall: &genai.FunctionCall{ID: "call-id", Name: "lookup", Args: map[string]any{"query": "value"}}}}
	client, script := newContinuationClient(t, 2, genai.BackendGeminiAPI, []continuationHTTPHop{first, last}, nil)
	req := continuationRequest()
	req.Tools = []*model.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
	response, err := client.Complete(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, response.Content, 1)
	assert.Equal(t, []model.Part{model.ThinkingPart{Signature: "signature"}, model.TextPart{Text: "after"}}, response.Content[0].Parts)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, "call-id", response.ToolCalls[0].ID)
	assert.Equal(t, "lookup", string(response.ToolCalls[0].Name))
	assert.Len(t, script.captured(), 2)
}

func TestClientContinuationFinalValidationRetainsFacts(t *testing.T) {
	client, _ := newContinuationClient(t, 2, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token", "not"), continuationHop(genai.FinishReasonStop, "", "json")}, nil)
	req := continuationRequest()
	req.StructuredOutput = &model.StructuredOutput{Name: "result", Schema: []byte(`{"type":"object"}`)}
	response, err := client.Complete(t.Context(), req)
	assert.Nil(t, response)
	var rejection *model.OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, 26, rejection.Usage().TotalTokens)
	assert.Equal(t, 10, rejection.Evidence().RequestInputUsage.InputTokens)
}

func TestClientContinuationUnknownUsageTotal(t *testing.T) {
	last := continuationHop(genai.FinishReasonStop, "", "last")
	last.response.UsageMetadata.TotalTokenCount = 0
	client, _ := newContinuationClient(t, 2, genai.BackendGeminiAPI, []continuationHTTPHop{continuationHop(genai.FinishReasonContinuation, "token", "first"), last}, nil)
	response, err := client.Complete(t.Context(), continuationRequest())
	require.NoError(t, err)
	assert.Zero(t, response.Usage.TotalTokens)
	assert.Equal(t, 16, response.Usage.InputTokens)
	assert.Equal(t, 10, response.RequestInputUsage.InputTokens)
}
