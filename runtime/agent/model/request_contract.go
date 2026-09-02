package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxModelOutputBytes = 16 << 20

	// MaxToolDefinitionsPerRequest is the largest exact tool catalog accepted by
	// the model contract and durable recovery boundary.
	MaxToolDefinitionsPerRequest = 256
)

type (
	// RequestContract is an immutable snapshot of the request fields that decide
	// whether provider output can be accepted.
	RequestContract struct {
		tools              map[string]*jsonschema.Schema
		model              string
		modelClass         ModelClass
		toolChoiceMode     ToolChoiceMode
		toolChoiceName     string
		structuredName     string
		structuredValidate *jsonschema.Schema
		completionValidate func(*Response, *Completion) error
	}

	validatedProviderStream struct {
		inner         Streamer
		contract      *RequestContract
		ctx           context.Context
		budget        cloneBudget
		evidence      hash.Hash
		evidenceBytes int

		mu                sync.Mutex
		content           []Message
		toolCalls         []ToolCall
		toolDeltaNames    map[string]string
		toolDeltaPayloads map[string]*strings.Builder
		finalToolCallIDs  map[string]struct{}
		completion        *Completion
		usage             TokenUsage
		pending           []Chunk
		stopped           bool
		stopReason        string
		outputLimited     bool
		terminal          bool
		consumerEOF       bool
		terminalErr       error
		response          *Response

		closeOnce sync.Once
		closeErr  error

		finalized   bool
		finalizeErr error
	}
)

// NewRequestContract validates req and snapshots the exact tool and structured
// output contracts used to accept provider output.
func NewRequestContract(req *Request) (*RequestContract, error) {
	request, err := cloneModelRequest(req)
	if err != nil {
		return nil, err
	}
	return newRequestContract(request)
}

func newRequestContract(req *Request) (*RequestContract, error) {
	if err := validateModelRequestConfiguration(req); err != nil {
		return nil, err
	}
	tools, err := buildToolContracts(req.Tools)
	if err != nil {
		return nil, err
	}
	contract := &RequestContract{tools: tools, model: req.Model, modelClass: req.ModelClass}
	if req.ToolChoice != nil {
		contract.toolChoiceMode = req.ToolChoice.Mode
		contract.toolChoiceName = req.ToolChoice.Name
		if err := contract.validateToolChoiceRequest(); err != nil {
			return nil, err
		}
	}
	if err := contract.captureStructuredOutput(req); err != nil {
		return nil, err
	}
	return contract, nil
}

func validateModelRequestConfiguration(req *Request) error {
	if req.MaxTokens < 0 {
		return errors.New("model request max tokens must be non-negative")
	}
	if math.IsNaN(float64(req.Temperature)) || math.IsInf(float64(req.Temperature), 0) {
		return errors.New("model request temperature must be finite")
	}
	if req.Thinking != nil && req.Thinking.BudgetTokens < 0 {
		return errors.New("model request thinking budget must be non-negative")
	}
	for index, message := range req.Messages {
		switch message.Role {
		case ConversationRoleSystem, ConversationRoleUser, ConversationRoleAssistant:
		default:
			return fmt.Errorf("model request message %d role %q is invalid", index, message.Role)
		}
	}
	return nil
}

func buildToolContracts(definitions []*ToolDefinition) (map[string]*jsonschema.Schema, error) {
	if len(definitions) > MaxToolDefinitionsPerRequest {
		return nil, fmt.Errorf("model request has %d tools; limit is %d", len(definitions), MaxToolDefinitionsPerRequest)
	}
	tools := make(map[string]*jsonschema.Schema, len(definitions))
	for index, definition := range definitions {
		if definition == nil {
			return nil, fmt.Errorf("model request tool %d is nil", index)
		}
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			return nil, fmt.Errorf("model request tool %d name is required", index)
		}
		if name != definition.Name {
			return nil, fmt.Errorf("model request tool %d name must not contain surrounding whitespace", index)
		}
		if _, exists := tools[name]; exists {
			return nil, fmt.Errorf("model request tool name %q is duplicated", name)
		}
		schema, err := compileModelSchema(definition.InputSchema, fmt.Sprintf("tool %q input", name))
		if err != nil {
			return nil, err
		}
		tools[name] = schema
	}
	return tools, nil
}

