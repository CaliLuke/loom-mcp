package model

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

const (
	maxModelValueDepth  = 64
	maxModelValueVisits = 100_000
)

type cloneBudget struct {
	bytes  int
	visits int
	active map[cloneContainer]struct{}
}

type cloneContainer struct {
	typeName reflect.Type
	pointer  uintptr
}

type modelBoundsError struct {
	cause error
}

// CloneRequest returns a bounded, ownership-safe copy of a model request.
func CloneRequest(request *Request) (*Request, error) {
	return cloneModelRequest(request)
}

// CloneResponse returns a bounded, ownership-safe copy of a model response.
func CloneResponse(response *Response) (*Response, error) {
	return cloneModelResponse(response)
}

// CloneToolDefinitions returns a bounded, ownership-safe copy of tool
// definitions and their input schemas.
func CloneToolDefinitions(definitions []*ToolDefinition) ([]*ToolDefinition, error) {
	budget := &cloneBudget{active: make(map[cloneContainer]struct{})}
	if err := budget.reserve(len(definitions)); err != nil {
		return nil, err
	}
	return cloneModelRequestTools(definitions, budget)
}

func (e *modelBoundsError) Error() string {
	return e.cause.Error()
}

func (e *modelBoundsError) Unwrap() error {
	return e.cause
}

func cloneModelRequest(request *Request) (*Request, error) {
	if request == nil {
		return nil, errors.New("model request is required")
	}
	budget := &cloneBudget{active: make(map[cloneContainer]struct{})}
	owned := *request
	if err := budget.addBytes(len(request.RunID) + len(request.Model) + len(request.ModelClass)); err != nil {
		return nil, err
	}
	if err := budget.reserve(len(request.PromptRefs) + len(request.Messages) + len(request.Tools)); err != nil {
		return nil, err
	}
	var err error
	owned.PromptRefs, err = cloneModelPromptRefs(request.PromptRefs, budget)
	if err != nil {
		return nil, err
	}
	owned.Messages, err = cloneModelRequestMessages(request.Messages, budget)
	if err != nil {
		return nil, err
	}
	owned.Tools, err = cloneModelRequestTools(request.Tools, budget)
	if err != nil {
		return nil, err
	}
	if err := cloneModelRequestOptions(request, &owned, budget); err != nil {
		return nil, err
	}
	return &owned, nil
}

func cloneModelPromptRefs(refs []prompt.PromptRef, budget *cloneBudget) ([]prompt.PromptRef, error) {
	owned := slices.Clone(refs)
	for _, ref := range refs {
		if err := budget.visit(); err != nil {
			return nil, err
		}
		if err := budget.addBytes(len(ref.ID) + len(ref.Version)); err != nil {
			return nil, err
		}
	}
	return owned, nil
}

func cloneModelRequestMessages(messages []*Message, budget *cloneBudget) ([]*Message, error) {
	owned := make([]*Message, len(messages))
	for index, message := range messages {
		if message == nil {
			return nil, fmt.Errorf("model request message %d is nil", index)
		}
		cloned, err := cloneModelMessage(*message, budget)
		if err != nil {
			return nil, fmt.Errorf("model request message %d: %w", index, err)
		}
		owned[index] = &cloned
	}
	return owned, nil
}

func cloneModelRequestTools(tools []*ToolDefinition, budget *cloneBudget) ([]*ToolDefinition, error) {
	owned := make([]*ToolDefinition, len(tools))
	for index, definition := range tools {
		if err := budget.visit(); err != nil {
			return nil, err
		}
		if definition == nil {
			return nil, fmt.Errorf("model request tool %d is nil", index)
		}
		cloned := *definition
		if err := budget.addBytes(len(definition.Name) + len(definition.Description)); err != nil {
			return nil, fmt.Errorf("model request tool %d: %w", index, err)
		}
		input, err := cloneModelDynamicValue(definition.InputSchema, budget)
		if err != nil {
			return nil, fmt.Errorf("model request tool %d schema: %w", index, err)
		}
		cloned.InputSchema = input
		owned[index] = &cloned
	}
	return owned, nil
}

func cloneModelRequestOptions(request *Request, owned *Request, budget *cloneBudget) error {
	if request.ToolChoice != nil {
		choice := *request.ToolChoice
		if err := budget.addBytes(len(choice.Mode) + len(choice.Name)); err != nil {
			return fmt.Errorf("model request tool choice: %w", err)
		}
		owned.ToolChoice = &choice
	}
	if request.StructuredOutput != nil {
		output := *request.StructuredOutput
		output.Schema = slices.Clone(request.StructuredOutput.Schema)
		if err := budget.addBytes(len(output.Name) + len(output.Schema)); err != nil {
			return fmt.Errorf("model request structured output: %w", err)
		}
		owned.StructuredOutput = &output
	}
	if request.Thinking != nil {
		thinking := *request.Thinking
		owned.Thinking = &thinking
	}
	if request.Cache != nil {
		cache := *request.Cache
		owned.Cache = &cache
	}
	return nil
}

