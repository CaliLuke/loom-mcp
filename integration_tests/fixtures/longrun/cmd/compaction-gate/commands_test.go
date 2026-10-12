package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
