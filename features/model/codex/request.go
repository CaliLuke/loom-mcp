package codex

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

const (
	wireAuto       = "auto"
	wireCallID     = "call_id"
	wireContentKey = "content"
	wireFunction   = "function"
	wireName       = "name"
	wireObject     = "object"
	wireOutputText = "output_text"
	wireRefusal    = "refusal"
	wireRequired   = "required"
	wireSummary    = "summary"
	wireType       = "type"
)

type wireRequest struct {
	Model             string         `json:"model"`
	Store             bool           `json:"store"`
	Stream            bool           `json:"stream"`
	Instructions      string         `json:"instructions,omitempty"`
	Input             []any          `json:"input"`
	Tools             []wireTool     `json:"tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	Include           []string       `json:"include"`
	Reasoning         map[string]any `json:"reasoning,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	ClientMetadata    map[string]any `json:"client_metadata,omitempty"`
}

type wireTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type builtRequest struct {
	body       wireRequest
	codec      *openaitoolname.Codec
	modelID    string
	modelClass model.ModelClass
	lite       bool
	sseBody    []byte
	wsBody     []byte
}
type partPosition struct {
	message int
	part    int
}

type transcriptPairing struct {
	matchedCalls   map[partPosition]bool
	matchedResults map[partPosition]bool
}

func (b *builtRequest) marshal(webSocket bool) ([]byte, error) {
	body := b.body
	if webSocket && b.lite {
		body.ClientMetadata = map[string]any{responsesLiteMetadata: "true"}
	}
	return json.Marshal(body)
}

func (b *builtRequest) prepare(transport Transport) error {
	if transport != TransportWebSocket {
		payload, err := b.marshal(false)
		if err != nil {
			return fmt.Errorf("codex: encode SSE request: %w", err)
		}
		b.sseBody = payload
	}
	if transport == TransportSSE {
		return nil
	}
	payload, err := b.marshal(true)
	if err != nil {
		return fmt.Errorf("codex: encode WebSocket request: %w", err)
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("codex: encode WebSocket request envelope: %w", err)
	}
	request[wireType] = "response.create"
	b.wsBody, err = json.Marshal(request)
	if err != nil {
		return fmt.Errorf("codex: encode WebSocket request envelope: %w", err)
	}
	return nil
}

func (c *Client) buildRequest(request *model.Request) (*builtRequest, error) { //nolint:maintidx // Validation and translation remain one atomic pre-I/O operation.
	if request == nil {
		return nil, errors.New("codex: request is required")
	}
	if len(request.Messages) == 0 {
		return nil, errors.New("codex: messages are required")
	}
	if request.Temperature != 0 {
		return nil, errors.New("codex: temperature is not supported")
	}
	if request.MaxTokens > 0 {
		return nil, errors.New("codex: max tokens is not supported")
	}
	if request.StructuredOutput != nil {
		return nil, fmt.Errorf("codex: %w", model.ErrStructuredOutputUnsupported)
	}
	if request.Cache != nil {
		return nil, errors.New("codex: cache options are not supported")
	}
	if request.Thinking != nil {
		if request.Thinking.BudgetTokens > 0 {
			return nil, errors.New("codex: numeric thinking budgets are not supported")
		}
		if request.Thinking.Interleaved {
			return nil, errors.New("codex: explicit interleaved thinking is not supported")
		}
	}

	tools, codec, err := encodeCodexTools(request.Tools)
	if err != nil {
		return nil, err
	}
	instructions, input, err := encodeCodexMessages(request.Messages, codec)
	if err != nil {
		return nil, err
	}
	choice, err := encodeCodexToolChoice(request.ToolChoice, request.Tools, codec)
	if err != nil {
		return nil, err
	}
	body := wireRequest{
		Model:        c.resolveModelID(request),
		Store:        false,
		Stream:       true,
		Instructions: instructions,
		Input:        input,
		Tools:        tools,
		ToolChoice:   choice,
		Include:      []string{"reasoning.encrypted_content"},
	}
	if request.Thinking != nil && request.Thinking.Enable {
		body.Reasoning = map[string]any{wireSummary: wireAuto}
	}
	if c.lite {
		applyResponsesLite(&body)
	}
	return &builtRequest{
		body:       body,
		codec:      codec,
		modelID:    body.Model,
		modelClass: request.ModelClass,
		lite:       c.lite,
	}, nil
}

