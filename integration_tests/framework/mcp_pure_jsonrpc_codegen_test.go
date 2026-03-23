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

func TestLoomGen_MCPPureServiceWithoutMethodLevelJSONRPC(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	fixtureRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(fixtureRoot, "design"), 0o750))

	goMod := `module example.com/repro

go 1.26.0

require (
	github.com/CaliLuke/loom v1.0.3
	github.com/CaliLuke/loom-mcp v0.0.0-00010101000000-000000000000
)

replace github.com/CaliLuke/loom-mcp => ` + repoRoot + `
`
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "go.mod"), []byte(goMod), 0o600))

	design := `package design

import (
	. "github.com/CaliLuke/loom/dsl"
	. "github.com/CaliLuke/loom-mcp/dsl"
)

var PingResult = Type("PingResult", func() {
	Attribute("ok", Boolean)
	Required("ok")
})

var _ = API("repro", func() {
	Title("Repro")
})

var _ = Service("demo", func() {
	MCP("demo", "0.1.0", ProtocolVersion("2025-06-18"))
	JSONRPC(func() { POST("/rpc") })

	Method("Ping", func() {
		Payload(func() {})
		Result(PingResult)
		Tool("ping", "Ping tool")
	})
})
`
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "design", "design.go"), []byte(design), 0o600))

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- test executes fixed toolchain commands with controlled args
		cmd.Dir = fixtureRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s\n%s", cmd.String(), string(out))
	}

	run("go", "mod", "tidy")
	run("go", "run", upstreampaths.LoomCLIPackage, "gen", "example.com/repro/design")
	run("go", "mod", "tidy")
	run("go", "test", "./...")
}
