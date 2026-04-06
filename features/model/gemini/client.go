// Package gemini provides a model.Client implementation backed by the Google
// Gen AI SDK. It translates loom-mcp requests into Gemini GenerateContent
// calls and maps responses back into the generic planner structures.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"

	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// ModelsClient captures the subset of the Gen AI SDK used by the adapter.
	ModelsClient interface {
		GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	}

	// Options configures optional Gemini adapter behavior.
	Options struct {
		// Client is the underlying Gen AI models client.
		Client ModelsClient

		// DefaultModel is the model identifier used when Request.Model is empty.
		DefaultModel string

		// HighModel is used when Request.ModelClass is high-reasoning and
		// Request.Model is empty.
		HighModel string

		// SmallModel is used when Request.ModelClass is small and Request.Model is
		// empty.
		SmallModel string
	}

	// Client implements model.Client using Gemini GenerateContent calls.
	Client struct {
		models       ModelsClient
		defaultModel string
		highModel    string
		smallModel   string
	}

	toolResultRef struct {
		name string
		id   string
	}
)

const toolExecutionFailed = "tool execution failed"

// New builds a Gemini-backed model client from the provided options.
func New(opts Options) (*Client, error) {
	if opts.Client == nil {
		return nil, errors.New("gemini client is required")
	}
	if opts.DefaultModel == "" {
		return nil, errors.New("default model identifier is required")
	}
	return &Client{
		models:       opts.Client,
		defaultModel: opts.DefaultModel,
		highModel:    opts.HighModel,
		smallModel:   opts.SmallModel,
	}, nil
}

// NewFromAPIKey constructs a Gemini API client using the official Gen AI SDK.
func NewFromAPIKey(apiKey, defaultModel string) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("api key is required")
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client init: %w", err)
	}
	return New(Options{Client: client.Models, DefaultModel: defaultModel})
}

// Complete renders a response using the configured Gemini client.
func (c *Client) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	modelID, contents, config, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.models.GenerateContent(ctx, modelID, contents, config)
	if err != nil {
		if isRateLimited(err) {
			return nil, fmt.Errorf("%w: %w", model.ErrRateLimited, err)
		}
		return nil, fmt.Errorf("gemini generate_content: %w", err)
	}
	return translateResponse(modelID, resp)
}

// Stream reports that Gemini streaming is not yet supported by this adapter.
func (c *Client) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (c *Client) buildRequest(req *model.Request) (string, []*genai.Content, *genai.GenerateContentConfig, error) {
	if len(req.Messages) == 0 {
		return "", nil, nil, errors.New("gemini: messages are required")
	}
	modelID := c.resolveModelID(req)
	if modelID == "" {
		return "", nil, nil, errors.New("gemini: model identifier is required")
	}
	contents, systemInstruction, err := encodeMessages(req.Messages)
	if err != nil {
		return "", nil, nil, err
	}
	if len(contents) == 0 {
		return "", nil, nil, errors.New("gemini: at least one user or assistant message is required")
	}
	config, err := buildGenerateContentConfig(systemInstruction, req)
	if err != nil {
		return "", nil, nil, err
	}
	return modelID, contents, config, nil
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

func buildGenerateContentConfig(systemInstruction *genai.Content, req *model.Request) (*genai.GenerateContentConfig, error) {
	config := &genai.GenerateContentConfig{}
	if systemInstruction != nil {
		config.SystemInstruction = systemInstruction
	}
	if req.Temperature != 0 {
		config.Temperature = genai.Ptr(req.Temperature)
	}
	if req.MaxTokens > 0 {
		if req.MaxTokens > math.MaxInt32 {
			return nil, fmt.Errorf("gemini: max tokens %d exceed int32 range", req.MaxTokens)
		}
		config.MaxOutputTokens = int32(req.MaxTokens)
	}
	if len(req.Tools) > 0 {
		tool, err := encodeToolDefinitions(req.Tools)
		if err != nil {
			return nil, err
		}
		config.Tools = []*genai.Tool{tool}
	}
	toolConfig, err := encodeToolChoice(req.ToolChoice, req.Tools)
	if err != nil {
		return nil, err
	}
	if toolConfig != nil {
		config.ToolConfig = toolConfig
	}
	return config, nil
}

func encodeMessages(msgs []*model.Message) ([]*genai.Content, *genai.Content, error) {
	contents := make([]*genai.Content, 0, len(msgs))
	systemParts := make([]*genai.Part, 0)
	toolCalls := make(map[string]toolResultRef)

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if msg.Role == model.ConversationRoleSystem {
			parts, err := encodeSystemParts(msg.Parts)
			if err != nil {
				return nil, nil, err
			}
			systemParts = append(systemParts, parts...)
			continue
		}
		content, err := encodeConversationMessage(msg, toolCalls)
		if err != nil {
			return nil, nil, err
		}
		if content != nil {
			contents = append(contents, content)
		}
	}

	var systemInstruction *genai.Content
	if len(systemParts) > 0 {
		systemInstruction = genai.NewContentFromParts(systemParts, genai.RoleUser)
	}
	return contents, systemInstruction, nil
}

