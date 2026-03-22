package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// RunPolicy emitted into registry registration.
func TestGolden_RunPolicy(t *testing.T) {
	design := testscenarios.RunPolicyBasic()
	files := buildAndGenerate(t, design)
	reg := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")
	require.Contains(t, reg, "Specs: specs.Specs")
	require.Contains(t, reg, "InterruptsAllowed")
	require.Contains(t, reg, "return nil")
}
