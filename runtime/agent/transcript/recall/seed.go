package recall

import (
	"context"
	"encoding/json/v2"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// seedLines describes caller-supplied parts using the same call/result identity
// and eligible-only projections as runtime-produced entries.
func (r *callReader) seedLines(ctx context.Context, runID, scope string, entry index.MessageEntry) ([]Line, error) {
	message, err := r.message(ctx, scope, entry)
	if err != nil {
		return nil, err
	}
	var lines []Line
	var text []string
	hasContent, hasTool := false, false
	for _, part := range message.Parts {
		switch part := part.(type) {
		case model.TextPart:
			hasContent = true
			text = append(text, part.Text)
		case model.ImagePart, model.DocumentPart:
			hasContent = true
		case model.ToolUsePart:
			hasTool = true
			payload, err := json.Marshal(part.Input)
			if err != nil {
				return nil, err
			}
			lines = append(lines, describeCall(runID, eligibleCall{Name: part.Name, ToolCallID: part.ID, Payload: payload}))
		case model.ToolResultPart:
			hasTool = true
			content, err := json.Marshal(part.Content)
			if err != nil {
				return nil, err
			}
			lines = append(lines, Line{ID: part.ToolUseID, Run: runID, Kind: "result", Bytes: len(content), Error: part.IsError})
		}
	}
	if hasContent || !hasTool {
		line := Line{ID: strconv.FormatUint(uint64(entry.ID), 10), Run: runID, Kind: string(entry.Kind), Text: firstLine(strings.Join(text, "\n"))}
		lines = append([]Line{line}, lines...)
	}
	return lines, nil
}
