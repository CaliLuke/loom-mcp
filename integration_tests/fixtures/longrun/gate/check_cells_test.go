package suite_test

import (
	"encoding/json"
	"os"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommittedCheckCells(t *testing.T) {
	data, err := os.ReadFile("calibration_default.json")
	require.NoError(t, err)
	var artifact suite.CalibrationArtifact
	require.NoError(t, json.Unmarshal(data, &artifact))
	require.Len(t, artifact.Checks.Cells, 2)
	assert.Equal(t, 200, artifact.Checks.Cells[0].Simulations)
	assert.Equal(t, 200, artifact.Checks.Cells[1].Simulations)
	result, err := gate.CalibrateChecks(t.Context(), artifact.Config)
	require.NoError(t, err)
	actual, err := json.Marshal(result)
	require.NoError(t, err)
	expected, err := json.Marshal(artifact.Checks)
	require.NoError(t, err)
	assert.Equal(t, string(expected), string(actual), "production-bootstrap check cells must reproduce byte for byte")
	if len(artifact.Report.Cells) > 0 {
		assert.Len(t, artifact.Report.Cells, 972)
		assert.Len(t, artifact.Report.Sensitivity, 2)
		assert.NotEmpty(t, artifact.Report.Targets)
	}
}
