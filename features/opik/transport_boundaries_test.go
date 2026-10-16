package opik_test

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type exportTransport func(*http.Request) (*http.Response, error)
type failedResponseBody struct{ readErr, closeErr error }

func TestExportTransportFailuresRemainRetryableWithSavedIdentities(t *testing.T) {
	boundaryErr := errors.New("transport boundary unavailable")
	tests := []struct {
		name, method, path string
		body               io.ReadCloser
		transportErr       error
		message            string
	}{
		{"send", http.MethodPut, "/datasets/items", nil, boundaryErr, "transport boundary"},
		{"read", http.MethodPut, "/datasets/items", &failedResponseBody{readErr: boundaryErr}, nil, "transport boundary"},
		{"close", http.MethodPut, "/datasets/items", &failedResponseBody{closeErr: boundaryErr}, nil, "transport boundary"},
		{"malformed response", http.MethodGet, "/experiments/", io.NopCloser(strings.NewReader(`{`)), nil, "opik response"},
		{"trace unavailable", http.MethodGet, "/traces/", io.NopCloser(strings.NewReader(`{`)), nil, "opik response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			failed := false
			transport := exportTransport(func(request *http.Request) (*http.Response, error) {
				if !failed && request.Method == tt.method && strings.Contains(request.URL.Path, tt.path) {
					failed = true
					if tt.transportErr != nil {
						return nil, tt.transportErr
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: tt.body}, nil
				}
				return http.DefaultTransport.RoundTrip(request)
			})
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", HTTPClient: &http.Client{Transport: transport}, Policy: sharingPolicy()})
			require.NoError(t, err)
			experiment := storedExperiment(t)
			first, err := client.Export(t.Context(), experiment)
			require.ErrorContains(t, err, tt.message)
			if tt.transportErr != nil || tt.name == "read" || tt.name == "close" {
				require.ErrorIs(t, err, boundaryErr)
			}
			require.NotNil(t, first)
			second, err := client.Export(t.Context(), experiment)
			require.NoError(t, err)
			assert.Equal(t, first.ExperimentID, second.ExperimentID)
			assert.Equal(t, first.Attempts[0].TraceID, second.Attempts[0].TraceID)
		})
	}
}

func TestExportFeedbackFailureResumesSameAttempt(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	fail := true
	transport := exportTransport(func(request *http.Request) (*http.Response, error) {
		if fail && strings.HasSuffix(request.URL.Path, "/feedback-scores") {
			fail = false
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", HTTPClient: &http.Client{Transport: transport}, Policy: sharingPolicy()})
	require.NoError(t, err)
	experiment := storedExperiment(t)
	first, err := client.Export(t.Context(), experiment)
	require.ErrorContains(t, err, "HTTP 503")
	second, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.Equal(t, first.Attempts[0].ItemID, second.Attempts[0].ItemID)
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/experiments/items"))
}

func TestExportRejectsOpaqueRemoteMetadata(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	remote.mu.Lock()
	key := "/v1/private/traces/" + result.Attempts[0].TraceID
	remote.resources[key] = jsontext.Value(`{"id":"` + result.Attempts[0].TraceID + `","metadata":"unstructured"}`)
	remote.mu.Unlock()
	_, err = client.Export(t.Context(), experiment)
	require.ErrorContains(t, err, "invalid existing metadata")
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/traces"))
}

func TestExportStoredMissingFilesRemainErrors(t *testing.T) {
	for _, kind := range []string{"attempt log", "capture plan"} {
		t.Run(kind, func(t *testing.T) {
			store, ref := pendingStoredExperiment(t)
			source, err := store.Load(ref)
			require.NoError(t, err)
			if kind == "attempt log" {
				require.NoError(t, os.Remove(filepath.Join(source.Dir, "attempts.jsonl")))
			} else {
				require.NoError(t, os.Mkdir(filepath.Join(source.Dir, "evidence"), 0700))
			}
			client, err := opik.New(opik.Config{Endpoint: "http://unused.invalid", Project: "review", Policy: sharingPolicy()})
			require.NoError(t, err)
			result, err := client.ExportStored(t.Context(), store, ref)
			require.ErrorIs(t, err, os.ErrNotExist)
			assert.Nil(t, result)
		})
	}
}

func TestExportMalformedManifestDoesNotAllocateReplacement(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	_, err = client.Export(t.Context(), experiment)
	require.NoError(t, err)
	paths, err := filepath.Glob(filepath.Join(experiment.Dir, "opik", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoError(t, os.WriteFile(paths[0], []byte(`{`), 0600))
	before := len(remote.requests)
	_, err = client.Export(t.Context(), experiment)
	require.ErrorContains(t, err, "opik manifest")
	assert.Len(t, remote.requests, before)
}

func (transport exportTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func (body *failedResponseBody) Read([]byte) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}
	return 0, io.EOF
}

func (body *failedResponseBody) Close() error {
	return body.closeErr
}
