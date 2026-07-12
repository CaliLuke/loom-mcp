package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderErrorPreservesStructuredContract(t *testing.T) {
	cause := errors.New("socket closed")
	err := NewProviderError(
		"bedrock",
		"converse_stream",
		503,
		ProviderErrorKindUnavailable,
		"service_unavailable",
		"try again",
		"request-1",
		true,
		cause,
	)

	assert.Equal(t, "bedrock", err.Provider())
	assert.Equal(t, "converse_stream", err.Operation())
	assert.Equal(t, 503, err.HTTPStatus())
	assert.Equal(t, ProviderErrorKindUnavailable, err.Kind())
	assert.Equal(t, "service_unavailable", err.Code())
	assert.Equal(t, "try again", err.Message())
	assert.Equal(t, "request-1", err.RequestID())
	assert.True(t, err.Retryable())
	assert.Equal(t, "bedrock unavailable 503 (converse_stream): service_unavailable: try again", err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestProviderErrorFormattingFallbacks(t *testing.T) {
	cases := []struct {
		name     string
		kind     ProviderErrorKind
		message  string
		cause    error
		expected string
	}{
		{name: "cause_message", kind: ProviderErrorKindUnknown, cause: errors.New("connection reset"), expected: "ollama unknown (request): connection reset"},
		{name: "generic_message", kind: ProviderErrorKindUnknown, expected: "ollama unknown (request): provider error"},
		{name: "explicit_message", kind: ProviderErrorKindInvalidRequest, message: "bad prompt", cause: errors.New("ignored"), expected: "ollama invalid_request (request): bad prompt"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := NewProviderError("ollama", "", 0, tt.kind, "", tt.message, "", false, tt.cause)
			assert.Equal(t, tt.expected, err.Error())
		})
	}
}

func TestNewProviderErrorRejectsMissingRequiredFields(t *testing.T) {
	assert.PanicsWithValue(t, "model: provider is required", func() {
		err := NewProviderError("", "request", 0, ProviderErrorKindUnknown, "", "", "", false, nil)
		assert.Nil(t, err)
	})
	assert.PanicsWithValue(t, "model: provider error kind is required", func() {
		err := NewProviderError("bedrock", "request", 0, "", "", "", "", false, nil)
		assert.Nil(t, err)
	})
}

func TestAsProviderErrorFindsWrappedError(t *testing.T) {
	providerErr := NewProviderError("gemini", "generate", 429, ProviderErrorKindRateLimited, "quota", "slow down", "", true, nil)
	wrapped := fmt.Errorf("complete model request: %w", providerErr)

	got, ok := AsProviderError(wrapped)
	require.True(t, ok)
	assert.Same(t, providerErr, got)

	got, ok = AsProviderError(errors.New("ordinary failure"))
	assert.False(t, ok)
	assert.Nil(t, got)
}
