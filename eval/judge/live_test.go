package judge

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/features/model/anthropic"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveAnthropicCalibration(t *testing.T) {
	const modelID = "claude-sonnet-4-5-20250929"
	agenttest.SkipUnlessLive(t, modelID)
	if testing.Short() {
		t.Skip("live calibration is outside the short-test gate")
	}
	provider, err := anthropic.NewFromAPIKey(os.Getenv("ANTHROPIC_API_KEY"), modelID)
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	j, err := New(Config{Client: client, ModelID: modelID, MaxOutputTokens: 1024, Live: true, Cache: NewMemoryCache(), Recalibrate: true})
	require.NoError(t, err)
	calibration, err := j.Calibrate(t.Context())
	require.NoError(t, err)
	assert.True(t, calibration.Passed, "%+v", calibration.Examples)
}
