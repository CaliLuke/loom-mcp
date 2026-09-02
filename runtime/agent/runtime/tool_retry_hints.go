package runtime

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const (
	validationConstraintMissingField = "missing_field"
	payloadFieldAnchor               = "$payload"
	maxGeneratedRetryHintBytes       = 4096
	maxGeneratedRetryHintFields      = 8
	maxGeneratedRetryHintFieldBytes  = 96
	maxGeneratedRetryQuestionBytes   = 1024
	maxGeneratedRetryMessageBytes    = 1536
)

func (r *Runtime) toolDecodeErrorOutput(toolName tools.Ident, decErr error) *ToolOutput {
	var specPtr *tools.ToolSpec
	if spec, ok := r.toolSpec(toolName); ok {
		cp := spec
		specPtr = &cp
	}
	if fields, question, reason, ok := buildRetryHintFromValidation(decErr, toolName); ok {
		return &ToolOutput{
			Error: decErr.Error(),
			RetryHint: boundGeneratedRetryHint(&planner.RetryHint{
				Reason:             reason,
				Tool:               toolName,
				MissingFields:      fields,
				ClarifyingQuestion: question,
				Message:            generatedFieldContractPtr(specPtr),
			}),
		}
	}
	if hint := buildRetryHintFromDecodeError(decErr, toolName, specPtr); hint != nil {
		return &ToolOutput{Error: decErr.Error(), RetryHint: hint}
	}
	return &ToolOutput{Error: decErr.Error()}
}

// buildRetryHintFromValidation attempts to extract structured validation issues from
// a generated ValidationError (emitted by tool codecs) and build a precise retry hint.
// It returns the field anchors, a clarifying question, and the retry reason when
// successful; otherwise ok is false.
func buildRetryHintFromValidation(err error, toolName tools.Ident) ([]string, string, planner.RetryReason, bool) {
	issues, ok := validationIssues(err)
	if !ok || len(issues) == 0 {
		return nil, "", planner.RetryReasonInvalidArguments, false
	}
	descs := validationDescriptions(err)
	fields, missing := collectValidationFields(issues)
	if len(fields) == 0 {
		return nil, "", planner.RetryReasonInvalidArguments, false
	}
	question := truncateUTF8Bytes(buildValidationRetryQuestion(fields, issues, descs, toolName), maxGeneratedRetryQuestionBytes)
	reason := planner.RetryReasonInvalidArguments
	if len(missing) > 0 {
		reason = planner.RetryReasonMissingFields
	}
	return fields, question, reason, true
}

func validationIssues(err error) ([]*tools.FieldIssue, bool) {
	var ip interface {
		Issues() []*tools.FieldIssue
	}
	if !errors.As(err, &ip) {
		return nil, false
	}
	return ip.Issues(), true
}

func validationDescriptions(err error) map[string]string {
	var described interface {
		Descriptions() map[string]string
	}
	if !errors.As(err, &described) {
		return nil
	}
	return described.Descriptions()
}

func collectValidationFields(issues []*tools.FieldIssue) ([]string, []string) {
	fields := make([]string, 0, len(issues))
	missing := make([]string, 0, len(issues))
	for _, is := range issues {
		if is.Field == "" {
			continue
		}
		field := truncateUTF8Bytes(is.Field, maxGeneratedRetryHintFieldBytes)
		if !slices.Contains(fields, field) && len(fields) < maxGeneratedRetryHintFields {
			fields = append(fields, field)
		}
		if is.Constraint == validationConstraintMissingField && !slices.Contains(missing, field) && len(missing) < maxGeneratedRetryHintFields {
			missing = append(missing, field)
		}
	}
	return fields, missing
}

func buildValidationRetryQuestion(fields []string, issues []*tools.FieldIssue, descs map[string]string, toolName tools.Ident) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(fields), 3))
	for _, field := range fields[:min(len(fields), 3)] {
		parts = append(parts, validationQuestionLabel(field, issues, descs))
	}
	list := strings.Join(parts, ", ")
	if toolName != "" {
		return "I need additional information to run " + string(toolName) + ". Please provide: " + list + "."
	}
	return "I need additional information. Please provide: " + list + "."
}

func validationQuestionLabel(field string, issues []*tools.FieldIssue, descs map[string]string) string {
	label := field
	if d, ok := descs[field]; ok && d != "" {
		label = field + " (" + d + ")"
	}
	for _, is := range issues {
		if is.Field == field && len(is.Allowed) > 0 {
			return label + " - one of: " + strings.Join(is.Allowed, ", ")
		}
	}
	return label
}