func (c *RequestContract) captureStructuredOutput(req *Request) error {
	if req.StructuredOutput == nil {
		return nil
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return errors.New("model request cannot combine structured output with tools")
	}
	name := strings.TrimSpace(req.StructuredOutput.Name)
	if name == "" {
		return errors.New("model request structured output name is required")
	}
	if name != req.StructuredOutput.Name {
		return errors.New("model request structured output name must not contain surrounding whitespace")
	}
	schema, err := compileModelSchema(req.StructuredOutput.Schema, "structured output")
	if err != nil {
		return err
	}
	c.structuredName = req.StructuredOutput.Name
	c.structuredValidate = schema
	c.completionValidate = req.completionValidate
	return nil
}

// ValidateResponse validates a complete provider response against the captured
// request contract and returns a detached response on success.
func (c *RequestContract) ValidateResponse(resp *Response) (*Response, error) {
	evidence := ResponseEvidence{Present: resp != nil}
	if resp == nil {
		return nil, newOutputValidationError(OutputValidationResponseShape, errors.New("provider returned a nil response"), evidence, nil)
	}
	owned, err := cloneModelResponse(resp)
	if err != nil {
		kind := OutputValidationResponseShape
		var boundsErr *modelBoundsError
		if errors.As(err, &boundsErr) {
			kind = OutputValidationOutputBounds
		}
		return nil, newOutputValidationError(kind, err, evidence, &resp.Usage)
	}
	evidence = responseEvidence(owned)
	if err := validateTokenUsage(owned.Usage); err != nil {
		return nil, newOutputValidationError(OutputValidationUsage, err, evidence, &owned.Usage)
	}
	if err := c.validateUsageIdentity(owned.Usage); err != nil {
		return nil, newOutputValidationError(OutputValidationUsage, err, evidence, &owned.Usage)
	}
	if owned.OutputLimited {
		return nil, newOutputValidationError(OutputValidationOutputBounds, errors.New("provider stopped at its output limit"), evidence, &owned.Usage)
	}
	if strings.TrimSpace(owned.StopReason) == "" {
		return nil, newOutputValidationError(OutputValidationResponseShape, errors.New("provider response stop reason is required"), evidence, &owned.Usage)
	}
	if len(owned.Content) == 0 && len(owned.ToolCalls) == 0 {
		return nil, newOutputValidationError(OutputValidationResponseShape, errors.New("provider response contains no assistant output"), evidence, &owned.Usage)
	}
	if err := validateResponseShape(owned); err != nil {
		return nil, newOutputValidationError(OutputValidationResponseShape, err, evidence, &owned.Usage)
	}
	if err := c.validateToolCalls(owned.ToolCalls); err != nil {
		return nil, errWithEvidence(err, evidence, &owned.Usage)
	}
	if c.structuredValidate != nil {
		if err := c.validateStructuredResponse(owned); err != nil {
			return nil, newOutputValidationError(OutputValidationStructuredOutput, err, evidence, &owned.Usage)
		}
	}
	return owned, nil
}

// ValidateStream wraps a raw provider stream with request-scoped chunk and
// terminal validation, canonical response access, and serialized finalization.
func (c *RequestContract) ValidateStream(stream Streamer) (ValidatedStreamer, error) {
	return c.validateStream(context.Background(), stream)
}

func (c *RequestContract) validateStream(ctx context.Context, stream Streamer) (ValidatedStreamer, error) {
	if isNilModelValue(stream) {
		return nil, newOutputValidationError(OutputValidationStreamProtocol, errors.New("provider returned a nil stream"), ResponseEvidence{}, nil)
	}
	return &validatedProviderStream{
		inner:             stream,
		contract:          c,
		ctx:               ctx,
		budget:            cloneBudget{active: make(map[cloneContainer]struct{})},
		evidence:          sha256.New(),
		toolDeltaNames:    make(map[string]string),
		toolDeltaPayloads: make(map[string]*strings.Builder),
		finalToolCallIDs:  make(map[string]struct{}),
	}, nil
}

