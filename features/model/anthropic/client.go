// Package anthropic provides a model.Client implementation backed by the
// Anthropic Claude Messages API. It translates loom-mcp requests into
// anthropic.Message calls using github.com/anthropics/anthropic-sdk-go and maps
// responses (text, tools, thinking, usage) back into the generic planner
// structures.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/claudecaps"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type (
	// MessagesClient captures the subset of the Anthropic SDK client used by the
	// adapter. It is satisfied by *sdk.MessageService so callers can pass either a
	// real client or a mock in tests.
	MessagesClient interface {
		New(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) (*sdk.Message, error)
		NewStreaming(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.MessageStreamEventUnion]
	}

	// Options configures optional Anthropic adapter behavior.
	Options struct {
		// DefaultModel is the default Claude model identifier used when
		// model.Request.Model is empty. Use the typed model constants from
		// github.com/anthropics/anthropic-sdk-go (for example,
		// string(sdk.ModelClaudeSonnet4_5_20250929)) or the identifiers listed in
		// the Anthropic model reference in their docs/console.
		DefaultModel string

		// HighModel is the high-reasoning model identifier used when
		// model.Request.ModelClass is ModelClassHighReasoning and Model is empty.
		// As with DefaultModel, prefer the anthropic-sdk-go Model constants or the
		// IDs from Anthropic's model catalogue.
		HighModel string

		// SmallModel is the small/cheap model identifier used when
		// model.Request.ModelClass is ModelClassSmall and Model is empty. Source
		// identifiers from the anthropic-sdk-go Model constants or Anthropic's
		// model documentation.
		SmallModel string

		// MaxTokens sets the default completion cap when a request does not specify
		// MaxTokens. When zero or negative, the client requires callers to set
		// Request.MaxTokens explicitly.
		MaxTokens int

		// Temperature is used when a request does not specify Temperature.
		Temperature float64

		// ThinkingBudget defines the default thinking token budget when thinking is
		// enabled. When zero or negative, callers must supply
		// Request.Thinking.BudgetTokens explicitly.
		ThinkingBudget int64
	}

	// Client implements model.Client on top of Anthropic Claude Messages.
	Client struct {
		msg          MessagesClient
		defaultModel string
		highModel    string
		smallModel   string
		maxTok       int
		temp         float64
		think        int64
	}
)

const anthropicContentTypeToolUse = "tool_use"

// New builds an Anthropic-backed model client from the provided Anthropic
// Messages client and configuration options.
func New(msg MessagesClient, opts Options) (*Client, error) {
	if msg == nil {
		return nil, errors.New("anthropic client is required")
	}
	if opts.DefaultModel == "" {
		return nil, errors.New("default model identifier is required")
	}
	maxTokens := opts.MaxTokens
	thinkBudget := opts.ThinkingBudget

	c := &Client{
		msg:          msg,
		defaultModel: opts.DefaultModel,
		highModel:    opts.HighModel,
		smallModel:   opts.SmallModel,
		maxTok:       maxTokens,
		temp:         opts.Temperature,
		think:        thinkBudget,
	}
	return c, nil
}

// NewFromAPIKey constructs a client using the default Anthropic HTTP client.
// It reads ANTHROPIC_API_KEY and related defaults from the environment via
// sdk.DefaultClientOptions.
func NewFromAPIKey(apiKey, defaultModel string) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("api key is required")
	}
	ac := sdk.NewClient(option.WithAPIKey(apiKey))
	return New(&ac.Messages, Options{DefaultModel: defaultModel})
}

// Complete issues a non-streaming Messages.New request and translates the
// response into planner-friendly structures (assistant messages + tool calls).
func (c *Client) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	params, provToCanon, toolUseIDs, err := c.prepareRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	msg, err := c.msg.New(ctx, *params)
	if err != nil {
		if isRateLimited(err) {
			return nil, fmt.Errorf("%w: %w", model.ErrRateLimited, err)
		}
		return nil, fmt.Errorf("anthropic messages.new: %w", err)
	}
	return translateResponse(msg, provToCanon, toolUseIDs, params.Model, req.ModelClass)
}

