package recall

import (
	"context"
	"encoding/json/v2"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type citationResolver struct {
	ctx     context.Context
	source  index.Source
	entries []index.MessageEntry
	through index.MessageID
}

// NewCitationResolver binds current-run citations to authorized canonical entries.
// Like K13's default nil RunChain, it resolves no rN labels before K18.
// Source must already be authorized for the activity's root run scope.
func NewCitationResolver(ctx context.Context, source index.Source, entries []index.MessageEntry, through index.MessageID) checkpoint.CitationResolver {
	return citationResolver{ctx: ctx, source: source, entries: entries, through: through}
}

func (r citationResolver) Resolve(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	if key.Pos != 0 || key.ID == 0 || key.ID > index.MessageID(len(r.entries)) {
		return checkpoint.CitationTarget{}, false, nil
	}
	entry := r.entries[key.ID-1]
	target := checkpoint.CitationTarget{Kind: entry.Kind, Role: entry.Role, Line: checkpoint.UserLine{ID: entry.ID, Origin: entry.Origin, Bytes: entry.Bytes}}
	if entry.Kind == index.Clarification {
		data, err := r.source(r.ctx, entry.Hash)
		if err != nil {
			return target, false, err
		}
		target.Role = model.ConversationRoleUser
		target.Line.Text = checkpoint.HeadTail(firstLine(string(data)), checkpoint.DefaultLimits.UserIndexLineBytes)
	} else if entry.Kind == index.Message && entry.Role == model.ConversationRoleUser {
		data, err := r.source(r.ctx, entry.Hash)
		if err != nil {
			return target, false, err
		}
		line, kind, err := citationUser(data, entry)
		if err != nil {
			return target, false, err
		}
		target.Line, target.Kind = line, kind
	}
	return target, true, nil
}

func (r citationResolver) Behind(key checkpoint.OrderKey) bool {
	return key.Pos == 0 && key.ID > 0 && key.ID <= r.through
}

// citationUser retains canonical user text and excludes result-only user-role messages.
func citationUser(data []byte, entry index.MessageEntry) (checkpoint.UserLine, index.EntryKind, error) {
	line := checkpoint.UserLine{ID: entry.ID, Origin: entry.Origin, Bytes: entry.Bytes}
	var message model.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return line, "", err
	}
	var text []string
	user := len(message.Parts) == 0
	for _, part := range message.Parts {
		switch p := part.(type) {
		case model.TextPart:
			user = true
			text = append(text, p.Text)
		case model.CitationsPart:
			user = true
			text = append(text, p.Text)
		case model.DocumentPart:
			user = true
			text = append(text, p.Text)
			text = append(text, p.Chunks...)
		case model.ToolResultPart:
		default:
			user = true
		}
	}
	kind := entry.Kind
	if !user {
		kind = index.ToolResult
	}
	line.Text = firstLine(strings.Join(text, "\n"))
	return line, kind, nil
}
