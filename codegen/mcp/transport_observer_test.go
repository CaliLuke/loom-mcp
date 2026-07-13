package codegen

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMCPTransportObserverEmissions pins the observability/transport
// integration in the regenerated assistant fixture. This is a source-text
// contract on the checked-in fixture so that a future regeneration that
// drops an emission site or shifts the alias would fail here.
//
// Contract history: the SDK server previously carried session-validation and
// events-stream emissions (TransportMCP, ReasonMCPSession*,
// ReasonMCPEventsStreamWriteFailed, BeginRequest, and 10 adapter.log calls).
// All of them lived inside the dead events/stream helpers removed for issue
// #131 (the SDK transport advertised a capability whose handler was
// unreachable), so they were dead logging and were removed with the dead
// code. The surviving SDK-server emission is the HTTP request span.
func TestMCPTransportObserverEmissions(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	sdkServer := filepath.Join(repoRoot, "integration_tests", "fixtures", "assistant", "gen", "mcp_assistant", "sdk_server.go")
	data, err := os.ReadFile(sdkServer) // #nosec G304 -- path computed from runtime.Caller, points to the checked-in fixture under test
	require.NoError(t, err, "regenerated assistant fixture sdk_server.go must exist; run `make regen-assistant-fixture`")
	src := string(data)

	for _, needle := range []string{
		`"github.com/CaliLuke/loom/observability/transport"`,
		"transport.BeginHTTPRequest(",
		"TransportObserver transport.Observer",
		"transport.HTTPMiddleware(transportObserver)(handler)",
	} {
		require.Containsf(t, src, needle, "regenerated assistant sdk_server.go must contain %q", needle)
	}

	for _, gone := range []string{
		"serveSDKEventsStream",
		"sdkSessionByID",
		"writeSDKNotificationEvent",
	} {
		require.NotContainsf(t, src, gone, "dead events/stream helper %q must stay removed from the SDK server (issue #131)", gone)
	}
}
