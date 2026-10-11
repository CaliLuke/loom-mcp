package recall

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

const callLineKind = "call"

func cursorPosition(token string, selector Cursor) (int, error) {
	if token == "" {
		return 0, nil
	}
	decoded, err := DecodeCursor(token)
	if err != nil {
		return 0, err
	}
	if decoded.Kind != selector.Kind || decoded.Handle != selector.Handle || decoded.Scan != selector.Scan || (decoded.Range == nil) != (selector.Range == nil) {
		return 0, invalidInput()
	}
	if decoded.Range != nil && *decoded.Range != *selector.Range {
		return 0, invalidInput()
	}
	return decoded.Position, nil
}

func describeCall(runID string, call eligibleCall) Line {
	name := call.Name
	if tools.Ident(name) == tools.RecallContext {
		return Line{ID: call.ToolCallID, Run: runID, Kind: callLineKind, Text: checkpoint.LookupLine(call.ToolCallID, firstLine(string(call.Payload)), -1)}
	}
	return Line{ID: call.ToolCallID, Run: runID, Kind: callLineKind, Text: fmt.Sprintf("%s (%s: %s)", call.ToolCallID, name, firstLine(string(call.Payload)))}
}

func (r *callReader) list(ctx context.Context, in ListInput) (Response, error) {
	selector := Cursor{Kind: List, Handle: in.Handle}
	if in.Handle == "" {
		selector.Range = &Range{From: in.From, Through: in.Through, Run: in.Run}
	}
	position, err := cursorPosition(in.Cursor, selector)
	if err != nil {
		return Response{}, err
	}
	var lines []Line
	if in.Handle != "" {
		lines, err = r.handleLines(ctx, in.Handle)
	} else {
		lines, err = r.rangeLines(ctx, in)
	}
	if err != nil {
		return Response{}, err
	}

	if position > len(lines) {
		return Response{}, invalidInput()
	}
	response := Response{}
	used := 0
	for position < len(lines) {
		data, err := json.Marshal(lines[position])
		if err != nil {
			return Response{}, err
		}
		if used+len(data) > max(r.req.PageBytes, 512) && len(response.Entries) > 0 {
			break
		}
		response.Entries = append(response.Entries, lines[position])
		used += len(data)
		position++
	}
	if position < len(lines) {
		selector.Position = position
		response.Cursor, err = EncodeCursor(selector)
	}
	return response, err
}

func (r *callReader) describe(ctx context.Context, runID, scope string, entry index.MessageEntry) ([]Line, error) {
	if entry.Kind == index.Message {
		return r.seedLines(ctx, runID, scope, entry)
	}

	var lines []Line
	id := strconv.FormatUint(uint64(entry.ID), 10)
	if entry.Kind == index.ToolResult {
		if len(entry.ToolUseIDs) > 0 {
			id = entry.ToolUseIDs[0]
		}
		line := Line{ID: id, Run: runID, Kind: "result", Bytes: entry.Bytes}
		if entry.Result != nil {
			line.Bytes = entry.Result.ContentBytes
			line.Error = entry.Result.Error != nil
		}
		return []Line{line}, nil
	}
	text, err := r.text(ctx, scope, entry)
	if err != nil {
		return nil, err
	}
	if text.text != "" || text.format != "" || entry.Kind == index.Message || entry.Kind == index.Clarification {
		lines = append(lines, Line{ID: id, Run: runID, Kind: string(entry.Kind), Text: firstLine(text.text)})
	}
	calls, err := r.calls(ctx, scope, entry)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		lines = append(lines, describeCall(runID, call))
	}
	return lines, nil
}

func (r *callReader) handleLines(ctx context.Context, handle string) ([]Line, error) {
	nodes, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	at, part, err := findHandle(nodes, handle)
	if err != nil {
		return nil, err
	}
	node := nodes[at]

	if part == "users" {
		return r.userLines(ctx, node)
	}
	var lines []Line
	for _, candidate := range nodes[at:] {
		if part == "receipt" && candidate.run != node.run {
			continue
		}
		if part == "earlier" && candidate.run == node.run {
			continue
		}
		for i := len(candidate.record.Receipt) - 1; i >= 0; i-- {
			line := candidate.record.Receipt[i]
			id := strconv.FormatUint(uint64(line.From), 10)
			text := firstLine(line.Text)
			if line.Kind == callLineKind {
				id = line.ToolUseID
				name := line.Tool
				if tools.Ident(name) == tools.RecallContext {
					text = checkpoint.LookupLine(id, text, -1)
				} else {
					text = fmt.Sprintf("%s (%s: %s)", id, name, text)
				}
			}
			lines = append(lines, Line{ID: id, Run: candidate.run, Kind: line.Kind, Text: text})
		}
	}
	return lines, nil
}

func (r *callReader) userLines(ctx context.Context, node recordNode) ([]Line, error) {
	var lines []Line
	entries, err := r.loadEntries(ctx, node.run, node.scope)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ID > node.record.Through {
			break
		}
		if entry.Kind != index.Clarification && (entry.Kind != index.Message || entry.Role != model.ConversationRoleUser) {
			continue
		}
		text, err := r.text(ctx, node.scope, entry)
		if err != nil {
			return nil, err
		}
		lines = append(lines, Line{ID: strconv.FormatUint(uint64(entry.ID), 10), Run: node.run, Kind: "user", Text: firstLine(text.text)})
	}
	return lines, nil
}

func (r *callReader) rangeLines(ctx context.Context, in ListInput) ([]Line, error) {
	var lines []Line
	runID, scope, _, err := r.target(ctx, "", in.Run)
	if err != nil {
		return nil, err
	}
	entries, err := r.loadEntries(ctx, runID, scope)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ID < in.From || entry.ID > in.Through || entry.Kind == index.TypedInput {
			continue
		}
		descriptors, err := r.describe(ctx, runID, scope, entry)
		if err != nil {
			return nil, err
		}
		lines = append(lines, descriptors...)
	}
	return lines, nil
}
