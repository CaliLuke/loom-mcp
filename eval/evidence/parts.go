package evidence

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
)

type fragment struct {
	text, asset string
	source      Source
	recallID    string
	received    bool
}

func fragments(messages []*model.Message) ([]fragment, error) {
	cp := -1
	names := map[string]bool{}
	for i, m := range messages {
		if m == nil {
			continue
		}
		if m.Meta[transcript.CheckpointMetaKey] == true {
			cp = i
		}
		for _, p := range m.Parts {
			if u, ok := p.(model.ToolUsePart); ok && recallName(u.Name) {
				names[u.ID] = true
			}
		}
	}
	var out []fragment
	for i, m := range messages {
		if m == nil {
			continue
		}
		source := Tail
		if cp >= 0 && i < cp {
			source = Pins
		}
		for _, p := range m.Parts {
			fs, err := partFragments(p, source, i == cp, names)
			if err != nil {
				return nil, err
			}
			for j := range fs {
				fs[j].received = receivedInput(m.Role, p)
			}
			out = append(out, fs...)
		}
	}
	return out, nil
}
func partFragments(p model.Part, source Source, isCheckpoint bool, names map[string]bool) ([]fragment, error) {
	if isCheckpoint {
		if text, ok := p.(model.TextPart); ok {
			return checkpointFragments(text.Text)
		}
		source = UserIndex
	}
	f := fragment{source: source}
	switch v := p.(type) {
	case model.TextPart:
		f.text = v.Text
	case model.CitationsPart:
		f.text = v.Text
	case model.ImagePart:
		f.asset = fmt.Sprintf("%x", sha256.Sum256(v.Bytes))
	case model.DocumentPart:
		if len(v.Bytes) > 0 {
			f.asset = fmt.Sprintf("%x", sha256.Sum256(v.Bytes))
		}
		f.text = v.Text + strings.Join(v.Chunks, "\n")
	case model.ToolResultPart:
		text, err := jsonText(v.Content)
		if err != nil {
			return nil, err
		}
		f.text = text
		if names[v.ToolUseID] && !v.IsError {
			f.source = Recall
			f.recallID = v.ToolUseID
		}
	case model.ToolUsePart:
		text, err := jsonText(v.Input)
		if err != nil {
			return nil, err
		}
		f.text = text
	default:
		return nil, nil
	}
	return []fragment{f}, nil
}
func jsonText(value any) (string, error) {
	if s, ok := value.(string); ok {
		return s, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func recallName(name string) bool {
	return tools.Ident(name) == tools.RecallContext || strings.Contains(name, "recall_context")
}
func contains(f fragment, e Evidence) bool {
	return e.Value != "" && strings.Contains(f.text, e.Value) || e.Asset != "" && f.asset == e.Asset
}

func checkpointFragments(text string) ([]fragment, error) {
	sections, valid := checkpoint.Sections(text)
	if !valid {
		return nil, fmt.Errorf("evidence: malformed checkpoint")
	}
	return []fragment{{text: sections.Users, source: UserIndex}, {text: sections.Summary, source: Summary}, {text: sections.Receipt, source: Receipt}}, nil
}

// receivedInput separates caller and tool input from assistant-authored claims.
// Checkpoint text is user-role input, including its generated summary section.
func receivedInput(role model.ConversationRole, part model.Part) bool {
	switch part.(type) {
	case model.ToolResultPart:
		return true
	case model.ToolUsePart:
		return false
	default:
		return role == model.ConversationRoleUser
	}
}

// supportsToken includes materializer-declared text rendered within received
// asset bytes; merely naming an asset in the spec does not establish support.
func supportsToken(input fragment, token string, evidence map[EvidenceID]Evidence) bool {
	if strings.Contains(input.text, token) {
		return true
	}
	if input.asset == "" {
		return false
	}
	for _, e := range evidence {
		if e.Asset == input.asset && e.Token == token {
			return true
		}
	}
	return false
}
