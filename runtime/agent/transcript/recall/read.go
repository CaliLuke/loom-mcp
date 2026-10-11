package recall

import (
	"context"
	"encoding/json/v2"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type eligibleText struct {
	text, format string
	bytes        int
}

func page(text string, offset, size int) Response {
	start := snap(text, min(offset, len(text)))
	end := snap(text, min(start+max(size, 512), len(text)))
	response := Response{Text: text[start:end]}
	if end < len(text) {
		response.NextOffset = &end
	}
	return response
}

func snap(text string, offset int) int {
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	return offset
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line[:snap(line, min(160, len(line)))]
}

func messageText(messages []*model.Message) eligibleText {
	var texts []string
	var result eligibleText
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			switch part := part.(type) {
			case model.TextPart:
				texts = append(texts, part.Text)
			case model.ToolResultPart:
				data, err := json.Marshal(part.Content)
				if err != nil {
					panic(err) // model content was decoded from valid JSON.
				}
				texts = append(texts, string(data))
			case model.ImagePart:
				result.format, result.bytes = string(part.Format), len(part.Bytes)
			case model.DocumentPart:
				result.format, result.bytes = string(part.Format), len(part.Bytes)+len(part.Text)
				for _, chunk := range part.Chunks {
					result.bytes += len(chunk)
				}
			}
		}
	}
	result.text = strings.Join(texts, "\n")
	return result
}

func (r *callReader) read(ctx context.Context, in ReadInput) (Response, error) {
	runID, scope, id, err := r.target(ctx, in.ID, in.Run)
	if err != nil {
		return Response{}, err
	}
	if strings.HasPrefix(id, "ck-") {
		return r.summary(ctx, runID, scope, id, in)
	}

	entries, err := r.loadEntries(ctx, runID, scope)
	if err != nil {
		return Response{}, err
	}
	if in.Part == Arguments {
		return r.arguments(ctx, scope, id, entries, in.Offset)
	}

	messageID, numericErr := strconv.ParseUint(id, 10, 64)
	for _, entry := range entries {
		matches := numericErr == nil && entry.ID == index.MessageID(messageID)
		if entry.Kind == index.ToolResult || entry.Kind == index.Structural {
			for _, callID := range entry.ToolUseIDs {
				matches = matches || callID == id
			}
		}
		if !matches {
			continue
		}

		text, err := r.text(ctx, scope, entry)
		if err != nil {
			return Response{}, err
		}
		response := page(text.text, in.Offset, r.req.PageBytes)
		if text.format != "" {
			response.Code, response.Format, response.Bytes = CodeUnsupportedPart, text.format, text.bytes
		}
		return response, nil
	}
	return r.readSeededResult(ctx, scope, id, entries, in.Offset)
}

func (r *callReader) call(ctx context.Context, scope string, hash runstore.Hash) (eligibleCall, error) {
	var call eligibleCall
	data, err := r.handler.Store.Get(ctx, scope, hash)
	if err != nil {
		return call, err
	}
	err = json.Unmarshal(data, &call)
	return call, err
}

// text deliberately never follows Record, Output or server-only call metadata.
func (r *callReader) text(ctx context.Context, scope string, entry index.MessageEntry) (eligibleText, error) {
	if entry.Kind == index.TypedInput {
		return eligibleText{}, &Error{Code: CodeNotFound}
	}
	if entry.Kind == index.Structural {
		messages, err := index.Render(ctx, []index.MessageEntry{entry}, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
			return r.handler.Store.Get(ctx, scope, hash)
		})
		if err != nil {
			return eligibleText{}, err
		}
		return messageText(messages), nil
	}
	if entry.Hash == "" {
		return eligibleText{}, nil
	}
	data, err := r.handler.Store.Get(ctx, scope, entry.Hash)
	if err != nil {
		return eligibleText{}, err
	}
	switch entry.Kind {
	case index.Clarification, index.ToolResult:
		return eligibleText{text: string(data)}, nil
	case index.Message:
		var message *model.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return eligibleText{}, err
		}
		return messageText([]*model.Message{message}), nil
	case index.TypedInput, index.Structural:
		return eligibleText{}, nil
	case index.Assistant:
		var messages []*model.Message
		if err := json.Unmarshal(data, &messages); err != nil {
			return eligibleText{}, err
		}
		return messageText(messages), nil
	}
	return eligibleText{}, nil
}

func (r *callReader) summary(ctx context.Context, runID, scope, id string, in ReadInput) (Response, error) {
	nodes, err := r.records(ctx)
	if err != nil {
		return Response{}, err
	}
	for _, node := range nodes {
		if node.run == runID && node.scope == scope && checkpoint.CheckpointID(string(node.hash)) == id && node.record.Summary != nil {
			if in.Part != Result {
				return Response{}, invalidInput()
			}
			data, err := r.handler.Store.Get(ctx, node.scope, *node.record.Summary)
			if err != nil {
				return Response{}, err
			}
			return page(string(data), in.Offset, r.req.PageBytes), nil
		}
	}
	return Response{}, &Error{Code: CodeNotFound}
}

func (r *callReader) arguments(ctx context.Context, scope, id string, entries []index.MessageEntry, offset int) (Response, error) {
	for _, entry := range entries {
		for i, callID := range entry.ToolUseIDs {
			if callID != id || i >= len(entry.Calls) {
				continue
			}
			call, err := r.call(ctx, scope, entry.Calls[i])
			if err != nil {
				return Response{}, err
			}
			if call.ToolCallID == id {
				return page(string(call.Payload), offset, r.req.PageBytes), nil
			}
		}
	}

	for _, entry := range entries {
		if entry.Kind != index.Message {
			continue
		}
		calls, err := r.calls(ctx, scope, entry)
		if err != nil {
			return Response{}, err
		}
		for _, call := range calls {
			if call.ToolCallID == id {
				return page(string(call.Payload), offset, r.req.PageBytes), nil
			}
		}
	}
	return Response{}, &Error{Code: CodeNotFound}
}

func (r *callReader) readSeededResult(ctx context.Context, scope, id string, entries []index.MessageEntry, offset int) (Response, error) {
	for _, entry := range entries {
		text, found, err := r.seededResult(ctx, scope, id, entry)
		if err != nil {
			return Response{}, err
		}
		if found {
			return page(text.text, offset, r.req.PageBytes), nil
		}
	}
	return Response{}, &Error{Code: CodeNotFound}
}
