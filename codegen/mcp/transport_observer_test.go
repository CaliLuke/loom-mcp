package codegen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMCPTransportObserverEmissions pins the observability/transport
// integration in the regenerated assistant fixture. This is a source-text
// contract on the checked-in fixture so that a future regeneration that
// drops an emission site or shifts the alias would fail here.
//
// The plan also requires the existing 10 adapter.log calls to remain
// unchanged; if a deliberate logging-contract change happens, the expected
// count below must be updated in the same review that justifies it.
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
		"transport.TransportMCP",
		"transport.ReasonMCPSessionMissing",
		"transport.ReasonMCPSessionNotFound",
		"transport.ReasonMCPSessionPrincipalMismatch",
		"transport.ReasonMCPEventsStreamWriteFailed",
		"transport.BeginHTTPRequest(",
		"transport.BeginRequest(",
	} {
		require.Containsf(t, src, needle, "regenerated assistant sdk_server.go must contain %q", needle)
	}

	const expectedAdapterLogCalls = 10
	got := strings.Count(src, "adapter.log(")
	require.Equalf(t, expectedAdapterLogCalls, got, "adapter.log( call count drifted; update expectedAdapterLogCalls only with a reviewed logging-contract change")
}
