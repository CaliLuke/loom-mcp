package testhelpers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/stretchr/testify/require"
)

// RunGeneratedModule executes application-owned tests against fully generated files.
// repo is the loom-mcp module root used by the generated consumer module.
func RunGeneratedModule(t *testing.T, module, repo string, files []*gcodegen.File, tests map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for _, file := range files {
		_, err := file.Render(dir)
		require.NoError(t, err, file.Path)
	}
	for name, source := range tests {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(source), 0600))
	}
	source, err := os.OpenRoot(repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, source.Close())
	})
	outputRoot, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, outputRoot.Close())
	})
	mod, err := source.ReadFile("go.mod")
	require.NoError(t, err)
	modText := strings.Replace(string(mod), "module github.com/CaliLuke/loom-mcp/v2", "module "+module, 1) + "\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\nreplace github.com/CaliLuke/loom-mcp/v2 => " + repo + "\n"
	require.NoError(t, outputRoot.WriteFile("go.mod", []byte(modText), 0600))
	sum, err := source.ReadFile("go.sum")
	require.NoError(t, err)
	require.NoError(t, outputRoot.WriteFile("go.sum", sum, 0600))
	command := exec.CommandContext(t.Context(), "go", "test", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "generated consumer module: %s", output)
}
