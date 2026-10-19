package runtime

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func TestCallRecordingContinuationCanonicalEvidence(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "later_failure"}[fails], func(t *testing.T) {
			provider, sends := recordingContinuationClient(t, fails)
			store := inmem.New()
			recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
			recording.maxModelCalls = 1
			client := &callInfoClientWrapper{inner: &callRecordingClient{inner: provider, recording: recording}, store: store, recording: recording, info: model.CallInfo{RootRunID: "root", RunID: "run", Attempt: 1, ModelID: "registered", TurnID: "turn"}}
			req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "initial question"}}}}}
			response, err := client.Complete(t.Context(), req)
			wantUsage := model.TokenUsage{Model: "gemini-test", InputTokens: 48, CacheReadTokens: 12, OutputTokens: 9, TotalTokens: 69}
			if fails {
				require.Error(t, err)
				assert.Nil(t, response)
				wantUsage = model.TokenUsage{Model: "gemini-test", InputTokens: 24, CacheReadTokens: 6, OutputTokens: 6, TotalTokens: 36}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, int32(3), sends.Load())
			_, err = client.Complete(t.Context(), req)
			require.ErrorIs(t, err, policy.ErrModelCallLimit)
			assert.Equal(t, int32(3), sends.Load(), "outer invocation needs another reservation")
			calls, err := store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			started, err := callrecord.DecodeStarted(calls[0].Started)
			require.NoError(t, err)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			require.NotNil(t, completion.Facts)
			assert.Equal(t, 10, completion.Facts.InputTokens, "initial prompt cannot become sum of billed hops")
			assert.Equal(t, wantUsage, completion.Usage)
			rebuilt, err := callrecord.Rebuild(t.Context(), store, "root", started, nil)
			require.NoError(t, err)
			assert.Equal(t, req.Messages, rebuilt.Messages)
			originalDigest, digestErr := callrecord.RequestDigest(req)
			require.NoError(t, digestErr)
			rebuiltDigest, digestErr := callrecord.RequestDigest(rebuilt)
			require.NoError(t, digestErr)
			assert.Equal(t, originalDigest, rebuiltDigest)
			record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "run", Calls: []runrecord.Call{{Record: calls[0], Started: started, Completion: &completion, Request: rebuilt}}}}, Content: map[runstore.Hash][]byte{}}
			if fails {
				assert.Nil(t, completion.Response)
				require.NotNil(t, completion.Error)
				assert.NotContains(t, completion.Error.Message, "private")
			} else {
				require.NotNil(t, completion.Response)
				data, getErr := store.Get(t.Context(), "root", *completion.Response)
				require.NoError(t, getErr)
				stored, decodeErr := callrecord.DecodeResponse(data)
				require.NoError(t, decodeErr)
				assert.Equal(t, response.Content, stored.Content)
				assert.Equal(t, response.Usage, stored.Usage)
				assert.Equal(t, response.StopReason, stored.StopReason)
				assert.Empty(t, stored.ToolCalls)
				assert.Equal(t, model.RequestInputUsage{Present: true, InputTokens: 10}, stored.RequestInputUsage)
				record.Content[*completion.Response] = data
			}
			encoded, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(encoded)
			require.NoError(t, err)
			assert.Equal(t, completion, *restored.Runs[0].Calls[0].Completion)
		})
	}
}

func recordingContinuationClient(t *testing.T, fails bool) (model.Client, *atomic.Int32) {
	t.Helper()
	sends := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			assert.NoError(t, json.MarshalWrite(w, map[string]any{"inputTokenLimit": 10000, "outputTokenLimit": 1000}))
			return
		}
		n := sends.Add(1)
		if fails && n == 3 {
			w.WriteHeader(http.StatusBadRequest)
			assert.NoError(t, json.MarshalWrite(w, map[string]any{"error": map[string]any{"code": 400, "message": "private failed hop", "status": "INVALID_ARGUMENT"}}))
			return
		}
		reason, token := genai.FinishReasonContinuation, []byte(strconv.FormatInt(int64(n), 10))
		if n == 3 {
			reason, token = genai.FinishReasonStop, nil
		}
		assert.NoError(t, json.MarshalWrite(w, &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "answer"}}}, FinishReason: reason, ContinuationToken: token}}, UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10 * n, CachedContentTokenCount: 2 * n, CandidatesTokenCount: 3, TotalTokenCount: 10*n + 3}}))
	}))
	t.Cleanup(server.Close)
	client, err := New().NewVertexGeminiModelClient(t.Context(), VertexConfig{APIKey: "test", DefaultModel: "gemini-test", MaxGenerationRequests: 3, HTTPClient: server.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}}})
	require.NoError(t, err)
	return client, sends
}

func TestCallRecordingExplicitUnmeasuredInput(t *testing.T) {
	store := &recordingTestStore{Store: inmem.New()}
	recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
	provider := &recordingTestClient{store: store, response: &model.Response{Usage: model.TokenUsage{InputTokens: 20, TotalTokens: 20}, RequestInputUsage: model.RequestInputUsage{Present: true}}}
	client := &callRecordingClient{inner: provider, recording: recording}
	_, err := client.Complete(recordingCallContext(1), &model.Request{})
	require.NoError(t, err)
	calls, err := store.ListCalls(t.Context(), "root", "run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	completion, err := callrecord.DecodeCompletion(calls[0].Completion)
	require.NoError(t, err)
	assert.Zero(t, completion.Facts.InputTokens)
	assert.Equal(t, 20, completion.Usage.InputTokens)
}
