package checkpoint

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

// TestUsersCorrection keeps an indexed correction after its pinned predecessor.
func TestUsersCorrection(t *testing.T) {
	users := []UserMessage{user(1, "use production"), user(57, "use staging only"), user(98, "skip migration"), user(140, "done")}
	b := index.RenderBudget{KeepUserTokens: 200}
	lim := DefaultLimits
	lim.MaxPins = 2
	pins := SelectPins(users, b, lim)
	assert.Equal(t, []index.MessageID{1, 140}, pins)
	got := renderUsers(userInput{users: users, pinned: pins, budget: b, limits: lim, currentPos: 1, recordID: "record"})
	assertUserView(t, got)
	assert.Contains(t, got.text, "[message 57, first line] use staging only")
	assert.Less(t, strings.Index(got.text, "[message 1]"), strings.Index(got.text, "[message 57, first line]"))
}

// TestUsersCitedCorrection prioritizes cited record text over newer uncited lines.
func TestUsersCitedCorrection(t *testing.T) {
	users := []UserMessage{user(1, "use production"), user(57, "use staging only")}
	for i := index.MessageID(58); i < 80; i++ {
		users = append(users, user(i, "new words"))
	}
	got := renderUsers(userInput{users: users, cited: []UserLine{{ID: 57, Text: "forged production instruction"}}, pinned: []index.MessageID{1}, budget: index.RenderBudget{KeepUserTokens: 125}, limits: DefaultLimits, currentPos: 1, recordID: "record"})
	assertUserView(t, got)
	assert.Contains(t, got.text, "[message 57, first line] use staging only")
	assert.NotContains(t, got.text, "forged")
	assert.Contains(t, got.text, "[users rc-")
	assert.LessOrEqual(t, len(got.text), 250)
}

// TestUsersPinsShrunkBudget rechecks stored pins first-user then newest-first.
func TestUsersPinsShrunkBudget(t *testing.T) {
	for _, tt := range []struct {
		name  string
		older string
	}{
		{"equal older sizes", strings.Repeat("a", 60)},
		{"older would fit after a gap", "a"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			users := []UserMessage{user(1, "task"), user(2, tt.older), user(3, strings.Repeat("b", 60)), user(4, "newest correction "+strings.Repeat("c", 80))}
			pins := SelectPins(users, index.RenderBudget{KeepUserTokens: 400}, DefaultLimits)
			assert.Equal(t, []index.MessageID{1, 4, 3, 2}, pins)
			got := renderUsers(userInput{users: users, pinned: pins, budget: index.RenderBudget{KeepUserTokens: 200}, limits: DefaultLimits, currentPos: 1, recordID: "record"})
			assertUserView(t, got)
			assert.Contains(t, got.text, "[message 1] task")
			assert.Contains(t, got.text, "[message 4] newest correction "+strings.Repeat("c", 80))
			for _, id := range []string{"2", "3"} {
				assert.Contains(t, got.text, "[message "+id+", first line]")
				assert.NotContains(t, got.text, "[message "+id+"]")
			}
			assert.LessOrEqual(t, len(got.text), 400)
		})
	}
}

// TestUsersIndexWhole keeps all index lines when their actual bytes fit.
func TestUsersIndexWhole(t *testing.T) {
	got := renderUsers(userInput{users: []UserMessage{user(1, "one"), user(2, "two"), user(3, "three")}, budget: index.RenderBudget{KeepUserTokens: 60}, limits: DefaultLimits, currentPos: 1, recordID: "record"})
	assertUserView(t, got)
	for _, line := range []string{"[message 1, first line] one", "[message 2, first line] two", "[message 3, first line] three"} {
		assert.Contains(t, got.text, line)
	}
	assert.NotContains(t, got.text, "[users rc-")
}

// TestUsersPreview checks oversized first-pin previews and recent-pin demotion.
func TestUsersPreview(t *testing.T) {
	text := "head\n" + strings.Repeat("long middle\n", 100) + "tail"
	got := renderUsers(userInput{users: []UserMessage{user(1, text)}, pinned: []index.MessageID{1}, budget: index.RenderBudget{KeepUserTokens: 300}, limits: DefaultLimits, currentPos: 1, recordID: "record"})
	assertUserView(t, got)
	assert.Contains(t, got.text, "[message 1]")
	assert.Contains(t, got.text, "bytes not shown")
	assert.Contains(t, got.text, "head")
	assert.Contains(t, got.text, "tail")
	// A previously whole recent pin becomes an index line under a smaller budget.
	got = renderUsers(userInput{users: []UserMessage{user(1, "task"), user(2, text)}, pinned: []index.MessageID{1, 2}, budget: index.RenderBudget{KeepUserTokens: 300}, limits: DefaultLimits, currentPos: 1, recordID: "record"})
	assertUserView(t, got)
	assert.Contains(t, got.text, "[message 2, first line]")
	assert.Contains(t, got.text, "bytes]")
}