func (c *RequestContract) validateToolChoiceRequest() error {
	switch c.toolChoiceMode {
	case "", ToolChoiceModeAuto:
		return nil
	case ToolChoiceModeNone:
		if c.toolChoiceName != "" {
			return errors.New("model request tool choice none cannot name a tool")
		}
		return nil
	case ToolChoiceModeAny:
		if len(c.tools) == 0 {
			return errors.New("model request tool choice any requires tools")
		}
		if c.toolChoiceName != "" {
			return errors.New("model request tool choice any cannot name a tool")
		}
		return nil
	case ToolChoiceModeTool:
		if c.toolChoiceName == "" {
			return errors.New("model request tool choice tool requires a name")
		}
		if _, ok := c.tools[c.toolChoiceName]; !ok {
			return errors.New("model request forced tool is not advertised")
		}
		return nil
	default:
		return fmt.Errorf("model request tool choice mode %q is invalid", c.toolChoiceMode)
	}
}

func (c *RequestContract) validateUsageIdentity(usage TokenUsage) error {
	if c.model != "" && usage.Model != "" && usage.Model != c.model {
		return errors.New("provider usage model does not match its request")
	}
	if c.modelClass != "" && usage.ModelClass != "" && usage.ModelClass != c.modelClass {
		return errors.New("provider usage model class does not match its request")
	}
	return nil
}

func (c *RequestContract) validateToolCalls(calls []ToolCall) error {
	if err := c.validateToolCallPayloads(calls); err != nil {
		return err
	}
	return c.validateToolChoiceResponse(calls)
}

func (c *RequestContract) validateToolCallPayloads(calls []ToolCall) error {
	seenIDs := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := call.Name.String()
		schema, ok := c.tools[name]
		if !ok {
			return newOutputValidationError(OutputValidationToolIdentity, errors.New("provider returned an unadvertised tool name"), ResponseEvidence{Present: true}, nil)
		}
		if call.ID != "" {
			if _, exists := seenIDs[call.ID]; exists {
				return newOutputValidationError(OutputValidationToolIdentity, errors.New("provider returned a duplicate tool call identifier"), ResponseEvidence{Present: true}, nil)
			}
			seenIDs[call.ID] = struct{}{}
		}
		var payload any
		if err := json.Unmarshal(call.Payload, &payload); err != nil {
			return newOutputValidationError(OutputValidationToolArguments, errors.New("provider returned malformed tool arguments"), ResponseEvidence{Present: true}, nil)
		}
		if schema != nil {
			if err := schema.Validate(payload); err != nil {
				return newOutputValidationError(OutputValidationToolArguments, errors.New("provider returned tool arguments outside the advertised contract"), ResponseEvidence{Present: true}, nil)
			}
		}
	}
	return nil
}

func (c *RequestContract) validateToolChoiceResponse(calls []ToolCall) error {
	switch c.toolChoiceMode {
	case "", ToolChoiceModeAuto:
		return nil
	case ToolChoiceModeNone:
		if len(calls) > 0 {
			return newOutputValidationError(OutputValidationToolChoice, errors.New("provider returned tool calls when tools were disabled"), ResponseEvidence{Present: true}, nil)
		}
	case ToolChoiceModeAny:
		if len(calls) == 0 {
			return newOutputValidationError(OutputValidationToolChoice, errors.New("provider did not return a required tool call"), ResponseEvidence{Present: true}, nil)
		}
	case ToolChoiceModeTool:
		if len(calls) == 0 {
			return newOutputValidationError(OutputValidationToolChoice, errors.New("provider did not return the forced tool"), ResponseEvidence{Present: true}, nil)
		}
		for _, call := range calls {
			if call.Name.String() != c.toolChoiceName {
				return newOutputValidationError(OutputValidationToolChoice, errors.New("provider returned a tool other than the forced tool"), ResponseEvidence{Present: true}, nil)
			}
		}
	}
	return nil
}

func (c *RequestContract) validateStructuredResponse(resp *Response) error {
	text, err := structuredResponseText(resp)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return errors.New("structured output response is not valid JSON")
	}
	if err := c.structuredValidate.Validate(value); err != nil {
		return errors.New("structured output response does not match its schema")
	}
	if c.completionValidate != nil {
		completion := &Completion{Name: c.structuredName, Payload: []byte(text)}
		if err := c.completionValidate(resp, completion); err != nil {
			return errors.New("structured output response failed its application contract")
		}
	}
	return nil
}

