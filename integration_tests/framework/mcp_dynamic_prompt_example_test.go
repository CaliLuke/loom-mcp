package framework

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/internal/upstreampaths"
	"github.com/stretchr/testify/require"
)

func TestLoomGen_DynamicOnlyPromptCompiles(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	localLoomDir := currentLocalLoomReplace(t, repoRoot)

	fixtureRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(fixtureRoot, "design"), 0o750))

	goMod := `module example.com/dynamicprompt

go 1.27.0

require (
	github.com/CaliLuke/loom v1.9.0-alpha.10
	github.com/CaliLuke/loom-mcp/v2 v2.0.0
)

replace github.com/CaliLuke/loom-mcp/v2 => ` + repoRoot + `
`
	if localLoomDir != "" {
		goMod += `
replace github.com/CaliLuke/loom => ` + localLoomDir + `
`
	}
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "go.mod"), []byte(goMod), 0o600))

	design := `package design

import (
	. "github.com/CaliLuke/loom/dsl"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
)

var PromptTemplates = Type("PromptTemplates", func() {
	Attribute("templates", ArrayOf(String))
	Required("templates")
})

var _ = API("dynamicprompt", func() {
	Title("Dynamic Prompt")
})

var _ = Service("prompt", func() {
	MCP("prompt", "0.1.0")

	Method("generate", func() {
		Payload(func() {
			Attribute("topic", String)
			Required("topic")
		})
		Result(PromptTemplates)
		DynamicPrompt("generate", "Generate a prompt")
	})
})
`
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "design", "design.go"), []byte(design), 0o600))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "go", args...) // #nosec G204 -- controlled toolchain command and fixture arguments
		cmd.Dir = fixtureRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s\n%s", cmd.String(), string(out))
	}

	run("mod", "tidy")
	run("run", "-mod=mod", upstreampaths.LoomCLIPackage, "gen", "example.com/dynamicprompt/design")
	stub, err := os.ReadFile(filepath.Join(fixtureRoot, "gen", "mcp_prompt", "sdk_server.go")) //nolint:gosec // path is rooted in the test temp directory
	require.NoError(t, err)
	require.Contains(t, string(stub), "func NewSDKServer")
	run("mod", "tidy")
	run("test", "./...")
}
