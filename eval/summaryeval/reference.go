package summaryeval

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

// boundedReference elides the middle of the largest units first, bounding both
// visible bytes and quoted/JSON-escaped bytes before passing input to the judge.
func boundedReference(ev summarize.Evidence, limit int) (string, bool) {
	parts := make([]string, 0, len(ev.Units)+1)
	parts = append(parts, "Previous summary:\n"+ev.Previous)
	for _, unit := range ev.Units {
		parts = append(parts, unit.Label+":\n"+unit.Text)
	}
	original := slices.Clone(parts)
	truncated := false
	for {
		text := strings.Join(parts, "\n\n")
		size := len(strconv.Quote(strconv.Quote(text)))
		if size <= limit {
			return text, truncated
		}
		largest := 0
		for i := range parts {
			if len(parts[i]) > len(parts[largest]) {
				largest = i
			}
		}
		target := len(parts[largest]) * 3 / 4
		if target < 40 {
			return elide(text, max(32, limit/8)), true
		}
		parts[largest] = elide(original[largest], target)
		truncated = true
	}
}

func elide(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	marker := fmt.Sprintf("\n[… %d bytes elided …]\n", len(text)-limit)
	kept := max(0, limit-len(marker))
	left := kept / 2
	right := len(text) - (kept - left)
	for left > 0 && !utf8.RuneStart(text[left]) {
		left--
	}
	for right < len(text) && !utf8.RuneStart(text[right]) {
		right++
	}
	return text[:left] + marker + text[right:]
}
