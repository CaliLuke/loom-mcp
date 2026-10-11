package agenttest

import (
	"context"
	"io"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type scriptStream struct {
	mu        sync.Mutex
	ctx       context.Context
	chunks    []model.Chunk
	index     int
	failure   error
	failAfter int
	builder   model.StreamResponseBuilder
	terminal  error
	eof       bool
	closed    bool
}

func assistantMessage(part model.Part) model.Message {
	return model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{part}}
}

func textFragments(text string, n int) []string {
	if n <= 1 {
		return []string{text}
	}
	runes := []rune(text)
	fragments := make([]string, n)
	// Divide by rune count so no fragment cuts a UTF-8 code point. Empty
	// fragments preserve the configured count when n exceeds the text length.
	width, extra := len(runes)/n, len(runes)%n
	start := 0
	for index := range fragments {
		end := start + width
		if index < extra {
			end++
		}
		fragments[index] = string(runes[start:end])
		start = end
	}
	return fragments
}

func (r scriptResponse) chunks(request *model.Request, fragments int) []model.Chunk {
	var chunks []model.Chunk
	for index, text := range r.thinking {
		chunks = append(chunks, model.ThinkingChunk{Message: assistantMessage(model.ThinkingPart{Text: text, Index: index, Final: true})})
	}
	for _, text := range r.text {
		for _, fragment := range textFragments(text, fragments) {
			chunks = append(chunks, model.TextChunk{Message: assistantMessage(model.TextPart{Text: fragment})})
		}
	}
	for _, call := range r.calls {
		chunks = append(chunks, model.ToolCallChunk{ToolCall: call})
	}
	usage := r.usage
	usage.Model, usage.ModelClass = request.Model, request.ModelClass
	return append(chunks, model.UsageChunk{Usage: usage}, model.StopChunk{Reason: r.stop, OutputLimited: r.limited})
}

func (r scriptResponse) complete(request *model.Request, fragments int) *model.Response {
	// Use the same content layout as streaming without pre-validating raw tool
	// arguments; the real Client owns response acceptance.
	response := &model.Response{StopReason: r.stop, OutputLimited: r.limited, Usage: r.usage}
	response.Usage.Model, response.Usage.ModelClass = request.Model, request.ModelClass
	for index, text := range r.thinking {
		response.Content = append(response.Content, assistantMessage(model.ThinkingPart{Text: text, Index: index, Final: true}))
	}
	for _, text := range r.text {
		for _, fragment := range textFragments(text, fragments) {
			response.Content = append(response.Content, assistantMessage(model.TextPart{Text: fragment}))
		}
	}
	response.ToolCalls = r.calls
	return response
}

func (s *scriptStream) Recv() (model.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return nil, s.terminal
	}
	if err := s.ctx.Err(); err != nil {
		s.terminal = err
		return nil, err
	}
	if s.closed {
		s.terminal = io.ErrClosedPipe
		return nil, s.terminal
	}
	if s.failure != nil && s.index >= s.failAfter {
		s.terminal = s.failure
		return nil, s.terminal
	}
	if s.index == len(s.chunks) {
		s.eof = true
		s.terminal = io.EOF
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	if err := s.builder.Add(chunk); err != nil {
		s.terminal = err
		return nil, err
	}
	s.index++
	return chunk, nil
}

func (s *scriptStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *scriptStream) Response() *model.Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.eof {
		return nil
	}
	return s.builder.Response()
}
