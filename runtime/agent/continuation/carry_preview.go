package continuation

import (
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
)

// previewEntries always reads the pre-loop projection; shrinking cannot wrap an
// earlier iteration's argument preview or lose its original omission count.
func previewEntries(entries, original []Entry, limit int) error {
	for i := range entries {
		entry := &entries[i]
		for m, message := range entry.Messages {
			for j, part := range original[i].Messages[m].Parts {
				switch p := part.(type) {
				case model.TextPart:
					if len(p.Text) > limit {
						p.Text = previewCarriedText(p.Text, limit, original[i].Form == Preview)
						entry.Form = Preview
					}
					message.Parts[j] = p
				case model.CitationsPart:
					if len(p.Text) > limit {
						p.Text = previewCarriedText(p.Text, limit, original[i].Form == Preview)
						entry.Form = Preview
					}
					message.Parts[j] = p
				case model.ToolUsePart:
					text, carried := carriedArgumentText(p.Input, original[i].Form)
					if !carried {
						data, err := json.Marshal(p.Input, json.Deterministic(true))
						if err != nil {
							return err
						}
						text = string(data)
					}
					if len(text) > limit {
						p.Input = map[string]string{Preview: previewCarriedText(text, limit, carried)}
						entry.Form = Preview
					}
					message.Parts[j] = p
				}
			}
		}
	}
	return nil
}

func carriedArgumentText(input any, form string) (string, bool) {
	if form != Preview {
		return "", false
	}
	switch value := input.(type) {
	case map[string]string:
		text, ok := value[Preview]
		return text, ok && len(value) == 1
	case map[string]any:
		text, ok := value[Preview].(string)
		return text, ok && len(value) == 1
	}
	return "", false
}

// previewCarriedText preserves the omitted bytes of an earlier carried preview.
// Only its retained head/tail may be shortened; the missing middle is never read.
func previewCarriedText(text string, limit int, carried bool) string {
	if !carried {
		return checkpoint.HeadTail(text, limit)
	}
	head, rest, found := strings.Cut(text, "… [")
	count, tail, closed := strings.Cut(rest, " bytes not shown] …")
	omitted, err := strconv.Atoi(count)
	if !found || !closed || err != nil || omitted < 0 {
		return checkpoint.HeadTail(text, limit)
	}
	originalBytes := len(head) + len(tail) + omitted
	available := max(0, limit-len(fmt.Sprintf("… [%d bytes not shown] …", originalBytes)))
	headEnd := min(len(head), available/2)
	for headEnd > 0 && headEnd < len(head) && !utf8.RuneStart(head[headEnd]) {
		headEnd--
	}
	tailStart := max(0, len(tail)-(available-available/2))
	for tailStart < len(tail) && !utf8.RuneStart(tail[tailStart]) {
		tailStart++
	}
	head, tail = head[:headEnd], tail[tailStart:]
	return fmt.Sprintf("%s… [%d bytes not shown] …%s", head, originalBytes-len(head)-len(tail), tail)
}
