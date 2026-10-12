package suite_test

import (
	"os"
	"path/filepath"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalibrationDriftRefusesReport(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 23))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gate"), 0750))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })
	require.NoError(t, root.WriteFile("gate/calibration_default.json", []byte("original"), 0600))
	f, err := suite.BuildFreeze(dir, "offline", []byte("original"), eval.VCS{Revision: "test"})
	require.NoError(t, err)
	require.NoError(t, suite.WriteFreeze(dir, f))
	_, err = suite.VerifyFreeze(dir, "offline")
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("gate/calibration_default.json", []byte("changed"), 0600))
	_, err = suite.VerifyFreeze(dir, "offline")
	assert.ErrorContains(t, err, "calibration")
}
