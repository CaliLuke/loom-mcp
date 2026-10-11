package checkpoint

import (
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnits checks batch, typed/finalize, clarification and open-await boundaries.
func TestUnits(t *testing.T) {
	tests := []struct {
		name    string
		entries []index.MessageEntry
		open    index.MessageID
		want    [][3]uint64
	}{
		{"batch", []index.MessageEntry{
			{Kind: index.Message, Role: model.ConversationRoleUser},
			{Kind: index.Assistant, ToolUseIDs: []string{"a", "b"}},
			{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping}},
			{Kind: index.ToolResult, ToolUseIDs: []string{"a"}},
			{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.UnknownTool}, ToolUseIDs: []string{"b"}},
			{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.Reminders}},
			{Kind: index.Assistant},
		}, 0, [][3]uint64{{1, 1, 0}, {2, 6, 1}, {7, 7, 1}}},
		{"typed and finalize", []index.MessageEntry{
			{Kind: index.Assistant, ToolUseIDs: []string{"a"}},
			{Kind: index.TypedInput},
			{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeResults}},
			{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeHint}},
			{Kind: index.Assistant},
		}, 0, [][3]uint64{{1, 4, 1}, {5, 5, 1}}},
		{"clarification", []index.MessageEntry{{Kind: index.Assistant}, {Kind: index.Clarification}, {Kind: index.Assistant}}, 0, [][3]uint64{{1, 1, 1}, {2, 2, 0}, {3, 3, 1}}},
		{"open await", []index.MessageEntry{{Kind: index.Message, Role: model.ConversationRoleUser}, {Kind: index.Assistant}, {Kind: index.TypedInput}, {Kind: index.Clarification}, {Kind: index.Assistant}}, 2, [][3]uint64{{1, 1, 0}, {2, 5, 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := range tt.entries {
				tt.entries[i].ID = index.MessageID(i + 1)
			}
			got := Units(tt.entries, entryRole, tt.open)
			var bounds [][3]uint64
			for _, u := range got {
				start := uint64(0)
				if u.StartsAssistant {
					start = 1
				}
				bounds = append(bounds, [3]uint64{uint64(u.From), uint64(u.Through), start})
			}
			assert.Equal(t, tt.want, bounds)
		})
	}
}

// TestChooseTail keeps the newest unit and chooses only safe boundaries.
func TestChooseTail(t *testing.T) {
	assistantNewest := []Unit{{From: 1, Through: 1}, {From: 2, Through: 3, StartsAssistant: true}, {From: 4, Through: 5, StartsAssistant: true}}
	userNewest := append(append([]Unit{}, assistantNewest...), Unit{From: 6, Through: 6})
	for _, tt := range []struct {
		name   string
		units  []Unit
		budget int
		want   index.MessageID
	}{
		{"assistant zero", assistantNewest, 0, 4},
		{"assistant small", assistantNewest, 3, 4},
		{"assistant two units", assistantNewest, 4, 2},
		{"assistant all", assistantNewest, 99, 2},
		{"user zero", userNewest, 0, 4},
		{"user small", userNewest, 3, 4},
		{"user two units", userNewest, 4, 4},
		{"user all", userNewest, 99, 2},
		{"no cuts", []Unit{{From: 1, Through: 1}, {From: 2, Through: 2}}, 0, 1},
		{"empty", nil, 10, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ChooseTail(tt.units, func(Unit) int {
				return 2
			}, tt.budget))
		})
	}
}

