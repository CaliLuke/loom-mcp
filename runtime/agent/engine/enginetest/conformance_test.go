package enginetest_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
)

// TestResolvedKnownGapFails exercises the public harness in a subprocess because
// an obsolete gap must fail testing.T, which cannot be cleared by the caller.
func TestResolvedKnownGapFails(t *testing.T) {
	const childMarker = "LOOM_MCP_TEST_RESOLVED_ENGINE_GAP"
	if os.Getenv(childMarker) == "1" {
		enginetest.RunConformance(t, enginetest.Factory{
			KnownGaps: map[string]string{"id_conflict": "deliberately obsolete: inmem already rejects active IDs"},
			New: func(_ *testing.T) enginetest.Fixture {
				return enginetest.Fixture{Engine: inmem.New(), TaskQueue: "obsolete-gap", ActivityQueue: "activities"}
			},
		})
		return
	}
	// This binary hosts only the in-memory ID-conflict scenario; no server starts.
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestResolvedKnownGapFails$/^id_conflict$", "-test.short", "-test.timeout=10s") // #nosec G204 -- re-executes this test binary with fixed arguments
	cmd.Env = append(os.Environ(), childMarker+"=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "obsolete gap must fail the harness")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Contains(t, string(output), "known gap no longer reproduces: remove it from the list")
}
