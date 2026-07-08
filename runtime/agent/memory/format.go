package memory

import (
	"fmt"
	"strings"
	"time"
)

const (
	longTermMemoryPromptOpen  = "<LONG_TERM_MEMORY>"
	longTermMemoryPromptClose = "</LONG_TERM_MEMORY>"
)

// FormatEntriesForPrompt renders long-term memory entries into stable planner prompt text.
func FormatEntriesForPrompt(entries []Entry) string {
	lines := make([]string, 0, len(entries)*2)
	for _, entry := range entries {
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		if !entry.Timestamp.IsZero() {
			lines = append(lines, fmt.Sprintf("Time: %s", entry.Timestamp.UTC().Format(time.RFC3339)))
		}
		if strings.TrimSpace(entry.Author) != "" {
			content = fmt.Sprintf("%s: %s", strings.TrimSpace(entry.Author), content)
		}
		lines = append(lines, content)
	}
	if len(lines) == 0 {
		return ""
	}
	return longTermMemoryPromptOpen + "\n" + strings.Join(lines, "\n") + "\n" + longTermMemoryPromptClose
}
