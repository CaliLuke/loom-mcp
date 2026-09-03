package assistantapi

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratedAdapterIsSmallerThanIssue276Baseline(t *testing.T) {
	source, err := os.ReadFile("gen/mcp_assistant/adapter_server.go")
	require.NoError(t, err)

	const issue276Baseline = 2818
	lines := bytes.Count(source, []byte{'\n'})
	require.Less(t, lines, issue276Baseline, "generated adapter must be smaller than the #276 baseline")
}
