package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type ollamaStreamer struct {
	body       io.Closer
	scanner    *bufio.Scanner
	output     *model.StructuredOutput
	modelID    string
	modelClass model.ModelClass

	queue    []model.Chunk
	text     strings.Builder
	done     bool
	closed   bool
	metadata map[string]any
	finalErr error
}

// Stream renders a streaming response using Ollama's chat API.
func (c *Client) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	chatReq, err := c.buildChatRequest(req, true)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama chat stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() {
			_ = resp.Body.Close()
		}()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, ollamaHTTPStatusError("ollama chat stream", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return &ollamaStreamer{
		body:       resp.Body,
		scanner:    scanner,
		output:     req.StructuredOutput,
		modelID:    chatReq.Model,
		modelClass: req.ModelClass,
	}, nil
}

func (s *ollamaStreamer) Recv() (model.Chunk, error) {
	if len(s.queue) > 0 {
		return s.pop(), nil
	}
	if s.closed {
		if s.finalErr != nil {
			return model.Chunk{}, s.finalErr
		}
		return model.Chunk{}, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		if err := s.handleLine(line); err != nil {
			s.finalErr = err
			s.closed = true
			return model.Chunk{}, err
		}
		if len(s.queue) > 0 {
			return s.pop(), nil
		}
	}
	s.closed = true
	if err := s.scanner.Err(); err != nil {
		s.finalErr = fmt.Errorf("ollama chat stream: %w", err)
		return model.Chunk{}, s.finalErr
	}
	if !s.done {
		s.finalErr = errors.New("ollama: stream ended before done")
		return model.Chunk{}, s.finalErr
	}
	return model.Chunk{}, io.EOF
}

func (s *ollamaStreamer) Close() error {
	s.closed = true
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}

func (s *ollamaStreamer) Metadata() map[string]any {
	if len(s.metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.metadata))
	for key, value := range s.metadata {
		out[key] = value
	}
	return out
}

func (s *ollamaStreamer) pop() model.Chunk {
	chunk := s.queue[0]
	s.queue[0] = model.Chunk{}
	s.queue = s.queue[1:]
	return chunk
}

func (s *ollamaStreamer) handleLine(line string) error {
	var resp ollamaChatResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return fmt.Errorf("ollama: decode stream chunk: %w", err)
	}
	if err := ollamaProviderError(resp.Error); err != nil {
		return fmt.Errorf("ollama chat stream: %w", err)
	}
	if resp.Model != "" {
		s.modelID = resp.Model
	}
	if err := s.enqueueMessage(resp.Message); err != nil {
		return err
	}
	if resp.Done {
		s.done = true
		if s.output != nil {
			payload, err := structuredOutputPayload([]model.Message{{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: s.text.String()}},
			}}, s.output)
			if err != nil {
				return err
			}
			s.queue = append(s.queue, model.Chunk{
				Type: model.ChunkTypeCompletion,
				Completion: &model.Completion{
					Name:    structuredOutputName(s.output),
					Payload: payload,
				},
			})
		}
		usage := responseUsage(resp, s.modelClass)
		usage.Model = s.modelID
		if usage.TotalTokens > 0 {
			s.metadata = map[string]any{"usage": usage}
			s.queue = append(s.queue, model.Chunk{Type: model.ChunkTypeUsage, UsageDelta: &usage})
		}
		s.queue = append(s.queue, model.Chunk{Type: model.ChunkTypeStop, StopReason: stopReason(resp)})
	}
	return nil
}

func (s *ollamaStreamer) enqueueMessage(msg ollamaMessage) error {
	if msg.Thinking != "" {
		s.queue = append(s.queue, model.Chunk{
			Type:     model.ChunkTypeThinking,
			Thinking: msg.Thinking,
			Message: &model.Message{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{model.ThinkingPart{
					Text:  msg.Thinking,
					Final: false,
				}},
			},
		})
	}
	if msg.Content != "" {
		s.text.WriteString(msg.Content)
		if s.output != nil {
			s.queue = append(s.queue, model.Chunk{
				Type: model.ChunkTypeCompletionDelta,
				CompletionDelta: &model.CompletionDelta{
					Name:  structuredOutputName(s.output),
					Delta: msg.Content,
				},
			})
		} else {
			s.queue = append(s.queue, model.Chunk{
				Type: model.ChunkTypeText,
				Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: msg.Content}},
				},
			})
		}
	}
	for _, call := range msg.ToolCalls {
		translated, err := translateToolCall(call)
		if err != nil {
			return err
		}
		s.queue = append(s.queue, model.Chunk{
			Type:     model.ChunkTypeToolCall,
			ToolCall: &translated,
		})
	}
	return nil
}

func structuredOutputName(output *model.StructuredOutput) string {
	if output == nil || output.Name == "" {
		return "structured_output"
	}
	return output.Name
}