func encodeSystemParts(parts []model.Part) ([]*genai.Part, error) {
	out := make([]*genai.Part, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case model.TextPart:
			if p.Text != "" {
				out = append(out, genai.NewPartFromText(p.Text))
			}
		case model.CitationsPart:
			if p.Text != "" {
				out = append(out, genai.NewPartFromText(p.Text))
			}
		case model.CacheCheckpointPart, model.ThinkingPart:
			continue
		default:
			return nil, fmt.Errorf("gemini: unsupported system message part %T", part)
		}
	}
	return out, nil
}

func encodeConversationMessage(msg *model.Message, toolCalls map[string]toolResultRef) (*genai.Content, error) {
	role, err := geminiRole(msg.Role)
	if err != nil {
		return nil, err
	}
	parts := make([]*genai.Part, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		encoded, err := encodeConversationPart(part, toolCalls)
		if err != nil {
			return nil, err
		}
		if encoded != nil {
			parts = append(parts, encoded)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return genai.NewContentFromParts(parts, role), nil
}

func geminiRole(role model.ConversationRole) (genai.Role, error) {
	switch role {
	case model.ConversationRoleUser:
		return genai.RoleUser, nil
	case model.ConversationRoleAssistant:
		return genai.RoleModel, nil
	case model.ConversationRoleSystem:
		return "", errors.New("gemini: system messages must be encoded separately")
	default:
		return "", fmt.Errorf("gemini: unsupported message role %q", role)
	}
}

func encodeConversationPart(part model.Part, toolCalls map[string]toolResultRef) (*genai.Part, error) {
	switch p := part.(type) {
	case model.TextPart:
		if p.Text == "" {
			return nil, nil
		}
		return genai.NewPartFromText(p.Text), nil
	case model.CitationsPart:
		if p.Text == "" {
			return nil, nil
		}
		return genai.NewPartFromText(p.Text), nil
	case model.ImagePart:
		return encodeImagePart(p)
	case model.ToolUsePart:
		return encodeToolUsePart(p, toolCalls)
	case model.ToolResultPart:
		return encodeToolResultPart(p, toolCalls)
	case model.ThinkingPart:
		return encodeThinkingPart(p), nil
	case model.CacheCheckpointPart:
		return nil, nil
	default:
		return nil, fmt.Errorf("gemini: unsupported message part %T", part)
	}
}

func encodeImagePart(part model.ImagePart) (*genai.Part, error) {
	mimeType, err := geminiImageMIMEType(part.Format)
	if err != nil {
		return nil, err
	}
	if len(part.Bytes) == 0 {
		return nil, errors.New("gemini: image bytes are required")
	}
	return genai.NewPartFromBytes(part.Bytes, mimeType), nil
}

func geminiImageMIMEType(format model.ImageFormat) (string, error) {
	switch format {
	case model.ImageFormatPNG:
		return "image/png", nil
	case model.ImageFormatJPEG:
		return "image/jpeg", nil
	case model.ImageFormatWEBP:
		return "image/webp", nil
	case model.ImageFormatGIF:
		return "", errors.New("gemini: GIF images are not supported")
	default:
		return "", fmt.Errorf("gemini: unsupported image format %q", format)
	}
}

func encodeToolUsePart(part model.ToolUsePart, toolCalls map[string]toolResultRef) (*genai.Part, error) {
	if part.Name == "" {
		return nil, errors.New("gemini: tool use part requires name")
	}
	args, err := objectValue(part.Input)
	if err != nil {
		return nil, fmt.Errorf("gemini: tool use %q args: %w", part.Name, err)
	}
	if part.ID != "" {
		toolCalls[part.ID] = toolResultRef{name: part.Name, id: part.ID}
	}
	return &genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   part.ID,
			Name: part.Name,
			Args: args,
		},
	}, nil
}