// TestUnitsExhaustive enumerates the valid index language, not arbitrary malformed entries.
func TestUnitsExhaustive(t *testing.T) {
	var walk func([]index.MessageEntry, []*model.Message, []string, model.ConversationRole)
	cases := 0
	walk = func(entries []index.MessageEntry, msgs []*model.Message, pending []string, last model.ConversationRole) {
		if len(pending) == 0 && len(msgs) > 0 {
			cases++
			for _, open := range []index.MessageID{0, index.MessageID(len(entries))} {
				units := Units(entries, entryRole, open)
				cuts := CutPoints(units)
				for _, budget := range []int{0, 3, 4, 99} {
					chosen := ChooseTail(units, func(Unit) int {
						return 2
					}, budget)
					assert.LessOrEqual(t, chosen, units[len(units)-1].From)
					if len(cuts) > 0 {
						assert.Contains(t, cuts, chosen, "entries=%v budget=%d", entries, budget)
					}
				}
				for _, cut := range cuts {
					tail := projectEntries(entries[cut-1:])
					view := transcript.AssembleView(nil, &model.Message{Role: model.ConversationRoleUser}, tail)
					assert.NoError(t, transcript.ValidateView(view, transcript.GrammarBedrock{}), "entries=%v cut=%d", entries, cut)
					assert.NoError(t, transcript.ValidateView(view, transcript.GrammarAnthropic{}))
					assertClosedPrefix(t, entries[:cut-1])
				}
			}
		}
		if len(entries) == 7 {
			return
		}
		appendStep := func(e index.MessageEntry, nextPending []string, nextRole model.ConversationRole) {
			e.ID = index.MessageID(len(entries) + 1)
			next := append(append([]index.MessageEntry{}, entries...), e)
			walk(next, projectEntries(next), nextPending, nextRole)
		}
		if len(entries) == 0 || (len(pending) == 0 && last == model.ConversationRoleAssistant) {
			appendStep(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser}, nil, model.ConversationRoleUser)
			appendStep(index.MessageEntry{Kind: index.Clarification}, nil, model.ConversationRoleUser)
		}
		if len(pending) == 0 && last == model.ConversationRoleUser {
			for n := 0; n <= 2; n++ {
				ids := make([]string, n)
				for j := range ids {
					ids[j] = fmt.Sprintf("%d-%d", len(entries), j)
				}
				appendStep(index.MessageEntry{Kind: index.Assistant, ToolUseIDs: ids}, ids, model.ConversationRoleAssistant)
			}
		}
		if len(pending) > 0 {
			appendStep(index.MessageEntry{Kind: index.ToolResult, ToolUseIDs: []string{pending[0]}}, pending[1:], model.ConversationRoleUser)
			appendStep(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeResults}, ToolUseIDs: pending}, nil, model.ConversationRoleUser)
		}
		if len(entries) > 0 {
			for _, kind := range []index.StructuralKind{index.Reminders, index.HideBookkeeping} {
				appendStep(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: kind}}, pending, last)
			}
			appendStep(index.MessageEntry{Kind: index.TypedInput}, pending, last)
		}
	}
	walk(nil, nil, nil, "")
	assert.Greater(t, cases, 1000)
}

// TestUnsafeCutInsideUnit rejects split batches and unresolved calls before a cut.
func TestUnsafeCutInsideUnit(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Assistant, ToolUseIDs: []string{"a", "b"}}, {ID: 2, Kind: index.ToolResult, ToolUseIDs: []string{"a", "b"}}, {ID: 3, Kind: index.Assistant}}
	view := transcript.AssembleView(nil, &model.Message{Role: model.ConversationRoleUser}, projectEntries(entries[1:]))
	require.Error(t, transcript.ValidateView(view, transcript.GrammarBedrock{}))
	require.Error(t, transcript.ValidateView(view, transcript.GrammarAnthropic{}))
	assert.Equal(t, []index.MessageID{3}, CutPoints(Units(entries, entryRole, 0)))
	unfinished := []index.MessageEntry{{ID: 1, Kind: index.Assistant, ToolUseIDs: []string{"a"}}, {ID: 2, Kind: index.Assistant}}
	assert.Empty(t, CutPoints(Units(unfinished, entryRole, 0)))
	hiddenUnfinished := []index.MessageEntry{{ID: 1, Kind: index.Assistant, ToolUseIDs: []string{"a"}}, {ID: 2, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping}, ToolUseIDs: []string{"a"}}, {ID: 3, Kind: index.Assistant}}
	assert.Empty(t, CutPoints(Units(hiddenUnfinished, entryRole, 0)))
}

func assertClosedPrefix(t *testing.T, entries []index.MessageEntry) {
	t.Helper()
	assert.NoError(t, transcript.ValidateView(append([]*model.Message{{Role: model.ConversationRoleUser}}, projectEntries(entries)...), transcript.GrammarAnthropic{}))
}

func projectEntries(entries []index.MessageEntry) []*model.Message {
	var msgs []*model.Message
	for _, e := range entries {
		switch e.Kind {
		case index.Message, index.Clarification:
			msgs = append(msgs, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "user"}}})
		case index.Assistant:
			parts := []model.Part{model.TextPart{Text: "assistant"}}
			for _, id := range e.ToolUseIDs {
				parts = append(parts, model.ToolUsePart{ID: id})
			}
			msgs = append(msgs, &model.Message{Role: model.ConversationRoleAssistant, Parts: parts})
		case index.ToolResult:
			msgs = projectResults(msgs, e.ToolUseIDs)
		case index.Structural:
			if e.Structural.Kind == index.FinalizeResults {
				msgs = projectResults(msgs, e.ToolUseIDs)
			}
		case index.TypedInput:
			// Typed inputs are not model messages.
		}
	}
	return msgs
}

func projectResults(msgs []*model.Message, ids []string) []*model.Message {
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != model.ConversationRoleUser {
		msgs = append(msgs, &model.Message{Role: model.ConversationRoleUser})
	}
	for _, id := range ids {
		last := msgs[len(msgs)-1]
		last.Parts = append(last.Parts, model.ToolResultPart{ToolUseID: id})
	}
	return msgs
}

func entryRole(e index.MessageEntry) (model.ConversationRole, error) {
	return e.Role, nil
}