// Stream invokes Messages.NewStreaming and adapts incremental events into
// model.Chunks so planners can surface partial responses.
func (c *Client) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	params, provToCanon, toolUseIDs, err := c.prepareRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := c.msg.NewStreaming(ctx, *params)
	if err := stream.Err(); err != nil {
		if isRateLimited(err) {
			return nil, fmt.Errorf("%w: %w", model.ErrRateLimited, err)
		}
		return nil, fmt.Errorf("anthropic messages.new stream: %w", err)
	}
	return newAnthropicStreamer(ctx, stream, params.Model, req.ModelClass, provToCanon, toolUseIDs), nil
}

func (c *Client) prepareRequest(ctx context.Context, req *model.Request) (*sdk.MessageNewParams, map[string]string, *toolUseIDCodec, error) {
	if req.StructuredOutput != nil {
		return nil, nil, nil, fmt.Errorf("anthropic: structured output is not supported: %w", model.ErrStructuredOutputUnsupported)
	}
	modelID, maxTokens, err := c.validateRequestInputs(req)
	if err != nil {
		return nil, nil, nil, err
	}
	tools, canonToProv, provToCanon, err := encodeTools(ctx, req.Tools)
	if err != nil {
		return nil, nil, nil, err
	}
	toolUseIDs := newToolUseIDCodec()
	msgs, system, err := encodeMessages(req.Messages, canonToProv, toolUseIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	applyAnthropicCachePolicy(req.Cache, system, tools)
	params := c.newMessageParams(ctx, modelID, maxTokens, msgs, system, tools, req.Temperature)
	if err := c.applyThinkingConfig(&params, req, maxTokens); err != nil {
		return nil, nil, nil, err
	}
	if err := applyAnthropicToolChoice(&params, req, canonToProv); err != nil {
		return nil, nil, nil, err
	}
	return &params, provToCanon, toolUseIDs, nil
}

func (c *Client) validateRequestInputs(req *model.Request) (string, int, error) {
	if len(req.Messages) == 0 {
		return "", 0, errors.New("anthropic: messages are required")
	}
	modelID := c.resolveModelID(req)
	if modelID == "" {
		return "", 0, errors.New("anthropic: model identifier is required")
	}
	maxTokens := c.effectiveMaxTokens(req.MaxTokens)
	if maxTokens <= 0 {
		return "", 0, errors.New("anthropic: max_tokens must be positive")
	}
	return modelID, maxTokens, nil
}

func (c *Client) newMessageParams(
	ctx context.Context,
	modelID string,
	maxTokens int,
	msgs []sdk.MessageParam,
	system []sdk.TextBlockParam,
	tools []sdk.ToolUnionParam,
	temperature float32,
) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
		Model:     modelID,
	}
	if len(system) > 0 {
		params.System = system
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	if t := c.effectiveTemperature(temperature); t > 0 {
		if claudecaps.TemperatureSupported(modelID) {
			params.Temperature = sdk.Float(t)
		} else {
			traceTemperatureOmitted(ctx, modelID, t)
		}
	}
	return params
}

func (c *Client) applyThinkingConfig(params *sdk.MessageNewParams, req *model.Request, maxTokens int) error {
	if req.Thinking == nil || !req.Thinking.Enable {
		return nil
	}
	budget, err := c.resolveThinkingBudget(req, maxTokens)
	if err != nil {
		return err
	}
	params.Thinking = sdk.ThinkingConfigParamOfEnabled(int64(budget))
	return nil
}

func (c *Client) resolveThinkingBudget(req *model.Request, maxTokens int) (int, error) {
	budget := req.Thinking.BudgetTokens
	if budget <= 0 {
		budget = int(c.think)
	}
	if budget <= 0 {
		return 0, errors.New("anthropic: thinking budget is required when thinking is enabled")
	}
	if budget < 1024 {
		return 0, fmt.Errorf("anthropic: thinking budget %d must be >= 1024", budget)
	}
	if int64(budget) >= int64(maxTokens) {
		return 0, fmt.Errorf("anthropic: thinking budget %d must be less than max_tokens %d", budget, maxTokens)
	}
	return budget, nil
}

func applyAnthropicToolChoice(params *sdk.MessageNewParams, req *model.Request, canonToProv map[string]string) error {
	if req.ToolChoice == nil {
		return nil
	}
	tc, err := encodeToolChoice(req.ToolChoice, canonToProv, req.Tools)
	if err != nil {
		return err
	}
	params.ToolChoice = tc
	return nil
}