func encodeToolResultPart(part model.ToolResultPart, toolCalls map[string]toolResultRef) (*genai.Part, error) {
	ref, ok := toolCalls[part.ToolUseID]
	if !ok || ref.name == "" {
		return nil, fmt.Errorf("gemini: tool result references unknown tool use id %q", part.ToolUseID)
	}
	response, err := toolResultResponse(part)
	if err != nil {
		return nil, err
	}
	return &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       ref.id,
			Name:     ref.name,
			Response: response,
		},
	}, nil
}

func toolResultResponse(part model.ToolResultPart) (map[string]any, error) {
	if part.IsError {
		return map[string]any{"error": stringifyToolResult(part.Content)}, nil
	}
	value, err := jsonValue(part.Content)
	if err != nil {
		return nil, fmt.Errorf("gemini: tool result %q: %w", part.ToolUseID, err)
	}
	return map[string]any{"output": value}, nil
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
		data, err := json.Marshal(v)
		if err != nil {
			return toolExecutionFailed
		}
		return string(data)
	}
}

func encodeThinkingPart(part model.ThinkingPart) *genai.Part {
	if part.Text == "" && len(part.Signature) == 0 {
		return nil
	}
	return &genai.Part{
		Text:             part.Text,
		Thought:          true,
		ThoughtSignature: []byte(part.Signature),
	}
}

func encodeToolDefinitions(defs []*model.ToolDefinition) (*genai.Tool, error) {
	declarations := make([]*genai.FunctionDeclaration, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		schema, err := schemaObject(def.Name, def.InputSchema)
		if err != nil {
			return nil, err
		}
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:                 def.Name,
			Description:          def.Description,
			ParametersJsonSchema: schema,
		})
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	return &genai.Tool{FunctionDeclarations: declarations}, nil
}

func encodeToolChoice(choice *model.ToolChoice, defs []*model.ToolDefinition) (*genai.ToolConfig, error) {
	if choice == nil {
		return nil, nil
	}
	cfg := &genai.FunctionCallingConfig{}
	switch choice.Mode {
	case "", model.ToolChoiceModeAuto:
		cfg.Mode = genai.FunctionCallingConfigModeAuto
	case model.ToolChoiceModeNone:
		cfg.Mode = genai.FunctionCallingConfigModeNone
	case model.ToolChoiceModeAny:
		cfg.Mode = genai.FunctionCallingConfigModeAny
		cfg.AllowedFunctionNames = toolDefinitionNames(defs)
	case model.ToolChoiceModeTool:
		if choice.Name == "" {
			return nil, fmt.Errorf("gemini: tool choice mode %q requires a tool name", choice.Mode)
		}
		if !hasToolDefinition(defs, choice.Name) {
			return nil, fmt.Errorf("gemini: tool choice name %q does not match any tool", choice.Name)
		}
		cfg.Mode = genai.FunctionCallingConfigModeAny
		cfg.AllowedFunctionNames = []string{choice.Name}
	default:
		return nil, fmt.Errorf("gemini: unsupported tool choice mode %q", choice.Mode)
	}
	return &genai.ToolConfig{FunctionCallingConfig: cfg}, nil
}

