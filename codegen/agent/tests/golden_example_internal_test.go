package tests

import (
	"os"
	"path/filepath"
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/stretchr/testify/require"
)

// buildAndGenerateExample provided by golden_helpers_test.go

func TestExampleInternal_MethodBacked(t *testing.T) {
	files := buildAndGenerateExample(t, testscenarios.MethodComplexEmbedded())

	// Bootstrap
	boot := fileContent(t, files, "internal/agents/alpha/bootstrap/bootstrap.go")
	assertGoldenGo(t, "example_internal_method", "bootstrap.go.golden", boot)

	// Planner stub
	plan := fileContent(t, files, "internal/agents/alpha/scribe/planner/planner.go")
	assertGoldenGo(t, "example_internal_method", "planner.go.golden", plan)

	// Executor stub for toolset profiles
	exec := fileContent(t, files, "internal/agents/alpha/scribe/toolsets/profiles/execute.go")
	assertGoldenGo(t, "example_internal_method", "executor.go.golden", exec)
	require.Contains(t, exec, "scribe.RegisterUsedToolsets(ctx, rt,")
	require.Contains(t, exec, "scribe.WithProfilesExecutor(runtime.ToolCallExecutorFunc(Execute))")
	require.Contains(t, exec, "case profilesspecs.Upsert:")
	require.Contains(t, exec, "profilesspecs.InitUpsertMethodPayload(args)")
	require.Contains(t, exec, "profilesspecs.InitUpsertToolResult(methodRes)")
	require.NotContains(t, exec, "NewScribeProfilesToolsetRegistration")
	require.NotContains(t, exec, "ToMethodPayload_Upsert")
	require.NotContains(t, exec, "ToToolReturn_Upsert")
	require.NotContains(t, exec, `case "upsert":`)
}

func TestExampleInternal_MCP(t *testing.T) {
	files := buildAndGenerateExample(t, testscenarios.MCPUse())

	// Bootstrap should include MCP caller stubs
	boot := fileContent(t, files, "internal/agents/alpha/bootstrap/bootstrap.go")
	assertGoldenGo(t, "example_internal_mcp", "bootstrap.go.golden", boot)

	// Planner stub exists
	plan := fileContent(t, files, "internal/agents/alpha/scribe/planner/planner.go")
	assertGoldenGo(t, "example_internal_mcp", "planner.go.golden", plan)
}

func TestExampleInternal_MultiServiceScaffoldsAreServiceScopedAndPreserved(t *testing.T) {
	files := buildAndGenerateExample(t, testscenarios.MultiServiceExample())
	pathCounts := make(map[string]int, len(files))
	for _, file := range files {
		require.True(t, file.SkipExist, "%s must preserve application-owned scaffold", file.Path)
		pathCounts[file.Path]++
	}
	for path, count := range pathCounts {
		require.Equal(t, 1, count, "%s must be emitted exactly once", path)
	}

	wantPaths := []string{
		"internal/agents/alpha/bootstrap/bootstrap.go",
		"internal/agents/alpha/scribe/planner/planner.go",
		"internal/agents/beta/bootstrap/bootstrap.go",
		"internal/agents/beta/scribe/planner/planner.go",
	}
	for _, wantPath := range wantPaths {
		require.Equal(t, 1, pathCounts[wantPath], "%s must be emitted exactly once", wantPath)
	}

	alphaBootstrap := fileContent(t, files, "internal/agents/alpha/bootstrap/bootstrap.go")
	betaBootstrap := fileContent(t, files, "internal/agents/beta/bootstrap/bootstrap.go")
	require.Contains(t, alphaBootstrap, "github.com/CaliLuke/loom-mcp/v2/alpha/agents/scribe")
	require.NotContains(t, alphaBootstrap, "github.com/CaliLuke/loom-mcp/v2/beta/agents/scribe")
	require.Contains(t, betaBootstrap, "github.com/CaliLuke/loom-mcp/v2/beta/agents/scribe")
	require.NotContains(t, betaBootstrap, "github.com/CaliLuke/loom-mcp/v2/alpha/agents/scribe")

	alphaMain := fileContent(t, files, "cmd/alpha/main.go")
	betaMain := fileContent(t, files, "cmd/beta/main.go")
	require.Contains(t, alphaMain, "\"github.com/CaliLuke/loom-mcp/v2/internal/agents/alpha/bootstrap\"")
	require.Contains(t, betaMain, "\"github.com/CaliLuke/loom-mcp/v2/internal/agents/beta/bootstrap\"")

	alphaPlanner := files[0]
	for _, file := range files {
		if file.Path == "internal/agents/alpha/scribe/planner/planner.go" {
			alphaPlanner = file
			break
		}
	}
	dir := t.TempDir()
	_, err := alphaPlanner.Render(dir)
	require.NoError(t, err)
	path := filepath.Join(dir, alphaPlanner.Path)
	const applicationEdit = "package planner\n\n// application-owned edit\n"
	require.NoError(t, os.WriteFile(path, []byte(applicationEdit), 0o600))
	_, err = alphaPlanner.Render(dir)
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, applicationEdit, string(got))
}

func TestExampleInternal_ReplacesUpstreamMainSections(t *testing.T) {
	genpkg, roots := testhelpers.RunDesign(t, testscenarios.MultiServiceExample())
	upstreamMain := &gcodegen.File{
		Path: "cmd/alpha/main.go",
		Sections: []gcodegen.Section{
			gcodegen.NewRawSection("upstream-main", "package main\n\nconst upstreamMarker = true\n"),
		},
	}

	files, err := agentcodegen.GenerateExample(genpkg, roots, []*gcodegen.File{upstreamMain})
	require.NoError(t, err)
	require.True(t, upstreamMain.SkipExist)
	main := fileContent(t, files, "cmd/alpha/main.go")
	require.Contains(t, main, "github.com/CaliLuke/loom-mcp/v2/internal/agents/alpha/bootstrap")
	require.NotContains(t, main, "upstreamMarker")
}
