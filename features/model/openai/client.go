// Package openai provides a model.Client implementation backed by the OpenAI
// Responses API. It translates loom-mcp requests into Responses API calls and
// maps responses back to the generic planner structures.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// ResponseClient captures the subset of the official OpenAI client used by the adapter.
type ResponseClient interface {
	New(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error)
}

// Options configures the OpenAI adapter.
type Options struct {
	Client       ResponseClient
	DefaultModel string
}

// Client implements model.Client via the OpenAI Responses API.
type Client struct {
	resp  ResponseClient
	model string
}

// New builds an OpenAI-backed model client from the provided options.
func New(opts Options) (*Client, error) {
	if opts.Client == nil {
		return nil, errors.New("openai client is required")
	}
	modelID := opts.DefaultModel
	if modelID == "" {
		return nil, errors.New("default model is required")
	}
	return &Client{resp: opts.Client, model: modelID}, nil
}

// NewFromAPIKey constructs a client using the official openai-go HTTP client.
func NewFromAPIKey(apiKey, defaultModel string) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("api key is required")
	}
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return New(Options{Client: &client.Responses, DefaultModel: defaultModel})
}

// Complete renders a response using the configured OpenAI client.
func (c *Client) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	request, err := c.buildResponseRequest(req)
	if err != nil {
		return nil, err
	}
	response, err := c.resp.New(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("openai responses: %w", err)
	}
	return translateResponse(response), nil
}

func (c *Client) buildResponseRequest(req *model.Request) (responses.ResponseNewParams, error) {
	if len(req.Messages) == 0 {
		return responses.ResponseNewParams{}, errors.New("messages are required")
	}
	input, err := encodeInput(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	tools, err := encodeTools(req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	request := responses.ResponseNewParams{
		Model: openaiModel(c.resolveModelID(req)),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		Tools:       tools,
		Temperature: openai.Float(float64(req.Temperature)),
	}
	if req.MaxTokens > 0 {
		request.MaxOutputTokens = openai.Int(int64(req.MaxTokens))
	}
	toolChoice, err := buildOpenAIToolChoice(req.ToolChoice, req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	if toolChoice != (responses.ResponseNewParamsToolChoiceUnion{}) {
		request.ToolChoice = toolChoice
	}
	return request, nil
}

func (c *Client) resolveModelID(req *model.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return c.model
}

func encodeInput(messages []*model.Message) (responses.ResponseInputParam, error) {
	items := make(responses.ResponseInputParam, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		text := messageTextContent(msg)
		if text != "" {
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRole(msg.Role),
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
				},
			})
		}
		partItems, err := encodeMessageParts(msg)
		if err != nil {
			return nil, err
		}
		items = append(items, partItems...)
	}
	return items, nil
}

func messageTextContent(m *model.Message) string {
	var text string
	for _, p := range m.Parts {
		switch tp := p.(type) {
		case model.TextPart:
			if tp.Text == "" {
				continue
			}
			text += tp.Text
		case model.CitationsPart:
			if tp.Text == "" {
				continue
			}
			text += tp.Text
		}
	}
	return text
}

func encodeMessageParts(msg *model.Message) ([]responses.ResponseInputItemUnionParam, error) {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case model.ToolUsePart:
			if p.Name == "" {
				return nil, errors.New("openai responses: tool use part requires name")
			}
			if p.ID == "" {
				return nil, errors.New("openai responses: tool use part requires id")
			}
			args, err := marshalJSONValue(p.Input)
			if err != nil {
				return nil, fmt.Errorf("openai responses: encode tool use %q: %w", p.Name, err)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCall: &responses.ResponseFunctionToolCallParam{
					Name:      p.Name,
					CallID:    p.ID,
					Arguments: string(args),
				},
			})
		case model.ToolResultPart:
			if p.ToolUseID == "" {
				return nil, errors.New("openai responses: tool result part requires tool use id")
			}
			output, err := marshalJSONValue(p.Content)
			if err != nil {
				return nil, fmt.Errorf("openai responses: encode tool result %q: %w", p.ToolUseID, err)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: p.ToolUseID,
					Output: string(output),
				},
			})
		}
	}
	return items, nil
}

