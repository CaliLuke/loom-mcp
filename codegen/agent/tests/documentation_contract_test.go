package tests

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGeneratedArtifactsDocumentationMatchesAgentFiles(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ToolSpecsMinimal())
	require.NotEmpty(t, fileContent(t, files, "gen/calc/agents/scribe/agent.go"))
	require.NotEmpty(t, fileContent(t, files, "gen/calc/agents/scribe/config.go"))
	require.NotEmpty(t, fileContent(t, files, "gen/calc/agents/scribe/registry.go"))

	doc, err := os.ReadFile("../../../docs/dsl.md")
	require.NoError(t, err)
	require.Contains(t, string(doc), "`agent.go`")
	require.Contains(t, string(doc), "`config.go`")
	require.Contains(t, string(doc), "`registry.go`")
	require.NotContains(t, string(doc), "- `workflow.go`")
	require.NotContains(t, string(doc), "- `activities.go`")
}
