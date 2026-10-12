package continuation

import (
	"strconv"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type modelCarryPart struct {
	part model.Part
	keep bool
	form string
}

// TestCarryModelBounds enumerates the exact initial domain of ContinuationCarry:
// older units have 1–2 valid parts, newest units 1–3; a third is large text/args.
func TestCarryModelBounds(t *testing.T) {
	variants := modelCarryVariants()
	units := make([][]modelCarryPart, 0, len(variants)+len(variants)*len(variants))
	for _, a := range variants {
		units = append(units, []modelCarryPart{a})
		for _, b := range variants {
			units = append(units, []modelCarryPart{a, b})
		}
	}
	newest := append([][]modelCarryPart{}, units...)
	third := []modelCarryPart{{part: model.TextPart{Text: strings.Repeat("t", 768)}, form: Full}, {part: model.ToolUsePart{ID: "third", Name: "tool", Input: map[string]string{"q": strings.Repeat("a", 768)}}, form: Full}}
	for _, a := range variants {
		for _, b := range variants {
			for _, c := range third {
				newest = append(newest, []modelCarryPart{a, b, c})
			}
		}
	}
	cases := len(newest) * (len(units) + 1)
	if cases != 214032 {
		t.Fatalf("model domain drift: %d cases", cases)
	}
	for i, tail := range newest {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			verifyModelCarry(t, []Entry{modelCarryEntry(1, tail)})
			for _, old := range units {
				verifyModelCarry(t, []Entry{modelCarryEntry(1, old), modelCarryEntry(2, tail)})
			}
		})
	}

	t.Logf("checked all %d initial model configurations", cases)
}

func modelCarryVariants() []modelCarryPart {
	variants := make([]modelCarryPart, 0, 16)
	for _, size := range []int{256, 768} {
		variants = append(variants, modelCarryPart{part: model.TextPart{Text: strings.Repeat("t", size)}, form: Full}, modelCarryPart{part: model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]string{"q": strings.Repeat("a", size)}}, form: Full}, modelCarryPart{part: model.ThinkingPart{Text: strings.Repeat("reasoning", size)}, form: Full})
		for _, keep := range []bool{false, true} {
			for _, form := range []string{Full, Preview, Placeholder} {
				if keep && form == Placeholder {
					continue
				}
				text := strings.Repeat("r", size)
				if form == Preview {
					text = text[:256]
				}
				if form == Placeholder {
					text = "[result cleared]"
				}
				variants = append(variants, modelCarryPart{part: model.ToolResultPart{ToolUseID: "result", Content: text}, keep: keep, form: form})
			}
		}
	}
	return variants
}

func modelCarryEntry(id index.MessageID, parts []modelCarryPart) Entry {
	entry := Entry{Entry: index.MessageEntry{ID: id, Kind: index.Assistant, Role: model.ConversationRoleAssistant, Origin: &index.Origin{Scope: "original", Run: "original", Pos: 1, ID: id, Form: Full}}, Form: Full}
	message := &model.Message{Role: model.ConversationRoleAssistant, Parts: make([]model.Part, 0, len(parts))}
	for _, p := range parts {
		message.Parts = append(message.Parts, p.part)
		entry.Entry.Keep = entry.Entry.Keep || p.keep
		if p.form != Full {
			entry.Form = p.form
		}
	}
	entry.Messages = []*model.Message{message}
	return entry
}

func verifyModelCarry(t *testing.T, entries []Entry) {
	plan, err := Carry(entries, index.ViewRef{}, 512, 256)
	if err != nil {
		t.Fatal(err)
	}
	assertCarryProperties(t, entries, plan, 512)
	if len(plan.Tail)+len(plan.Evicted) != len(entries) {
		t.Fatal("unaccounted unit")
	}
	for _, entry := range plan.Tail {
		for _, message := range entry.Messages {
			for _, part := range message.Parts {
				if _, ok := part.(model.ThinkingPart); ok {
					t.Fatal("reasoning carried")
				}
			}
		}
	}
	again, err := Carry(plan.Tail, index.ViewRef{TailFrom: plan.TailFrom}, 512, 256)
	if err != nil {
		t.Fatal(err)
	}
	assertCarryProperties(t, plan.Tail, again, 512)
	for _, entry := range again.Tail {
		if entry.Entry.Origin.Scope != "original" || entry.Entry.Origin.ID != entry.Entry.ID {
			t.Fatalf("origin changed: %+v", entry.Entry)
		}
	}
}