// resolveModelID decides which concrete model ID to use based on Request.Model
// and Request.ModelClass. Request.Model takes precedence; when empty, the class
// is mapped to the configured identifiers. Falls back to the default model.
func (c *Client) resolveModelID(req *model.Request) string {
	if s := req.Model; s != "" {
		return s
	}
	switch string(req.ModelClass) {
	case string(model.ModelClassHighReasoning):
		if c.highModel != "" {
			return c.highModel
		}
	case string(model.ModelClassSmall):
		if c.smallModel != "" {
			return c.smallModel
		}
	}
	return c.defaultModel
}

func (c *Client) effectiveMaxTokens(requested int) int {
	if requested > 0 {
		return requested
	}
	return c.maxTok
}

func (c *Client) effectiveTemperature(requested float32) float64 {
	if requested > 0 {
		return float64(requested)
	}
	return c.temp
}

func encodeMessages(msgs []*model.Message, nameMap map[string]string, toolUseIDs *toolUseIDCodec) ([]sdk.MessageParam, []sdk.TextBlockParam, error) {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, part := range m.Parts {
			switch v := part.(type) {
			case model.ToolUsePart:
				toolUseIDs.reserve(v.ID)
			case model.ToolResultPart:
				toolUseIDs.reserve(v.ToolUseID)
			}
		}
	}

	conversation := make([]sdk.MessageParam, 0, len(msgs))
	system := make([]sdk.TextBlockParam, 0, len(msgs))

	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.Role == model.ConversationRoleSystem {
			blocks, err := systemTextBlocks(m.Parts)
			if err != nil {
				return nil, nil, err
			}
			system = append(system, blocks...)
			continue
		}
		blocks, err := anthropicMessageBlocks(m.Role, m.Parts, nameMap, toolUseIDs)
		if err != nil {
			return nil, nil, err
		}
		if len(blocks) == 0 {
			continue
		}
		msg, err := anthropicConversationMessage(m.Role, blocks)
		if err != nil {
			return nil, nil, fmt.Errorf("anthropic: unsupported message role %q", m.Role)
		}
		conversation = append(conversation, msg)
	}
	if len(conversation) == 0 {
		return nil, nil, errors.New("anthropic: at least one user/assistant message is required")
	}
	return conversation, system, nil
}

func systemTextBlocks(parts []model.Part) ([]sdk.TextBlockParam, error) {
	blocks := make([]sdk.TextBlockParam, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case model.TextPart:
			if v.Text != "" {
				blocks = append(blocks, sdk.TextBlockParam{Text: v.Text})
			}
		case model.CitationsPart:
			if v.Text != "" {
				blocks = append(blocks, sdk.TextBlockParam{Text: v.Text})
			}
		case model.CacheCheckpointPart:
			markLastSystemBlockCached(blocks)
			continue
		default:
			return nil, fmt.Errorf("anthropic: unsupported system message part %T", p)
		}
	}
	return blocks, nil
}

