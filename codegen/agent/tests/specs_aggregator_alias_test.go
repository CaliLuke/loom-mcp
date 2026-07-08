package tests

import (
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// TestSpecsAggregatorToolsetNamedPolicy verifies that a toolset named "policy"
// does not collide with the fixed runtime policy import in the generated
// specs aggregator.
func TestSpecsAggregatorToolsetNamedPolicy(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ToolsetNamedPolicy())

	content := fileContent(t, files, "gen/alpha/agents/helper/specs/specs.go")
	require.Contains(t, content, `"github.com/CaliLuke/loom-mcp/runtime/agent/policy"`,
		"runtime policy import must be preserved")
	require.Contains(t, content, `policyspecs "`,
		"toolset named 'policy' must be aliased away from the runtime policy import")
	require.Contains(t, content, "policyspecs.SpecEvaluate")
	assertUniqueImportAliases(t, content)
}

// assertUniqueImportAliases parses the source and requires every import to
// bind a distinct package identifier so the file compiles.
func assertUniqueImportAliases(t *testing.T, src string) {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "specs.go", src, parser.ImportsOnly)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Imports)
	seen := make(map[string]string, len(parsed.Imports))
	for _, imp := range parsed.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		name := path.Base(importPath)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		prev, dup := seen[name]
		require.Falsef(t, dup, "import identifier %q bound by both %q and %q", name, prev, importPath)
		seen[name] = importPath
	}
}
