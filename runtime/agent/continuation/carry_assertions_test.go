package continuation

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// assertCarryProperties derives bounds and copy safety from rendered parts,
// independently of Plan.Minimal and the implementation's form classification.
func assertCarryProperties(t *testing.T, source []Entry, plan Plan, capBytes int) {
	t.Helper()
	const minimum = 256
	measured := 0
	minimal := true
	for _, entry := range plan.Tail {
		var original Entry
		for _, candidate := range source {
			if candidate.Entry.ID == entry.Entry.ID {
				original = candidate
				break
			}
		}
		data, err := json.Marshal(entry.Messages, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		measured += len(data)
		// CopyIsRendered: the new scope receives exactly the rendered projection;
		// it must decode and render without any source-scope content.
		var copied []*model.Message
		if err := json.Unmarshal(data, &copied); err != nil {
			t.Fatal(err)
		}
		rendered, err := json.Marshal(copied, json.Deterministic(true))
		if err != nil || string(rendered) != string(data) {
			t.Fatalf("CopyIsRendered/RenderableFromCopy: %s: %v", rendered, err)
		}
		for m, message := range entry.Messages {
			originalParts := make([]model.Part, 0)
			for _, part := range original.Messages[m].Parts {
				if _, thinking := part.(model.ThinkingPart); !thinking {
					originalParts = append(originalParts, part)
				}
			}
			for j, part := range message.Parts {
				if entry.Form == Full && !reflect.DeepEqual(originalParts[j], part) {
					t.Fatal("RenderableFromCopy: a full part changed")
				}
				switch p := part.(type) {
				case model.TextPart:
					minimal = minimal && len(p.Text) <= minimum
				case model.CitationsPart:
					minimal = minimal && len(p.Text) <= minimum
				case model.ToolUsePart:
					size, err := json.Marshal(p.Input)
					if err != nil {
						t.Fatal(err)
					}
					var preview map[string]string
					if err := json.Unmarshal(size, &preview); err == nil && len(preview) == 1 && preview[Preview] != "" {
						minimal = minimal && len(preview[Preview]) <= minimum
					} else {
						minimal = minimal && len(size) <= minimum
					}
				case model.ToolResultPart:
					text, ok := p.Content.(string)
					placeholder := ok && strings.HasPrefix(text, "[result cleared")
					minimal = minimal && placeholder
					if old, ok := originalParts[j].(model.ToolResultPart); ok {
						oldText, wasText := old.Content.(string)
						if wasText && strings.HasPrefix(oldText, "[result cleared") && !placeholder {
							t.Fatal("PlaceholdersStick: original content restored")
						}
						if !placeholder {
							before, err := json.Marshal(old)
							if err != nil {
								t.Fatal(err)
							}
							after, err := json.Marshal(p)
							if err != nil || string(before) != string(after) {
								t.Fatalf("RenderableFromCopy: full result changed: %v", err)
							}
						}
					}
				case model.ThinkingPart:
					t.Fatal("NoReasoningCarried")
				}
			}
		}
	}
	if measured != plan.Bytes {
		t.Fatalf("CopyIsRendered: measured %d, reported %d", measured, plan.Bytes)
	}
	if measured > capBytes && !minimal {
		t.Fatalf("CarryBounded: %d > %d and rendered parts are not independently minimal", measured, capBytes)
	}
}