func structuredResponseText(resp *Response) (string, error) {
	if len(resp.ToolCalls) > 0 {
		return "", errors.New("structured output response contains tool calls")
	}
	if len(resp.Content) != 1 {
		return "", errors.New("structured output response must contain exactly one assistant message")
	}
	message := resp.Content[0]
	if message.Role != ConversationRoleAssistant {
		return "", errors.New("structured output response role must be assistant")
	}
	var text string
	contentParts := 0
	for _, part := range message.Parts {
		switch value := part.(type) {
		case TextPart:
			contentParts++
			if contentParts > 1 {
				return "", errors.New("structured output response contains multiple content parts")
			}
			text = value.Text
		case CitationsPart:
			contentParts++
			if contentParts > 1 {
				return "", errors.New("structured output response contains multiple content parts")
			}
			text = value.Text
		case ThinkingPart, CacheCheckpointPart:
			continue
		default:
			return "", errors.New("structured output response contains an unsupported part")
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("structured output response is empty")
	}
	return text, nil
}

// SetCompletionValidator attaches an application-owned decoder to the exact
// structured-output request. NewClient captures the function before provider
// work, and providers cannot replace it through exported request fields.
func SetCompletionValidator(request *Request, validate func(*Response, *Completion) error) error {
	if request == nil {
		return errors.New("completion validator requires a request")
	}
	if request.StructuredOutput == nil {
		return errors.New("completion validator requires structured output")
	}
	if validate == nil {
		return errors.New("completion validator is required")
	}
	request.completionValidate = validate
	return nil
}

func validateResponseShape(resp *Response) error {
	for messageIndex, message := range resp.Content {
		if message.Role != ConversationRoleAssistant {
			return fmt.Errorf("provider response message %d is not an assistant message", messageIndex)
		}
		if len(message.Parts) == 0 {
			return fmt.Errorf("provider response message %d has no content parts", messageIndex)
		}
		for partIndex, part := range message.Parts {
			switch part.(type) {
			case TextPart, CitationsPart, ThinkingPart:
				continue
			default:
				return fmt.Errorf("provider response message %d part %d has an unsupported type", messageIndex, partIndex)
			}
		}
	}
	return nil
}

func (s *validatedProviderStream) Recv() (Chunk, error) { //nolint:maintidx // The stream state machine keeps terminal and pending transitions serialized in one owner.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		if s.finalizeErr != nil {
			return Chunk{}, s.finalizeErr
		}
		return Chunk{}, io.EOF
	}
	if s.response != nil && len(s.pending) > 0 {
		return s.takePending(), nil
	}
	if s.terminal {
		if s.terminalErr != nil {
			return Chunk{}, s.terminalErr
		}
		s.consumerEOF = true
		return Chunk{}, io.EOF
	}
	for {
		chunk, err := s.inner.Recv()
		if err != nil {
			return s.finishReceive(err)
		}
		if s.stopped {
			return Chunk{}, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream emitted output after its terminal stop"), nil)
		}
		owned, cloneErr := cloneModelChunk(chunk, &s.budget)
		if cloneErr != nil {
			kind := OutputValidationResponseShape
			var boundsErr *modelBoundsError
			if errors.As(cloneErr, &boundsErr) {
				kind = OutputValidationOutputBounds
			}
			return Chunk{}, s.failStream(kind, cloneErr, nil)
		}
		if evidenceErr := s.recordChunkEvidence(owned); evidenceErr != nil {
			return Chunk{}, s.failStream(OutputValidationOutputBounds, evidenceErr, nil)
		}
		withhold, validateErr := s.acceptChunk(owned)
		if validateErr != nil {
			return Chunk{}, validateErr
		}
		if withhold || len(s.pending) > 0 {
			s.pending = append(s.pending, owned)
			continue
		}
		return owned, nil
	}
}

func (s *validatedProviderStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.inner.Close()
	})
	return s.closeErr
}

func (s *validatedProviderStream) Metadata() map[string]any {
	metadata := s.inner.Metadata()
	if metadata == nil {
		return nil
	}
	copy := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

// Response returns the accepted canonical response after a clean EOF.
func (s *validatedProviderStream) Response() *Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.consumerEOF {
		return nil
	}
	response, err := cloneModelResponse(s.response)
	if err != nil {
		return nil
	}
	return response
}

// Finalize closes the provider stream exactly once and joins cleanup with the
// receive or processing result supplied by the consumer.
func (s *validatedProviderStream) Finalize(primaryErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return s.finalizeErr
	}
	if primaryErr == nil && !s.consumerEOF {
		primaryErr = errors.New("model stream was not completely consumed")
	}
	if !s.consumerEOF || primaryErr != nil {
		primaryErr = joinContextError(primaryErr, s.ctx.Err())
	}
	if s.terminalErr != nil && !errors.Is(primaryErr, s.terminalErr) {
		primaryErr = errors.Join(primaryErr, s.terminalErr)
	}
	s.finalized = true
	s.finalizeErr = errors.Join(primaryErr, s.Close())
	return s.finalizeErr
}