func anthropicMessageBlocks(role model.ConversationRole, parts []model.Part, nameMap map[string]string, toolUseIDs *toolUseIDCodec) ([]sdk.ContentBlockParamUnion, error) {
	blocks := make([]sdk.ContentBlockParamUnion, 0, len(parts))
	for _, part := range parts {
		if _, ok := part.(model.CacheCheckpointPart); ok {
			markLastContentBlockCached(blocks)
			continue
		}
		block, ok, err := anthropicMessageBlock(role, part, nameMap, toolUseIDs)
		if err != nil {
			return nil, err
		}
		if ok {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

func anthropicMessageBlock(role model.ConversationRole, part model.Part, nameMap map[string]string, toolUseIDs *toolUseIDCodec) (sdk.ContentBlockParamUnion, bool, error) {
	if v, ok := part.(model.TextPart); ok {
		if v.Text == "" {
			return sdk.ContentBlockParamUnion{}, false, nil
		}
		return sdk.NewTextBlock(v.Text), true, nil
	}
	if v, ok := part.(model.CitationsPart); ok {
		if v.Text == "" {
			return sdk.ContentBlockParamUnion{}, false, nil
		}
		return sdk.NewTextBlock(v.Text), true, nil
	}
	if v, ok := part.(model.ThinkingPart); ok {
		block, ok := anthropicThinkingBlock(v)
		return block, ok, nil
	}
	if v, ok := part.(model.ToolUsePart); ok {
		block, err := anthropicToolUseBlock(v, nameMap, toolUseIDs)
		return block, err == nil, err
	}
	if v, ok := part.(model.ToolResultPart); ok {
		block, err := encodeToolResult(v, toolUseIDs)
		return block, err == nil, err
	}
	if v, ok := part.(model.ImagePart); ok {
		block, err := anthropicImageBlock(role, v)
		return block, err == nil, err
	}
	if _, ok := part.(model.CacheCheckpointPart); ok {
		return sdk.ContentBlockParamUnion{}, false, nil
	}
	return sdk.ContentBlockParamUnion{}, false, fmt.Errorf("anthropic: unsupported message part %T", part)
}

func applyAnthropicCachePolicy(cache *model.CacheOptions, system []sdk.TextBlockParam, tools []sdk.ToolUnionParam) {
	if cache == nil {
		return
	}
	if cache.AfterSystem {
		markLastSystemBlockCached(system)
	}
	if cache.AfterTools {
		markLastToolCached(tools)
	}
}

func markLastSystemBlockCached(blocks []sdk.TextBlockParam) {
	if len(blocks) == 0 {
		return
	}
	blocks[len(blocks)-1].CacheControl = sdk.NewCacheControlEphemeralParam()
}

func markLastContentBlockCached(blocks []sdk.ContentBlockParamUnion) {
	for _, block := range slices.Backward(blocks) {
		cache := block.GetCacheControl()
		if cache == nil {
			continue
		}
		*cache = sdk.NewCacheControlEphemeralParam()
		return
	}
}

func markLastToolCached(toolList []sdk.ToolUnionParam) {
	for _, t := range slices.Backward(toolList) {
		cache := t.GetCacheControl()
		if cache == nil {
			continue
		}
		*cache = sdk.NewCacheControlEphemeralParam()
		return
	}
}

func anthropicThinkingBlock(v model.ThinkingPart) (sdk.ContentBlockParamUnion, bool) {
	if v.Signature != "" && v.Text != "" {
		return sdk.NewThinkingBlock(v.Signature, v.Text), true
	}
	if len(v.Redacted) > 0 {
		return sdk.NewRedactedThinkingBlock(string(v.Redacted)), true
	}
	return sdk.ContentBlockParamUnion{}, false
}

func anthropicImageBlock(role model.ConversationRole, v model.ImagePart) (sdk.ContentBlockParamUnion, error) {
	if role != model.ConversationRoleUser {
		return sdk.ContentBlockParamUnion{}, fmt.Errorf("anthropic: image parts are only supported in user messages (role=%s)", role)
	}
	mimeType, err := anthropicImageMIMEType(v.Format)
	if err != nil {
		return sdk.ContentBlockParamUnion{}, err
	}
	if len(v.Bytes) == 0 {
		return sdk.ContentBlockParamUnion{}, errors.New("anthropic: image bytes are required")
	}
	return sdk.NewImageBlockBase64(mimeType, base64.StdEncoding.EncodeToString(v.Bytes)), nil
}

func anthropicImageMIMEType(format model.ImageFormat) (string, error) {
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
		return "", fmt.Errorf("anthropic: unsupported image format %q", format)
	}
}

func anthropicToolUseBlock(v model.ToolUsePart, nameMap map[string]string, toolUseIDs *toolUseIDCodec) (sdk.ContentBlockParamUnion, error) {
	if v.Name == "" {
		return sdk.ContentBlockParamUnion{}, errors.New("anthropic: tool_use part missing name")
	}
	id := toolUseIDs.encode(v.ID)
	if sanitized, ok := nameMap[v.Name]; ok && sanitized != "" {
		return sdk.NewToolUseBlock(id, v.Input, sanitized), nil
	}
	sanitized, err := anthropicUnavailableToolName(nameMap, v.Name)
	if err != nil {
		return sdk.ContentBlockParamUnion{}, err
	}
	return sdk.NewToolUseBlock(id, map[string]any{
		"requested_tool":    v.Name,
		"requested_payload": v.Input,
	}, sanitized), nil
}

func anthropicUnavailableToolName(nameMap map[string]string, requested string) (string, error) {
	unavailable := tools.ToolUnavailable.String()
	sanitized, ok := nameMap[unavailable]
	if !ok || sanitized == "" {
		return "", fmt.Errorf(
			"anthropic: tool_use in messages references %q which is not in the current tool configuration and tool_unavailable is not available",
			requested,
		)
	}
	return sanitized, nil
}

func anthropicConversationMessage(role model.ConversationRole, blocks []sdk.ContentBlockParamUnion) (sdk.MessageParam, error) {
	switch role { //nolint:exhaustive
	case model.ConversationRoleUser:
		return sdk.NewUserMessage(blocks...), nil
	case model.ConversationRoleAssistant:
		return sdk.NewAssistantMessage(blocks...), nil
	default:
		return sdk.MessageParam{}, errors.New("unsupported message role")
	}
}

func encodeToolResult(v model.ToolResultPart, toolUseIDs *toolUseIDCodec) (sdk.ContentBlockParamUnion, error) {
	var content string
	switch c := v.Content.(type) {
	case nil:
		content = ""
	case string:
		content = c
	case []byte:
		content = string(c)
	default:
		data, err := json.Marshal(c)
		if err != nil {
			return sdk.ContentBlockParamUnion{}, fmt.Errorf("anthropic: encode tool result %q: %w", v.ToolUseID, err)
		}
		content = string(data)
	}
	return sdk.NewToolResultBlock(toolUseIDs.encode(v.ToolUseID), content, v.IsError), nil
}

func encodeTools(ctx context.Context, defs []*model.ToolDefinition) ([]sdk.ToolUnionParam, map[string]string, map[string]string, error) {
	if len(defs) == 0 {
		return nil, nil, nil, nil
	}
	toolList := make([]sdk.ToolUnionParam, 0, len(defs))
	canonToSan := make(map[string]string, len(defs))
	sanToCanon := make(map[string]string, len(defs))

	for _, def := range defs {
		if def == nil {
			continue
		}
		canonical := def.Name
		if canonical == "" {
			continue
		}
		sanitized := sanitizeToolName(canonical)
		if prev, ok := sanToCanon[sanitized]; ok && prev != canonical {
			return nil, nil, nil, fmt.Errorf(
				"anthropic: tool name %q sanitizes to %q which collides with %q",
				canonical, sanitized, prev,
			)
		}
		sanToCanon[sanitized] = canonical
		canonToSan[canonical] = sanitized
		if def.Description == "" {
			return nil, nil, nil, fmt.Errorf("anthropic: tool %q is missing description", canonical)
		}
		schema, err := toolInputSchema(ctx, def.InputSchema)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("anthropic: tool %q schema: %w", canonical, err)
		}
		u := sdk.ToolUnionParamOfTool(schema, sanitized)
		if u.OfTool != nil {
			u.OfTool.Description = sdk.String(def.Description)
		}
		toolList = append(toolList, u)
	}
	if len(toolList) == 0 {
		return nil, nil, nil, nil
	}
	return toolList, canonToSan, sanToCanon, nil
}

func toolInputSchema(_ context.Context, schema any) (sdk.ToolInputSchemaParam, error) {
	if schema == nil {
		return sdk.ToolInputSchemaParam{}, nil
	}
	var raw json.RawMessage
	switch v := schema.(type) {
	case json.RawMessage:
		raw = v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return sdk.ToolInputSchemaParam{}, err
		}
		raw = data
	}
	if len(raw) == 0 {
		return sdk.ToolInputSchemaParam{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return sdk.ToolInputSchemaParam{}, err
	}
	return sdk.ToolInputSchemaParam{
		ExtraFields: m,
	}, nil
}

func encodeToolChoice(choice *model.ToolChoice, canonToProv map[string]string, defs []*model.ToolDefinition) (sdk.ToolChoiceUnionParam, error) {
	if choice == nil {
		return sdk.ToolChoiceUnionParam{}, nil
	}
	switch choice.Mode {
	case "", model.ToolChoiceModeAuto:
		return sdk.ToolChoiceUnionParam{}, nil
	case model.ToolChoiceModeNone:
		none := sdk.NewToolChoiceNoneParam()
		return sdk.ToolChoiceUnionParam{OfNone: &none}, nil
	case model.ToolChoiceModeAny:
		return sdk.ToolChoiceUnionParam{
			OfAny: &sdk.ToolChoiceAnyParam{},
		}, nil
	case model.ToolChoiceModeTool:
		if choice.Name == "" {
			return sdk.ToolChoiceUnionParam{}, fmt.Errorf("anthropic: tool choice mode %q requires a tool name", choice.Mode)
		}
		if !hasToolDefinition(defs, choice.Name) {
			return sdk.ToolChoiceUnionParam{}, fmt.Errorf("anthropic: tool choice name %q does not match any tool", choice.Name)
		}
		sanitized, ok := canonToProv[choice.Name]
		if !ok || sanitized == "" {
			return sdk.ToolChoiceUnionParam{}, fmt.Errorf("anthropic: tool choice name %q does not match any tool", choice.Name)
		}
		tool := sdk.ToolChoiceParamOfTool(sanitized)
		return tool, nil
	default:
		return sdk.ToolChoiceUnionParam{}, fmt.Errorf("anthropic: unsupported tool choice mode %q", choice.Mode)
	}
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

func isRateLimited(err error) bool {
	if errors.Is(err, model.ErrRateLimited) {
		return true
	}
	var apiErr *sdk.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

func translateResponse(msg *sdk.Message, nameMap map[string]string, toolUseIDs *toolUseIDCodec, modelID string, modelClass model.ModelClass) (*model.Response, error) {
	if msg == nil {
		return nil, errors.New("anthropic: response message is nil")
	}
	resp, err := translateResponseContent(msg.Content, nameMap, toolUseIDs)
	if err != nil {
		return nil, err
	}
	if msg.Model != "" {
		modelID = msg.Model
	}
	resp.Usage = anthropicUsage(msg.Usage, modelID, modelClass)
	resp.StopReason = string(msg.StopReason)
	return resp, nil
}

func translateResponseContent(blocks []sdk.ContentBlockUnion, nameMap map[string]string, toolUseIDs *toolUseIDCodec) (*model.Response, error) {
	resp := &model.Response{}
	parts := make([]model.Part, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			appendAnthropicText(&parts, block.Text)
		case "thinking":
			appendAnthropicThinking(&parts, block.AsThinking())
		case "redacted_thinking":
			appendAnthropicRedactedThinking(&parts, block.AsRedactedThinking())
		case anthropicContentTypeToolUse:
			call, err := anthropicToolCall(block, nameMap, toolUseIDs)
			if err != nil {
				return nil, err
			}
			resp.ToolCalls = append(resp.ToolCalls, call)
		}
	}
	if len(parts) > 0 {
		resp.Content = append(resp.Content, model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: parts,
		})
	}
	return resp, nil
}

func appendAnthropicText(parts *[]model.Part, text string) {
	if text == "" {
		return
	}
	*parts = append(*parts, model.TextPart{Text: text})
}

func appendAnthropicThinking(parts *[]model.Part, block sdk.ThinkingBlock) {
	if block.Thinking == "" || block.Signature == "" {
		return
	}
	*parts = append(*parts, model.ThinkingPart{
		Text:      block.Thinking,
		Signature: block.Signature,
		Final:     true,
	})
}

func appendAnthropicRedactedThinking(parts *[]model.Part, block sdk.RedactedThinkingBlock) {
	if block.Data == "" {
		return
	}
	*parts = append(*parts, model.ThinkingPart{
		Redacted: []byte(block.Data),
		Final:    true,
	})
}

func anthropicToolCall(block sdk.ContentBlockUnion, nameMap map[string]string, toolUseIDs *toolUseIDCodec) (model.ToolCall, error) {
	payload := rawjson.Message(block.Input)
	if _, err := payload.MarshalJSON(); err != nil {
		return model.ToolCall{}, fmt.Errorf("anthropic: tool call %q payload: %w", block.ID, err)
	}
	return model.ToolCall{
		Name:    tools.Ident(resolveAnthropicToolName(block.Name, nameMap)),
		Payload: payload,
		ID:      toolUseIDs.decode(block.ID),
	}, nil
}

func resolveAnthropicToolName(raw string, nameMap map[string]string) string {
	if raw == "" {
		return ""
	}
	if canonical, ok := nameMap[raw]; ok {
		return canonical
	}
	return raw
}

func anthropicUsage(u sdk.Usage, modelID string, modelClass model.ModelClass) model.TokenUsage {
	return model.TokenUsage{
		Model:            modelID,
		ModelClass:       modelClass,
		InputTokens:      int(u.InputTokens),
		OutputTokens:     int(u.OutputTokens),
		TotalTokens:      int(u.InputTokens + u.OutputTokens),
		CacheReadTokens:  int(u.CacheReadInputTokens),
		CacheWriteTokens: int(u.CacheCreationInputTokens),
	}
}
