package runtime

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

// fakeValidationError mimics the generated ValidationError without importing the concrete type.
type fakeValidationError struct {
	issues []*tools.FieldIssue
	descs  map[string]string
}

func (f *fakeValidationError) Error() string                   { return "validation error" }
func (f *fakeValidationError) Issues() []*tools.FieldIssue     { return f.issues }
func (f *fakeValidationError) Descriptions() map[string]string { return f.descs }

func TestBuildRetryHint_MissingField(t *testing.T) {
	ferr := &fakeValidationError{
		issues: []*tools.FieldIssue{{Field: "q", Constraint: "missing_field"}},
		descs:  map[string]string{"q": "Search query"},
	}
	fields, q, reason, ok := buildRetryHintFromValidation(ferr, "svc.search")
	require.True(t, ok)
	require.Equal(t, planner.RetryReasonMissingFields, reason)
	require.Len(t, fields, 1)
	require.Equal(t, "q", fields[0])
	require.NotEmpty(t, q)
	require.True(t, containsAll(q, []string{"svc.search", "q"}))
}

func TestBuildRetryHint_InvalidEnum(t *testing.T) {
	ferr := &fakeValidationError{
		issues: []*tools.FieldIssue{{Field: "format", Constraint: "invalid_enum_value", Allowed: []string{"a", "b"}}},
		descs:  map[string]string{"format": "Output format"},
	}
	fields, q, reason, ok := buildRetryHintFromValidation(ferr, "svc.process")
	require.True(t, ok)
	require.Equal(t, planner.RetryReasonInvalidArguments, reason)
	require.Equal(t, []string{"format"}, fields)
	require.True(t, containsAll(q, []string{"format", "one of: a, b"}))
}

func TestBuildRetryHint_LengthPatternFormat(t *testing.T) {
	min := 2
	ferr := &fakeValidationError{
		issues: []*tools.FieldIssue{
			{Field: "name", Constraint: "invalid_length", MinLen: &min},
			{Field: "email", Constraint: "invalid_format", Format: "email"},
			{Field: "code", Constraint: "invalid_pattern", Pattern: "^[A-Z]+$"},
		},
	}
	fields, q, reason, ok := buildRetryHintFromValidation(ferr, "svc.create")
	require.True(t, ok)
	require.Equal(t, planner.RetryReasonInvalidArguments, reason)
	require.Equal(t, []string{"name", "email", "code"}, fields)
	require.NotEmpty(t, q)
	require.True(t, containsAll(q, []string{"name", "email", "code"}))
}

// TestBuildRetryHintFromDecodeError verifies that a JSON syntax error produces a
// planner.RetryHint whose Reason follows the decode-failure path
// (MissingFields), with the synthetic $payload anchor and a clarifying question
// that names the tool.
func TestBuildRetryHintFromDecodeError(t *testing.T) {
	var decErr error
	if err := json.Unmarshal([]byte("{not json"), &struct{}{}); err == nil {
		t.Fatal("expected json.Unmarshal to return syntax error on malformed input")
	} else {
		decErr = err
	}
	hint := buildRetryHintFromDecodeError(decErr, "svc.broken", nil)
	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.Equal(t, tools.Ident("svc.broken"), hint.Tool)
	require.Equal(t, []string{"$payload"}, hint.MissingFields)
	require.True(t, containsAll(hint.ClarifyingQuestion, []string{"svc.broken", "JSON"}))
}

// TestBuildRetryHintFromAgentToolRequestError verifies that an agent-tool
// request-validation error produces a planner.RetryHint that carries the
// validation fields and a matching reason.
func TestBuildRetryHintFromAgentToolRequestError(t *testing.T) {
	ferr := &fakeValidationError{
		issues: []*tools.FieldIssue{{Field: "topic", Constraint: "missing_field"}},
		descs:  map[string]string{"topic": "Topic to research"},
	}
	hint := buildRetryHintFromAgentToolRequestError(ferr, "svc.agent", &tools.ToolSpec{Payload: tools.TypeSpec{
		Schema: []byte(`{"type":"object","properties":{"topic":{"type":"string"}},"required":["topic"]}`),
	}})
	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.Equal(t, tools.Ident("svc.agent"), hint.Tool)
	require.Equal(t, []string{"topic"}, hint.MissingFields)
	require.True(t, containsAll(hint.ClarifyingQuestion, []string{"svc.agent", "topic"}))
	require.Contains(t, hint.Message, "topic:string,required")
}

