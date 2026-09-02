package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const (
	maxRecoveryCorrectionBytes = 4096
	maxModelRecoveryRecords    = 64
)

type modelRecoveryRecorder struct {
	mu       sync.Mutex
	records  []*modelRecoveryRecord
	overflow bool
}

type modelRecoveryRecord struct {
	err         *model.OutputValidationError
	request     *model.Request
	terminal    error
	streamUsage bool
}

type recoveryCapturingClient struct {
	inner    model.Client
	recorder *modelRecoveryRecorder
}

type recoveryCapturingStream struct {
	inner      model.ValidatedStreamer
	recorder   *modelRecoveryRecorder
	request    *model.Request
	requestErr error
	once       sync.Once
}

func newRecoveryCapturingClient(inner model.Client, recorder *modelRecoveryRecorder) model.Client {
	if inner == nil || recorder == nil {
		return inner
	}
	return &recoveryCapturingClient{inner: inner, recorder: recorder}
}

func (c *recoveryCapturingClient) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	response, err := c.inner.Complete(ctx, request)
	c.recorder.record(request, err)
	return response, err
}

func (c *recoveryCapturingClient) Stream(ctx context.Context, request *model.Request) (model.ValidatedStreamer, error) {
	stream, err := c.inner.Stream(ctx, request)
	if err != nil {
		c.recorder.record(request, err)
		return nil, err
	}
	requestCopy, copyErr := cloneRecoveryRequest(request)
	return &recoveryCapturingStream{
		inner:      stream,
		recorder:   c.recorder,
		request:    requestCopy,
		requestErr: copyErr,
	}, nil
}

func (s *recoveryCapturingStream) Recv() (model.Chunk, error) {
	chunk, err := s.inner.Recv()
	if err != nil && !errors.Is(err, io.EOF) {
		s.once.Do(func() { s.recorder.recordSnapshot(s.request, s.requestErr, err, true) })
	}
	return chunk, err
}

func (s *recoveryCapturingStream) Close() error {
	return s.inner.Close()
}

func (s *recoveryCapturingStream) Response() *model.Response {
	return s.inner.Response()
}
func (s *recoveryCapturingStream) Finalize(primaryErr error) error {
	err := s.inner.Finalize(primaryErr)
	s.once.Do(func() { s.recorder.recordSnapshot(s.request, s.requestErr, err, true) })
	return err
}

func (r *modelRecoveryRecorder) record(request *model.Request, err error) {
	requestCopy, copyErr := cloneRecoveryRequest(request)
	r.recordSnapshot(requestCopy, copyErr, err, false)
}

func (r *modelRecoveryRecorder) recordSnapshot(request *model.Request, requestErr error, err error, streamUsage bool) {
	if r == nil {
		return
	}
	var validationErr *model.OutputValidationError
	if !errors.As(err, &validationErr) {
		return
	}
	record := &modelRecoveryRecord{err: validationErr, request: request, terminal: errors.Join(err, requestErr), streamUsage: streamUsage}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == maxModelRecoveryRecords {
		r.overflow = true
		return
	}
	r.records = append(r.records, record)
}

