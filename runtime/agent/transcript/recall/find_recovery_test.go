package recall

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindRecoveryHiddenResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		view index.ViewRef
	}{
		{name: "preview", view: index.ViewRef{TailFrom: 1, PreviewThrough: 1}},
		{name: "forced clear", view: index.ViewRef{TailFrom: 1, ForceClearThrough: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			f.entries(
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(`"hidden needle"`)), ToolUseIDs: []string{"hidden"}, Keep: true},
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(`"visible needle"`)), ToolUseIDs: []string{"visible"}},
			)
			f.req.View.TailFrom = tc.view.TailFrom
			f.req.View.PreviewThrough = tc.view.PreviewThrough
			f.req.View.ForceClearThrough = tc.view.ForceClearThrough
			found := f.call(`{"find":{"text":"needle"}}`)
			require.Len(t, found.Matches, 1)
			assert.Equal(t, "hidden", found.Matches[0].ID)
			assert.Contains(t, f.call(`{"read":{"id":"hidden","part":"result","offset":0}}`).Text, "hidden needle")
		})
	}
}