func encodeCodexTools(definitions []*model.ToolDefinition) ([]wireTool, *openaitoolname.Codec, error) {
	codec := openaitoolname.New(len(definitions))
	tools := make([]wireTool, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			return nil, nil, errors.New("codex: nil tool definition")
		}
		if _, exists := seen[definition.Name]; exists {
			return nil, nil, fmt.Errorf("codex: duplicate tool name %q", definition.Name)
		}
		seen[definition.Name] = struct{}{}
		if strings.TrimSpace(definition.Name) == "" {
			return nil, nil, errors.New("codex: tool name is required")
		}
		wireName, err := codec.Register(definition.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("codex: %w", err)
		}
		schema, err := encodeObjectSchema(definition.Name, definition.InputSchema)
		if err != nil {
			return nil, nil, err
		}
		tools = append(tools, wireTool{
			Type:        wireFunction,
			Name:        wireName,
			Description: definition.Description,
			Parameters:  schema,
		})
	}
	return tools, codec, nil
}

func encodeObjectSchema(name string, schema any) (map[string]any, error) {
	if schema == nil {
		return nil, fmt.Errorf("codex: tool %q requires an object input schema", name)
	}
	encoded, err := marshalJSONValue(schema)
	if err != nil {
		return nil, fmt.Errorf("codex: encode tool %q schema: %w", name, err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return nil, fmt.Errorf("codex: tool %q requires an object input schema", name)
	}
	if kind, ok := object[wireType]; ok && kind != wireObject {
		return nil, fmt.Errorf("codex: tool %q requires an object input schema", name)
	}
	if _, ok := object[wireType]; !ok {
		object[wireType] = wireObject
	}
	return object, nil
}

func encodeCodexMessages(messages []*model.Message, codec *openaitoolname.Codec) (string, []any, error) {
	pairing, err := pairTranscriptTools(messages, codec)
	if err != nil {
		return "", nil, err
	}

	var instructions, continuation string
	developers := make([]any, 0)
	input := make([]any, 0, len(messages))
	for messageIndex, message := range messages {
		if message.Role == model.ConversationRoleSystem {
			text, err := encodeSystemText(message)
			if err != nil {
				return "", nil, err
			}
			continuation = text
			if instructions == "" {
				instructions = text
			} else {
				developers = append(developers, wireMessage("developer", []any{wireText("input_text", text)}))
			}
			continue
		}
		items, err := encodeCodexMessage(message, messageIndex, codec, pairing)
		if err != nil {
			return "", nil, err
		}
		input = append(input, items...)
	}
	if len(input) == 0 {
		input = append(input, wireMessage("user", []any{wireText("input_text", continuation)}))
	}
	return instructions, append(developers, input...), nil
}

func pairTranscriptTools(messages []*model.Message, codec *openaitoolname.Codec) (transcriptPairing, error) {
	pairing := transcriptPairing{
		matchedCalls:   make(map[partPosition]bool),
		matchedResults: make(map[partPosition]bool),
	}
	pending := make(map[string][]partPosition)
	for messageIndex, message := range messages {
		if message == nil {
			return transcriptPairing{}, errors.New("codex: nil message")
		}
		for partIndex, part := range message.Parts {
			position := partPosition{message: messageIndex, part: partIndex}
			switch value := part.(type) {
			case model.ToolUsePart:
				if message.Role != model.ConversationRoleAssistant {
					return transcriptPairing{}, errors.New("codex: tool use is supported only in assistant messages")
				}
				if value.Name != "" {
					if _, err := codec.Register(value.Name); err != nil {
						return transcriptPairing{}, fmt.Errorf("codex: %w", err)
					}
				}
				if value.ID != "" {
					pending[value.ID] = append(pending[value.ID], position)
				}
			case model.ToolResultPart:
				if message.Role != model.ConversationRoleUser {
					return transcriptPairing{}, errors.New("codex: tool result is supported only in user messages")
				}
				calls := pending[value.ToolUseID]
				if len(calls) == 0 {
					continue
				}
				call := calls[0]
				pending[value.ToolUseID] = calls[1:]
				pairing.matchedCalls[call] = true
				pairing.matchedResults[position] = true
			}
		}
	}
	return pairing, nil
}

func encodeSystemText(message *model.Message) (string, error) {
	var text strings.Builder
	for _, part := range message.Parts {
		value, ok := part.(model.TextPart)
		if !ok || value.Text == "" {
			return "", fmt.Errorf("codex: unsupported system message part %T", part)
		}
		text.WriteString(value.Text)
	}
	if text.Len() == 0 {
		return "", errors.New("codex: system message is empty")
	}
	return text.String(), nil
}

func encodeCodexMessage(message *model.Message, messageIndex int, codec *openaitoolname.Codec, pairing transcriptPairing) ([]any, error) { //nolint:maintidx // Transcript repair requires one ordered part walk.
	if message.Role != model.ConversationRoleUser && message.Role != model.ConversationRoleAssistant {
		return nil, fmt.Errorf("codex: unsupported message role %q", message.Role)
	}
	items := make([]any, 0, len(message.Parts))
	content := make([]any, 0, len(message.Parts))
	flush := func() {
		if len(content) == 0 {
			return
		}
		items = append(items, wireMessage(string(message.Role), content))
		content = nil
	}
	for partIndex, part := range message.Parts {
		switch value := part.(type) {
		case model.TextPart:
			if value.Text == "" {
				return nil, errors.New("codex: empty text part")
			}
			kind := "input_text"
			if message.Role == model.ConversationRoleAssistant {
				kind = wireOutputText
			}
			content = append(content, wireText(kind, value.Text))
		case model.ImagePart:
			if message.Role != model.ConversationRoleUser {
				return nil, errors.New("codex: images are supported only in user messages")
			}
			image, err := encodeCodexImage(value)
			if err != nil {
				return nil, err
			}
			content = append(content, image)
		case model.ToolUsePart:
			flush()
			call, err := encodeCodexToolUse(value, codec)
			if err != nil {
				return nil, err
			}
			items = append(items, call)
			if !pairing.matchedCalls[partPosition{message: messageIndex, part: partIndex}] {
				items = append(items, map[string]any{
					wireType: "function_call_output", wireCallID: value.ID, "output": interruptedToolOutput,
				})
			}
		case model.ToolResultPart:
			flush()
			result, err := encodeCodexToolResult(value)
			if err != nil {
				return nil, err
			}
			if pairing.matchedResults[partPosition{message: messageIndex, part: partIndex}] {
				items = append(items, result)
			} else {
				items = append(items, orphanToolResultMessage(value, result["output"]))
			}
		case model.ThinkingPart:
			flush()
			if message.Role != model.ConversationRoleAssistant || len(value.Redacted) == 0 {
				return nil, errors.New("codex: only encrypted assistant thinking is replayable")
			}
			items = append(items, map[string]any{
				wireType: wireReasoningItem, "encrypted_content": string(value.Redacted), wireSummary: []any{},
			})
		case model.DocumentPart:
			return nil, errors.New("codex: documents are not supported")
		case model.CitationsPart:
			return nil, errors.New("codex: citations are not supported")
		case model.CacheCheckpointPart:
			return nil, errors.New("codex: cache checkpoints are not supported")
		default:
			return nil, fmt.Errorf("codex: unsupported message part %T", part)
		}
	}
	flush()
	if len(items) == 0 {
		return nil, errors.New("codex: message is empty")
	}
	return items, nil
}

func encodeCodexImage(image model.ImagePart) (map[string]any, error) {
	if len(image.Bytes) == 0 {
		return nil, errors.New("codex: image bytes are required")
	}
	var mime string
	switch image.Format {
	case model.ImageFormatPNG:
		mime = "image/png"
	case model.ImageFormatJPEG:
		mime = "image/jpeg"
	case model.ImageFormatGIF:
		mime = "image/gif"
	case model.ImageFormatWEBP:
		mime = "image/webp"
	default:
		return nil, fmt.Errorf("codex: unsupported image format %q", image.Format)
	}
	return map[string]any{
		wireType:    "input_image",
		"image_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image.Bytes),
		"detail":    wireAuto,
	}, nil
}

func encodeCodexToolUse(part model.ToolUsePart, codec *openaitoolname.Codec) (map[string]any, error) {
	if part.ID == "" || part.Name == "" {
		return nil, errors.New("codex: tool use requires id and name")
	}
	arguments, err := marshalJSONValue(part.Input)
	if err != nil || !jsonObject(arguments) {
		return nil, fmt.Errorf("codex: tool call %q requires valid object arguments", part.ID)
	}
	return map[string]any{
		wireType: wireFunctionCallItem, wireCallID: part.ID, wireName: codec.WireName(part.Name), "arguments": string(arguments),
	}, nil
}

func encodeCodexToolResult(part model.ToolResultPart) (map[string]any, error) {
	if part.ToolUseID == "" {
		return nil, errors.New("codex: tool result requires tool use id")
	}
	output, err := marshalJSONValue(part.Content)
	if err != nil {
		return nil, fmt.Errorf("codex: encode tool result %q: %w", part.ToolUseID, err)
	}
	if !jsontext.Value(output).IsValid() {
		return nil, fmt.Errorf("codex: tool result %q contains invalid JSON", part.ToolUseID)
	}
	return map[string]any{wireType: "function_call_output", wireCallID: part.ToolUseID, "output": string(output)}, nil
}

func orphanToolResultMessage(part model.ToolResultPart, output any) map[string]any {
	text := fmt.Sprint(output)
	if len(text) > maxOrphanOutputBytes {
		cut := maxOrphanOutputBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "\n...[truncated]"
	}
	return wireMessage("assistant", []any{wireText(wireOutputText, fmt.Sprintf("[Previous tool result; call_id=%s]: %s", part.ToolUseID, text))})
}

func encodeCodexToolChoice(choice *model.ToolChoice, definitions []*model.ToolDefinition, codec *openaitoolname.Codec) (any, error) {
	if choice == nil || choice.Mode == "" || choice.Mode == model.ToolChoiceModeAuto {
		return wireAuto, nil
	}
	switch choice.Mode {
	case model.ToolChoiceModeAuto:
		return wireAuto, nil
	case model.ToolChoiceModeNone:
		return "none", nil
	case model.ToolChoiceModeAny:
		return wireRequired, nil
	case model.ToolChoiceModeTool:
		if choice.Name == "" {
			return nil, errors.New("codex: named tool choice requires a name")
		}
		for _, definition := range definitions {
			if definition != nil && definition.Name == choice.Name {
				return map[string]any{wireType: wireFunction, wireName: codec.WireName(choice.Name)}, nil
			}
		}
		return nil, fmt.Errorf("codex: tool choice name %q does not match any tool", choice.Name)
	default:
		return nil, fmt.Errorf("codex: unsupported tool choice mode %q", choice.Mode)
	}
}

func applyResponsesLite(body *wireRequest) {
	additional := body.Tools
	if choice, ok := body.ToolChoice.(map[string]any); ok && choice[wireType] == wireFunction {
		name, _ := choice[wireName].(string)
		for _, tool := range body.Tools {
			if tool.Name == name {
				additional = []wireTool{tool}
				body.ToolChoice = wireRequired
				break
			}
		}
	}
	prefix := []any{map[string]any{wireType: "additional_tools", "role": "developer", "tools": additional}}
	if body.Instructions != "" {
		prefix = append(prefix, wireMessage("developer", []any{wireText("input_text", body.Instructions)}))
	}
	stripImageDetail(body.Input)
	body.Input = append(prefix, body.Input...)
	if body.ToolChoice != "none" && body.ToolChoice != wireRequired {
		body.ToolChoice = wireAuto
	}
	body.Instructions = ""
	body.Tools = nil
	parallel := false
	body.ParallelToolCalls = &parallel
	if body.Reasoning == nil {
		body.Reasoning = make(map[string]any)
	}
	body.Reasoning["context"] = "all_turns"
}

func stripImageDetail(items []any) {
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content, _ := object[wireContentKey].([]any)
		for _, part := range content {
			if image, ok := part.(map[string]any); ok && image[wireType] == "input_image" {
				delete(image, "detail")
			}
		}
	}
}

func wireMessage(role string, content []any) map[string]any {
	return map[string]any{wireType: wireMessageItem, "role": role, wireContentKey: content}
}

func wireText(kind, text string) map[string]any {
	return map[string]any{wireType: kind, "text": text}
}

func marshalJSONValue(value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		return []byte("null"), nil
	case rawjson.Message:
		if len(typed) == 0 {
			return []byte("null"), nil
		}
		return typed.RawMessage(), nil
	case jsontext.Value:
		if len(typed) == 0 {
			return []byte("null"), nil
		}
		return typed, nil
	case []byte:
		if len(typed) == 0 {
			return []byte("null"), nil
		}
		return typed, nil
	default:
		return json.Marshal(value)
	}
}

func jsonObject(data []byte) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	var object map[string]any
	return json.Unmarshal(data, &object) == nil && object != nil
}