func (r *modelRecoveryRecorder) activityUsage(observed model.TokenUsage) (model.TokenUsage, error) {
	if r == nil {
		return observed, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.overflow {
		return model.TokenUsage{}, errors.New("model recovery record limit exceeded")
	}
	usage := observed
	for _, record := range r.records {
		if record.streamUsage {
			continue
		}
		rejectedUsage := record.err.Usage()
		if rejectedUsage == nil {
			continue
		}
		var err error
		usage, err = checkedAddTokenUsage(usage, *rejectedUsage)
		if err != nil {
			return model.TokenUsage{}, fmt.Errorf("account rejected model output: %w", err)
		}
	}
	return usage, nil
}

func (r *modelRecoveryRecorder) recovery(plannerErr error, attempt int) (*api.ModelRecovery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if plannerErr == nil {
		return nil, plannerErr
	}
	if r.overflow {
		return nil, errors.Join(plannerErr, errors.New("model recovery record limit exceeded"))
	}
	var record *modelRecoveryRecord
	for i := len(r.records) - 1; i >= 0; i-- {
		if onlyRecordedValidation(plannerErr, r.records[i].err) {
			record = r.records[i]
			break
		}
	}
	if record == nil {
		return nil, plannerErr
	}
	if record.terminal != nil && !onlyRecordedValidation(record.terminal, record.err) {
		return nil, plannerErr
	}
	correction, disableTools, ok := recoveryCorrection(record.err.Kind(), record.request)
	if !ok {
		return nil, plannerErr
	}
	if len(correction) > maxRecoveryCorrectionBytes {
		correction = truncateUTF8Bytes(correction, maxRecoveryCorrectionBytes)
	}
	evidence := record.err.Evidence()
	recovery := &api.ModelRecovery{
		Kind:         record.err.Kind(),
		Fingerprint:  evidence.Fingerprint,
		ByteCount:    evidence.ByteCount,
		Attempt:      attempt,
		Correction:   correction,
		DisableTools: disableTools,
	}
	if !disableTools {
		recovery.ToolCatalog = recoveryToolIdents(record.request)
	}
	if usage := record.err.Usage(); usage != nil {
		recovery.Usage = *usage
	}
	return recovery, nil
}

func recoveryToolIdents(request *model.Request) []tools.Ident {
	names := recoveryToolNames(request)
	catalog := make([]tools.Ident, 0, len(names))
	for _, name := range names {
		catalog = append(catalog, tools.Ident(name))
	}
	return catalog
}

func cloneRecoveryRequest(request *model.Request) (*model.Request, error) {
	if request == nil {
		return nil, errors.New("recovery request is missing")
	}
	definitions, err := model.CloneToolDefinitions(request.Tools)
	if err != nil {
		return nil, fmt.Errorf("clone recovery tool definitions: %w", err)
	}
	return &model.Request{Tools: definitions}, nil
}

func onlyRecordedValidation(err error, expected *model.OutputValidationError) bool {
	if err == nil {
		return false
	}
	var validationErr *model.OutputValidationError
	if reflect.TypeOf(err) == reflect.TypeOf(expected) && errors.As(err, &validationErr) && validationErr == expected {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !onlyRecordedValidation(cause, expected) {
				return false
			}
		}
		return true
	}
	if cause := errors.Unwrap(err); cause != nil {
		return onlyRecordedValidation(cause, expected)
	}
	return false
}

func recoveryCorrection(kind model.OutputValidationKind, request *model.Request) (string, bool, bool) {
	switch kind {
	case model.OutputValidationToolIdentity:
		return "Replace the rejected tool call. Use one of these exact advertised tool names: " + strings.Join(recoveryToolNames(request), ", "), false, true
	case model.OutputValidationToolArguments:
		return "Replace the rejected tool call with arguments that satisfy these field contracts: " + recoveryFieldContracts(request), false, true
	case model.OutputValidationOutputBounds:
		return "Replace the rejected final answer with a shorter complete answer that stays within the output limit. Tools are disabled for this replacement turn.", true, true
	case model.OutputValidationStructuredOutput:
		return "Replace the rejected final answer with one value that satisfies the requested application schema. Tools are disabled for this replacement turn.", true, true
	case model.OutputValidationResponseShape,
		model.OutputValidationToolChoice,
		model.OutputValidationStreamProtocol,
		model.OutputValidationUsage:
		return "", false, false
	default:
		return "", false, false
	}
}

func recoveryToolNames(request *model.Request) []string {
	if request == nil {
		return nil
	}
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool != nil && tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}

func recoveryFieldContracts(request *model.Request) string {
	if request == nil {
		return "no tool fields are advertised"
	}
	contracts := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool != nil {
			contracts = append(contracts, fmt.Sprintf("%s(%s)", tool.Name, topLevelFieldContract(tool.InputSchema)))
		}
	}
	sort.Strings(contracts)
	return strings.Join(contracts, "; ")
}

func topLevelFieldContract(schema any) string {
	root, ok := schema.(map[string]any)
	if !ok {
		return jsonSchemaTypeValue
	}
	required := make(map[string]struct{})
	if values, ok := root["required"].([]string); ok {
		for _, value := range values {
			required[value] = struct{}{}
		}
	}
	if values, ok := root["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	properties, ok := root[jsonSchemaPropertiesKey].(map[string]any)
	if !ok || len(properties) == 0 {
		return jsonSchemaTypeValue
	}
	fields := make([]string, 0, len(properties))
	for name, value := range properties {
		kind := "value"
		if property, ok := value.(map[string]any); ok {
			if declared, ok := property["type"].(string); ok && declared != "" {
				kind = declared
			}
		}
		if _, ok := required[name]; ok {
			kind += ",required"
		}
		fields = append(fields, name+":"+kind)
	}
	sort.Strings(fields)
	return strings.Join(fields, ",")
}

func applyRecoveryInstruction(messages []*model.Message, recovery *api.ModelRecovery) []*model.Message {
	if recovery == nil || strings.TrimSpace(recovery.Correction) == "" {
		return messages
	}
	out := append([]*model.Message(nil), messages...)
	out = append(out, &model.Message{
		Role:  model.ConversationRoleSystem,
		Parts: []model.Part{model.TextPart{Text: recovery.Correction}},
	})
	return out
}
