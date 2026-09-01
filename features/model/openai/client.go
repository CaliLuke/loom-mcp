// Package openai provides a model.Client implementation backed by the OpenAI
// Responses API. It translates loom-mcp requests into Responses API calls and
// maps responses back to the generic planner structures.
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ResponseClient captures the subset of the official OpenAI client used by the adapter.
type ResponseClient interface {
	New(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error)
	NewStreaming(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
}

// Options configures the OpenAI adapter.
type Options struct {
	Client       ResponseClient
	DefaultModel string

	// HighModel is used when Request.ModelClass is high-reasoning and
	// Request.Model is empty.
	HighModel string

	// SmallModel is used when Request.ModelClass is small and Request.Model is
	// empty.
	SmallModel string
}

// Client implements model.Client via the OpenAI Responses API.
type Client struct {
	resp       ResponseClient
	model      string
	highModel  string
	smallModel string
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
	return &Client{
		resp:       opts.Client,
		model:      modelID,
		highModel:  opts.HighModel,
		smallModel: opts.SmallModel,
	}, nil
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
		if isRateLimited(err) {
			return nil, fmt.Errorf("%w: %w", model.ErrRateLimited, err)
		}
		return nil, fmt.Errorf("openai responses: %w", err)
	}
	return translateResponse(response, codec, req.ModelClass, req.StructuredOutput)
}

func (c *Client) buildResponseRequest(req *model.Request) (responses.ResponseNewParams, *openAIToolCodec, error) {
	if len(req.Messages) == 0 {
		return responses.ResponseNewParams{}, nil, errors.New("messages are required")
	}
	if req.StructuredOutput != nil && (len(req.Tools) > 0 || req.ToolChoice != nil) {
		return responses.ResponseNewParams{}, nil, errors.New("openai: structured output cannot be combined with tools")
	}
	tools, codec, err := encodeTools(req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	input, err := encodeInput(req.Messages, codec)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	request := responses.ResponseNewParams{
		Model: openaiModel(c.resolveModelID(req)),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		Tools: tools,
	}
	if req.Temperature > 0 {
		request.Temperature = openai.Float(float64(req.Temperature))
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
	toolChoice, err := buildOpenAIToolChoice(req.ToolChoice, req.Tools, codec)
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
	switch req.ModelClass {
	case "", model.ModelClassDefault:
		return c.model
	case model.ModelClassHighReasoning:
		if c.highModel != "" {
			return c.highModel
		}
	case model.ModelClassSmall:
		if c.smallModel != "" {
			return c.smallModel
		}
	}
	return c.model
}

func encodeInput(messages []*model.Message, codec *openAIToolCodec) (responses.ResponseInputParam, error) {
	items := make(responses.ResponseInputParam, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content, ok, err := encodeMessageContent(msg)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRole(msg.Role),
					Content: content,
				},
			})
		}
		partItems, err := encodeMessageParts(msg, codec)
		if err != nil {
			return nil, err
		}
		items = append(items, partItems...)
	}
	return items, nil
}

func encodeMessageContent(msg *model.Message) (responses.EasyInputMessageContentUnionParam, bool, error) {
	builder := messageContentBuilder{
		content: make(responses.ResponseInputMessageContentListParam, 0, len(msg.Parts)),
	}
	for _, part := range msg.Parts {
		if err := builder.add(msg.Role, part); err != nil {
			return responses.EasyInputMessageContentUnionParam{}, false, err
		}
	}
	return builder.union()
}

// messageContentBuilder accumulates OpenAI Responses content items, coalescing
// consecutive text-bearing parts into a single input_text item.
type messageContentBuilder struct {
	content responses.ResponseInputMessageContentListParam
	text    strings.Builder
}

func (b *messageContentBuilder) add(role model.ConversationRole, part model.Part) error {
	switch p := part.(type) {
	case model.TextPart:
		b.text.WriteString(p.Text)
	case model.CitationsPart:
		b.text.WriteString(p.Text)
	case model.ImagePart:
		b.flushText()
		image, err := encodeImageContent(role, p)
		if err != nil {
			return err
		}
		b.content = append(b.content, image)
	case model.ToolUsePart, model.ToolResultPart:
		// Encoded separately as response items, not message content.
		return nil
	case model.CacheCheckpointPart, model.ThinkingPart:
		// Cache checkpoints are ignored because this adapter has no prompt
		// caching; replayed thinking parts from other providers cannot be
		// sent to the Responses API and are dropped.
		return nil
	default:
		return fmt.Errorf("openai responses: unsupported message part %T", part)
	}
	return nil
}

func (b *messageContentBuilder) flushText() {
	if b.text.Len() == 0 {
		return
	}
	b.content = append(b.content, responses.ResponseInputContentUnionParam{
		OfInputText: &responses.ResponseInputTextParam{Text: b.text.String()},
	})
	b.text.Reset()
}

func (b *messageContentBuilder) union() (responses.EasyInputMessageContentUnionParam, bool, error) {
	b.flushText()
	if len(b.content) == 0 {
		return responses.EasyInputMessageContentUnionParam{}, false, nil
	}
	if len(b.content) == 1 && b.content[0].OfInputText != nil {
		return responses.EasyInputMessageContentUnionParam{
			OfString: openai.String(b.content[0].OfInputText.Text),
		}, true, nil
	}
	return responses.EasyInputMessageContentUnionParam{
		OfInputItemContentList: b.content,
	}, true, nil
}

