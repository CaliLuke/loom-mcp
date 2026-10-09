package mcp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingDefaultTransport struct {
	err error
}

func (transport failingDefaultTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func TestHTTPCallerPreservesCustomDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	sentinel := errors.New("custom default transport called")
	http.DefaultTransport = failingDefaultTransport{err: sentinel}
	_, err := NewHTTPCaller(t.Context(), HTTPOptions{Endpoint: "http://unused.invalid"})
	require.ErrorContains(t, err, sentinel.Error())
}

func TestMCPHTTPClientPreservesExplicitClient(t *testing.T) {
	explicit := &http.Client{Transport: failingDefaultTransport{}}
	require.Same(t, explicit, mcpHTTPClient(explicit))
}