func (s *validatedProviderStream) acceptChunk(chunk Chunk) (bool, error) { //nolint:maintidx // Exhaustive dispatch keeps every open chunk variant on the same validation boundary.
	if err := validateChunkShape(chunk); err != nil {
		kind := OutputValidationResponseShape
		switch chunk.Type {
		case ChunkTypeUsage:
			kind = OutputValidationUsage
		case ChunkTypeCompletion, ChunkTypeCompletionDelta:
			kind = OutputValidationStructuredOutput
		case ChunkTypeStop:
			kind = OutputValidationStreamProtocol
		}
		return false, s.failStream(kind, err, chunk.UsageDelta)
	}
	switch chunk.Type {
	case ChunkTypeText:
		if s.contract.structuredValidate != nil {
			return false, s.failStream(OutputValidationStructuredOutput, errors.New("structured output stream emitted text"), nil)
		}
		message, err := cloneStreamMessageForCanonicalResponse(*chunk.Message)
		if err != nil {
			return false, s.failStream(OutputValidationResponseShape, err, nil)
		}
		s.content = append(s.content, message)
	case ChunkTypeThinking:
		if chunk.Message != nil {
			message, err := cloneStreamMessageForCanonicalResponse(*chunk.Message)
			if err != nil {
				return false, s.failStream(OutputValidationResponseShape, err, nil)
			}
			s.content = append(s.content, message)
		} else {
			s.content = append(s.content, Message{
				Role:  ConversationRoleAssistant,
				Parts: []Part{ThinkingPart{Text: chunk.Thinking}},
			})
		}
	case ChunkTypeToolCall:
		return s.acceptToolCall(*chunk.ToolCall)
	case ChunkTypeToolCallDelta:
		return s.acceptToolCallDelta(*chunk.ToolCallDelta)
	case ChunkTypeCompletion:
		return s.acceptCompletion(chunk.Completion)
	case ChunkTypeCompletionDelta:
		return s.acceptCompletionDelta(chunk.CompletionDelta)
	case ChunkTypeUsage:
		return false, s.acceptUsage(chunk.UsageDelta)
	case ChunkTypeStop:
		return s.acceptStop(chunk)
	}
	return false, nil
}

func (s *validatedProviderStream) acceptToolCall(call ToolCall) (bool, error) {
	if s.contract.structuredValidate != nil {
		return false, s.failStream(OutputValidationStructuredOutput, errors.New("structured output stream emitted a tool call"), nil)
	}
	if err := s.contract.validateToolCalls([]ToolCall{call}); err != nil {
		return false, s.failExistingValidation(err)
	}
	if err := s.reconcileToolCall(call); err != nil {
		return false, err
	}
	s.toolCalls = append(s.toolCalls, call)
	return true, nil
}

func (s *validatedProviderStream) reconcileToolCall(call ToolCall) error {
	if call.ID == "" {
		return nil
	}
	if _, exists := s.finalToolCallIDs[call.ID]; exists {
		return s.failStream(OutputValidationStreamProtocol, errors.New("provider stream repeated a finalized tool call"), nil)
	}
	if name := s.toolDeltaNames[call.ID]; name != "" && name != call.Name.String() {
		return s.failStream(OutputValidationStreamProtocol, errors.New("provider stream changed a tool name before finalization"), nil)
	}
	if payload := s.toolDeltaPayloads[call.ID]; payload != nil && payload.String() != string(call.Payload) {
		return s.failStream(OutputValidationStreamProtocol, errors.New("provider stream finalized tool arguments that differ from their deltas"), nil)
	}
	s.finalToolCallIDs[call.ID] = struct{}{}
	delete(s.toolDeltaNames, call.ID)
	delete(s.toolDeltaPayloads, call.ID)
	return nil
}

