package checkpoint

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type (
	// PartTokens estimates one non-text part; nil uses the design's fixed fallbacks.
	PartTokens    func(model.Part) int
	partAllowance struct {
		count, tokens int
		measure       PartTokens
	}
)

// nonTextTokens measures each part independently of index entry estimates.
func nonTextTokens(part model.Part, measure PartTokens) int {
	if measure != nil {
		return max(0, measure(part))
	}
	if value, ok := partValue(part).(model.DocumentPart); ok && len(value.Bytes) > 0 {
		return (len(value.Bytes) + 1) / BytesPerToken
	}
	return 1500
}

// PartPlaceholder names an omitted original attachment without retaining its bytes.
func PartPlaceholder(part model.Part, label string, number int) string {
	kind, format, size := "image", "", 0
	switch value := partValue(part).(type) {
	case model.ImagePart:
		format, size = string(value.Format), len(value.Bytes)
	case model.DocumentPart:
		kind, format, size = "document", string(value.Format), len(value.Bytes)
		if value.URI != "" {
			size = len(value.URI)
		}
	}
	return fmt.Sprintf("[%s not shown: %s, part %d, %s, %s]", kind, label, number, format, byteSize(size))
}

// userText excludes reasoning and includes record-authored text document content.
func userText(message *model.Message) string {
	var texts []string
	for _, part := range message.Parts {
		switch value := partValue(part).(type) {
		case model.TextPart:
			texts = append(texts, value.Text)
		case model.CitationsPart:
			texts = append(texts, value.Text)
		case model.DocumentPart:
			if value.Text != "" {
				texts = append(texts, value.Text)
			} else if len(value.Chunks) > 0 {
				texts = append(texts, strings.Join(value.Chunks, "\n"))
			}
		}
	}
	return strings.Join(texts, "\n")
}

func hasNonText(part model.Part) bool {
	switch value := partValue(part).(type) {
	case model.ImagePart:
		return true
	case model.DocumentPart:
		return len(value.Bytes) > 0 || value.URI != ""
	default:
		return false
	}
}

// pinParts consumes a copy of the allowance, committed only if the pin fits.
func pinParts(user UserMessage, label string, allowance *partAllowance) (string, []model.Part) {
	var placeholders []string
	var parts []model.Part
	for i, part := range user.Message.Parts {
		if !hasNonText(part) {
			continue
		}
		cost := nonTextTokens(part, allowance.measure)
		if allowance.count > 0 && cost <= allowance.tokens {
			allowance.count--
			allowance.tokens -= cost
			parts = append(parts, part)
		} else {
			placeholders = append(placeholders, PartPlaceholder(part, label, i+1))
		}
	}
	return strings.Join(placeholders, "\n"), parts
}

// partValue accepts the value and pointer forms implemented by model.Part.
func partValue(part model.Part) model.Part {
	switch value := part.(type) {
	case *model.TextPart:
		return *value
	case *model.CitationsPart:
		return *value
	case *model.ImagePart:
		return *value
	case *model.DocumentPart:
		return *value
	default:
		return part
	}
}