func cloneModelResponse(response *Response) (*Response, error) {
	if response == nil {
		return nil, nil
	}
	budget := &cloneBudget{active: make(map[cloneContainer]struct{})}
	owned := *response
	if err := budget.addBytes(len(response.StopReason) + len(response.Usage.Model) + len(response.Usage.ModelClass)); err != nil {
		return nil, err
	}
	if err := budget.reserve(len(response.Content) + len(response.ToolCalls)); err != nil {
		return nil, err
	}
	owned.Content = make([]Message, len(response.Content))
	for index, message := range response.Content {
		cloned, err := cloneModelMessage(message, budget)
		if err != nil {
			return nil, fmt.Errorf("model response message %d: %w", index, err)
		}
		owned.Content[index] = cloned
	}
	owned.ToolCalls = slices.Clone(response.ToolCalls)
	for index := range owned.ToolCalls {
		if err := budget.visit(); err != nil {
			return nil, err
		}
		if err := budget.addBytes(len(response.ToolCalls[index].Name) + len(response.ToolCalls[index].ID) + len(response.ToolCalls[index].Payload)); err != nil {
			return nil, fmt.Errorf("model response tool call %d: %w", index, err)
		}
		owned.ToolCalls[index].Payload = slices.Clone(response.ToolCalls[index].Payload)
	}
	return &owned, nil
}

func cloneModelChunk(chunk Chunk, budget *cloneBudget) (Chunk, error) {
	switch value := chunk.(type) {
	case TextChunk:
		message, err := cloneModelMessage(value.Message, budget)
		return TextChunk{Message: message}, err
	case ThinkingChunk:
		message, err := cloneModelMessage(value.Message, budget)
		return ThinkingChunk{Message: message}, err
	case ToolCallChunk:
		call := value.ToolCall
		if err := budget.addBytes(len(call.Name) + len(call.ID) + len(call.Payload)); err != nil {
			return nil, err
		}
		call.Payload = slices.Clone(call.Payload)
		return ToolCallChunk{ToolCall: call}, nil
	case ToolCallDeltaChunk:
		delta := value.Delta
		if err := budget.addBytes(len(delta.Name) + len(delta.ID) + len(delta.Delta)); err != nil {
			return nil, err
		}
		return ToolCallDeltaChunk{Delta: delta}, nil
	case CompletionChunk:
		completion := value.Completion
		if err := budget.addBytes(len(completion.Name) + len(completion.Payload)); err != nil {
			return nil, err
		}
		completion.Payload = slices.Clone(completion.Payload)
		return CompletionChunk{Completion: completion}, nil
	case CompletionDeltaChunk:
		delta := value.Delta
		if err := budget.addBytes(len(delta.Name) + len(delta.Delta)); err != nil {
			return nil, err
		}
		return CompletionDeltaChunk{Delta: delta}, nil
	case UsageChunk:
		return value, nil
	case StopChunk:
		if err := budget.addBytes(len(value.Reason)); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return nil, errors.New("model stream emitted an unsupported chunk variant")
	}
}

func cloneModelMessage(message Message, budget *cloneBudget) (Message, error) {
	if err := budget.visit(); err != nil {
		return Message{}, err
	}
	if err := budget.addBytes(len(message.Role)); err != nil {
		return Message{}, err
	}
	owned := message
	if err := budget.reserve(len(message.Parts)); err != nil {
		return Message{}, err
	}
	owned.Parts = make([]Part, len(message.Parts))
	for index, part := range message.Parts {
		cloned, err := cloneModelPart(part, budget)
		if err != nil {
			return Message{}, fmt.Errorf("part %d: %w", index, err)
		}
		owned.Parts[index] = cloned
	}
	if message.Meta != nil {
		meta, err := cloneModelDynamicValue(message.Meta, budget)
		if err != nil {
			return Message{}, fmt.Errorf("metadata: %w", err)
		}
		owned.Meta = meta.(map[string]any)
	}
	return owned, nil
}

