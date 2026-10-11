package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolDispatchMode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   DispatchMode
		inline bool
		want   DispatchMode
	}{
		{name: "default", want: DispatchActivity},
		{name: "inline inferred", inline: true, want: DispatchInline},
		{name: "inline explicit", mode: DispatchInline, want: DispatchInline},
		{name: "child explicit", mode: DispatchAgentChild, want: DispatchAgentChild},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{newAnyJSONSpec("echo.echo", "echo")}, Inline: tc.inline, DispatchMode: tc.mode,
				Execute: func(_ context.Context, _ *planner.ToolRequest) (*ToolExecutionResult, error) {
					return Executed(&planner.ToolResult{}), nil
				},
			}))
			got, ok := rt.ToolDispatchMode("echo.echo")
			assert.True(t, ok)
			assert.Equal(t, tc.want, got)
			got, ok = rt.ToolDispatchMode("missing")
			assert.False(t, ok)
			assert.Equal(t, DispatchUnset, got)
		})
	}
}
