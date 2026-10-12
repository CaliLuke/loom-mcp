package checkpoint

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSectionsRoundTrip(t *testing.T) {
	g := testGenerator{state: 193}
	for trial := range 200 {
		in := randomInput(&g, trial)
		b := index.RenderBudget{KeepUserTokens: 100, MaxSummaryTokens: 100, ReceiptTokens: 100, FramingTokens: 300}
		header := checkpointHeader(in)
		framing := len(header) + len(userLabel) + len(summaryLabel) + len(receiptLabel)
		summaryBytes, receiptBytes, err := sectionBudgets(b, framing, in.Summary != nil)
		require.NoError(t, err)
		summary, err := renderSummary(in, summaryBytes)
		require.NoError(t, err)
		users := renderUsers(userInput{users: in.Users, cited: in.CitedUsers, pinned: in.Pinned, budget: b, limits: Limits{}, currentPos: in.CurrentPos, recordID: in.ID})
		want := Parsed{Header: header, Users: users.text, Summary: summary, Receipt: renderReceipt(in.Receipt, in.Earlier, receiptBytes, in.ID, in.CurrentPos)}
		for _, component := range []string{want.Users, want.Summary, want.Receipt} {
			for _, label := range []string{userLabel, summaryLabel, receiptLabel} {
				require.NotContains(t, component, label, "property requires label-free components")
			}
		}
		msg, err := Render(in, b, Limits{}, nil)
		require.NoError(t, err)
		parsed, ok := Sections(msg.Parts[0].(model.TextPart).Text)
		require.True(t, ok)
		assert.Equal(t, want, parsed, "trial %d: compare actual components, not concatenation", trial)
	}
	_, ok := Sections("ordinary message")
	assert.False(t, ok)
}

func TestSectionsLabelPrecedence(t *testing.T) {
	// Each component contains framing text. These are explicit expected splits,
	// not boundaries calculated with the parser's search functions.
	header := "checkpoint header\n"
	users := "user before" + userLabel + "user after" + summaryLabel + "user summary label"
	summary := "summary before" + summaryLabel + "summary after"
	receipt := "receipt before" + receiptLabel + "receipt after"
	text := header + userLabel + users + summaryLabel + summary + receiptLabel + receipt
	got, ok := Sections(text)
	require.True(t, ok)
	assert.Equal(t, Parsed{
		Header:  header,
		Users:   users + summaryLabel + "summary before",
		Summary: "summary after" + receiptLabel + "receipt before",
		Receipt: "receipt after",
	}, got)
}