func (s *validatedProviderStream) acceptToolCallDelta(delta ToolCallDelta) (bool, error) {
	if s.contract.structuredValidate != nil {
		return false, s.failStream(OutputValidationStructuredOutput, errors.New("structured output stream emitted a tool call delta"), nil)
	}
	if _, ok := s.contract.tools[delta.Name.String()]; !ok {
		return false, s.failStream(OutputValidationToolIdentity, errors.New("provider stream emitted a tool delta for an unadvertised tool"), nil)
	}
	if delta.ID == "" {
		return false, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream emitted a tool delta without an identifier"), nil)
	}
	if _, finalized := s.finalToolCallIDs[delta.ID]; finalized {
		return false, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream emitted a tool delta after finalization"), nil)
	}
	name := delta.Name.String()
	if prior := s.toolDeltaNames[delta.ID]; prior != "" && prior != name {
		return false, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream changed a tool delta name"), nil)
	}
	s.toolDeltaNames[delta.ID] = name
	payload := s.toolDeltaPayloads[delta.ID]
	if payload == nil {
		payload = &strings.Builder{}
		s.toolDeltaPayloads[delta.ID] = payload
	}
	payload.WriteString(delta.Delta)
	return true, nil
}

func (s *validatedProviderStream) acceptCompletion(completion *Completion) (bool, error) {
	if s.contract.structuredValidate == nil {
		return false, s.failStream(OutputValidationStructuredOutput, errors.New("provider stream emitted a completion without a structured output request"), nil)
	}
	if s.completion != nil {
		return false, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream emitted multiple completions"), nil)
	}
	if completion.Name != s.contract.structuredName {
		return false, s.failStream(OutputValidationStructuredOutput, errors.New("provider stream completion name does not match its request"), nil)
	}
	s.completion = completion
	return true, nil
}

func (s *validatedProviderStream) acceptCompletionDelta(delta *CompletionDelta) (bool, error) {
	if s.contract.structuredValidate == nil || delta.Name != s.contract.structuredName {
		return false, s.failStream(OutputValidationStructuredOutput, errors.New("provider stream completion delta does not match its request"), nil)
	}
	return false, nil
}

func (s *validatedProviderStream) acceptUsage(delta *TokenUsage) error {
	if err := s.contract.validateUsageIdentity(*delta); err != nil {
		return s.failStream(OutputValidationUsage, err, delta)
	}
	usage, err := addTokenUsage(s.usage, *delta)
	if err != nil {
		return s.failStream(OutputValidationUsage, err, delta)
	}
	s.usage = usage
	return nil
}

func (s *validatedProviderStream) acceptStop(chunk Chunk) (bool, error) {
	if len(s.toolDeltaNames) > 0 && !chunk.OutputLimited {
		return false, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream stopped before all tool calls were finalized"), nil)
	}
	if chunk.OutputLimited {
		clear(s.toolDeltaNames)
		clear(s.toolDeltaPayloads)
	}
	s.stopped = true
	s.stopReason = chunk.StopReason
	s.outputLimited = chunk.OutputLimited
	return true, nil
}

func (s *validatedProviderStream) finishReceive(err error) (Chunk, error) {
	//nolint:errorlint // Wrapped EOF is a provider failure, not clean completion.
	if err != io.EOF {
		s.terminal = true
		s.terminalErr = joinContextError(err, s.ctx.Err())
		s.pending = nil
		return Chunk{}, s.terminalErr
	}
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		s.terminal = true
		s.terminalErr = ctxErr
		s.pending = nil
		return Chunk{}, ctxErr
	}
	if !s.stopped {
		return Chunk{}, s.failStream(OutputValidationStreamProtocol, errors.New("provider stream ended without a terminal stop"), nil)
	}
	if !s.outputLimited && s.contract.structuredValidate != nil && s.completion == nil {
		return Chunk{}, s.failStream(OutputValidationStructuredOutput, errors.New("structured output stream ended without a completion"), nil)
	}
	response := &Response{
		Content:       s.content,
		ToolCalls:     s.toolCalls,
		Usage:         s.usage,
		StopReason:    s.stopReason,
		OutputLimited: s.outputLimited,
	}
	if s.completion != nil {
		parts := streamedThinkingParts(s.content)
		parts = append(parts, TextPart{Text: string(s.completion.Payload)})
		response.Content = []Message{{Role: ConversationRoleAssistant, Parts: parts}}
	}
	accepted, validateErr := s.contract.ValidateResponse(response)
	if validateErr != nil {
		s.terminal = true
		s.terminalErr = validateErr
		s.pending = nil
		return Chunk{}, validateErr
	}
	s.response = accepted
	s.terminal = true
	if len(s.pending) > 0 {
		return s.takePending(), nil
	}
	s.consumerEOF = true
	return Chunk{}, io.EOF
}

