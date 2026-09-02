package model

import (
	"errors"
	"reflect"
)

// StreamResponseBuilder assembles a provider-owned terminal response from the
// chunks emitted by one provider stream. A builder is not safe for concurrent
// use.
type StreamResponseBuilder struct {
	content       []Message
	toolCalls     []ToolCall
	usage         TokenUsage
	completion    *Completion
	stopReason    string
	outputLimited bool
	stopped       bool
	budget        cloneBudget
}

// Add records one chunk in the provider's terminal response.
func (b *StreamResponseBuilder) Add(chunk Chunk) error {
	if b == nil {
		return errors.New("nil stream response builder")
	}
	if b.stopped {
		return errors.New("stream response builder received output after stop")
	}
	if b.budget.active == nil {
		b.budget.active = make(map[cloneContainer]struct{})
	}
	owned, err := cloneModelChunk(chunk, &b.budget)
	if err != nil {
		return err
	}
	if err := validateChunkShape(owned); err != nil {
		return err
	}
	switch value := owned.(type) {
	case TextChunk:
		b.content = append(b.content, value.Message)
	case ThinkingChunk:
		b.addThinking(value.Message)
	case ToolCallChunk:
		b.toolCalls = append(b.toolCalls, value.ToolCall)
	case CompletionChunk:
		completion := value.Completion
		b.completion = &completion
	case UsageChunk:
		usage, err := addTokenUsage(b.usage, value.Usage)
		if err != nil {
			return err
		}
		b.usage = usage
	case StopChunk:
		b.stopReason = value.Reason
		b.outputLimited = value.OutputLimited
		b.stopped = true
	}
	return nil
}

// Response returns the assembled response after a stop chunk. It returns nil
// while the stream is incomplete.
func (b *StreamResponseBuilder) Response() *Response {
	if b == nil || !b.stopped {
		return nil
	}
	response := &Response{
		Content:       b.content,
		ToolCalls:     b.toolCalls,
		Usage:         b.usage,
		StopReason:    b.stopReason,
		OutputLimited: b.outputLimited,
	}
	if b.completion != nil {
		parts := streamedThinkingParts(b.content)
		parts = append(parts, TextPart{Text: string(b.completion.Payload)})
		response.Content = []Message{{Role: ConversationRoleAssistant, Parts: parts}}
	}
	return response
}

func (b *StreamResponseBuilder) addThinking(message Message) {
	part, ok := message.Parts[0].(ThinkingPart)
	if !ok || !part.Final {
		b.content = append(b.content, message)
		return
	}
	filtered := b.content[:0]
	for _, existing := range b.content {
		if isProvisionalThinkingIndex(existing, part.Index) {
			continue
		}
		filtered = append(filtered, existing)
	}
	b.content = filtered
	b.content = append(b.content, message)
}

func isProvisionalThinkingIndex(message Message, index int) bool {
	if len(message.Parts) != 1 {
		return false
	}
	part, ok := message.Parts[0].(ThinkingPart)
	return ok && !part.Final && part.Index == index
}

func reconcileStreamResponses(observed, provided *Response) error {
	if observed == nil {
		return errors.New("stream did not produce an observed terminal response")
	}
	if provided == nil {
		return errors.New("provider stream ended without a terminal response")
	}
	if observed.StopReason != provided.StopReason || observed.OutputLimited != provided.OutputLimited {
		return errors.New("provider terminal response disagrees with stream stop state")
	}
	if !reflect.DeepEqual(observed.Usage, provided.Usage) {
		return errors.New("provider terminal response disagrees with stream usage")
	}
	if !equalStreamToolCalls(observed.ToolCalls, provided.ToolCalls) {
		return errors.New("provider terminal response disagrees with streamed tool calls")
	}
	if provided.OutputLimited {
		return nil
	}
	if !equalStreamContentSemantics(streamContentSemantics(observed.Content), streamContentSemantics(provided.Content)) {
		return errors.New("provider terminal response disagrees with streamed content")
	}
	return nil
}

type streamContentSemantic struct {
	text      string
	citations []Citation
	thinking  map[int]ThinkingPart
}

func streamContentSemantics(content []Message) streamContentSemantic {
	semantic := streamContentSemantic{thinking: make(map[int]ThinkingPart)}
	for _, message := range content {
		for _, part := range message.Parts {
			switch value := part.(type) {
			case TextPart:
				semantic.text += value.Text
			case CitationsPart:
				semantic.text += value.Text
				semantic.citations = append(semantic.citations, value.Citations...)
			case ThinkingPart:
				value.Final = false
				prior := semantic.thinking[value.Index]
				prior.Text += value.Text
				prior.Redacted = append(prior.Redacted, value.Redacted...)
				if value.Signature != "" {
					prior.Signature = value.Signature
				}
				prior.Index = value.Index
				semantic.thinking[value.Index] = prior
			}
		}
	}
	return semantic
}

func equalStreamToolCalls(left, right []ToolCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !reflect.DeepEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func equalStreamContentSemantics(left, right streamContentSemantic) bool {
	if left.text != right.text || !reflect.DeepEqual(left.thinking, right.thinking) {
		return false
	}
	if len(left.citations) != len(right.citations) {
		return false
	}
	for index := range left.citations {
		if !reflect.DeepEqual(left.citations[index], right.citations[index]) {
			return false
		}
	}
	return true
}
