// Package testhelpers provides shared test utilities for codegen packages.
package testhelpers

import (
	"bytes"
	"path/filepath"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom-mcp/testutil"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// SetupEvalRoots initializes and registers eval roots for testing.
func SetupEvalRoots(t *testing.T) {
	t.Helper()
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsExpr.Root = &agentsExpr.RootExpr{}
	require.NoError(t, eval.Register(agentsExpr.Root))
}

// RunDesign prepares roots for generation by executing the DSL.
func RunDesign(t *testing.T, design func()) (string, []eval.Root) {
	t.Helper()
	SetupEvalRoots(t)
	ok := eval.Execute(design, nil)
	require.True(t, ok, eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	return "github.com/CaliLuke/loom-mcp", []eval.Root{goaexpr.Root, agentsExpr.Root}
}

// BuildAndGenerate executes the DSL, runs codegen and returns generated files.
func BuildAndGenerate(t *testing.T, design func()) []*gcodegen.File {
	t.Helper()
	genpkg, roots := RunDesign(t, design)
	files, err := codegen.Generate(genpkg, roots, nil)
	require.NoError(t, err)
	return files
}

// BuildAndGenerateWithPkg executes the DSL with a custom package path.
func BuildAndGenerateWithPkg(t *testing.T, genpkg string, design func()) []*gcodegen.File {
	t.Helper()
	SetupEvalRoots(t)
	ok := eval.Execute(design, nil)
	require.True(t, ok, eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	files, err := codegen.Generate(genpkg, []eval.Root{goaexpr.Root, agentsExpr.Root}, nil)
	require.NoError(t, err)
	return files
}

// BuildAndGenerateExample executes the DSL, runs example-phase codegen and returns files.
func BuildAndGenerateExample(t *testing.T, design func()) []*gcodegen.File {
	t.Helper()
	genpkg, roots := RunDesign(t, design)
	files, err := codegen.GenerateExample(genpkg, roots, nil)
	require.NoError(t, err)
	return files
}

// FileContent locates a generated file by path (slash-normalized) and returns the concatenated sections.
func FileContent(t *testing.T, files []*gcodegen.File, wantPath string) string {
	t.Helper()
	normWant := filepath.ToSlash(wantPath)
	for _, f := range files {
		if filepath.ToSlash(f.Path) != normWant {
			continue
		}
		var buf bytes.Buffer
		for _, s := range f.AllSections() {
			err := s.Write(&buf)
			require.NoErrorf(t, err, "render section %s", s.SectionName())
		}
		content := buf.String()
		require.NotEmptyf(t, content, "empty content for %s", wantPath)
		return content
	}
	require.Failf(t, "not found", "generated file not found: %s", wantPath)
	return "" // unreachable
}

// FileExists checks if a file exists in the generated files.
func FileExists(files []*gcodegen.File, wantPath string) bool {
	normWant := filepath.ToSlash(wantPath)
	for _, f := range files {
		if filepath.ToSlash(f.Path) == normWant {
			return true
		}
	}
	return false
}

// FindFile locates a generated file by path (slash-normalized).
func FindFile(files []*gcodegen.File, wantPath string) *gcodegen.File {
	normWant := filepath.ToSlash(wantPath)
	for _, f := range files {
		if filepath.ToSlash(f.Path) == normWant {
			return f
		}
	}
	return nil
}

// AssertGoldenGo compares content as Go source with the golden file path
// relative to testdata/golden/<scenario>/...
func AssertGoldenGo(t *testing.T, scenario string, name string, content string) {
	t.Helper()
	p := filepath.Join("testdata", "golden", scenario, name)
	testutil.AssertGo(t, p, content)
}

// AssertGoldenGoAbs compares content as Go source with an absolute golden file path.
func AssertGoldenGoAbs(t *testing.T, goldenPath string, content string) {
	t.Helper()
	testutil.AssertGo(t, goldenPath, content)
}
