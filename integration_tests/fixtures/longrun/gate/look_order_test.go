package suite_test

import (
	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

// V1 is an archive contract. It cannot open a v2 look, even when its own bytes
// and sidecar remain intact. V2 scheduling transitions have the PlanLook matrix.
func TestLookRefusesHistoricalFreezeBeforeHarness(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 29))
	f, err := historicalFreeze(dir, "offline", []byte("calibration"), eval.VCS{Revision: "test"})
	require.NoError(t, err)
	require.NoError(t, suite.WriteFreeze(dir, f))
	storeDir := t.TempDir()
	store, err := eval.Open(storeDir)
	require.NoError(t, err)
	before, err := os.ReadDir(storeDir)
	require.NoError(t, err)
	_, err = suite.Look(t.Context(), dir, "offline", "must-not-build", store, 1, 1)
	require.ErrorContains(t, err, "historical freeze")
	after, err := os.ReadDir(storeDir)
	require.NoError(t, err)
	require.Equal(t, before, after, "refusal must precede experiment and harness construction")
}