func (s *validatedProviderStream) failStream(kind OutputValidationKind, cause error, usage *TokenUsage) error {
	err := joinContextError(newOutputValidationError(kind, cause, s.streamEvidence(), usage), s.ctx.Err())
	s.terminal = true
	s.terminalErr = err
	s.pending = nil
	return err
}

func (s *validatedProviderStream) recordChunkEvidence(chunk Chunk) error {
	raw, err := deterministicModelJSON(chunk)
	if err != nil {
		return fmt.Errorf("encode stream evidence: %w", err)
	}
	if len(raw) > maxModelOutputBytes-s.evidenceBytes {
		return &modelBoundsError{cause: fmt.Errorf("value exceeds maximum size %d bytes", maxModelOutputBytes)}
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(raw)))
	if _, err := s.evidence.Write(size[:]); err != nil {
		return fmt.Errorf("hash stream evidence length: %w", err)
	}
	if _, err := s.evidence.Write(raw); err != nil {
		return fmt.Errorf("hash stream evidence chunk: %w", err)
	}
	s.evidenceBytes += len(raw)
	return nil
}

func (s *validatedProviderStream) streamEvidence() ResponseEvidence {
	evidence := ResponseEvidence{Present: s.evidenceBytes > 0, ByteCount: s.evidenceBytes}
	copy(evidence.Fingerprint[:], s.evidence.Sum(nil))
	return evidence
}

func (s *validatedProviderStream) failExistingValidation(err error) error {
	var validationErr *OutputValidationError
	if errors.As(err, &validationErr) {
		return s.failStream(validationErr.Kind(), validationErr.Unwrap(), validationErr.Usage())
	}
	return s.failStream(OutputValidationResponseShape, err, nil)
}

func (s *validatedProviderStream) takePending() Chunk {
	chunk := s.pending[0]
	s.pending = s.pending[1:]
	return chunk
}

func validateChunkShape(chunk Chunk) error { //nolint:maintidx // Exhaustive chunk-shape validation makes each closed protocol variant explicit.
	payloads := chunkPayloadCount(chunk)
	switch chunk.Type {
	case ChunkTypeText:
		if chunk.Message == nil || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid text chunk shape")
		}
	case ChunkTypeThinking:
		thinkingPayloads := 0
		if chunk.Message != nil {
			thinkingPayloads++
		}
		if chunk.Thinking != "" {
			thinkingPayloads++
		}
		if thinkingPayloads == 0 || payloads != thinkingPayloads || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid thinking chunk shape")
		}
		if err := validateThinkingChunkMessage(chunk); err != nil {
			return err
		}
	case ChunkTypeToolCall:
		if chunk.ToolCall == nil || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid tool call chunk shape")
		}
	case ChunkTypeToolCallDelta:
		if chunk.ToolCallDelta == nil || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid tool delta chunk shape")
		}
	case ChunkTypeCompletion:
		if chunk.Completion == nil || len(chunk.Completion.Payload) == 0 || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid completion chunk shape")
		}
	case ChunkTypeCompletionDelta:
		if chunk.CompletionDelta == nil || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid completion delta chunk shape")
		}
	case ChunkTypeUsage:
		if chunk.UsageDelta == nil || payloads != 1 || chunk.OutputLimited {
			return errors.New("provider stream emitted invalid usage chunk shape")
		}
		if err := validateTokenUsage(*chunk.UsageDelta); err != nil {
			return err
		}
	case ChunkTypeStop:
		if strings.TrimSpace(chunk.StopReason) == "" || payloads != 1 {
			return errors.New("provider stream emitted invalid stop chunk shape")
		}
	default:
		return errors.New("provider stream emitted an unknown chunk type")
	}
	return nil
}

func validateThinkingChunkMessage(chunk Chunk) error {
	if chunk.Message == nil {
		return nil
	}
	if chunk.Message.Role != ConversationRoleAssistant || len(chunk.Message.Parts) != 1 {
		return errors.New("provider stream thinking message must contain exactly one assistant thinking part")
	}
	part, err := normalizeMessagePart(chunk.Message.Parts[0])
	if err != nil {
		return errors.New("provider stream thinking message contains an invalid part")
	}
	thinking, ok := part.(ThinkingPart)
	if !ok {
		return errors.New("provider stream thinking message contains a non-thinking part")
	}
	if chunk.Thinking != "" && thinking.Text != chunk.Thinking {
		return errors.New("provider stream thinking payloads disagree")
	}
	return nil
}

