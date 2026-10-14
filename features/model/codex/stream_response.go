package codex

import (
	"cmp"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// terminalResponse preserves completed provider message boundaries. Presentation
// deltas determine streamed semantics, but are not separate replay messages.
func (s *codexStreamer) terminalResponse() *model.Response {
	response := s.builder.Response()
	if response == nil {
		return nil
	}
	response.Content = nil
	seen := make(map[*itemState]bool)
	var items []*itemState
	for _, item := range s.state.items {
		if !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(a, b *itemState) int { return cmp.Compare(a.outputIndex, b.outputIndex) })
	for _, item := range items {
		switch item.item.Type {
		case wireMessageItem:
			message := model.Message{Role: model.ConversationRoleAssistant}
			for index := range item.item.Content {
				if text := item.text[index].String(); text != "" {
					message.Parts = append(message.Parts, model.TextPart{Text: text})
				}
			}
			if len(message.Parts) > 0 {
				response.Content = append(response.Content, message)
			}
		case wireReasoningItem:
			response.Content = append(response.Content, completedReasoning(item)...)
		}
	}
	return response
}

// completedReasoning retains the indexes and exact final bytes emitted by the
// stream, including summary/content separation and opaque encrypted reasoning.
func completedReasoning(item *itemState) []model.Message {
	var messages []model.Message
	appendText := func(kind string, parts []wireContent) {
		for index := range parts {
			key := reasoningPartKey{kind: kind, index: index}
			if text := item.thinking[key].String(); text != "" {
				messages = append(messages, model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: text, Index: item.thinkingIndexes[key], Final: true}}})
			}
		}
	}
	appendText(wireSummary, item.item.Summary)
	appendText(wireContentKey, item.item.Content)
	if item.item.EncryptedContent != nil && *item.item.EncryptedContent != "" {
		messages = append(messages, model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Redacted: []byte(*item.item.EncryptedContent), Index: item.thinkingIndexes[reasoningPartKey{kind: "encrypted"}], Final: true}}})
	}
	return messages
}