// TestUserParts checks pin part counts, fallbacks, placeholders and text documents.
func TestUserParts(t *testing.T) {
	image := model.ImagePart{Format: "png", Bytes: make([]byte, 1200000)}
	tests := []struct {
		name        string
		parts       []model.Part
		tokens      PartTokens
		budget      int
		kept        int
		placeholder string
	}{
		{"three images", []model.Part{image, image, image}, func(model.Part) int {
			return 10
		}, 1000, 2, "part 4, png, 1.2 MB"},
		{"quarter exceeded", []model.Part{image}, func(model.Part) int {
			return 251
		}, 1000, 0, "part 2, png, 1.2 MB"},
		{"fallback fits", []model.Part{image}, nil, 6000, 1, ""},
		{"fallback too big", []model.Part{image}, nil, 1000, 0, "image not shown: message 1"},
		{"small text budget", []model.Part{image}, nil, 100, 0, "image not shown: message 1"},
		{"pointer image", []model.Part{&image}, nil, 6000, 1, ""},
		{"URI", []model.Part{model.DocumentPart{Name: "attachment", Format: "pdf", URI: "s3://document"}}, func(model.Part) int {
			return 10
		}, 1000, 1, ""},
		{"byte document", []model.Part{model.DocumentPart{Name: "attachment", Format: "pdf", Bytes: make([]byte, 800)}}, nil, 1000, 0, "document not shown"},
		{"text document", []model.Part{model.DocumentPart{Name: "attachment", Format: "txt", Text: strings.Repeat("line\n", 200)}}, nil, 1000, 0, "bytes not shown"},
		{"chunk document", []model.Part{model.DocumentPart{Name: "attachment", Format: "txt", Chunks: []string{strings.Repeat("line\n", 200), "end"}}}, nil, 1000, 0, "bytes not shown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderUsers(userInput{users: []UserMessage{user(1, "task", tt.parts...)}, pinned: []index.MessageID{1}, budget: index.RenderBudget{KeepUserTokens: tt.budget}, limits: DefaultLimits, partTokens: tt.tokens, currentPos: 1, recordID: "record"})
			assertUserView(t, got)
			assert.Len(t, got.parts, tt.kept)
			if tt.placeholder != "" {
				assert.Contains(t, got.text, tt.placeholder)
			}
			assert.LessOrEqual(t, len(got.text), tt.budget*2)
		})
	}
}

// TestHeadTail checks UTF-8-safe deterministic previews and cleared result text.
func TestHeadTail(t *testing.T) {
	for _, text := range []string{"first\n" + strings.Repeat("line\n", 80) + "last", strings.Repeat("世界🙂", 80), "short"} {
		for _, budget := range []int{0, 1, 15, 80, 160, 1000} {
			got := HeadTail(text, budget)
			assert.LessOrEqual(t, len(got), budget)
			assert.True(t, utf8.ValidString(got))
			assert.Equal(t, got, HeadTail(text, budget))
			if len(text) <= budget {
				assert.Equal(t, text, got)
			}
		}
	}
	assert.Equal(t, "[result cleared: 18.2 KB, toolu_123]", ClearedResult(18200, "toolu_123", ""))
	assert.Equal(t, "[result cleared: 18.2 KB, toolu_123]\nfailed", ClearedResult(18200, "toolu_123", "failed\nsecret"))
}

func assertUserView(t *testing.T, section userSection) {
	t.Helper()
	message := &model.Message{Role: model.ConversationRoleUser, Parts: append([]model.Part{model.TextPart{Text: section.text}}, section.parts...)}
	assertView(t, message)
}

func user(id index.MessageID, text string, parts ...model.Part) UserMessage {
	return UserMessage{ID: id, Message: &model.Message{Role: model.ConversationRoleUser, Parts: append([]model.Part{model.TextPart{Text: text}}, parts...)}}
}
