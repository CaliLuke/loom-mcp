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
		Streaming: ProviderStreamingConformance{
			SetupError:   providerCase("stream_setup"),
			ReceiveError: providerCase("stream_receive"),
			Terminal:     providerCase("stream_terminal"),
		},
	})

	require.Equal(t, map[string]bool{
		"provider_error":      true,
		"rate_limit":          true,
		"malformed_tool_call": true,
		"cancellation":        true,
		"structured_output":   true,
		"usage":               true,
		"stream_setup":        true,
		"stream_receive":      true,
		"stream_terminal":     true,
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
		Streaming: ProviderStreamingConformance{
			Unsupported: func(*testing.T) {
				ranUnsupported = true
			},
		},
	})

	require.True(t, ranUnsupported)
}
