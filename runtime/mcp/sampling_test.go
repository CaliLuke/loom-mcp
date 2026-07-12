package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSampleUsesContextSampler(t *testing.T) {
	request := SampleRequest{
		Messages:     []SampleMessage{{Role: "user", Text: "Summarize this"}},
		SystemPrompt: "Be concise",
		MaxTokens:    64,
	}
	want := &SampleResult{
		Role:       "assistant",
		Text:       "A concise summary.",
		Model:      "fixture-model",
		StopReason: "endTurn",
	}
	sampler := samplerFunc(func(ctx context.Context, got SampleRequest) (*SampleResult, error) {
		require.Equal(t, request, got)
		return want, nil
	})

	result, err := Sample(WithSampler(context.Background(), sampler), request)

	require.NoError(t, err)
	require.Equal(t, want, result)
}

func TestSampleReturnsUnavailableWithoutContextSampler(t *testing.T) {
	_, err := Sample(context.Background(), SampleRequest{})

	require.ErrorIs(t, err, ErrSamplerUnavailable)
}

func TestSampleReturnsSamplerErrors(t *testing.T) {
	wantErr := errors.New("client rejected sampling")
	sampler := samplerFunc(func(context.Context, SampleRequest) (*SampleResult, error) {
		return nil, wantErr
	})

	_, err := Sample(WithSampler(context.Background(), sampler), SampleRequest{})

	require.ErrorIs(t, err, wantErr)
}

type samplerFunc func(context.Context, SampleRequest) (*SampleResult, error)

func (f samplerFunc) Sample(ctx context.Context, req SampleRequest) (*SampleResult, error) {
	return f(ctx, req)
}
