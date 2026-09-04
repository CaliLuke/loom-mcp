package codegen

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMCPTransportObserverEmissions pins observability across generated options
// and the shared SDK bridge. Generated services carry only typed configuration;
// the bridge owns the common HTTP request span and observer middleware.
func TestMCPTransportObserverEmissions(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	sdkServer := filepath.Join(repoRoot, "integration_tests", "fixtures", "assistant", "gen", "mcp_assistant", "sdk_server.go")
	data, err := os.ReadFile(sdkServer) // #nosec G304 -- fixed checked-in fixture path
	require.NoError(t, err, "regenerated assistant fixture sdk_server.go must exist; run make regen-assistant-fixture")
	src := string(data)

	bridgeFile := filepath.Join(repoRoot, "runtime", "mcp", "sdkbridge", "bridge.go")
	bridgeData, err := os.ReadFile(bridgeFile) // #nosec G304 -- fixed checked-in runtime path
	require.NoError(t, err)
	bridgeSource := string(bridgeData)
	for _, needle := range []string{"transport.BeginHTTPRequest(", "transport.HTTPMiddleware(options.TransportObserver)(handler)"} {
		require.Containsf(t, bridgeSource, needle, "shared SDK bridge must contain %q", needle)
	}
	for _, needle := range []string{"TransportObserver transport.Observer", "sdkbridge.NewServer"} {
		require.Containsf(t, src, needle, "regenerated assistant sdk_server.go must contain %q", needle)
	}
	require.NotContains(t, src, "transport.BeginHTTPRequest(")
	require.NotContains(t, src, "transport.HTTPMiddleware(")
	for _, gone := range []string{"serveSDKEventsStream", "sdkSessionByID", "writeSDKNotificationEvent"} {
		require.NotContainsf(t, src, gone, "dead events/stream helper %q must stay removed from the SDK server (issue #131)", gone)
	}
}
