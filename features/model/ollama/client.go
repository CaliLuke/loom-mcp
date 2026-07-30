package ollama

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Options configures the Ollama adapter.
type Options struct {
	// HTTPClient is used for requests. When nil, a client is created from Timeout.
	HTTPClient *http.Client

	// ServerURL is the local Ollama base URL, for example http://localhost:11434.
	ServerURL string

	// DefaultModel is the model identifier used when Request.Model is empty.
	DefaultModel string

	// HighModel is used when Request.ModelClass is high-reasoning and
	// Request.Model is empty.
	HighModel string

	// SmallModel is used when Request.ModelClass is small and Request.Model is
	// empty.
	SmallModel string

	// MaxTokens is mapped to Ollama's num_predict option when Request.MaxTokens
	// is unset.
	MaxTokens int

	// Temperature is the default sampling temperature when Request.Temperature
	// is unset.
	Temperature float32

	// Timeout configures the response header timeout when HTTPClient is nil.
	// Request lifetime deadlines should be owned by the request context.
	Timeout time.Duration
}

// Client implements model.Client using Ollama's /api/chat endpoint.
type Client struct {
	httpClient   *http.Client
	serverURL    string
	defaultModel string
	highModel    string
	smallModel   string
	maxTokens    int
	temperature  float32
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    *bool           `json:"think,omitempty"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
	Format   any             `json:"format,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Thinking  string           `json:"thinking,omitempty"`
	Content   string           `json:"content,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ollamaToolCall struct {
	ID       string             `json:"id,omitempty"`
	Function ollamaFunctionCall `json:"function"`
}

type ollamaFunctionCall struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

type ollamaChatResponse struct {
	Model           string          `json:"model"`
	Message         ollamaMessage   `json:"message"`
	Done            bool            `json:"done"`
	DoneReason      string          `json:"done_reason"`
	PromptEvalCount int             `json:"prompt_eval_count"`
	EvalCount       int             `json:"eval_count"`
	Error           json.RawMessage `json:"error,omitempty"`
}

const (
	defaultResponseHeaderTimeout = 30 * time.Second
	toolExecutionFailed          = "tool execution failed"
)

// New builds an Ollama-backed model client from the provided options.
func New(opts Options) (*Client, error) {
	serverURL := strings.TrimRight(strings.TrimSpace(opts.ServerURL), "/")
	if serverURL == "" {
		return nil, errors.New("ollama server URL is required")
	}
	if strings.TrimSpace(opts.DefaultModel) == "" {
		return nil, errors.New("default model is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newDefaultHTTPClient(opts.Timeout)
	}
	return &Client{
		httpClient:   httpClient,
		serverURL:    serverURL,
		defaultModel: opts.DefaultModel,
		highModel:    opts.HighModel,
		smallModel:   opts.SmallModel,
		maxTokens:    opts.MaxTokens,
		temperature:  opts.Temperature,
	}, nil
}

// Complete renders a non-streaming response using Ollama's chat API.
func (c *Client) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	chatReq, err := c.buildChatRequest(req, false)
	if err != nil {
		return nil, err
	}
	var out ollamaChatResponse
	if err := c.doJSON(ctx, chatReq, &out); err != nil {
		return nil, err
	}
	return translateChatResponse(out, req.ModelClass, req.StructuredOutput)
}

func newDefaultHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = defaultResponseHeaderTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	return &http.Client{Transport: transport}
}

func (c *Client) buildChatRequest(req *model.Request, stream bool) (ollamaChatRequest, error) {
	if req == nil {
		return ollamaChatRequest{}, errors.New("ollama: request is required")
	}
	if len(req.Messages) == 0 {
		return ollamaChatRequest{}, errors.New("ollama: messages are required")
	}
	modelID := c.resolveModelID(req)
	if modelID == "" {
		return ollamaChatRequest{}, errors.New("ollama: model identifier is required")
	}
	messages, err := encodeMessages(req.Messages)
	if err != nil {
		return ollamaChatRequest{}, err
	}
	if len(messages) == 0 {
		return ollamaChatRequest{}, errors.New("ollama: at least one message is required")
	}
	tools, err := encodeTools(req.Tools)
	if err != nil {
		return ollamaChatRequest{}, err
	}
	if err := applyToolChoice(&tools, req.ToolChoice); err != nil {
		return ollamaChatRequest{}, err
	}
	format, err := encodeStructuredOutput(req.StructuredOutput, tools)
	if err != nil {
		return ollamaChatRequest{}, err
	}
	return ollamaChatRequest{
		Model:    modelID,
		Messages: messages,
		Stream:   stream,
		Think:    encodeThinking(req.Thinking),
		Tools:    tools,
		Options:  c.requestOptions(req),
		Format:   format,
	}, nil
}

func (c *Client) resolveModelID(req *model.Request) string {
	if req.Model != "" {
		return req.Model
	}
	switch req.ModelClass {
	case "", model.ModelClassDefault:
		return c.defaultModel
	case model.ModelClassHighReasoning:
		if c.highModel != "" {
			return c.highModel
		}
	case model.ModelClassSmall:
		if c.smallModel != "" {
			return c.smallModel
		}
	}
	return c.defaultModel
}

func (c *Client) requestOptions(req *model.Request) map[string]any {
	options := make(map[string]any)
	temperature := req.Temperature
	if temperature == 0 {
		temperature = c.temperature
	}
	if temperature != 0 {
		options["temperature"] = temperature
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = c.maxTokens
	}
	if maxTokens > 0 {
		options["num_predict"] = maxTokens
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

func encodeThinking(thinking *model.ThinkingOptions) *bool {
	if thinking == nil {
		return nil
	}
	enabled := thinking.Enable
	return &enabled
}

func (c *Client) doJSON(ctx context.Context, chatReq ollamaChatRequest, out *ollamaChatResponse) error {
	payload, err := json.Marshal(chatReq)
	if err != nil {
		return fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama chat: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ollamaHTTPStatusError("ollama chat", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("ollama chat: decode response: %w", err)
	}
	if err := ollamaProviderError(out.Error); err != nil {
		return fmt.Errorf("ollama chat: %w", err)
	}
	return nil
}

func ollamaProviderError(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var message string
	if err := json.Unmarshal(raw, &message); err == nil {
		message = strings.TrimSpace(message)
		if message == "" {
			return nil
		}
		return fmt.Errorf("provider error: %s", message)
	}
	compact := strings.TrimSpace(string(raw))
	if compact == "" {
		return nil
	}
	return fmt.Errorf("provider error: %s", compact)
}

func encodeMessages(messages []*model.Message) ([]ollamaMessage, error) {
	out := make([]ollamaMessage, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		role, err := ollamaRole(msg.Role)
		if err != nil {
			return nil, err
		}
		encoded, err := encodeMessage(role, msg.Parts)
		if err != nil {
			return nil, err
		}
		if encoded.Content == "" && len(encoded.Images) == 0 && len(encoded.ToolCalls) == 0 {
			continue
		}
		out = append(out, encoded)
	}
	return out, nil
}

func ollamaRole(role model.ConversationRole) (string, error) {
	switch role {
	case model.ConversationRoleSystem:
		return "system", nil
	case model.ConversationRoleUser:
		return "user", nil
	case model.ConversationRoleAssistant:
		return "assistant", nil
	default:
		return "", fmt.Errorf("ollama: unsupported message role %q", role)
	}
}

func encodeMessage(role string, parts []model.Part) (ollamaMessage, error) {
	encoded := ollamaMessage{Role: role}
	var content strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case model.TextPart:
			content.WriteString(p.Text)
		case model.CitationsPart:
			content.WriteString(p.Text)
		case model.ImagePart:
			image, err := encodeImagePart(p)
			if err != nil {
				return ollamaMessage{}, err
			}
			encoded.Images = append(encoded.Images, image)
		case model.ToolUsePart:
			toolCall, err := encodeToolUsePart(p)
			if err != nil {
				return ollamaMessage{}, err
			}
			encoded.ToolCalls = append(encoded.ToolCalls, toolCall)
		case model.ToolResultPart:
			text, err := encodeToolResultPart(p)
			if err != nil {
				return ollamaMessage{}, err
			}
			content.WriteString(text)
		case model.CacheCheckpointPart, model.ThinkingPart:
			continue
		default:
			return ollamaMessage{}, fmt.Errorf("ollama: unsupported message part %T", part)
		}
	}
	encoded.Content = content.String()
	return encoded, nil
}

func encodeImagePart(part model.ImagePart) (string, error) {
	switch part.Format {
	case model.ImageFormatPNG, model.ImageFormatJPEG, model.ImageFormatWEBP:
	case model.ImageFormatGIF:
		return "", errors.New("ollama: GIF images are not supported")
	default:
		return "", fmt.Errorf("ollama: unsupported image format %q", part.Format)
	}
	if len(part.Bytes) == 0 {
		return "", errors.New("ollama: image bytes are required")
	}
	return base64.StdEncoding.EncodeToString(part.Bytes), nil
}

func encodeToolUsePart(part model.ToolUsePart) (ollamaToolCall, error) {
	if part.Name == "" {
		return ollamaToolCall{}, errors.New("ollama: tool use part requires name")
	}
	return ollamaToolCall{
		ID: part.ID,
		Function: ollamaFunctionCall{
			Name:      part.Name,
			Arguments: part.Input,
		},
	}, nil
}

func encodeToolResultPart(part model.ToolResultPart) (string, error) {
	if part.ToolUseID == "" {
		return "", errors.New("ollama: tool result part requires tool use id")
	}
	if part.IsError {
		return fmt.Sprintf(`{"error":%q}`, stringifyToolResult(part.Content)), nil
	}
	data, err := marshalJSONValue(part.Content)
	if err != nil {
		return "", fmt.Errorf("ollama: tool result %q: %w", part.ToolUseID, err)
	}
	return string(data), nil
}

func encodeTools(defs []*model.ToolDefinition) ([]ollamaTool, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make([]ollamaTool, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		if strings.TrimSpace(def.Name) == "" {
			return nil, errors.New("ollama: tool name is required")
		}
		out = append(out, ollamaTool{
			Type: "function",
			Function: ollamaFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.InputSchema,
			},
		})
	}
	return out, nil
}

func applyToolChoice(tools *[]ollamaTool, choice *model.ToolChoice) error {
	if choice == nil || choice.Mode == "" || choice.Mode == model.ToolChoiceModeAuto {
		return nil
	}
	switch choice.Mode {
	case "", model.ToolChoiceModeAuto:
		return nil
	case model.ToolChoiceModeNone:
		*tools = nil
		return nil
	case model.ToolChoiceModeAny, model.ToolChoiceModeTool:
		return fmt.Errorf("ollama: tool choice mode %q is not supported", choice.Mode)
	default:
		return fmt.Errorf("ollama: unsupported tool choice mode %q", choice.Mode)
	}
}

func encodeStructuredOutput(output *model.StructuredOutput, tools []ollamaTool) (any, error) {
	if output == nil {
		return nil, nil
	}
	if len(tools) > 0 {
		return nil, fmt.Errorf("ollama: structured output cannot be combined with tools: %w", model.ErrStructuredOutputUnsupported)
	}
	if len(output.Schema) == 0 {
		return nil, errors.New("ollama: structured output schema is required")
	}
	var schema any
	if err := json.Unmarshal(output.Schema, &schema); err != nil {
		return nil, fmt.Errorf("ollama: structured output schema must be valid JSON: %w", err)
	}
	return schema, nil
}

func translateChatResponse(resp ollamaChatResponse, modelClass model.ModelClass, output *model.StructuredOutput) (*model.Response, error) {
	content, toolCalls, err := translateMessage(resp.Message)
	if err != nil {
		return nil, err
	}
	content, err = canonicalizeStructuredOutputMessages(content, output)
	if err != nil {
		return nil, err
	}
	return &model.Response{
		Content:    content,
		ToolCalls:  toolCalls,
		Usage:      responseUsage(resp, modelClass),
		StopReason: stopReason(resp),
	}, nil
}

func translateMessage(msg ollamaMessage) ([]model.Message, []model.ToolCall, error) {
	content := make([]model.Message, 0, 1)
	if msg.Thinking != "" || msg.Content != "" {
		parts := make([]model.Part, 0, 2)
		if msg.Thinking != "" {
			parts = append(parts, model.ThinkingPart{Text: msg.Thinking, Final: true})
		}
		if msg.Content != "" {
			parts = append(parts, model.TextPart{Text: msg.Content})
		}
		content = append(content, model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: parts,
		})
	}
	toolCalls := make([]model.ToolCall, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		translated, err := translateToolCall(call)
		if err != nil {
			return nil, nil, err
		}
		toolCalls = append(toolCalls, translated)
	}
	return content, toolCalls, nil
}

func translateToolCall(call ollamaToolCall) (model.ToolCall, error) {
	if strings.TrimSpace(call.Function.Name) == "" {
		return model.ToolCall{}, errors.New("ollama: tool call name is required")
	}
	payload, err := marshalJSONValue(call.Function.Arguments)
	if err != nil {
		return model.ToolCall{}, fmt.Errorf("ollama: tool call %q payload: %w", call.Function.Name, err)
	}
	return model.ToolCall{
		Name:    tools.Ident(call.Function.Name),
		Payload: rawjson.Message(payload),
		ID:      call.ID,
	}, nil
}

func canonicalizeStructuredOutputMessages(messages []model.Message, output *model.StructuredOutput) ([]model.Message, error) {
	if output == nil {
		return messages, nil
	}
	payload, err := structuredOutputPayload(messages, output)
	if err != nil {
		return nil, err
	}
	return []model.Message{{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: string(payload)}},
	}}, nil
}

func structuredOutputPayload(content []model.Message, output *model.StructuredOutput) (rawjson.Message, error) {
	text := strings.TrimSpace(extractAssistantText(content))
	if text == "" {
		return nil, fmt.Errorf("ollama: structured output %q completed without content", output.Name)
	}
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("ollama: structured output %q payload is not valid JSON", output.Name)
	}
	return rawjson.Message(text), nil
}

func extractAssistantText(content []model.Message) string {
	var text strings.Builder
	for _, message := range content {
		if message.Role != model.ConversationRoleAssistant {
			continue
		}
		for _, part := range message.Parts {
			switch actual := part.(type) {
			case model.TextPart:
				text.WriteString(actual.Text)
			case model.CitationsPart:
				text.WriteString(actual.Text)
			}
		}
	}
	return text.String()
}

func responseUsage(resp ollamaChatResponse, modelClass model.ModelClass) model.TokenUsage {
	total := resp.PromptEvalCount + resp.EvalCount
	return model.TokenUsage{
		Model:        resp.Model,
		ModelClass:   modelClass,
		InputTokens:  resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
		TotalTokens:  total,
	}
}

func ollamaHTTPStatusError(operation string, status int, body string) error {
	if status == http.StatusTooManyRequests {
		return fmt.Errorf("%s: %w: status %d: %s", operation, model.ErrRateLimited, status, body)
	}
	return fmt.Errorf("%s: status %d: %s", operation, status, body)
}

func stopReason(resp ollamaChatResponse) string {
	if resp.DoneReason != "" {
		return resp.DoneReason
	}
	if resp.Done {
		return "stop"
	}
	return ""
}

func marshalJSONValue(v any) ([]byte, error) {
	switch val := v.(type) {
	case nil:
		return []byte("null"), nil
	case rawjson.Message:
		return val.MarshalJSON()
	case json.RawMessage:
		return rawjson.Message(val).MarshalJSON()
	case []byte:
		return rawjson.Message(val).MarshalJSON()
	default:
		return json.Marshal(v)
	}
}

func stringifyToolResult(v any) string {
	switch value := v.(type) {
	case nil:
		return toolExecutionFailed
	case string:
		if value == "" {
			return toolExecutionFailed
		}
		return value
	case []byte:
		if len(value) == 0 {
			return toolExecutionFailed
		}
		return string(value)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}
