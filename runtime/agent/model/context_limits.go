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
	counter, info, parts, configuration := caps.Counter, caps.Info, caps.ToolResultParts, caps.ProviderConfiguration
	if isNilModelValue(counter) {
		counter = nil
	}
	if isNilModelValue(info) {
		info = nil
	}
	if isNilModelValue(parts) {
		parts = nil
	}
	if isNilModelValue(configuration) {
		configuration = nil
	}
	mask := 0
	if counter != nil {
		mask |= 1
	}
	if info != nil {
		mask |= 2
	}
	if parts != nil {
		mask |= 4
	}
	if configuration != nil {
		mask |= 8
	}
	return withCapabilityMask(client, Capabilities{Counter: counter, Info: info, ToolResultParts: parts, ProviderConfiguration: configuration}, mask)
}

func withCapabilityMask(client Client, caps Capabilities, mask int) Client {
	counter, info, parts := caps.Counter, caps.Info, caps.ToolResultParts
	if mask&8 != 0 {
		return withConfigurationMask(client, caps, mask)
	}
	switch mask {
	case 1:
		return struct {
			Client
			TokenCounter
		}{client, counter}
	case 2:
		return struct {
			Client
			ContextInfo
		}{client, info}
	case 3:
		return struct {
			Client
			TokenCounter
			ContextInfo
		}{client, counter, info}
	case 4:
		return struct {
			Client
			ToolResultPartsInfo
		}{client, parts}
	case 5:
		return struct {
			Client
			TokenCounter
			ToolResultPartsInfo
		}{client, counter, parts}
	case 6:
		return struct {
			Client
			ContextInfo
			ToolResultPartsInfo
		}{client, info, parts}
	case 7:
		return struct {
			Client
			TokenCounter
			ContextInfo
			ToolResultPartsInfo
		}{client, counter, info, parts}
	default:
		return client
	}
}

func withConfigurationMask(client Client, caps Capabilities, mask int) Client {
	counter, info, parts, configuration := caps.Counter, caps.Info, caps.ToolResultParts, caps.ProviderConfiguration
	switch mask {
	case 8:
		return struct {
			Client
			ProviderConfigurationInfo
		}{client, configuration}
	case 9:
		return struct {
			Client
			TokenCounter
			ProviderConfigurationInfo
		}{client, counter, configuration}
	case 10:
		return struct {
			Client
			ContextInfo
			ProviderConfigurationInfo
		}{client, info, configuration}
	case 11:
		return struct {
			Client
			TokenCounter
			ContextInfo
			ProviderConfigurationInfo
		}{client, counter, info, configuration}
	case 12:
		return struct {
			Client
			ToolResultPartsInfo
			ProviderConfigurationInfo
		}{client, parts, configuration}
	case 13:
		return struct {
			Client
			TokenCounter
			ToolResultPartsInfo
			ProviderConfigurationInfo
		}{client, counter, parts, configuration}
	case 14:
		return struct {
			Client
			ContextInfo
			ToolResultPartsInfo
			ProviderConfigurationInfo
		}{client, info, parts, configuration}
	case 15:
		return struct {
			Client
			TokenCounter
			ContextInfo
			ToolResultPartsInfo
			ProviderConfigurationInfo
		}{client, counter, info, parts, configuration}

	default:
		return client
	}
}