func cloneModelPart(part Part, budget *cloneBudget) (Part, error) { //nolint:maintidx // One exhaustive ownership switch keeps all public Part variants consistent.
	if err := budget.visit(); err != nil {
		return nil, err
	}
	normalized, err := normalizeMessagePart(part)
	if err != nil {
		return nil, err
	}
	switch actual := normalized.(type) {
	case TextPart:
		return actual, budget.addBytes(len(actual.Text))
	case ImagePart:
		if err := validateImagePart(actual); err != nil {
			return nil, err
		}
		if err := budget.addBytes(len(actual.Format) + len(actual.Bytes)); err != nil {
			return nil, err
		}
		actual.Bytes = slices.Clone(actual.Bytes)
		return actual, nil
	case DocumentPart:
		if err := validateDocumentPart(actual); err != nil {
			return nil, err
		}
		return cloneModelDocumentPart(actual, budget)
	case CitationsPart:
		return cloneModelCitationsPart(actual, budget)
	case ThinkingPart:
		if err := budget.addBytes(len(actual.Text) + len(actual.Signature) + len(actual.Redacted)); err != nil {
			return nil, err
		}
		actual.Redacted = slices.Clone(actual.Redacted)
		return actual, nil
	case ToolUsePart:
		if err := validateToolUsePart(actual); err != nil {
			return nil, err
		}
		input, err := cloneModelDynamicValue(actual.Input, budget)
		if err != nil {
			return nil, err
		}
		actual.Input = input
		return actual, budget.addBytes(len(actual.ID) + len(actual.Name))
	case ToolResultPart:
		if err := validateToolResultPart(actual); err != nil {
			return nil, err
		}
		content, err := cloneModelDynamicValue(actual.Content, budget)
		if err != nil {
			return nil, err
		}
		actual.Content = content
		return actual, budget.addBytes(len(actual.ToolUseID))
	case CacheCheckpointPart:
		return actual, nil
	case nil:
		return nil, errors.New("message part is nil")
	default:
		return nil, fmt.Errorf("unsupported message part type %T", part)
	}
}

func cloneModelDocumentPart(part DocumentPart, budget *cloneBudget) (Part, error) {
	if err := budget.addBytes(len(part.Name) + len(part.Format) + len(part.Text) + len(part.URI) + len(part.Context) + len(part.Bytes)); err != nil {
		return nil, err
	}
	if err := budget.reserve(len(part.Chunks)); err != nil {
		return nil, err
	}
	for _, chunk := range part.Chunks {
		if err := budget.visit(); err != nil {
			return nil, err
		}
		if err := budget.addBytes(len(chunk)); err != nil {
			return nil, err
		}
	}
	part.Bytes = slices.Clone(part.Bytes)
	part.Chunks = slices.Clone(part.Chunks)
	return part, nil
}

func cloneModelCitationsPart(part CitationsPart, budget *cloneBudget) (Part, error) {
	if err := budget.addBytes(len(part.Text)); err != nil {
		return nil, err
	}
	if err := budget.reserve(len(part.Citations)); err != nil {
		return nil, err
	}
	part.Citations = slices.Clone(part.Citations)
	for index := range part.Citations {
		if err := cloneModelCitation(&part.Citations[index], budget); err != nil {
			return nil, err
		}
	}
	return part, nil
}

func cloneModelCitation(citation *Citation, budget *cloneBudget) error {
	if err := budget.visit(); err != nil {
		return err
	}
	if err := budget.addBytes(len(citation.Title) + len(citation.Source)); err != nil {
		return err
	}
	if err := budget.reserve(len(citation.SourceContent)); err != nil {
		return err
	}
	for _, content := range citation.SourceContent {
		if err := budget.visit(); err != nil {
			return err
		}
		if err := budget.addBytes(len(content)); err != nil {
			return err
		}
	}
	citation.SourceContent = slices.Clone(citation.SourceContent)
	citation.Location = cloneCitationLocation(citation.Location)
	return nil
}

func cloneCitationLocation(location CitationLocation) CitationLocation {
	owned := location
	if location.DocumentChar != nil {
		value := *location.DocumentChar
		owned.DocumentChar = &value
	}
	if location.DocumentChunk != nil {
		value := *location.DocumentChunk
		owned.DocumentChunk = &value
	}
	if location.DocumentPage != nil {
		value := *location.DocumentPage
		owned.DocumentPage = &value
	}
	return owned
}

func cloneModelDynamicValue(value any, budget *cloneBudget) (any, error) {
	if value == nil {
		return nil, budget.visit()
	}
	cloned, err := cloneModelDynamicReflect(reflect.ValueOf(value), 0, budget)
	if err != nil {
		return nil, err
	}
	return cloned.Interface(), nil
}

