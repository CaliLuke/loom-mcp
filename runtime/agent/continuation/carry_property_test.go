package continuation

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCarryExhaustiveParts(t *testing.T) {
	// Exhaust all model kinds, sizes, Keep flags and result forms in each part
	// position, with one/two units and one/three parts in the newest unit.
	variants := make([]model.Part, 0, 12)
	for _, size := range []int{1, 768} {
		variants = append(variants, model.TextPart{Text: strings.Repeat("t", size)}, model.ToolUsePart{ID: "c", Name: "t", Input: map[string]string{"arg": strings.Repeat("a", size)}}, model.ThinkingPart{Text: strings.Repeat("r", size)})
		for _, form := range []string{"full", "preview", "placeholder"} {
			text := strings.Repeat("x", size)
			if form == "preview" {
				text = strings.Repeat("x", min(size, 256))
			}
			if form == "placeholder" {
				text = "[result cleared]"
			}
			variants = append(variants, model.ToolResultPart{ToolUseID: "c", Content: text})
		}
	}
	cases := 0
	for units := 1; units <= 2; units++ {
		for count := 1; count <= 3; count++ {
			combinations := 1
			for range count {
				combinations *= len(variants)
			}
			for n := range combinations {
				for _, keep := range []bool{false, true} {
					parts := make([]model.Part, count)
					code := n
					for i := range parts {
						parts[i] = variants[code%len(variants)]
						code /= len(variants)
					}
					entries := []Entry{}
					if units == 2 {
						entries = append(entries, Entry{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant}, Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("old", 256)}}}}})
					}
					entries = append(entries, Entry{Entry: index.MessageEntry{ID: index.MessageID(units), Kind: index.Assistant, Role: model.ConversationRoleAssistant, Keep: keep, Origin: &index.Origin{Scope: "r1", Run: "r1", Pos: 1, ID: 1, Form: "full"}}, Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: parts}}})
					plan, err := Carry(entries, index.ViewRef{}, 768, 256)
					require.NoError(t, err)
					assertCarryProperties(t, entries, plan, 768)
					assert.Equal(t, len(entries), len(plan.Tail)+len(plan.Evicted))
					for _, entry := range plan.Tail {
						for _, msg := range entry.Messages {
							for _, part := range msg.Parts {
								_, reasoning := part.(model.ThinkingPart)
								require.False(t, reasoning)
							}
						}
					}
					// Re-carry is self contained: inputs are the carried bytes, never originals.
					again, err := Carry(plan.Tail, index.ViewRef{TailFrom: plan.TailFrom}, 768, 256)
					require.NoError(t, err)
					assertCarryProperties(t, plan.Tail, again, 768)
					for i := range again.Tail {
						assert.Equal(t, plan.Tail[i].Entry.Origin, again.Tail[i].Entry.Origin)
					}
					cases++
				}
			}
		}
	}
	t.Logf("checked %d carried forms", cases)
}

func TestCarryFullIsByteIdenticalAndCap(t *testing.T) {
	message := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "decision after compaction"}}}
	plan, err := Carry([]Entry{{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant}, Messages: []*model.Message{message}}}, index.ViewRef{}, 10000, 256)
	require.NoError(t, err)
	before, err := json.Marshal(message)
	require.NoError(t, err)
	after, err := json.Marshal(plan.Tail[0].Messages[0])
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.Equal(t, 120000, Cap(compaction.Settings{}, 0))
	assert.Equal(t, 4000, Cap(compaction.Settings{}, 8000))
}
