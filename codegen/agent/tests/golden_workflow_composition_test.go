package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_WorkflowComposition_DefaultPlanner(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.WorkflowComposition())
	agent := fileContent(t, files, "gen/alpha/agents/scribe/agent.go")
	config := fileContent(t, files, "gen/alpha/agents/scribe/config.go")

	require.Contains(t, agent, "planner.NewSequentialWorkflowPlanner(planner.SequentialWorkflowConfig{")
	require.Contains(t, agent, `Name:    "draft"`)
	require.Contains(t, agent, `Tool:    tools.Ident("writer.draft")`)
	require.Contains(t, agent, `Payload: rawjson.Message([]byte("{\"topic\":\"loom\"}"))`)
	require.Contains(t, agent, `FinalMessage: "workflow complete"`)
	require.NotContains(t, config, `planner is required`)
}
