package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// buildAndGenerateExample provided by golden_helpers_test.go

func TestExampleInternal_MethodBacked(t *testing.T) {
	files := buildAndGenerateExample(t, testscenarios.MethodComplexEmbedded())

	// Bootstrap
	boot := fileContent(t, files, "internal/agents/bootstrap/bootstrap.go")
	assertGoldenGo(t, "example_internal_method", "bootstrap.go.golden", boot)

	// Planner stub
	plan := fileContent(t, files, "internal/agents/scribe/planner/planner.go")
	assertGoldenGo(t, "example_internal_method", "planner.go.golden", plan)

	// Executor stub for toolset profiles
	exec := fileContent(t, files, "internal/agents/scribe/toolsets/profiles/execute.go")
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
	boot := fileContent(t, files, "internal/agents/bootstrap/bootstrap.go")
	assertGoldenGo(t, "example_internal_mcp", "bootstrap.go.golden", boot)

	// Planner stub exists
	plan := fileContent(t, files, "internal/agents/scribe/planner/planner.go")
	assertGoldenGo(t, "example_internal_mcp", "planner.go.golden", plan)
}
