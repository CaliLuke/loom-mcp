package summarize

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryInputRejectsIncompleteCanonicalEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entry    index.MessageEntry
		contents map[runstore.Hash][]byte
		want     string
	}{
		{"unknown kind", index.MessageEntry{Kind: "unknown"}, nil, "unknown entry kind"},
		{"missing message", index.MessageEntry{Kind: index.Message, Hash: "missing"}, nil, "missing eligible content"},
		{"malformed assistant", index.MessageEntry{Kind: index.Assistant, Hash: "bad"}, map[runstore.Hash][]byte{"bad": []byte(`{`)}, ""},
		{"missing call", index.MessageEntry{Kind: index.Assistant, Calls: []runstore.Hash{"missing"}}, nil, "missing eligible content"},
		{"missing clarification", index.MessageEntry{Kind: index.Clarification, Hash: "missing"}, nil, "missing eligible content"},
		{"result without call", index.MessageEntry{Kind: index.ToolResult}, nil, "one tool use ID"},
		{"result multiple calls", index.MessageEntry{Kind: index.ToolResult, ToolUseIDs: []string{"a", "b"}}, nil, "one tool use ID"},
		{"missing result", index.MessageEntry{Kind: index.ToolResult, Hash: "missing", ToolUseIDs: []string{"call"}}, nil, "missing eligible content"},
		{"malformed result", index.MessageEntry{Kind: index.ToolResult, Hash: "bad", ToolUseIDs: []string{"call"}}, map[runstore.Hash][]byte{"bad": []byte(`{`)}, "invalid eligible result JSON"},
		{"missing error text", index.MessageEntry{Kind: index.ToolResult, ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Error: &index.TextParameter{Hash: "missing"}}}, nil, "missing eligible content"},
		{"structural without parameters", index.MessageEntry{Kind: index.Structural}, nil, "missing structural parameters"},
		{"unknown structural kind", index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: "unknown"}}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.entry.ID = 1
			out, err := RenderInput(Input{From: 1, To: 2, Entries: []index.MessageEntry{tc.entry}, Contents: tc.contents})
			require.Error(t, err)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			}
			assert.Empty(t, out.Units, "partial evidence must not survive a corrupt entry")
		})
	}
}

func TestSummaryRetainsCanonicalToolErrorText(t *testing.T) {
	for _, param := range []index.TextParameter{{Text: "lookup denied"}, {Hash: "error"}} {
		out, err := RenderInput(Input{From: 1, To: 2, Contents: map[runstore.Hash][]byte{"error": []byte("lookup denied")}, Entries: []index.MessageEntry{{ID: 1, Kind: index.ToolResult, ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Error: &param}}}})
		require.NoError(t, err)
		require.Len(t, out.Units, 1)
		assert.Equal(t, "lookup denied", out.Units[0].Text)
		assert.Equal(t, "call", out.Units[0].ToolUseID)
		assert.True(t, out.Units[0].IsResult)
	}
}

func TestSummaryRejectsInvalidInputWindow(t *testing.T) {
	for _, in := range []Input{{From: 0, To: 1}, {From: 1, To: 1}, {From: 1, To: 2, CurrentPos: -1}, {From: 1, To: 2, Previous: "summary", SummaryPos: -1}} {
		_, err := RenderInput(in)
		require.Error(t, err)
	}
}