func buildOpenAIToolChoice(choice *model.ToolChoice, defs []*model.ToolDefinition) (responses.ResponseNewParamsToolChoiceUnion, error) {
	if choice == nil {
		return responses.ResponseNewParamsToolChoiceUnion{}, nil
	}
	switch choice.Mode {
	case "", model.ToolChoiceModeAuto:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		}, nil
	case model.ToolChoiceModeNone:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
		}, nil
	case model.ToolChoiceModeTool:
		return namedOpenAIToolChoice(choice, defs)
	case model.ToolChoiceModeAny:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsRequired),
		}, nil
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: unsupported tool choice mode %q", choice.Mode)
	}
}

func namedOpenAIToolChoice(choice *model.ToolChoice, defs []*model.ToolDefinition) (responses.ResponseNewParamsToolChoiceUnion, error) {
	if choice.Name == "" {
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: tool choice mode %q requires a tool name", choice.Mode)
	}
	if !hasToolDefinition(defs, choice.Name) {
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: tool choice name %q does not match any tool", choice.Name)
	}
	return responses.ResponseNewParamsToolChoiceUnion{
		OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: choice.Name},
	}, nil
}

func hasToolDefinition(defs []*model.ToolDefinition, name string) bool {
	for _, def := range defs {
		if def == nil {
			continue
		}
		if def.Name == name {
			return true
		}
	}
	return false
}

// Stream reports that OpenAI Responses streaming is not yet supported by this
// adapter. Callers should fall back to Complete.
func (c *Client) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func encodeTools(defs []*model.ToolDefinition) ([]responses.ToolUnionParam, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	tools := make([]responses.ToolUnionParam, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		schema, err := schemaObject(def.Name, def.InputSchema)
		if err != nil {
			return nil, err
		}
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        def.Name,
				Description: openai.String(def.Description),
				Parameters:  schema,
				Strict:      openai.Bool(true),
			},
		})
	}
	return tools, nil
}

func translateResponse(resp *responses.Response) *model.Response {
	if resp == nil {
		return &model.Response{}
	}
	messages := make([]model.Message, 0, len(resp.Output))
	toolCalls := make([]model.ToolCall, 0)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			msg := translateOutputMessage(item)
			if len(msg.Parts) > 0 {
				messages = append(messages, msg)
			}
		case "function_call":
			payload := parseToolArguments(item.Arguments)
			toolCallID := item.CallID
			if toolCallID == "" {
				toolCallID = item.ID
			}
			toolCalls = append(toolCalls, model.ToolCall{
				Name:    tools.Ident(item.Name),
				Payload: payload,
				ID:      toolCallID,
			})
		}
	}
	usage := model.TokenUsage{
		Model:           resp.Model,
		InputTokens:     int(resp.Usage.InputTokens),
		OutputTokens:    int(resp.Usage.OutputTokens),
		TotalTokens:     int(resp.Usage.TotalTokens),
		CacheReadTokens: int(resp.Usage.InputTokensDetails.CachedTokens),
	}
	return &model.Response{
		Content:    messages,
		ToolCalls:  toolCalls,
		Usage:      usage,
		StopReason: string(resp.Status),
	}
}

func translateOutputMessage(item responses.ResponseOutputItemUnion) model.Message {
	msg := model.Message{Role: model.ConversationRoleAssistant}
	for _, content := range item.Content {
		switch content.Type {
		case "output_text":
			if content.Text != "" {
				msg.Parts = append(msg.Parts, model.TextPart{Text: content.Text})
			}
		case "refusal":
			if content.Refusal != "" {
				msg.Parts = append(msg.Parts, model.TextPart{Text: content.Refusal})
			}
		}
	}
	return msg
}

func parseToolArguments(raw string) rawjson.Message {
	if raw == "" {
		return nil
	}
	data := []byte(raw)
	if len(data) == 0 {
		return nil
	}
	return rawjson.Message(data)
}

func marshalJSONValue(v any) ([]byte, error) {
	switch val := v.(type) {
	case nil:
		return []byte("null"), nil
	case rawjson.Message:
		if len(val) == 0 {
			return []byte("null"), nil
		}
		return val.RawMessage(), nil
	case json.RawMessage:
		if len(val) == 0 {
			return []byte("null"), nil
		}
		return val, nil
	case []byte:
		if len(val) == 0 {
			return []byte("null"), nil
		}
		return val, nil
	default:
		return json.Marshal(v)
	}
}

func schemaObject(name string, v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{"type": "object"}, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal tool %s schema: %w", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode tool %s schema object: %w", name, err)
	}
	return out, nil
}

func openaiModel(id string) shared.ResponsesModel {
	return id
}