// buildRetryHintFromDecodeError examines JSON decode errors that occur before tool
// execution and attempts to build a structured RetryHint. It treats malformed or
// wrong-shape JSON as conceptually equivalent to missing required fields so that
// planners and UIs can guide callers toward a schema-compliant payload.
//
// Generated hints contain only bounded schema-derived field guidance. They do
// not retain example or submitted input values.
func buildRetryHintFromDecodeError(err error, toolName tools.Ident, spec *tools.ToolSpec) *planner.RetryHint {
	var (
		semanticErr *json.SemanticError
		syntaxErr   *jsontext.SyntacticError
		fields      []string
		reason      planner.RetryReason
		question    string
	)

	switch {
	case errors.As(err, &semanticErr):
		field := semanticErr.JSONPointer.LastToken()
		if field == "" {
			field = payloadFieldAnchor
		}
		fields = []string{field}
		reason = planner.RetryReasonMissingFields
		question = fmt.Sprintf(
			"I could not decode the %s tool input. The %s field has the wrong JSON shape. Please resend this tool call with a JSON object that matches the expected schema.",
			toolName,
			field,
		)
	case errors.As(err, &syntaxErr):
		fields = []string{payloadFieldAnchor}
		reason = planner.RetryReasonMissingFields
		question = fmt.Sprintf(
			"I could not parse the %s tool input as JSON (syntax error near byte offset %d). Please resend this tool call with a valid JSON object payload.",
			toolName,
			syntaxErr.ByteOffset,
		)
	default:
		// Not a JSON decode error we can interpret.
		return nil
	}

	return boundGeneratedRetryHint(&planner.RetryHint{
		Reason:             reason,
		Tool:               toolName,
		MissingFields:      fields,
		ClarifyingQuestion: question,
		Message:            generatedFieldContractPtr(spec),
	})
}

func generatedFieldContractPtr(spec *tools.ToolSpec) string {
	if spec == nil {
		return ""
	}
	return truncateUTF8Bytes(appendFieldContract("", generatedFieldContract(*spec)), maxGeneratedRetryMessageBytes)
}

func generatedFieldContract(spec tools.ToolSpec) string {
	if len(spec.Payload.Schema) == 0 {
		return jsonSchemaTypeValue
	}
	var schema any
	if err := json.Unmarshal(spec.Payload.Schema, &schema); err != nil {
		return jsonSchemaTypeValue
	}
	return topLevelFieldContract(schema)
}

func appendFieldContract(message, contract string) string {
	if contract == "" {
		return message
	}
	if message == "" {
		return "Field contract: " + contract
	}
	return message + " Field contract: " + contract
}

func buildRetryHintFromAgentToolRequestError(err error, toolName tools.Ident, spec *tools.ToolSpec) *planner.RetryHint {
	if fields, question, reason, ok := buildRetryHintFromValidation(err, toolName); ok {
		return boundGeneratedRetryHint(&planner.RetryHint{
			Reason:             reason,
			Tool:               toolName,
			MissingFields:      fields,
			ClarifyingQuestion: question,
			Message:            generatedFieldContractPtr(spec),
		})
	}
	if hint := buildRetryHintFromDecodeError(err, toolName, spec); hint != nil {
		return hint
	}
	return nil
}

// BoundGeneratedRetryHint removes submitted payloads and bounds generated
// retry guidance before it crosses a durable or provider-facing boundary.
func BoundGeneratedRetryHint(hint *planner.RetryHint) *planner.RetryHint {
	return boundGeneratedRetryHint(hint)
}

func boundGeneratedRetryHint(hint *planner.RetryHint) *planner.RetryHint {
	if hint == nil {
		return nil
	}
	bounded := *hint
	bounded.ExampleInput = nil
	bounded.PriorInput = nil
	bounded.MissingFields = append([]string(nil), hint.MissingFields[:min(len(hint.MissingFields), maxGeneratedRetryHintFields)]...)
	for index, field := range bounded.MissingFields {
		bounded.MissingFields[index] = truncateUTF8Bytes(field, maxGeneratedRetryHintFieldBytes)
	}
	bounded.ClarifyingQuestion = truncateUTF8Bytes(bounded.ClarifyingQuestion, maxGeneratedRetryQuestionBytes)
	bounded.Message = truncateUTF8Bytes(bounded.Message, maxGeneratedRetryMessageBytes)

	for {
		encoded, err := json.Marshal(bounded)
		if err != nil {
			return nil
		}
		if len(encoded) <= maxGeneratedRetryHintBytes {
			return &bounded
		}
		overflow := len(encoded) - maxGeneratedRetryHintBytes
		switch {
		case bounded.Message != "":
			bounded.Message = shrinkUTF8Bytes(bounded.Message, overflow)
		case bounded.ClarifyingQuestion != "":
			bounded.ClarifyingQuestion = shrinkUTF8Bytes(bounded.ClarifyingQuestion, overflow)
		case len(bounded.MissingFields) > 0:
			bounded.MissingFields = bounded.MissingFields[:len(bounded.MissingFields)-1]
		default:
			return nil
		}
	}
}

func shrinkUTF8Bytes(value string, overflow int) string {
	target := len(value) - overflow - 16
	if target < 0 {
		target = 0
	}
	return truncateUTF8Bytes(value, target)
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
