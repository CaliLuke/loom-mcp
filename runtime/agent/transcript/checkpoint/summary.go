package checkpoint

import "strings"

type (
	// Summary preserves the source run and record of immutable stored summary text.
	Summary struct {
		Text, Run, ID string
		// RecordPos identifies the checkpoint scope holding this copy, when recorded.
		RecordPos *int
	}
	summarySection struct {
		heading, body string
	}
)

// BoundSummary allocates a byte budget across ordered Markdown sections.
// Whole text is unchanged. Short sections pass their unused share forward.
func BoundSummary(text string, maxBytes int, closing string) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	if len(closing) >= maxBytes {
		return runePrefix(closing, maxBytes)
	}
	sections := summarySections(text)
	available := maxBytes - len(closing)
	kept := make([]summarySection, 0, len(sections))
	for _, section := range sections {
		if len(section.heading) > available {
			break
		}
		kept = append(kept, section)
		available -= len(section.heading)
	}
	if len(kept) == 0 {
		return closing
	}
	var result strings.Builder
	for i, section := range kept {
		result.WriteString(section.heading)
		share := available / (len(kept) - i)
		body := summaryPrefix(section.body, share)
		result.WriteString(body)
		available -= len(body)
	}
	result.WriteString(closing)
	return result.String()
}

// summarySections recognizes ATX headings outside fenced code blocks.
func summarySections(text string) []summarySection {
	var sections []summarySection
	current := summarySection{}
	bodyStart, offset := 0, 0
	fence := ""
	for line := range strings.SplitAfterSeq(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "~~~") || strings.HasPrefix(trimmed, "\x60\x60\x60") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if marker == fence {
				fence = ""
			}
		}
		if fence == "" && markdownHeading(line) {
			current.body = text[bodyStart:offset]
			if current.heading != "" || len(current.body) > 0 {
				sections = append(sections, current)
			}
			current = summarySection{heading: line}
			bodyStart = offset + len(line)
		}
		offset += len(line)
	}
	current.body = text[bodyStart:]
	if current.heading != "" || current.body != "" {
		sections = append(sections, current)
	}
	return sections
}

func markdownHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	return hashes > 0 && hashes <= 6 && (len(trimmed) == hashes || trimmed[hashes] == ' ' || trimmed[hashes] == '\n')
}

// summaryPrefix keeps complete lines; a single unbroken line uses rune cuts.
func summaryPrefix(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	prefix := runePrefix(text, maxBytes)
	if end := strings.LastIndexByte(prefix, '\n'); end >= 0 {
		return prefix[:end+1]
	}
	if !strings.Contains(text, "\n") {
		return prefix
	}
	return ""
}
