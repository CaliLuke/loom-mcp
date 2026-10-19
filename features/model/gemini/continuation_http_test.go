package gemini_test

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type continuationHTTPHop struct {
	response *genai.GenerateContentResponse
	status   int
	message  string
}

type continuationHTTP struct {
	mu       sync.Mutex
	hops     []continuationHTTPHop
	requests []map[string]any
}

type cancelHopTransport struct {
	inner  http.RoundTripper
	cancel context.CancelFunc
}

type cancelHopBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func newContinuationClient(t *testing.T, limit int, backend genai.Backend, hops []continuationHTTPHop, cancel context.CancelFunc) (model.Client, *continuationHTTP) {
	t.Helper()
	return newContinuationClientWithRetries(t, limit, backend, hops, cancel, 1)
}

func newContinuationClientWithRetries(t *testing.T, limit int, backend genai.Backend, hops []continuationHTTPHop, cancel context.CancelFunc, attempts int32) (model.Client, *continuationHTTP) {
	t.Helper()
	script := &continuationHTTP{hops: hops}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			assert.NoError(t, r.Body.Close())
		}()
		var body map[string]any
		if !assert.NoError(t, json.UnmarshalRead(r.Body, &body)) {
			return
		}
		script.mu.Lock()
		defer script.mu.Unlock()
		script.requests = append(script.requests, body)
		w.Header().Set("Content-Type", "application/json")
		if len(script.requests) > len(script.hops) {
			w.WriteHeader(http.StatusBadRequest)
			assert.NoError(t, json.MarshalWrite(w, map[string]any{"error": map[string]any{"code": 400, "message": "unexpected hop", "status": "INVALID_ARGUMENT"}}))
			return
		}
		hop := script.hops[len(script.requests)-1]
		if hop.status != 0 {
			w.WriteHeader(hop.status)
			if hop.message == "" {
				hop.message = "private-provider-error"
			}
			assert.NoError(t, json.MarshalWrite(w, map[string]any{"error": map[string]any{"code": hop.status, "message": hop.message, "status": "INVALID_ARGUMENT"}}))
			return
		}
		assert.NoError(t, json.MarshalWrite(w, hop.response))
	}))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	if cancel != nil {
		httpClient.Transport = cancelHopTransport{inner: httpClient.Transport, cancel: cancel}
	}
	sdk, err := genai.NewClient(t.Context(), &genai.ClientConfig{APIKey: "test", Backend: backend, HTTPClient: httpClient, HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(attempts)}}})
	require.NoError(t, err)
	provider, err := geminimodel.New(geminimodel.Options{Client: sdk.Models, DefaultModel: "gemini-test", MaxGenerationRequests: limit})
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	return client, script
}

func continuationHop(reason genai.FinishReason, token, text string) continuationHTTPHop {
	return continuationHTTPHop{response: &genai.GenerateContentResponse{
		Candidates:    []*genai.Candidate{{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}, FinishReason: reason, ContinuationToken: []byte(token)}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CachedContentTokenCount: 2, CandidatesTokenCount: 3, TotalTokenCount: 13},
	}}
}

func (s *continuationHTTP) captured() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

func (t cancelHopTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.inner.RoundTrip(request)
	if response != nil {
		response.Body = &cancelHopBody{ReadCloser: response.Body, cancel: t.cancel}
	}
	return response, err
}

func (b *cancelHopBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if err != nil {
		b.cancel()
	}
	return n, err
}
