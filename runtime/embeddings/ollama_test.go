package embeddings_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/embeddings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/embed", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		var body struct {
			Model    string   `json:"model"`
			Input    []string `json:"input"`
			Truncate bool     `json:"truncate"`
		}
		assert.NoError(t, json.UnmarshalRead(r.Body, &body))
		assert.Equal(t, "model", body.Model)
		assert.Equal(t, []string{"first", "second"}, body.Input)
		assert.False(t, body.Truncate)
		_, err := w.Write([]byte(`{"embeddings":[[1,2],[3,4]]}`))
		assert.NoError(t, err)
	}))
	defer server.Close()
	client, err := embeddings.NewOllama(server.URL, "model", server.Client())
	require.NoError(t, err)
	vectors, err := client.EmbedBatch(t.Context(), []string{"first", "second"})
	require.NoError(t, err)
	assert.Equal(t, [][]float64{{1, 2}, {3, 4}}, vectors)
}

func TestOllamaCancellationAfterHeaders(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%t", deadline), func(t *testing.T) {
			headers := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				close(headers)
				<-r.Context().Done()
			}))
			defer server.Close()
			client, err := embeddings.NewOllama(server.URL, "model", server.Client())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
				want = context.DeadlineExceeded
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := client.Embed(ctx, "text"); result <- err }()
			<-headers
			if !deadline {
				cancel()
			}
			require.ErrorIs(t, <-result, want)
		})
	}
}

func TestOllamaResponseContract(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"single", `{"embeddings":[[1,2]]}`, 200, true},
		{"missing", `{"embeddings":[]}`, 200, false},
		{"extra", `{"embeddings":[[1],[2]]}`, 200, false},
		{"empty", `{"embeddings":[[]]}`, 200, false},
		{"zero", `{"embeddings":[[0,0]]}`, 200, false},
		{"malformed", `secret input`, 200, false},
		{"provider", `secret input`, 503, false},
		{"oversized", strings.Repeat("x", 16*1024*1024+1), 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, err := w.Write([]byte(test.body))
				assert.NoError(t, err)
			}))
			defer server.Close()
			client, err := embeddings.NewOllama(server.URL, "model", server.Client())
			require.NoError(t, err)
			vector, err := client.Embed(t.Context(), "secret input")
			if test.valid {
				require.NoError(t, err)
				assert.Equal(t, []float64{1, 2}, vector)
			} else {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "secret input")
				assert.Nil(t, vector)
			}
		})
	}
}

func TestOllamaConfigurationAndCancellation(t *testing.T) {
	for _, address := range []string{"", "file:///tmp/x", "://", "http://user:secret@localhost"} {
		_, err := embeddings.NewOllama(address, "model", nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret")
	}
	_, err := embeddings.NewOllama("http://localhost", " ", nil)
	require.Error(t, err)
	client, err := embeddings.NewOllama("http://localhost:1", "model", nil)
	require.NoError(t, err)
	_, err = client.EmbedBatch(t.Context(), nil)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.Embed(ctx, "text")
	require.ErrorIs(t, err, context.Canceled)
}
