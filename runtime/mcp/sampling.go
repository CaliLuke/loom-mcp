package mcp

import (
	"context"
	"errors"
)

// ErrSamplerUnavailable is returned when a context has no MCP sampler.
var ErrSamplerUnavailable = errors.New("mcp sampler unavailable")

// SampleMessage is a text message sent in an MCP sampling request.
type SampleMessage struct {
	Role string
	Text string
}

// SampleRequest describes a server-to-client MCP text sampling request.
type SampleRequest struct {
	Messages      []SampleMessage
	SystemPrompt  string
	MaxTokens     int64
	StopSequences []string
	Temperature   float64
	Metadata      any
}

// SampleResult describes the client's text sampling response.
type SampleResult struct {
	Role       string
	Text       string
	Model      string
	StopReason string
}

// Sampler requests text generation from an MCP client.
type Sampler interface {
	Sample(context.Context, SampleRequest) (*SampleResult, error)
}

type samplerKey struct{}

// WithSampler stores an MCP sampler in ctx.
func WithSampler(ctx context.Context, sampler Sampler) context.Context {
	return context.WithValue(ctx, samplerKey{}, sampler)
}

// SamplerFromContext returns the MCP sampler stored in ctx.
func SamplerFromContext(ctx context.Context) (Sampler, bool) {
	if ctx == nil {
		return nil, false
	}
	sampler, ok := ctx.Value(samplerKey{}).(Sampler)
	if !ok || sampler == nil {
		return nil, false
	}
	return sampler, true
}

// Sample requests text generation through the MCP sampler stored in ctx.
func Sample(ctx context.Context, req SampleRequest) (*SampleResult, error) {
	sampler, ok := SamplerFromContext(ctx)
	if !ok {
		return nil, ErrSamplerUnavailable
	}
	return sampler.Sample(ctx, req)
}
