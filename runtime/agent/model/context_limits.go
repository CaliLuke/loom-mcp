package model

import "context"

// ContextLimits describes a model's token limits. Zero fields are unknown,
// except ImageTokens: zero selects the measurement default of 1,500 tokens.
type ContextLimits struct {
	// InvalidOverride marks a merged runtime override with unusable provider limits.
	InvalidOverride bool `json:"invalid_override,omitzero"`
	// ContextWindow is the shared token window used for input/output budgeting.
	ContextWindow int
	// MaxOutputTokens is the provider's maximum completion cap.
	MaxOutputTokens int
	// DefaultMaxTokens is the configured completion cap sent when MaxTokens is zero.
	DefaultMaxTokens int
	// ImageTokens is the per-image measurement cost; zero selects 1,500.
	ImageTokens int
}

// ContextInfo is an optional provider/client capability. Lookup resolves the
// request's model and returns its ID even when limits are unknown. The bool
// reports whether ContextWindow is known. Network lookups belong in activities,
// never workflow code.
type ContextInfo interface {
	// ContextLimits resolves the request model and reports its limits and known window.
	ContextLimits(ctx context.Context, req *Request) (ContextLimits, string, bool)
}

// WithCapabilities constructs a decorated Client preserving exactly the optional
// capabilities supplied by the caller. Keeping method-set selection here avoids
// duplicating capability-specific wrapper types in client middleware.
func WithCapabilities(client Client, counter TokenCounter, info ContextInfo) Client {
	return WithCapabilitySet(client, Capabilities{Counter: counter, Info: info})
}

// WithCapabilitySet preserves exactly the supplied optional capabilities.
func WithCapabilitySet(client Client, caps Capabilities) Client {
	counter, info, parts := caps.Counter, caps.Info, caps.ToolResultParts
	if isNilModelValue(counter) {
		counter = nil
	}
	if isNilModelValue(info) {
		info = nil
	}
	if isNilModelValue(parts) {
		parts = nil
	}
	switch {
	case counter != nil && info != nil && parts != nil:
		return struct {
			Client
			TokenCounter
			ContextInfo
			ToolResultPartsInfo
		}{client, counter, info, parts}
	case counter != nil && parts != nil:
		return struct {
			Client
			TokenCounter
			ToolResultPartsInfo
		}{client, counter, parts}
	case info != nil && parts != nil:
		return struct {
			Client
			ContextInfo
			ToolResultPartsInfo
		}{client, info, parts}
	case parts != nil:
		return struct {
			Client
			ToolResultPartsInfo
		}{client, parts}
	default:
		return withTextCapabilities(client, counter, info)
	}
}

// withTextCapabilities selects the original counting/limits method sets.
func withTextCapabilities(client Client, counter TokenCounter, info ContextInfo) Client {
	switch {
	case counter != nil && info != nil:
		return struct {
			Client
			TokenCounter
			ContextInfo
		}{client, counter, info}
	case counter != nil:
		return struct {
			Client
			TokenCounter
		}{client, counter}
	case info != nil:
		return struct {
			Client
			ContextInfo
		}{client, info}
	default:
		return client
	}
}