func toolDefinitionNames(defs []*model.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil || def.Name == "" {
			continue
		}
		names = append(names, def.Name)
	}
	return names
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

func translateResponse(modelID string, resp *genai.GenerateContentResponse) (*model.Response, error) {
	if resp == nil {
		return &model.Response{}, nil
	}
	messages := make([]model.Message, 0, len(resp.Candidates))
	toolCalls := make([]model.ToolCall, 0)
	stopReason := ""

	for _, candidate := range resp.Candidates {
		if candidate == nil {
			continue
		}
		if stopReason == "" && candidate.FinishReason != "" {
			stopReason = string(candidate.FinishReason)
		}
		msg, calls, err := translateCandidate(candidate)
		if err != nil {
			return nil, err
		}
		if len(msg.Parts) > 0 {
			messages = append(messages, msg)
		}
		toolCalls = append(toolCalls, calls...)
	}

	return &model.Response{
		Content:    messages,
		ToolCalls:  toolCalls,
		Usage:      translateUsage(modelID, resp.UsageMetadata),
		StopReason: stopReason,
	}, nil
}

func translateCandidate(candidate *genai.Candidate) (model.Message, []model.ToolCall, error) {
	msg := model.Message{Role: model.ConversationRoleAssistant}
	toolCalls := make([]model.ToolCall, 0)

	if candidate.Content == nil {
		return msg, toolCalls, nil
	}
	for _, part := range candidate.Content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "":
			if part.Thought {
				msg.Parts = append(msg.Parts, model.ThinkingPart{
					Text:      part.Text,
					Signature: string(part.ThoughtSignature),
				})
				continue
			}
			msg.Parts = append(msg.Parts, model.TextPart{Text: part.Text})
		case part.FunctionCall != nil:
			payload, err := marshalJSONValue(part.FunctionCall.Args)
			if err != nil {
				return model.Message{}, nil, fmt.Errorf("gemini: decode tool call %q: %w", part.FunctionCall.Name, err)
			}
			toolCalls = append(toolCalls, model.ToolCall{
				Name:    tools.Ident(part.FunctionCall.Name),
				Payload: rawjson.Message(payload),
				ID:      part.FunctionCall.ID,
			})
		}
	}
	return msg, toolCalls, nil
}

func translateUsage(modelID string, usage *genai.GenerateContentResponseUsageMetadata) model.TokenUsage {
	if usage == nil {
		return model.TokenUsage{Model: modelID}
	}
	return model.TokenUsage{
		Model:           modelID,
		InputTokens:     int(usage.PromptTokenCount + usage.ToolUsePromptTokenCount),
		OutputTokens:    int(usage.CandidatesTokenCount),
		TotalTokens:     int(usage.TotalTokenCount),
		CacheReadTokens: int(usage.CachedContentTokenCount),
	}
}

func objectValue(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	data, err := marshalJSONValue(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("expected JSON object")
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func jsonValue(v any) (any, error) {
	switch value := v.(type) {
	case nil:
		return nil, nil
	case rawjson.Message:
		if len(value) == 0 {
			return nil, nil
		}
		var out any
		if err := json.Unmarshal(value.RawMessage(), &out); err != nil {
			return nil, err
		}
		return out, nil
	case json.RawMessage:
		if len(value) == 0 {
			return nil, nil
		}
		var out any
		if err := json.Unmarshal(value, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return v, nil
	}
}

func marshalJSONValue(v any) ([]byte, error) {
	switch value := v.(type) {
	case nil:
		return []byte("null"), nil
	case rawjson.Message:
		if len(value) == 0 {
			return []byte("null"), nil
		}
		return value.RawMessage(), nil
	case json.RawMessage:
		if len(value) == 0 {
			return []byte("null"), nil
		}
		return value, nil
	case []byte:
		if len(value) == 0 {
			return []byte("null"), nil
		}
		return value, nil
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

func isRateLimited(err error) bool {
	if errors.Is(err, model.ErrRateLimited) {
		return true
	}
	var apiErr genai.APIError
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests
}
