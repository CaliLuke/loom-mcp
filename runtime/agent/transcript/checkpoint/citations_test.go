package checkpoint

import (
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type citationSource struct {
	lines   map[OrderKey]UserLine
	roles   map[OrderKey]model.ConversationRole
	kinds   map[OrderKey]index.EntryKind
	through map[int]index.MessageID
}

type brokenResolver struct{}

var errResolve = errors.New("store unavailable")

// TestValidateCitations rejects non-user, unknown and uncovered references.
func TestValidateCitations(t *testing.T) {
	source := citationSource{lines: map[OrderKey]UserLine{{Pos: 1, ID: 5}: {ID: 5, Text: "record"}}, roles: map[OrderKey]model.ConversationRole{{Pos: 1, ID: 5}: model.ConversationRoleUser, {Pos: 1, ID: 6}: model.ConversationRoleAssistant, {Pos: 1, ID: 7}: model.ConversationRoleUser}, through: map[int]index.MessageID{1: 6}}
	source.roles[OrderKey{Pos: 1, ID: 4}] = model.ConversationRoleUser
	source.kinds = map[OrderKey]index.EntryKind{{Pos: 1, ID: 4}: index.ToolResult}
	for _, tt := range []struct {
		text string
		want error
	}{
		{"[message 5]", nil}, {"[message 6]", ErrCitesNonUser}, {"[message 4]", ErrCitesNonUser}, {"[message 99]", ErrCitesUnknown}, {"[message 7]", ErrCitesNotBehind},
	} {
		lines, err := ValidateCitations(tt.text, 1, source)
		require.ErrorIs(t, err, tt.want)
		if tt.want == nil {
			require.NoError(t, err)
			assert.Equal(t, "record", lines[0].Text)
		}
	}
	assert.Empty(t, ParseCitations("[Message 5] [messages 5–7] [message 0] [r0 message 5] [message 05] [r01 message 5]"))
	assert.Len(t, ParseCitations("[message 5] [r2 message 5]"), 2)
	_, err := ValidateCitations("[message 5] [message 5]", 1, source)
	assert.NoError(t, err)
}

// TestCitationContinuation keeps copied and re-summarized citations bound to their origin.
func TestCitationContinuation(t *testing.T) {
	source := citationSource{lines: map[OrderKey]UserLine{}, roles: map[OrderKey]model.ConversationRole{}, through: map[int]index.MessageID{1: 99, 2: 99, 3: 99}}
	for pos := 1; pos <= 3; pos++ {
		key := OrderKey{Pos: pos, ID: 5}
		source.lines[key] = UserLine{ID: 5, Origin: &index.Origin{Pos: pos, ID: 5}, Text: Label(5, &index.Origin{Pos: pos, ID: 5}, 0)}
		source.roles[key] = model.ConversationRoleUser
	}
	for _, current := range []int{2, 3} {
		qualified := Qualify("Keep [message 5] and [r2 message 5].", 1, current)
		assert.Equal(t, "Keep [r1 message 5] and [r2 message 5].", qualified)
		got, err := ValidateCitations(qualified, current, source)
		require.NoError(t, err)
		assert.Equal(t, 1, got[0].Origin.Pos)
		raw, err := ValidateCitations("[message 5]", current, source)
		require.NoError(t, err)
		assert.Equal(t, current, raw[0].Origin.Pos)
		assert.NotEqual(t, raw[0], got[0])
	}
	assert.Equal(t, "[message 5]", Qualify("[message 5]", 1, 1))
}

// TestCitationResolverError preserves errors from the authorized resolver.
func TestCitationResolverError(t *testing.T) {
	_, err := ValidateCitations("[message 5]", 1, brokenResolver{})
	assert.ErrorIs(t, err, errResolve)
}

func (s citationSource) Resolve(key OrderKey) (CitationTarget, bool, error) {
	role, found := s.roles[key]
	kind := s.kinds[key]
	if kind == "" {
		kind = index.Message
	}
	return CitationTarget{Line: s.lines[key], Role: role, Kind: kind}, found, nil
}
func (s citationSource) Behind(key OrderKey) bool {
	return key.ID <= s.through[key.Pos]
}

func (brokenResolver) Resolve(OrderKey) (CitationTarget, bool, error) {
	return CitationTarget{}, false, errResolve
}
func (brokenResolver) Behind(OrderKey) bool {
	return true
}
