package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
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
	hint := buildRetryHintFromAgentToolRequestError(ferr, "svc.agent", nil)
	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.Equal(t, tools.Ident("svc.agent"), hint.Tool)
	require.Equal(t, []string{"topic"}, hint.MissingFields)
	require.True(t, containsAll(hint.ClarifyingQuestion, []string{"svc.agent", "topic"}))
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