func cloneModelDynamicReflect(value reflect.Value, depth int, budget *cloneBudget) (reflect.Value, error) { //nolint:maintidx // Exhaustive reflect-kind handling is the JSON-compatible value boundary.
	if depth > maxModelValueDepth {
		return reflect.Value{}, &modelBoundsError{cause: fmt.Errorf("value exceeds maximum depth %d", maxModelValueDepth)}
	}
	if err := budget.visit(); err != nil {
		return reflect.Value{}, err
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		cloned, err := cloneModelDynamicReflect(value.Elem(), depth, budget)
		if err != nil {
			return reflect.Value{}, err
		}
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result, nil
	}
	container, tracked, err := budget.enter(value)
	if err != nil {
		return reflect.Value{}, err
	}
	if tracked {
		defer delete(budget.active, container)
	}
	switch value.Kind() {
	case reflect.Map:
		return cloneModelDynamicMap(value, depth, budget)
	case reflect.Slice:
		return cloneModelDynamicSlice(value, depth, budget)
	case reflect.Array:
		return cloneModelDynamicArray(value, depth, budget)
	case reflect.String:
		return value, budget.addBytes(value.Len())
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) {
			return reflect.Value{}, errors.New("non-finite number is not JSON-compatible")
		}
		return value, nil
	case reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64:
		return value, nil
	case reflect.Invalid:
		return reflect.Value{}, errors.New("invalid value is not JSON-compatible")
	case reflect.Uintptr,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Pointer,
		reflect.Struct,
		reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("value type %s is not JSON-compatible", value.Type())
	}
	panic("unreachable reflect kind")
}

func cloneModelDynamicMap(value reflect.Value, depth int, budget *cloneBudget) (reflect.Value, error) {
	if value.Type().Key().Kind() != reflect.String {
		return reflect.Value{}, fmt.Errorf("map key type %s is not a string", value.Type().Key())
	}
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	if err := budget.reserve(value.Len()); err != nil {
		return reflect.Value{}, err
	}
	owned := reflect.MakeMapWithSize(value.Type(), value.Len())
	iterator := value.MapRange()
	for iterator.Next() {
		key := iterator.Key()
		if err := budget.addBytes(len(key.String())); err != nil {
			return reflect.Value{}, err
		}
		cloned, err := cloneModelDynamicReflect(iterator.Value(), depth+1, budget)
		if err != nil {
			return reflect.Value{}, err
		}
		owned.SetMapIndex(key, cloned)
	}
	return owned, nil
}

func cloneModelDynamicSlice(value reflect.Value, depth int, budget *cloneBudget) (reflect.Value, error) {
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	if value.Type().Elem().Kind() == reflect.Uint8 {
		if err := budget.addBytes(value.Len()); err != nil {
			return reflect.Value{}, err
		}
		owned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(owned, value)
		return owned, nil
	}
	if err := budget.reserve(value.Len()); err != nil {
		return reflect.Value{}, err
	}
	owned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	if err := cloneModelDynamicElements(value, owned, depth, budget); err != nil {
		return reflect.Value{}, err
	}
	return owned, nil
}

func cloneModelDynamicArray(value reflect.Value, depth int, budget *cloneBudget) (reflect.Value, error) {
	if err := budget.reserve(value.Len()); err != nil {
		return reflect.Value{}, err
	}
	owned := reflect.New(value.Type()).Elem()
	if err := cloneModelDynamicElements(value, owned, depth, budget); err != nil {
		return reflect.Value{}, err
	}
	return owned, nil
}

func cloneModelDynamicElements(value, owned reflect.Value, depth int, budget *cloneBudget) error {
	for index := 0; index < value.Len(); index++ {
		cloned, err := cloneModelDynamicReflect(value.Index(index), depth+1, budget)
		if err != nil {
			return err
		}
		owned.Index(index).Set(cloned)
	}
	return nil
}

func (b *cloneBudget) visit() error {
	b.visits++
	if b.visits > maxModelValueVisits {
		return &modelBoundsError{cause: fmt.Errorf("value exceeds maximum visited values %d", maxModelValueVisits)}
	}
	return nil
}

func (b *cloneBudget) reserve(count int) error {
	if count < 0 || count > maxModelValueVisits-b.visits {
		return &modelBoundsError{cause: fmt.Errorf("value exceeds maximum visited values %d", maxModelValueVisits)}
	}
	return nil
}

func (b *cloneBudget) addBytes(count int) error {
	if count < 0 || b.bytes > maxModelOutputBytes-count {
		return &modelBoundsError{cause: fmt.Errorf("value exceeds maximum size %d bytes", maxModelOutputBytes)}
	}
	b.bytes += count
	return nil
}

func (b *cloneBudget) enter(value reflect.Value) (cloneContainer, bool, error) {
	if value.Kind() != reflect.Map && value.Kind() != reflect.Slice {
		return cloneContainer{}, false, nil
	}
	if value.IsNil() {
		return cloneContainer{}, false, nil
	}
	container := cloneContainer{typeName: value.Type(), pointer: value.Pointer()}
	if _, exists := b.active[container]; exists {
		return cloneContainer{}, false, errors.New("cyclic value is not JSON-compatible")
	}
	b.active[container] = struct{}{}
	return container, true, nil
}
