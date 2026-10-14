package recall

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindContextRead(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, needle, suffix string
	}{
		{"identifier before filename", strings.Repeat("prior ", 30) + `{"document_id":"doc-recent","filename":"`, "recent.pdf", `","text":"original evidence"}`},
		{"start", "", "filename", " following evidence"},
		{"end", "preceding evidence ", "filename", ""},
		{"unicode", strings.Repeat("界", 51) + " id=42 ", "📄résumé", strings.Repeat("界", 80)},
		{"full snippet literal", "preceding ", strings.Repeat("x", 160), " following"},
		{"near limit unicode", strings.Repeat("界", 30), strings.Repeat("x", 155), " following"},
		{"long literal", "preceding ", strings.Repeat("界", 60), " following"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			original := tc.prefix + tc.needle + tc.suffix
			f.entries(index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(original)), ToolUseIDs: []string{"source"}})
			f.req.View.TailFrom = 2
			found := f.call(stringJSON(t, Input{Find: &FindInput{Text: tc.needle}}))
			require.Len(t, found.Matches, 1)
			var response struct {
				ReadHint string `json:"read_hint"`
			}
			require.NoError(t, json.Unmarshal([]byte(stringJSON(t, found)), &response))
			assert.Contains(t, response.ReadHint, "snippet_offset")
			assert.Contains(t, response.ReadHint, "partial")
			// Inspect the public JSON, including a context offset of zero.
			var match struct {
				ID            string `json:"id"`
				Run           string `json:"run"`
				Part          Part   `json:"part"`
				Offset        int    `json:"offset"`
				SnippetOffset *int   `json:"snippet_offset"`
				Snippet       string `json:"snippet"`
			}
			require.NoError(t, json.Unmarshal([]byte(stringJSON(t, found.Matches[0])), &match))
			assert.Equal(t, len(tc.prefix), match.Offset)
			require.NotNil(t, match.SnippetOffset, "find must supply the read offset for surrounding evidence")
			start := *match.SnippetOffset
			require.GreaterOrEqual(t, start, 0)
			require.LessOrEqual(t, start, match.Offset)
			assert.LessOrEqual(t, match.Offset-start, 64)
			assert.LessOrEqual(t, len(match.Snippet), 160)
			assert.True(t, utf8.ValidString(match.Snippet))
			assert.Equal(t, original[start:start+len(match.Snippet)], match.Snippet)
			if len(tc.needle) <= 160 {
				assert.Contains(t, match.Snippet, tc.needle)
				if tc.prefix != "" && len(tc.needle) < 158 {
					assert.Less(t, start, match.Offset)
				}
			}
			context := f.call(stringJSON(t, Input{Read: &ReadInput{ID: match.ID, Run: match.Run, Part: match.Part, Offset: start}}))
			assert.Empty(t, context.Code)
			assert.True(t, strings.HasPrefix(context.Text, match.Snippet))
			if tc.name == "identifier before filename" {
				assert.Contains(t, match.Snippet, `"document_id":"doc-recent"`)
				assert.Contains(t, context.Text, `"document_id":"doc-recent"`)
			}
			exact := f.call(stringJSON(t, Input{Read: &ReadInput{ID: match.ID, Run: match.Run, Part: match.Part, Offset: match.Offset}}))
			assert.True(t, strings.HasPrefix(exact.Text, tc.needle))
		})
	}
}
