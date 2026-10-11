package checkpoint

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// BytesPerToken is the fixed conversion used by every checkpoint text budget.
const BytesPerToken = 2

// HeadTail makes a deterministic UTF-8-safe preview within maxBytes.
func HeadTail(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	marker := fmt.Sprintf("… [%d bytes not shown] …", len(text))
	if len(marker) > maxBytes {
		return runePrefix(marker, maxBytes)
	}
	available := maxBytes - len(marker)
	head := linePrefix(text, available/2)
	tail := lineSuffix(text, available-available/2)
	omitted := len(text) - len(head) - len(tail)
	marker = fmt.Sprintf("… [%d bytes not shown] …", omitted)
	return head + marker + tail
}

// ClearedResult names the original result without recommending a tool.
func ClearedResult(bytes int, toolUseID, errFirstLine string) string {
	text := fmt.Sprintf("[result cleared: %s, %s]", byteSize(bytes), toolUseID)
	if errFirstLine != "" {
		text += "\n" + firstLine(errFirstLine)
	}
	return text
}

// linePrefix favors complete lines; a single long line uses rune boundaries.
func linePrefix(text string, maxBytes int) string {
	prefix := runePrefix(text, maxBytes)
	if len(prefix) == len(text) {
		return prefix
	}
	if end := strings.LastIndexByte(prefix, '\n'); end >= 0 {
		return prefix[:end+1]
	}
	return prefix
}

// lineSuffix favors complete last lines within its allowance.
func lineSuffix(text string, maxBytes int) string {
	start := max(0, len(text)-maxBytes)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	suffix := text[start:]
	if start == 0 {
		return suffix
	}
	if end := strings.IndexByte(suffix, '\n'); end >= 0 {
		return suffix[end+1:]
	}
	return suffix
}

// runePrefix never cuts within a UTF-8 rune.
func runePrefix(text string, maxBytes int) string {
	end := min(len(text), max(0, maxBytes))
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func byteSize(bytes int) string {
	switch {
	case bytes >= 1000000:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1000000)
	case bytes >= 1000:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1000)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
