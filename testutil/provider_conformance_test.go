package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunProviderConformanceRunsRequiredCases(t *testing.T) {
	ran := make(map[string]bool)
	providerCase := func(name string) ProviderConformanceCase {
		return func(t *testing.T) {
			t.Helper()
			ran[name] = true
		}
	}

	RunProviderConformance(t, ProviderConformanceSuite{
		Provider:                      "test",
		OrdinaryProviderError:         providerCase("provider_error"),
		RateLimit:                     providerCase("rate_limit"),
		MalformedToolCall:             providerCase("malformed_tool_call"),
		Cancellation:                  providerCase("cancellation"),
		StructuredOutputAndToolChoice: providerCase("structured_output"),
		UsageAccounting:               providerCase("usage"),
		MultimodalInput: ProviderCapabilityConformance{
			Supported: providerCase("multimodal"),
		},
		TypedThinking: ProviderCapabilityConformance{
			Unsupported: providerCase("thinking_unsupported"),
		},
		ExactTokenCounting: ProviderCapabilityConformance{
			Supported: providerCase("token_counting"),
		},
		ToolNameRoundTrip: ProviderCapabilityConformance{
			Supported: providerCase("tool_name"),
		},
		Streaming: ProviderStreamingConformance{
			SetupError:    providerCase("stream_setup"),
			ReceiveError:  providerCase("stream_receive"),
			StateMachine:  providerCase("stream_state_machine"),
			EarlyEOF:      providerCase("stream_early_eof"),
			PartialCancel: providerCase("stream_partial_cancel"),
			CloseError:    providerCase("stream_close_error"),
			ReceiveRateLimit: ProviderCapabilityConformance{
				Supported: providerCase("stream_rate_limit"),
			},
			Terminal: providerCase("stream_terminal"),
		},
	})

	require.Equal(t, map[string]bool{
		"provider_error":        true,
		"rate_limit":            true,
		"malformed_tool_call":   true,
		"cancellation":          true,
		"structured_output":     true,
		"usage":                 true,
		"multimodal":            true,
		"thinking_unsupported":  true,
		"token_counting":        true,
		"tool_name":             true,
		"stream_setup":          true,
		"stream_receive":        true,
		"stream_rate_limit":     true,
		"stream_state_machine":  true,
		"stream_early_eof":      true,
		"stream_partial_cancel": true,
		"stream_close_error":    true,
		"stream_terminal":       true,
	}, ran)
}

func TestRunProviderConformanceRunsUnsupportedStreamingCase(t *testing.T) {
	ranUnsupported := false
	providerCase := func(*testing.T) {}

	RunProviderConformance(t, ProviderConformanceSuite{
		Provider:                      "test",
		OrdinaryProviderError:         providerCase,
		RateLimit:                     providerCase,
		MalformedToolCall:             providerCase,
		Cancellation:                  providerCase,
		StructuredOutputAndToolChoice: providerCase,
		UsageAccounting:               providerCase,
		MultimodalInput:               ProviderCapabilityConformance{Supported: providerCase},
		TypedThinking:                 ProviderCapabilityConformance{Supported: providerCase},
		ExactTokenCounting:            ProviderCapabilityConformance{Unsupported: providerCase},
		ToolNameRoundTrip:             ProviderCapabilityConformance{Supported: providerCase},
		Streaming: ProviderStreamingConformance{
			Unsupported: func(*testing.T) {
				ranUnsupported = true
			},
		},
	})

	require.True(t, ranUnsupported)
}

func TestValidateUnsupportedStreamingRejectsRateLimitDeclaration(t *testing.T) {
	err := validateUnsupportedStreaming(ProviderStreamingConformance{
		Unsupported: func(*testing.T) {},
		ReceiveRateLimit: ProviderCapabilityConformance{
			Supported: func(*testing.T) {},
		},
	})
	require.EqualError(t, err, "streaming cannot be both unsupported and define receive rate limit")
}
