package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_WorkflowGraph(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.WorkflowGraph())
	agent := fileContent(t, files, "gen/alpha/agents/coordinator/agent.go")
	require.Contains(t, agent, "planner.NewGraphWorkflowPlanner(planner.WorkflowGraphConfig{")
	require.Contains(t, agent, `ID:`)
	require.Contains(t, agent, `"draft"`)
	require.Contains(t, agent, `DependsOn: []string{"draft", "review"}`)
	require.Contains(t, agent, `planner.WorkflowNodeTypedInput`)
	require.Contains(t, agent, `"Approval"`)
	require.Contains(t, agent, `rawjson.Message([]byte("{\"type\":\"object\",\"properties\":{\"approved\":{\"type\":\"boolean\"}}}"))`)
	require.Contains(t, agent, `FinalMessage: "graph complete"`)
}
