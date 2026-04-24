package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func (r *Runtime) toolDecodeErrorOutput(toolName tools.Ident, decErr error) *ToolOutput {
	if fields, question, reason, ok := buildRetryHintFromValidation(decErr, toolName); ok {
		return &ToolOutput{
			Error: decErr.Error(),
			RetryHint: &planner.RetryHint{
				Reason:             reason,
				Tool:               toolName,
				MissingFields:      fields,
				ClarifyingQuestion: question,
			},
		}
	}
	var specPtr *tools.ToolSpec
	if spec, ok := r.toolSpec(toolName); ok {
		cp := spec
		specPtr = &cp
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
	question := buildValidationRetryQuestion(fields, issues, descs, toolName)
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
		if !slices.Contains(fields, is.Field) {
			fields = append(fields, is.Field)
		}
		if is.Constraint == "missing_field" && !slices.Contains(missing, is.Field) {
			missing = append(missing, is.Field)
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
// When a payload example is available in the tool specs, the hint attaches it as
// ExampleInput so consumers can display a concrete, valid payload.
func buildRetryHintFromDecodeError(err error, toolName tools.Ident, spec *tools.ToolSpec) *planner.RetryHint {
	var (
		typeErr   *json.UnmarshalTypeError
		syntaxErr *json.SyntaxError
		fields    []string
		reason    planner.RetryReason
		question  string
	)

	switch {
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "$payload"
		}
		fields = []string{field}
		reason = planner.RetryReasonMissingFields
		question = fmt.Sprintf(
			"I could not decode the %s tool input. The %s field has the wrong JSON shape. Please resend this tool call with a JSON object that matches the expected schema.",
			toolName,
			field,
		)
	case errors.As(err, &syntaxErr):
		fields = []string{"$payload"}
		reason = planner.RetryReasonMissingFields
		question = fmt.Sprintf(
			"I could not parse the %s tool input as JSON (syntax error near byte offset %d). Please resend this tool call with a valid JSON object payload.",
			toolName,
			syntaxErr.Offset,
		)
	default:
		// Not a JSON decode error we can interpret.
		return nil
	}

	var example map[string]any
	if spec != nil && len(spec.Payload.ExampleInput) > 0 {
		example = spec.Payload.ExampleInput
	}

	return &planner.RetryHint{
		Reason:             reason,
		Tool:               toolName,
		MissingFields:      fields,
		ExampleInput:       example,
		ClarifyingQuestion: question,
	}
}

func buildRetryHintFromAgentToolRequestError(err error, toolName tools.Ident, spec *tools.ToolSpec) *planner.RetryHint {
	if fields, question, reason, ok := buildRetryHintFromValidation(err, toolName); ok {
		return &planner.RetryHint{
			Reason:             reason,
			Tool:               toolName,
			MissingFields:      fields,
			ClarifyingQuestion: question,
		}
	}
	if hint := buildRetryHintFromDecodeError(err, toolName, spec); hint != nil {
		return hint
	}
	return nil
}
