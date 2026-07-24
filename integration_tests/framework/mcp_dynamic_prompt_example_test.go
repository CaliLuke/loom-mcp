package framework

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
	"github.com/stretchr/testify/require"
)

func TestLoomExample_DynamicOnlyPromptCompiles(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	localLoomDir := currentLocalLoomReplace(t, repoRoot)

	fixtureRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(fixtureRoot, "design"), 0o750))

	goMod := `module example.com/dynamicprompt

go 1.26.5

require (
	github.com/CaliLuke/loom v1.7.1
	github.com/CaliLuke/loom-mcp v0.0.0-00010101000000-000000000000
)

replace github.com/CaliLuke/loom-mcp => ` + repoRoot + `
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
	. "github.com/CaliLuke/loom-mcp/dsl"
)

var PromptTemplates = Type("PromptTemplates", func() {
	Attribute("templates", ArrayOf(String))
	Required("templates")
})

var _ = API("dynamicprompt", func() {
	Title("Dynamic Prompt")
})

var _ = Service("prompt", func() {
	MCP("prompt", "0.1.0", ProtocolVersion("2025-06-18"))
	JSONRPC(func() { POST("/rpc") })

	Method("generate", func() {
		Payload(func() {
			Attribute("topic", String)
			Required("topic")
		})
		Result(PromptTemplates)
		DynamicPrompt("generate", "Generate a prompt")
		JSONRPC(func() {})
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
	run("run", upstreampaths.LoomCLIPackage, "gen", "example.com/dynamicprompt/design")
	run("run", upstreampaths.LoomCLIPackage, "example", "example.com/dynamicprompt/design")

	stub, err := os.ReadFile(filepath.Join(fixtureRoot, "mcp_prompt.go")) //nolint:gosec // path is rooted in the test temp directory
	require.NoError(t, err)
	require.Contains(t, string(stub), "NewMCPAdapter(NewPrompt(), nil, nil)")
	run("mod", "tidy")
	run("test", "./...")
}