func chunkPayloadCount(chunk Chunk) int {
	count := 0
	values := []bool{
		chunk.Message != nil,
		chunk.Thinking != "",
		chunk.ToolCall != nil,
		chunk.ToolCallDelta != nil,
		chunk.Completion != nil,
		chunk.CompletionDelta != nil,
		chunk.UsageDelta != nil,
		chunk.StopReason != "",
	}
	for _, present := range values {
		if present {
			count++
		}
	}
	return count
}

func addTokenUsage(current, delta TokenUsage) (TokenUsage, error) {
	if err := validateTokenUsage(delta); err != nil {
		return TokenUsage{}, err
	}
	if current.Model != "" && delta.Model != "" && current.Model != delta.Model {
		return TokenUsage{}, errors.New("provider stream changed usage model")
	}
	if current.ModelClass != "" && delta.ModelClass != "" && current.ModelClass != delta.ModelClass {
		return TokenUsage{}, errors.New("provider stream changed usage model class")
	}
	if current.Model == "" {
		current.Model = delta.Model
	}
	if current.ModelClass == "" {
		current.ModelClass = delta.ModelClass
	}
	counts := []*int{&current.InputTokens, &current.OutputTokens, &current.TotalTokens, &current.CacheReadTokens, &current.CacheWriteTokens}
	deltas := []int{delta.InputTokens, delta.OutputTokens, delta.TotalTokens, delta.CacheReadTokens, delta.CacheWriteTokens}
	maxInt := int(^uint(0) >> 1)
	for index := range counts {
		if deltas[index] > maxInt-*counts[index] {
			return TokenUsage{}, errors.New("provider stream token usage overflowed")
		}
		*counts[index] += deltas[index]
	}
	return current, nil
}

func streamedThinkingParts(messages []Message) []Part {
	var parts []Part
	for _, message := range messages {
		for _, part := range message.Parts {
			if thinking, ok := part.(ThinkingPart); ok {
				parts = append(parts, thinking)
			}
		}
	}
	return parts
}

func joinContextError(primary, contextErr error) error {
	if contextErr == nil || errors.Is(primary, contextErr) {
		return primary
	}
	return errors.Join(primary, contextErr)
}

func cloneStreamMessageForCanonicalResponse(message Message) (Message, error) {
	budget := &cloneBudget{active: make(map[cloneContainer]struct{})}
	return cloneModelMessage(message, budget)
}

func compileModelSchema(schema any, label string) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, fmt.Errorf("model request %s schema is required", label)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("model request %s schema: %w", label, err)
	}
	if len(raw) == 0 || len(raw) > maxModelOutputBytes {
		return nil, fmt.Errorf("model request %s schema size is invalid", label)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("model request %s schema: %w", label, err)
	}
	compiler := jsonschema.NewCompiler()
	resource := "schema://model/" + fmt.Sprintf("%x", sha256.Sum256(raw)) + ".json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, fmt.Errorf("model request %s schema: %w", label, err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("model request %s schema: %w", label, err)
	}
	return compiled, nil
}

func responseEvidence(resp *Response) ResponseEvidence {
	if resp == nil {
		return ResponseEvidence{}
	}
	raw, err := deterministicModelJSON(resp)
	if err != nil || len(raw) > maxModelOutputBytes {
		return ResponseEvidence{Present: true, ByteCount: len(raw)}
	}
	return ResponseEvidence{
		Present:     true,
		ByteCount:   len(raw),
		Fingerprint: sha256.Sum256(raw),
	}
}

func deterministicModelJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxModelOutputBytes {
		return raw, &modelBoundsError{cause: fmt.Errorf("value exceeds maximum size %d bytes", maxModelOutputBytes)}
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return json.Marshal(document, json.Deterministic(true))
}

func errWithEvidence(err error, evidence ResponseEvidence, usage *TokenUsage) error {
	var validationErr *OutputValidationError
	if !errors.As(err, &validationErr) {
		return newOutputValidationError(OutputValidationResponseShape, err, evidence, usage)
	}
	return newOutputValidationError(validationErr.Kind(), validationErr.Unwrap(), evidence, usage)
}
