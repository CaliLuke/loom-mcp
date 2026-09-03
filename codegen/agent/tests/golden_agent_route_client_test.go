package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// Verify that agent.go emits Route() and NewClient(rt) helpers for caller-only usage.
func TestGolden_Agent_Route_And_NewClient(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ToolSpecsMinimal())
	content := fileContent(t, files, "gen/calc/agents/scribe/agent.go")
	assertGoldenGo(t, "agent_route_client", "agent.go.golden", content)
}

func TestAgentRouteCarriesWorkerTiming(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.RunPolicyBasic())
	content := fileContent(t, files, "gen/alpha/agents/scribe/agent.go")

	require.Contains(t, content, "TimeBudget:            time.Duration(int64(30000000000))")
	require.Contains(t, content, "ResumeActivityTimeout: time.Duration(int64(120000000000))")
}
