package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestApplyChildRunSummaryClearsResultWhenAllChildrenFail(t *testing.T) {
	result := &planner.ToolResult{
		Name:   tools.Ident("parent"),
		Result: "stale success",
	}
	output := &RunOutput{ToolEvents: []*api.ToolEvent{
		{Name: tools.Ident("child"), Error: planner.NewToolError("boom")},
	}}

	applyChildRunSummary(result.Name, output, result)

	require.Nil(t, result.Result)
	require.NotNil(t, result.Error)
}
