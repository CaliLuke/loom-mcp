package agentfeatures_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	validationregistry "example.com/agentfeatures/gen/features/registry/validation_registry"
	"github.com/stretchr/testify/require"
)

type capabilitiesContextKey struct{}

type capabilitiesRoundTripFunc func(*http.Request) (*http.Response, error)

func (f capabilitiesRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGeneratedRegistryCapabilitiesPreservesContextAndReturnsErrors(t *testing.T) {
	t.Run("preserves caller context", func(t *testing.T) {
		var seenValue any
		transport := capabilitiesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenValue = req.Context().Value(capabilitiesContextKey{})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"SemanticSearch":true}`)),
				Header:     make(http.Header),
			}, nil
		})
		client := validationregistry.NewClient(
			validationregistry.WithHTTPClient(&http.Client{Transport: transport}),
			validationregistry.WithRetry(0, time.Millisecond),
		)
		ctx := context.WithValue(context.Background(), capabilitiesContextKey{}, "trace-value")

		caps, err := client.Capabilities(ctx)
		require.NoError(t, err)
		require.Equal(t, "trace-value", seenValue)
		require.True(t, caps.SemanticSearch)
		require.True(t, caps.KeywordSearch)
	})

	t.Run("returns registry failures", func(t *testing.T) {
		transport := capabilitiesRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("registry unavailable")),
				Header:     make(http.Header),
			}, nil
		})
		client := validationregistry.NewClient(
			validationregistry.WithHTTPClient(&http.Client{Transport: transport}),
			validationregistry.WithRetry(0, time.Millisecond),
		)

		caps, err := client.Capabilities(context.Background())
		require.Equal(t, validationregistry.SearchCapabilities{}, caps)
		require.ErrorContains(t, err, "status 503")
	})
}
