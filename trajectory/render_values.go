package trajectory

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func shortHash(hash string) string {
	if digest, ok := strings.CutPrefix(hash, "sha256:"); ok {
		return "sha256:" + digest[:min(12, len(digest))]
	}
	return hash[:min(12, len(hash))]
}

func finalPart(part model.Part) string {
	switch p := part.(type) {
	case model.TextPart:
		return p.Text
	case *model.TextPart:
		return p.Text
	case model.ThinkingPart, *model.ThinkingPart:
		return "[thinking part]"
	case model.ImagePart, *model.ImagePart:
		return "[image part]"
	case model.DocumentPart, *model.DocumentPart:
		return "[document part]"
	case model.CitationsPart, *model.CitationsPart:
		return "[citations part]"
	case model.ToolUsePart, *model.ToolUsePart:
		return "[tool_use part]"
	case model.ToolResultPart, *model.ToolResultPart:
		return "[tool_result part]"
	case model.CacheCheckpointPart, *model.CacheCheckpointPart:
		return "[cache_checkpoint part]"
	default:
		panic("trajectory: unsupported final message part")
	}
}

// compactValue orders raw JSON without decoding its number tokens. Other bytes
// are quoted text. The returned value owns its bytes and never mutates evidence.
func compactValue(data []byte) string {
	if len(data) == 0 {
		return "null"
	}
	raw := append(jsontext.Value(nil), data...)
	if err := raw.Format(jsontext.ReorderRawObjects(true)); err == nil {
		return string(raw)
	}
	return strconv.Quote(string(data))
}

// jsonFence exceeds the longest backtick run, including in truncated values.
func jsonFence(value string) string {
	longest, current := 0, 0
	for _, char := range value {
		if char == '`' {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func (r *renderer) value(indent, label string, data []byte) {
	value := r.limit(compactValue(data))
	if r.opts.Format == FormatMarkdown {
		r.line(indent, label+":")
		r.fenced(indent+"  ", value, "json")
		return
	}
	r.line(indent, label+": "+value)
}

// text protects readable Markdown content that could open a code block or
// introduce list structure, while retaining inline rendering for simple text.
func (r *renderer) text(indent, label, value string) {
	value = r.limit(value)
	if r.opts.Format == FormatMarkdown && strings.ContainsAny(value, "\n`") {
		if label == "" {
			label = "Text"
		}
		r.line(indent, label+":")
		r.fenced(indent+"  ", value, "")
		return
	}
	if label != "" {
		value = label + ": " + value
	}
	r.line(indent, value)
}

func (r *renderer) fenced(indent, value, language string) {
	fence := jsonFence(value)
	r.out.WriteByte('\n')
	r.out.WriteString(indent + fence + language + "\n")
	for _, line := range strings.Split(value, "\n") {
		r.out.WriteString(strings.TrimRight(indent+line, " \t\r") + "\n")
	}
	r.out.WriteString(indent + fence + "\n")
}

// limit reports the retained byte count after moving a cut to a UTF-8 boundary.
func (r *renderer) limit(value string) string {
	maxBytes := r.opts.MaxValueBytes
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…[truncated: " + strconv.Itoa(cut) + " of " + strconv.Itoa(len(value)) + " bytes]"
}

func (r *renderer) event(s Step, indent, mark string) {
	e := s.Event
	label := "Event"
	if s.Kind == StepAwait {
		label = "Await"
	} else if s.Kind == StepAnswer {
		label = "Answer"
	}
	r.line(indent, label+": "+string(e.Type)+mark)
	fields := indent + "  "
	r.line(fields, "Timestamp: "+e.Timestamp.UTC().Format(time.RFC3339Nano))
	r.value(fields, "Payload", e.Payload)
	// Payload stays generic JSON; only the documented human-facing prompt and
	// thinking strings are also projected as readable, multiline text.
	if e.Type == hooks.ThinkingBlock || s.Kind == StepAwait {
		var obj map[string]jsontext.Value
		if err := json.Unmarshal(e.Payload, &obj); err != nil {
			return
		}
		for _, key := range []string{"ID", "Title", "Text", "Question", "Prompt"} {
			var text string
			if raw, ok := obj[key]; ok {
				if err := json.Unmarshal(raw, &text); err == nil {
					if e.Type == hooks.AwaitQuestions && key == "Title" && text == "" {
						continue
					}
					r.text(fields, key, text)
				}
			}
		}
		r.questionPrompts(obj["Questions"], fields)
	}
}

func (r *renderer) questionPrompts(data jsontext.Value, indent string) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "null" {
		return
	}
	var questions []struct{ ID, Prompt string }
	if err := json.Unmarshal(data, &questions); err != nil {
		return
	}
	for _, question := range questions {
		r.text(indent, "Question "+question.ID, question.Prompt)
	}
}
