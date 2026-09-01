package testutil

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ProviderConformanceCase proves one observable model-provider contract.
type ProviderConformanceCase func(t *testing.T)

// ProviderCapabilityConformance requires each adapter to prove either the
// supported behavior or an explicit unsupported contract for one capability.
// Exactly one case must be set.
type ProviderCapabilityConformance struct {
	Supported   ProviderConformanceCase
	Unsupported ProviderConformanceCase
}

// ProviderStreamingConformance describes either an explicit unsupported
// streaming contract or the required lifecycle and event-grammar cases for a
// streaming provider.
type ProviderStreamingConformance struct {
	Unsupported      ProviderConformanceCase
	SetupError       ProviderConformanceCase
	ReceiveError     ProviderConformanceCase
	ReceiveRateLimit ProviderCapabilityConformance
	StateMachine     ProviderConformanceCase
	EarlyEOF         ProviderConformanceCase
	PartialCancel    ProviderConformanceCase
	CloseError       ProviderConformanceCase
	Terminal         ProviderConformanceCase
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
	MultimodalInput               ProviderCapabilityConformance
	TypedThinking                 ProviderCapabilityConformance
	ExactTokenCounting            ProviderCapabilityConformance
	ToolNameRoundTrip             ProviderCapabilityConformance
	Streaming                     ProviderStreamingConformance
}

// RunProviderConformance validates and runs the provider-neutral behavioral
// matrix. Streaming providers must prove setup and receive errors, event
// ordering, premature termination, partial cancellation, close errors, and
// successful terminal behavior. Adapters without streaming must prove their
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
	capabilities := []struct {
		name       string
		capability ProviderCapabilityConformance
	}{
		{name: "multimodal input", capability: suite.MultimodalInput},
		{name: "typed thinking", capability: suite.TypedThinking},
		{name: "exact token counting", capability: suite.ExactTokenCounting},
		{name: "tool name round trip", capability: suite.ToolNameRoundTrip},
	}
	for _, capability := range capabilities {
		validateCapabilityConformance(t, suite.Provider, capability.name, capability.capability)
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
	for _, capability := range capabilities {
		runCapabilityConformance(t, capability.name, capability.capability)
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
		{name: "state machine", run: streaming.StateMachine},
		{name: "early EOF", run: streaming.EarlyEOF},
		{name: "partial cancellation", run: streaming.PartialCancel},
		{name: "close error", run: streaming.CloseError},
		{name: "terminal", run: streaming.Terminal},
	}
	if streaming.Unsupported != nil {
		if err := validateUnsupportedStreaming(streaming); err != nil {
			t.Fatalf("provider conformance %s: %s", provider, err)
		}
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
	validateCapabilityConformance(t, provider, "stream receive rate limit", streaming.ReceiveRateLimit)
}

func validateUnsupportedStreaming(streaming ProviderStreamingConformance) error {
	if streaming.Unsupported == nil {
		return nil
	}
	if streaming.ReceiveRateLimit.Supported != nil || streaming.ReceiveRateLimit.Unsupported != nil {
		return fmt.Errorf("streaming cannot be both unsupported and define receive rate limit")
	}
	return nil
}

func runStreamingConformance(t *testing.T, streaming ProviderStreamingConformance) {
	t.Helper()
	if streaming.Unsupported != nil {
		t.Run("streaming unsupported", streaming.Unsupported)
		return
	}
	t.Run("stream setup error", streaming.SetupError)
	t.Run("stream receive error", streaming.ReceiveError)
	runCapabilityConformance(t, "stream receive rate limit", streaming.ReceiveRateLimit)
	t.Run("stream state machine", streaming.StateMachine)
	t.Run("stream early EOF", streaming.EarlyEOF)
	t.Run("stream partial cancellation", streaming.PartialCancel)
	t.Run("stream close error", streaming.CloseError)
	t.Run("stream terminal", streaming.Terminal)
}

func validateCapabilityConformance(t *testing.T, provider, name string, capability ProviderCapabilityConformance) {
	t.Helper()
	if capability.Supported != nil && capability.Unsupported != nil {
		t.Fatalf("provider conformance %s: %s cannot be both supported and unsupported", provider, name)
	}
	if capability.Supported == nil && capability.Unsupported == nil {
		t.Fatalf("provider conformance %s: %s capability declaration is required", provider, name)
	}
}

func runCapabilityConformance(t *testing.T, name string, capability ProviderCapabilityConformance) {
	t.Helper()
	if capability.Supported != nil {
		t.Run(name+" supported", capability.Supported)
		return
	}
	t.Run(name+" unsupported", capability.Unsupported)
}