func TestGeneratedRetryHintsAreBounded(t *testing.T) {
	t.Parallel()

	issues := make([]*tools.FieldIssue, 0, 64)
	descriptions := make(map[string]string, 64)
	for index := range 64 {
		field := fmt.Sprintf("field_%02d_%s", index, strings.Repeat("界", 80))
		issues = append(issues, &tools.FieldIssue{
			Field:      field,
			Constraint: validationConstraintMissingField,
			Allowed:    []string{strings.Repeat("allowed", 256)},
		})
		descriptions[field] = strings.Repeat("description", 256)
	}
	hint := buildRetryHintFromAgentToolRequestError(&fakeValidationError{issues: issues, descs: descriptions}, "svc.generated", nil)
	require.NotNil(t, hint)
	require.LessOrEqual(t, len(hint.MissingFields), maxGeneratedRetryHintFields)
	for _, field := range hint.MissingFields {
		require.LessOrEqual(t, len(field), maxGeneratedRetryHintFieldBytes)
		require.Equal(t, field, strings.ToValidUTF8(field, ""))
	}
	requireRetryHintWithinBound(t, hint)

	properties := make(map[string]any, 128)
	for index := range 128 {
		properties[fmt.Sprintf("property_%03d_%s", index, strings.Repeat("x", 128))] = map[string]any{"type": "string"}
	}
	schema, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	require.NoError(t, err)
	decodeHint := buildRetryHintFromDecodeError(
		&json.SemanticError{JSONPointer: jsontext.Pointer("/" + strings.Repeat("field", 128))},
		"svc.generated",
		&tools.ToolSpec{Payload: tools.TypeSpec{Schema: schema}},
	)
	require.NotNil(t, decodeHint)
	requireRetryHintWithinBound(t, decodeHint)
}

func TestActivityRetryHintRemainsBoundedAfterFieldContractEnrichment(t *testing.T) {
	t.Parallel()

	properties := make(map[string]any, 128)
	for index := range 128 {
		properties[fmt.Sprintf("property_%03d_%s", index, strings.Repeat("界", 96))] = map[string]any{"type": "string"}
	}
	schema, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	require.NoError(t, err)
	toolResult := &planner.ToolResult{}
	applyActivityRetryHint(toolResult, tools.ToolSpec{Payload: tools.TypeSpec{Schema: schema}}, &ToolOutput{
		RetryHint: &planner.RetryHint{
			Reason:             planner.RetryReasonMissingFields,
			MissingFields:      []string{strings.Repeat("field", 256)},
			ClarifyingQuestion: strings.Repeat("question", 1024),
			Message:            strings.Repeat("message", 1024),
			ExampleInput:       map[string]any{"secret": "example"},
			PriorInput:         map[string]any{"secret": "submitted"},
		},
	})

	require.NotNil(t, toolResult.RetryHint)
	requireRetryHintWithinBound(t, toolResult.RetryHint)
}

func requireRetryHintWithinBound(t *testing.T, hint *planner.RetryHint) {
	t.Helper()
	encoded, err := json.Marshal(hint)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), maxGeneratedRetryHintBytes)
	require.LessOrEqual(t, len(hint.ClarifyingQuestion), maxGeneratedRetryQuestionBytes)
	require.LessOrEqual(t, len(hint.Message), maxGeneratedRetryMessageBytes)
	require.Nil(t, hint.ExampleInput)
	require.Nil(t, hint.PriorInput)
}

// containsAll helper
func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
