package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoricalCalibrationCannotAuthorizeCurrentCommands(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("LOOM_LIVE_MODEL", "0")
	dir, err := filepath.Abs("../..")
	require.NoError(t, err)
	for _, command := range [][]string{{"pilot"}, {"look", "1"}, {"calibrate"}, {"freeze", "--inventory", filepath.Join(dir, "gate/inventory_offline.json")}} {
		t.Run(command[0], func(t *testing.T) {
			store := filepath.Join(t.TempDir(), "unopened")
			args := append(slices.Clone(command), "--dir", dir, "--config", "offline", "--out", store)
			var stdout, stderr bytes.Buffer
			require.Equal(t, 1, run(t.Context(), args, &stdout, &stderr))
			assert.Contains(t, stderr.String(), "current executable source differs")
			assert.Empty(t, stdout.String())
			_, err := os.Stat(store)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestLiveCommandsFailBeforeProviderConstruction(t *testing.T) {
	t.Setenv("LOOM_LIVE_MODEL", "0")
	for _, command := range [][]string{{"pilot"}, {"look", "1"}} {
		var stdout, stderr bytes.Buffer
		args := slices.Clone(command)
		args = append(args, "--config", "anthropic", "--dir", t.TempDir())
		assert.Equal(t, 1, run(t.Context(), args, &stdout, &stderr))
		assert.Contains(t, stderr.String(), "live model disabled")
	}
}
func TestUnknownConfigurationCannotSelectPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 1, run(t.Context(), []string{"report", "--config", "../../outside"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "unknown provider configuration")
}

func TestOfflineCannotLabelLivePilotOrLook(t *testing.T) {
	for _, command := range [][]string{{"pilot"}, {"look", "1"}} {
		var stdout, stderr bytes.Buffer
		args := append(slices.Clone(command), "--offline", "--config", "anthropic", "--dir", t.TempDir())
		assert.Equal(t, 1, run(t.Context(), args, &stdout, &stderr))
		assert.Contains(t, stderr.String(), "--offline requires --config offline")
	}
}

func TestK19cContractEvidenceCommandsRequireExplicitInputs(t *testing.T) {
	for _, tc := range []struct{ command, message string }{{"inventory", "complete-store declaration"}, {"archive-v1", "--inventory"}, {"freeze", "--inventory"}} {
		t.Run(tc.command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			dir := t.TempDir()
			assert.Equal(t, 1, run(t.Context(), []string{tc.command, "--dir", dir}, &stdout, &stderr))
			assert.Contains(t, stderr.String(), tc.message)
			_, err := os.Stat(filepath.Join(dir, ".loom-evals"))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestCalibrationProgressUsesStderrAndReportsFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"calibrate", "--dir", t.TempDir(), "--config", "offline"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "phase=\"preflight\"")
	assert.Contains(t, stderr.String(), "status=failed")
	assert.NotContains(t, stderr.String(), "status=complete")
}
