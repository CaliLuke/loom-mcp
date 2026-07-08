package ollama

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewDefaultHTTPClientUsesHeaderTimeoutOnly(t *testing.T) {
	client, err := New(Options{
		ServerURL:    "http://localhost:11434",
		DefaultModel: "llama3.1",
	})
	require.NoError(t, err)
	require.Zero(t, client.httpClient.Timeout)

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, defaultResponseHeaderTimeout, transport.ResponseHeaderTimeout)
}

func TestNewDefaultHTTPClientAppliesConfiguredHeaderTimeout(t *testing.T) {
	client, err := New(Options{
		ServerURL:    "http://localhost:11434",
		DefaultModel: "llama3.1",
		Timeout:      time.Second,
	})
	require.NoError(t, err)
	require.Zero(t, client.httpClient.Timeout)

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, time.Second, transport.ResponseHeaderTimeout)
}
