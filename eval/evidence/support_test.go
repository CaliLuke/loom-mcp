package evidence

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedTokenAmendment(t *testing.T) {
	const token = "XYZ-00000"
	for _, tc := range []struct {
		name            string
		input           *model.Message
		storedOutput    bool
		wantUnsupported bool
	}{
		{"earlier assistant turn", answer(token), true, true},
		{"received tool result", &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "lookup", Content: token}}}, false, false},
		{"final answer only", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []*model.Message{msg("NEED: ABC-12345")}
			if tc.input != nil {
				messages = append(messages, tc.input)
			}
			r := record(call(1, messages...))
			r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 " + token)}
			if tc.storedOutput {
				r.Content["output"] = []byte(token)
			}
			got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
			require.NoError(t, err)
			assert.Equal(t, tc.wantUnsupported, got.UnsupportedClaim)
		})
	}
}

func TestUnsupportedTokenInputBoundaries(t *testing.T) {
	const token = "XYZ-00000"
	toolArgs := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolUsePart{ID: "action", Name: "deploy", Input: map[string]any{"target": token}}}}
	system := msg(token)
	system.Role = model.ConversationRoleSystem
	abandoned := call(1, msg(token))
	abandoned.Class = runrecord.Abandoned
	summarizer := call(1, msg(token))
	summarizer.Activity = callrecord.ActivitySummarize
	for _, tc := range []struct {
		name            string
		prior           runrecord.Call
		wantUnsupported bool
	}{
		{"user", call(1, msg(token)), false},
		{"recall result", call(1, recallResult(token)), false},
		{"tool arguments even in a user message", call(1, toolArgs), true},
		{"system", call(1, system), true},
		{"abandoned", abandoned, true},
		{"summarizer", summarizer, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := record(tc.prior, call(2, msg("NEED: ABC-12345")))
			r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 " + token)}
			got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
			require.NoError(t, err)
			assert.Equal(t, tc.wantUnsupported, got.UnsupportedClaim)
		})
	}
}

func TestCheckpointSupportsGrammarTokens(t *testing.T) {
	const token = "XYZ-00000"
	for _, source := range []Source{UserIndex, Summary, Receipt} {
		t.Run(string(source), func(t *testing.T) {
			in := checkpoint.Input{Through: 4, ID: "cp", CurrentPos: 1}
			switch source {
			case UserIndex:
				in.Users = []checkpoint.UserMessage{{ID: 1, Message: msg(token)}}
			case Summary:
				in.Summary = &checkpoint.Summary{Text: token}
			case Receipt:
				in.Receipt = func(yield func(checkpoint.ReceiptItem) bool) {
					yield(checkpoint.ReceiptItem{ToolUseID: "action", Tool: "deploy", Args: token})
				}
			case Pins, Tail, Recall:
			}
			cp, err := checkpoint.Render(in, index.RenderBudget{KeepUserTokens: 200, MaxSummaryTokens: 100, ReceiptTokens: 200, FramingTokens: 300}, checkpoint.DefaultLimits, nil)
			require.NoError(t, err)
			r := record(call(1, cp, msg("NEED: ABC-12345")))
			r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 " + token)}
			got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
			require.NoError(t, err)
			assert.False(t, got.UnsupportedClaim)
		})
	}
}

func TestFinalActionDoesNotSupportItsToken(t *testing.T) {
	r := record(call(1, msg("NEED: ABC-12345")))
	r.Runs[0].Transcript = []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "ABC-12345"}, model.ToolUsePart{ID: "action", Name: "deploy", Input: map[string]any{"target": "XYZ-00000"}}}}}
	r.Content["action"] = []byte(`{"target":"XYZ-00000"}`)
	got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.UnsupportedClaim)
}
