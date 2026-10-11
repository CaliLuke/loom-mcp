package expect_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallPayloadTypeMismatch(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "build", "./testdata/mismatch")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "fixture must reject the wrong predicate type")
	assert.Contains(t, string(output), "Other")
	assert.Contains(t, string(output), "EchoPayload")
	assert.Contains(t, string(output), "does not match")
}
