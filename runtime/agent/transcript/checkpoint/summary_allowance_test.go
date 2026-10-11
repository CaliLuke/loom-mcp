package checkpoint

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryAllowanceMatchesRender(t *testing.T) {
	for _, b := range []index.RenderBudget{
		{KeepUserTokens: 100, MaxSummaryTokens: 400, ReceiptTokens: 60, FramingTokens: 200},
		{KeepUserTokens: 100, MaxSummaryTokens: 400, ReceiptTokens: 60, FramingTokens: 20},
	} {
		in := Input{Through: 15, ID: "record", Summary: &Summary{ID: "record"}}
		allowance, err := SummaryBytes(in, b)
		require.NoError(t, err)
		in.Summary.Text = strings.Repeat("x", allowance)
		msg, err := Render(in, b, DefaultLimits, nil)
		require.NoError(t, err)
		assert.Contains(t, msg.Parts[0].(model.TextPart).Text, in.Summary.Text)
		in.Summary.Text += "x"
		msg, err = Render(in, b, DefaultLimits, nil)
		require.NoError(t, err)
		assert.NotContains(t, msg.Parts[0].(model.TextPart).Text, in.Summary.Text)
	}
}
