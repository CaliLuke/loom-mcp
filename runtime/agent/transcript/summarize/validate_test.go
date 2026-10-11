package summarize

import (
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const headings = "# Goal\n# Decisions\n# Facts\n# Progress\n# Open questions\n# Next\n"

type citationSource struct{ fail bool }

func TestValidationKindsAndOrder(t *testing.T) {
	tests := []struct {
		name, text, stop, kind string
		unfinished, custom     bool
		maxBytes, newSize      int
	}{
		{name: "empty first", text: "  \n", stop: "length", kind: "empty"},
		{name: "length", text: headings, stop: "length", kind: "truncated"},
		{name: "max tokens", text: headings, stop: "max_tokens", kind: "truncated"},
		{name: "unfinished", text: headings, unfinished: true, kind: "truncated"},
		{name: "too large", text: strings.Repeat("x", 101), maxBytes: 100, kind: "too_large"},
		{name: "exact boundary", text: strings.Repeat("x", 100), maxBytes: 100, custom: true},
		{name: "no budget", text: "x", maxBytes: -1, kind: "too_large"},
		{name: "equal size", text: headings, newSize: 1000, kind: "not_smaller"},
		{name: "larger size", text: headings, newSize: 1001, kind: "not_smaller"},
		{name: "unknown", text: headings + "[message 99]", kind: "cites_unknown"},
		{name: "prior run unknown", text: headings + "[r2 message 3]", kind: "cites_unknown"},
		{name: "tail boundary", text: headings + "[message 10]", kind: "cites_not_behind"},
		{name: "assistant", text: headings + "[message 3]", kind: "cites_non_user"},
		{name: "user role tool result", text: headings + "[message 4]", kind: "cites_non_user"},
		{name: "missing headings", text: "Goal\nDecisions\nFacts\nProgress\nOpen questions\nNext", kind: "missing_headings"},
		{name: "fenced headings", text: "```\n" + headings + "```", kind: "missing_headings"},
		{name: "internal canonical", text: headings + "recall_context", kind: "names_internal_tool"},
		{name: "internal provider", text: headings + "provider_recall", kind: "names_internal_tool"},
		{name: "custom still rejects tool", text: "recall_context", custom: true, kind: "names_internal_tool"},
		{name: "custom no headings", text: "valid", custom: true},
		{name: "headings any order case level", text: "### NEXT ###\n## open QUESTIONS\n# PROGRESS\n###### FACTS\n# decisions\n# goal\n"},
		{name: "headings first", text: "recall_context", kind: "missing_headings"},
		{name: "citations first", text: "[message 99] recall_context", kind: "cites_unknown"},
		{name: "size first", text: headings + "[message 99]", newSize: 1000, kind: "not_smaller"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput(tc.text)
			in.StopReason, in.Unfinished, in.DefaultPrompt = tc.stop, tc.unfinished, !tc.custom
			if tc.maxBytes != 0 {
				in.SummaryBytes = max(0, tc.maxBytes)
			}
			if tc.newSize != 0 {
				in.RenderedCheckpoint = func(string) (int, error) {
					return tc.newSize, nil
				}
			}
			_, err := Validate(in)
			if tc.kind == "" {
				require.NoError(t, err)
				return
			}
			var rejection *Rejection
			require.ErrorAs(t, err, &rejection)
			assert.Equal(t, tc.kind, rejection.Kind)
		})
	}
}

func TestValidationRenderedSizeAndCitations(t *testing.T) {
	for _, replaced := range []int{40, 80, 65536} {
		in := validInput("fact")
		in.DefaultPrompt = false
		in.ReplacedTokens = replaced
		in.RenderedCheckpoint = func(string) (int, error) {
			return replaced, nil
		}
		_, err := Validate(in)
		var rejected *Rejection
		require.ErrorAs(t, err, &rejected)
		assert.Equal(t, "not_smaller", rejected.Kind)
		in.RenderedCheckpoint = func(string) (int, error) {
			return replaced - 1, nil
		}
		_, err = Validate(in)
		require.NoError(t, err)
	}
	in := validInput(headings + "[message 2] [message 1] [message 2] [Message 57] messages 5–7")
	result, err := Validate(in)
	require.NoError(t, err)
	assert.Equal(t, 2, result.LooseRefs)
	require.Len(t, result.CitedUsers, 2)
	assert.Equal(t, index.MessageID(2), result.CitedUsers[0].ID)
	assert.Equal(t, "record correction", result.CitedUsers[0].Text)
	assert.Equal(t, index.MessageID(1), result.CitedUsers[1].ID)
	in.Text = headings + "[message 9]"
	_, err = Validate(in)
	require.NoError(t, err)
	in.Resolver = citationSource{fail: true}
	_, err = Validate(in)
	require.ErrorIs(t, err, errResolver)
}

var errResolver = errors.New("resolver infrastructure failure")

func validInput(text string) ValidationInput {
	return ValidationInput{Text: text, SummaryBytes: 10000, ReplacedTokens: 1000, RenderedCheckpoint: func(string) (int, error) {
		return 100, nil
	}, CurrentPos: 1, To: 10, Resolver: citationSource{}, DefaultPrompt: true, RecallNames: []string{"provider_recall"}}
}

func (s citationSource) Resolve(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	if s.fail {
		return checkpoint.CitationTarget{}, false, errResolver
	}
	if key.ID > 10 {
		return checkpoint.CitationTarget{}, false, nil
	}
	target := checkpoint.CitationTarget{Kind: index.Message, Role: model.ConversationRoleUser, Line: checkpoint.UserLine{ID: key.ID, Text: "record correction"}}
	if key.ID == 3 {
		target.Role = model.ConversationRoleAssistant
	}
	if key.ID == 4 {
		target.Kind = index.ToolResult
	}
	return target, true, nil
}
func (citationSource) Behind(checkpoint.OrderKey) bool {
	return true
}

func TestSizeRuleUsesRenderedOmissions(t *testing.T) {
	raw := strings.Repeat("x", 5000000)
	for _, view := range []string{`{"omitted":true,"reason":"size_limit"}`, checkpoint.ClearedResult(len(raw), "call-1", "")} {
		summary := strings.Repeat("fact ", 100)
		oldCheckpoint := "old checkpoint"
		before := contextmeasure.Estimate(contextmeasure.Rate{}, 0, len(oldCheckpoint)+len(view), 0)
		after := contextmeasure.Estimate(contextmeasure.Rate{}, 0, len(summary)+len(oldCheckpoint), 0)
		assert.Less(t, len(summary), len(raw))
		assert.Greater(t, after, before)
		in := validInput(summary)
		in.DefaultPrompt = false
		in.ReplacedTokens = before
		in.RenderedCheckpoint = func(string) (int, error) {
			return after, nil
		}
		_, err := Validate(in)
		var rejected *Rejection
		require.ErrorAs(t, err, &rejected)
		assert.Equal(t, "not_smaller", rejected.Kind)
	}
	for _, allowance := range []int{2, 3, 4} {
		in := validInput("界")
		in.DefaultPrompt = false
		in.SummaryBytes = allowance
		_, err := Validate(in)
		if allowance < 3 {
			var rejected *Rejection
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, "too_large", rejected.Kind)
		} else {
			require.NoError(t, err)
		}
	}
	in := validInput(headings)
	in.RenderedCheckpoint = func(string) (int, error) {
		return 0, errResolver
	}
	_, err := Validate(in)
	require.ErrorIs(t, err, errResolver)
}
