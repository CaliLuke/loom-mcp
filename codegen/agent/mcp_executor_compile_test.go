package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/stretchr/testify/require"
)

// TestGeneratedFromMCPToolsetCompiles runs the agent generator on a minimal
// FromMCP design and compiles the full generated output with `go build` in a
// throwaway module that resolves loom-mcp to this repository. This guards the
// generated mcp_executor.go (and every other emitted file) against emitting
// code that only fails at user compile time, such as tools.Ident/string
// mismatches or codec misuse.
func TestGeneratedFromMCPToolsetCompiles(t *testing.T) {
	roots := runAliasedMCPDesign(t)
	files, err := codegen.Generate("example.com/fmcp/gen", roots, nil)
	require.NoError(t, err)

	// Sanity-check the executor contract before spending time on the build:
	// tools.Ident is converted once, and the payload is decoded (validated)
	// with the payload codec before being re-encoded for the MCP caller.
	executor := testhelpers.FileContent(t, files, "gen/alpha/agents/scribe/calc_remote/mcp_executor.go")
	require.Contains(t, executor, "name := string(full)")
	require.Contains(t, executor, `raw = []byte("{}")`)
	require.Contains(t, executor, "decoded, err := pc.FromJSON(raw)")
	require.Contains(t, executor, "payload, err := pc.ToJSON(decoded)")
	require.NotContains(t, executor, "pc.ToJSON(call.Payload)",
		"call.Payload is raw JSON and must not be passed to the typed encoder")

	moduleDir := writeGeneratedModule(t, files)

	build := exec.CommandContext(t.Context(), "go", "build", "./...")
	build.Dir = moduleDir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "generated FromMCP output does not compile:\n%s", string(out))
}

// writeGeneratedModule materializes the generated .go files into a temp module
// whose go.mod mirrors this repository's dependency set and replaces loom-mcp
// with the local checkout so the build runs offline against the working tree.
func writeGeneratedModule(t *testing.T, files []*gcodegen.File) string {
	t.Helper()

	moduleDir := t.TempDir()
	for _, f := range files {
		// Render applies the production finalizers (gofmt + import pruning),
		// matching exactly what `loom gen` writes to disk.
		_, err := f.Render(moduleDir)
		require.NoErrorf(t, err, "render %s", f.Path)
	}

	repoRoot := repositoryRoot(t)
	goMod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod")) // #nosec G304 -- repoRoot is resolved from this test file's location.
	require.NoError(t, err)
	modContent := strings.Replace(
		string(goMod),
		"module github.com/CaliLuke/loom-mcp",
		"module example.com/fmcp",
		1,
	)
	modContent += "\nrequire github.com/CaliLuke/loom-mcp v0.0.0-00010101000000-000000000000\n" +
		"\nreplace github.com/CaliLuke/loom-mcp => " + repoRoot + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(modContent), 0o600)) // #nosec G703 -- moduleDir is a test-owned temp dir.

	goSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum")) // #nosec G304 -- repoRoot is resolved from this test file's location.
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.sum"), goSum, 0o600)) // #nosec G703 -- moduleDir is a test-owned temp dir.

	return moduleDir
}

// repositoryRoot resolves the loom-mcp repository root from this file's location.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve test file location")
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}