func encodeImageContent(role model.ConversationRole, part model.ImagePart) (responses.ResponseInputContentUnionParam, error) {
	if role != model.ConversationRoleUser {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("openai responses: image parts are only supported in user messages (role=%s)", role)
	}
	mimeType, err := openAIImageMIMEType(part.Format)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, err
	}
	if len(part.Bytes) == 0 {
		return responses.ResponseInputContentUnionParam{}, errors.New("openai responses: image bytes are required")
	}
	return responses.ResponseInputContentUnionParam{
		OfInputImage: &responses.ResponseInputImageParam{
			Detail:   responses.ResponseInputImageDetailAuto,
			ImageURL: openai.String("data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Bytes)),
		},
	}, nil
}

func openAIImageMIMEType(format model.ImageFormat) (string, error) {
	switch format {
	case model.ImageFormatPNG:
		return "image/png", nil
	case model.ImageFormatJPEG:
		return "image/jpeg", nil
	case model.ImageFormatWEBP:
		return "image/webp", nil
	case model.ImageFormatGIF:
		return "image/gif", nil
	default:
		return "", fmt.Errorf("openai responses: unsupported image format %q", format)
	}
}

func encodeMessageParts(msg *model.Message, codec *openAIToolCodec) ([]responses.ResponseInputItemUnionParam, error) {
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
					Name:      codec.wireName(p.Name),
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
		case model.TextPart, model.CitationsPart, model.ImagePart:
			continue
		case model.CacheCheckpointPart, model.ThinkingPart:
			// Cache checkpoints are ignored because this adapter has no prompt
			// caching; replayed thinking parts from other providers cannot be
			// sent to the Responses API and are dropped.
			continue
		default:
			return nil, fmt.Errorf("openai responses: unsupported message part %T", part)
		}
	}
	return items, nil
}

func buildOpenAIToolChoice(choice *model.ToolChoice, defs []*model.ToolDefinition, codec *openAIToolCodec) (responses.ResponseNewParamsToolChoiceUnion, error) {
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
		return namedOpenAIToolChoice(choice, defs, codec)
	case model.ToolChoiceModeAny:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsRequired),
		}, nil
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: unsupported tool choice mode %q", choice.Mode)
	}
}

func namedOpenAIToolChoice(choice *model.ToolChoice, defs []*model.ToolDefinition, codec *openAIToolCodec) (responses.ResponseNewParamsToolChoiceUnion, error) {
	if choice.Name == "" {
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: tool choice mode %q requires a tool name", choice.Mode)
	}
	if !hasToolDefinition(defs, choice.Name) {
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: tool choice name %q does not match any tool", choice.Name)
	}
	return responses.ResponseNewParamsToolChoiceUnion{
		OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: codec.wireName(choice.Name)},
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

// Stream renders a streaming response using the configured OpenAI client.
func (c *Client) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	request, codec, err := c.buildResponseRequest(req)
	if err != nil {
		return nil, err
	}
	stream := c.resp.NewStreaming(ctx, request)
	if stream == nil {
		return nil, errors.New("openai: stream is nil")
	}
	if err := stream.Err(); err != nil {
		_ = stream.Close()
		return nil, wrapResponsesStreamError(err)
	}
	return newOpenAIStreamer(ctx, stream, codec, c.resolveModelID(req), req.ModelClass, req.StructuredOutput), nil
}

func wrapResponsesStreamError(err error) error {
	if isRateLimited(err) {
		return fmt.Errorf("%w: %w", model.ErrRateLimited, err)
	}
	return fmt.Errorf("openai responses stream: %w", err)
}

func isRateLimited(err error) bool {
	if errors.Is(err, model.ErrRateLimited) {
		return true
	}
	var apiErr *openai.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

func encodeTools(defs []*model.ToolDefinition) ([]responses.ToolUnionParam, *openAIToolCodec, error) {
	if len(defs) == 0 {
		return nil, nil, nil
	}
	tools := make([]responses.ToolUnionParam, 0, len(defs))
	codec := &openAIToolCodec{
		schemas:    make(map[string]rawjson.Message, len(defs)),
		canonToSan: make(map[string]string, len(defs)),
		sanToCanon: make(map[string]string, len(defs)),
	}
	for _, def := range defs {
		if def == nil {
			continue
		}
		canonical := def.Name
		sanitized := sanitizeOpenAIToolName(canonical)
		if prev, ok := codec.sanToCanon[sanitized]; ok && prev != canonical {
			return nil, nil, fmt.Errorf(
				"openai: tool name %q sanitizes to %q which collides with %q",
				canonical, sanitized, prev,
			)
		}
		schema, err := schemaMessage(canonical, def.InputSchema)
		if err != nil {
			return nil, nil, err
		}
		parameters, err := projectStrictSchema(schema)
		if err != nil {
			return nil, nil, fmt.Errorf("openai: tool %q schema: %w", canonical, err)
		}
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        sanitized,
				Description: openai.String(def.Description),
				Parameters:  parameters,
				Strict:      openai.Bool(true),
			},
		})
		codec.schemas[canonical] = schema
		codec.canonToSan[canonical] = sanitized
		codec.sanToCanon[sanitized] = canonical
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

func translateResponse(resp *responses.Response, codec *openAIToolCodec, modelClass model.ModelClass, output *model.StructuredOutput) (*model.Response, error) {
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
		ModelClass:      modelClass,
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
	canonical := codec.canonicalName(item.Name)
	if codec != nil {
		if schema := codec.schemas[canonical]; len(schema) > 0 {
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
		Name:    tools.Ident(canonical),
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
	if !jsontext.Value(data).IsValid() {
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
	case jsontext.Value:
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
	case jsontext.Value:
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
	if !jsontext.Value(text).IsValid() {
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
