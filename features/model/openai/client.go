// Package openai provides a model.Client implementation backed by the OpenAI
// Responses API. It translates loom-mcp requests into Responses API calls and
// maps responses back to the generic planner structures.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

type openAIToolCodec struct {
	schemas map[string]rawjson.Message
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
	request, codec, err := c.buildResponseRequest(req)
	if err != nil {
		return nil, err
	}
	response, err := c.resp.New(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("openai responses: %w", err)
	}
	return translateResponse(response, codec, req.StructuredOutput)
}

func (c *Client) buildResponseRequest(req *model.Request) (responses.ResponseNewParams, *openAIToolCodec, error) {
	if len(req.Messages) == 0 {
		return responses.ResponseNewParams{}, nil, errors.New("messages are required")
	}
	if req.StructuredOutput != nil && (len(req.Tools) > 0 || req.ToolChoice != nil) {
		return responses.ResponseNewParams{}, nil, errors.New("openai: structured output cannot be combined with tools")
	}
	input, err := encodeInput(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	tools, codec, err := encodeTools(req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
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
	textConfig, err := encodeStructuredOutput(req.StructuredOutput)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	if textConfig != (responses.ResponseTextConfigParam{}) {
		request.Text = textConfig
	}
	toolChoice, err := buildOpenAIToolChoice(req.ToolChoice, req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	if toolChoice != (responses.ResponseNewParamsToolChoiceUnion{}) {
		request.ToolChoice = toolChoice
	}
	return request, codec, nil
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

func encodeTools(defs []*model.ToolDefinition) ([]responses.ToolUnionParam, *openAIToolCodec, error) {
	if len(defs) == 0 {
		return nil, nil, nil
	}
	tools := make([]responses.ToolUnionParam, 0, len(defs))
	codec := &openAIToolCodec{schemas: make(map[string]rawjson.Message, len(defs))}
	for _, def := range defs {
		if def == nil {
			continue
		}
		schema, err := schemaMessage(def.Name, def.InputSchema)
		if err != nil {
			return nil, nil, err
		}
		parameters, err := projectStrictSchema(schema)
		if err != nil {
			return nil, nil, fmt.Errorf("openai: tool %q schema: %w", def.Name, err)
		}
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        def.Name,
				Description: openai.String(def.Description),
				Parameters:  parameters,
				Strict:      openai.Bool(true),
			},
		})
		codec.schemas[def.Name] = schema
	}
	return tools, codec, nil
}

func encodeStructuredOutput(output *model.StructuredOutput) (responses.ResponseTextConfigParam, error) {
	if output == nil {
		return responses.ResponseTextConfigParam{}, nil
	}
	if len(output.Schema) == 0 {
		return responses.ResponseTextConfigParam{}, errors.New("openai: structured output schema is required")
	}
	name := output.Name
	if name == "" {
		name = "structured_output"
	}
	schema, err := schemaMessage(name, output.Schema)
	if err != nil {
		return responses.ResponseTextConfigParam{}, fmt.Errorf("openai: structured output schema: %w", err)
	}
	parameters, err := projectStrictSchema(schema)
	if err != nil {
		return responses.ResponseTextConfigParam{}, fmt.Errorf("openai: structured output schema: %w", err)
	}
	return responses.ResponseTextConfigParam{
		Format: responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
				Name:   name,
				Schema: parameters,
				Strict: param.NewOpt(true),
			},
		},
	}, nil
}

func translateResponse(resp *responses.Response, codec *openAIToolCodec, output *model.StructuredOutput) (*model.Response, error) {
	if resp == nil {
		return &model.Response{}, nil
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
			toolCall, err := translateFunctionCall(item, codec)
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}
	messages, err := canonicalizeStructuredOutputMessages(messages, output)
	if err != nil {
		return nil, err
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
	}, nil
}

func translateFunctionCall(item responses.ResponseOutputItemUnion, codec *openAIToolCodec) (model.ToolCall, error) {
	payload, err := parseToolArguments(item.Arguments)
	if err != nil {
		return model.ToolCall{}, fmt.Errorf("openai responses: tool call %q payload: %w", item.CallID, err)
	}
	if codec != nil {
		if schema := codec.schemas[item.Name]; len(schema) > 0 {
			payload, err = canonicalizeStrictPayload(schema, payload)
			if err != nil {
				return model.ToolCall{}, fmt.Errorf("openai responses: tool call %q payload: %w", item.CallID, err)
			}
		}
	}
	toolCallID := item.CallID
	if toolCallID == "" {
		toolCallID = item.ID
	}
	return model.ToolCall{
		Name:    tools.Ident(item.Name),
		Payload: payload,
		ID:      toolCallID,
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

func parseToolArguments(raw string) (rawjson.Message, error) {
	if raw == "" {
		return nil, nil
	}
	data := []byte(raw)
	if len(data) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON")
	}
	return rawjson.Message(data), nil
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

func schemaMessage(name string, v any) (rawjson.Message, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case rawjson.Message:
		return val, nil
	case json.RawMessage:
		return rawjson.Message(val), nil
	case []byte:
		return rawjson.Message(val), nil
	case string:
		return rawjson.Message(val), nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal tool %s schema: %w", name, err)
	}
	return rawjson.Message(data), nil
}

func structuredOutputPayload(content []model.Message, output *model.StructuredOutput) (rawjson.Message, error) {
	text := strings.TrimSpace(extractAssistantText(content))
	if text == "" {
		return nil, fmt.Errorf("openai: structured output %q completed without content", output.Name)
	}
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("openai: structured output %q payload is not valid JSON", output.Name)
	}
	payload, err := canonicalizeStrictPayload(output.Schema, rawjson.Message(text))
	if err != nil {
		return nil, fmt.Errorf("openai: structured output %q payload: %w", output.Name, err)
	}
	return payload, nil
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

func openaiModel(id string) shared.ResponsesModel {
	return id
}
