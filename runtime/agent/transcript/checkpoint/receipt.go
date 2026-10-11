package checkpoint

import (
	"fmt"
	"iter"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// ByteRange is an inclusive unsummarised byte range in the original result.
	ByteRange = index.ByteRange
	// ReceiptItem supplies a tool call and/or assistant text from the record chain.
	ReceiptItem struct {
		ToolUseID, Tool, Args string
		// Lookup hides the runtime lookup tool name while retaining its call ID.
		Lookup       bool
		AssistantID  index.MessageID
		Text         string
		Unsummarised []ByteRange
		Origin       *index.Origin
	}
	// EarlierCounts carries bounded receipt totals for predecessor runs.
	EarlierCounts struct {
		Calls, Assistants int
		FirstPos, LastPos int
	}
	receiptCounts struct {
		calls, assistants int
		first, last       string
	}
	receiptLine struct {
		text, label string
		pos         int
		assistant   bool
	}
)

const (
	// ReceiptArgsBytes bounds the eligible call argument preview stored by K12.
	ReceiptArgsBytes = 160
	// MaxReceiptLineBytes caps one rendered receipt line including its newline.
	MaxReceiptLineBytes    = 512
	receiptRangeLabelBytes = 64
	compactReceiptBytes    = len("… [receipt rc-xxxxxxxx]\n") + len("… [earlier rc-xxxxxxxx]\n")
)

// renderReceipt traverses the chain once and retains only bounded newest lines.
// Aggregate lines are measured exactly, evicting the oldest retained lines
// until the newest prefix and both aggregate handles fit together.
func renderReceipt(items iter.Seq[ReceiptItem], earlier EarlierCounts, maxBytes int, recordID string, currentPos int) string {
	if maxBytes <= 0 {
		return ""
	}
	shown, omitted, earlier := selectReceiptLines(items, earlier, maxBytes, currentPos)
	shownBytes := receiptBytes(shown)
	for {
		current, previous := collapseReceipt(omitted, recordID), collapseEarlier(earlier, recordID)
		if shownBytes+len(current)+len(previous) <= maxBytes {
			var text strings.Builder
			for _, line := range shown {
				text.WriteString(line.text)
			}
			text.WriteString(current + previous)
			return text.String()
		}
		if len(shown) == 0 {
			current, previous = "", ""
			if omitted.calls != 0 || omitted.assistants != 0 {
				current = compactHandle("receipt", recordID)
			}
			if earlier.Calls != 0 || earlier.Assistants != 0 {
				previous = compactHandle("earlier", recordID)
			}
			if len(current)+len(previous) <= maxBytes {
				return current + previous
			}
			// Render reserves both compact lines; standalone smaller budgets cannot.
			return ""
		}
		last := shown[len(shown)-1]
		shownBytes -= len(last.text)
		shown = shown[:len(shown)-1]
		if last.pos != currentPos {
			countEarlier(&earlier, last)
		} else {
			first := omitted.first
			countReceipt(&omitted, last)
			if first != "" {
				omitted.first = first
			}
			omitted.last = last.label
		}
	}
}

func selectReceiptLines(items iter.Seq[ReceiptItem], earlier EarlierCounts, maxBytes, currentPos int) ([]receiptLine, receiptCounts, EarlierCounts) {
	var shown []receiptLine
	omitted := receiptCounts{}
	remaining := maxBytes
	stopped := false
	if items != nil {
		for item := range items {
			for _, line := range receiptLines(item, currentPos) {
				if !stopped && len(line.text) <= remaining {
					shown = append(shown, line)
					remaining -= len(line.text)
				} else {
					stopped = true
					if line.pos != currentPos {
						countEarlier(&earlier, line)
					} else {
						countReceipt(&omitted, line)
					}
				}
			}
		}
	}
	return shown, omitted, earlier
}

// receiptLines produces separate discovery lines for calls and assistant prose.
func receiptLines(item ReceiptItem, currentPos int) []receiptLine {
	lines := make([]receiptLine, 0, 2)
	pos := currentPos
	if item.Origin != nil {
		pos = item.Origin.Pos
	}
	if item.ToolUseID != "" {
		id := item.ToolUseID
		if pos != currentPos {
			id = fmt.Sprintf("r%d:%s", pos, id)
		}
		text := fmt.Sprintf("%s %s(%s)", id, item.Tool, item.Args)
		if item.Lookup {
			prefix := id + " (context lookup: "
			text = prefix + HeadTail(item.Args, max(0, MaxReceiptLineBytes-len(prefix)-2)) + ")"
		}
		for _, span := range item.Unsummarised {
			text += fmt.Sprintf("; bytes %s–%s not summarised", thousands(span.From), thousands(span.Through))
		}
		lines = append(lines, receiptLine{text: HeadTail(text, MaxReceiptLineBytes-1) + "\n", pos: pos, label: HeadTail(item.ToolUseID, receiptRangeLabelBytes)})
	}
	if item.Text != "" {
		prefix := Label(item.AssistantID, item.Origin, currentPos) + ", assistant: \""
		text := runePrefix(firstLine(item.Text), max(0, MaxReceiptLineBytes-len(prefix)-2))
		id := Key(item.AssistantID, item.Origin, currentPos).ID
		lines = append(lines, receiptLine{text: prefix + text + "\"\n", assistant: true, pos: pos, label: fmt.Sprintf("%d", id)})
	}
	return lines
}

func countReceipt(counts *receiptCounts, line receiptLine) {
	if line.assistant {
		counts.assistants++
	} else {
		counts.calls++
	}
	if counts.last == "" {
		counts.last = line.label
	}
	counts.first = line.label
}

func countEarlier(counts *EarlierCounts, line receiptLine) {
	if line.assistant {
		counts.Assistants++
	} else {
		counts.Calls++
	}
	if counts.FirstPos == 0 || line.pos < counts.FirstPos {
		counts.FirstPos = line.pos
	}
	counts.LastPos = max(counts.LastPos, line.pos)
}

func collapseReceipt(counts receiptCounts, recordID string) string {
	if counts.calls+counts.assistants == 0 {
		return ""
	}
	return fmt.Sprintf("… %d earlier calls and %d assistant messages in %s–%s [receipt %s]\n", counts.calls, counts.assistants, counts.first, counts.last, Handle("receipt", recordID))
}

func collapseEarlier(counts EarlierCounts, recordID string) string {
	if counts.Calls+counts.Assistants == 0 {
		return ""
	}
	return fmt.Sprintf("… %d calls and %d assistant messages in earlier runs r%d–r%d [receipt %s]\n", counts.Calls, counts.Assistants, counts.FirstPos, counts.LastPos, Handle("earlier", recordID))
}

func receiptBytes(lines []receiptLine) int {
	bytes := 0
	for _, line := range lines {
		bytes += len(line.text)
	}
	return bytes
}

func thousands(value int) string {
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}
