package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
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

func TestLastSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		sep  rune
		want string
	}{
		{name: "ASCII", text: "catalog.search", sep: '.', want: "search"},
		{name: "Unicode", text: "catalog→search", sep: '→', want: "search"},
		{name: "trailing separator", text: "catalog.", sep: '.', want: ""},
		{name: "no separator", text: "search", sep: '.', want: "search"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, lastSegment(test.text, test.sep))
		})
	}
}
