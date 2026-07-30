package testutil

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ProviderConformanceCase proves one observable model-provider contract.
type ProviderConformanceCase func(t *testing.T)

// ProviderStreamingConformance describes either an explicit unsupported
// streaming contract or the three required lifecycle cases for a streaming
// provider.
type ProviderStreamingConformance struct {
	Unsupported  ProviderConformanceCase
	SetupError   ProviderConformanceCase
	ReceiveError ProviderConformanceCase
	Terminal     ProviderConformanceCase
}

// ProviderConformanceSuite is the minimum behavioral matrix every model
// adapter must execute. Provider-specific SDK fixtures stay in the owning
// package; this suite standardizes only externally observable model.Client
// behavior.
type ProviderConformanceSuite struct {
	Provider                      string
	OrdinaryProviderError         ProviderConformanceCase
	RateLimit                     ProviderConformanceCase
	MalformedToolCall             ProviderConformanceCase
	Cancellation                  ProviderConformanceCase
	StructuredOutputAndToolChoice ProviderConformanceCase
	UsageAccounting               ProviderConformanceCase
	Streaming                     ProviderStreamingConformance
}

// RunProviderConformance validates and runs the provider-neutral behavioral
// matrix. Streaming providers must prove setup errors, receive errors, and
// successful terminal behavior; adapters without streaming must prove their
// explicit unsupported result.
func RunProviderConformance(t *testing.T, suite ProviderConformanceSuite) {
	t.Helper()
	if suite.Provider == "" {
		t.Fatal("provider conformance: provider name is required")
	}

	cases := []struct {
		name string
		run  ProviderConformanceCase
	}{
		{name: "ordinary provider error", run: suite.OrdinaryProviderError},
		{name: "rate limit", run: suite.RateLimit},
		{name: "malformed tool call", run: suite.MalformedToolCall},
		{name: "cancellation", run: suite.Cancellation},
		{name: "structured output and tool choice", run: suite.StructuredOutputAndToolChoice},
		{name: "usage accounting", run: suite.UsageAccounting},
	}
	for _, tc := range cases {
		if tc.run == nil {
			t.Fatalf("provider conformance %s: %s case is required", suite.Provider, tc.name)
		}
	}
	validateStreamingConformance(t, suite.Provider, suite.Streaming)

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
	runStreamingConformance(t, suite.Streaming)
}

// CollectStreamChunks drains a provider-neutral model stream through EOF and
// fails the test on any other receive error.
func CollectStreamChunks(t *testing.T, streamer model.Streamer) []model.Chunk {
	t.Helper()
	var chunks []model.Chunk
	for {
		chunk, err := streamer.Recv()
		if errors.Is(err, io.EOF) {
			return chunks
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
}

func validateStreamingConformance(t *testing.T, provider string, streaming ProviderStreamingConformance) {
	t.Helper()
	supportedCases := []struct {
		name string
		run  ProviderConformanceCase
	}{
		{name: "setup error", run: streaming.SetupError},
		{name: "receive error", run: streaming.ReceiveError},
		{name: "terminal", run: streaming.Terminal},
	}
	if streaming.Unsupported != nil {
		for _, tc := range supportedCases {
			if tc.run != nil {
				t.Fatalf("provider conformance %s: streaming cannot be both unsupported and define %s", provider, tc.name)
			}
		}
		return
	}
	for _, tc := range supportedCases {
		if tc.run == nil {
			t.Fatalf("provider conformance %s: streaming %s case is required", provider, tc.name)
		}
	}
}

func runStreamingConformance(t *testing.T, streaming ProviderStreamingConformance) {
	t.Helper()
	if streaming.Unsupported != nil {
		t.Run("streaming unsupported", streaming.Unsupported)
		return
	}
	t.Run("stream setup error", streaming.SetupError)
	t.Run("stream receive error", streaming.ReceiveError)
	t.Run("stream terminal", streaming.Terminal)
}
