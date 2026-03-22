package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/CaliLuke/loom-mcp/testutil"
)

// Verifies aggregated specs import and merge multiple per-toolset packages.
func TestGolden_MultiToolset_Aggregate(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.MultiToolset())
	content := fileContent(t, files, "gen/alpha/agents/scribe/specs/specs.go")
	testutil.AssertGo(t, "testdata/golden/multi_toolset/specs.go.golden", content)
}
