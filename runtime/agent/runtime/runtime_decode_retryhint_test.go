package runtime

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

// TestBuildRetryHintFromDecodeError_SemanticError verifies that a JSON
// type mismatch produces a RetryHint with MissingFields, ReasonMissingFields,
// and an attached example when available.
func TestBuildRetryHintFromDecodeError_SemanticError(t *testing.T) {
	// Simulate a type error on the "summary" field.
	semanticErr := &json.SemanticError{JSONPointer: "/summary"}
	spec := &tools.ToolSpec{
		Payload: tools.TypeSpec{
			Schema: []byte(`{"type":"object","properties":{"summary":{"type":"object"}},"required":["summary"]}`),
			ExampleInput: map[string]any{
				"summary": map[string]any{
					"summary": "Headline",
				},
				"recommendations":      []any{"Do X"},
				"requires_remediation": true,
			},
		},
	}

	hint := buildRetryHintFromDecodeError(semanticErr, tools.Ident("diagnostics.emit.emit_diagnosis_result"), spec)
	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.Equal(t, tools.Ident("diagnostics.emit.emit_diagnosis_result"), hint.Tool)
	require.Equal(t, []string{"summary"}, hint.MissingFields)
	require.NotEmpty(t, hint.ClarifyingQuestion)
	require.Contains(t, hint.ClarifyingQuestion, "summary")
	require.Nil(t, hint.ExampleInput)
	require.Contains(t, hint.Message, "summary:object,required")
}

// TestBuildRetryHintFromDecodeError_SyntaxError verifies that malformed JSON
// yields a RetryHint with $payload marked as missing.
func TestBuildRetryHintFromDecodeError_SyntaxError(t *testing.T) {
	se := &jsontext.SyntacticError{ByteOffset: 10, Err: errors.New("invalid JSON")}
	hint := buildRetryHintFromDecodeError(se, tools.Ident("svc.ts.tool"), nil)
	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.Equal(t, []string{"$payload"}, hint.MissingFields)
	require.NotEmpty(t, hint.ClarifyingQuestion)
}

// TestBuildRetryHintFromDecodeError_NonJSONError verifies that non-JSON errors
// do not produce a RetryHint.
func TestBuildRetryHintFromDecodeError_NonJSONError(t *testing.T) {
	hint := buildRetryHintFromDecodeError(errors.New("some other error"), tools.Ident("svc.ts.tool"), nil)
	require.Nil(t, hint)
}

// TestExecuteToolActivity_DecodeErrorRetryHint ensures ExecuteToolActivity
// returns a ToolOutput with a RetryHint when payload decoding fails.
func TestExecuteToolActivity_DecodeErrorRetryHint(t *testing.T) {
	rt := &Runtime{
		logger:   telemetry.NoopLogger{},
		toolsets: make(map[string]ToolsetRegistration),
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			"svc.ts.tool": {
				Name:    "svc.ts.tool",
				Service: "svc",
				Toolset: "svc.ts",
				Payload: tools.TypeSpec{
					Name:   "P",
					Schema: []byte(`{"type":"object","properties":{"summary":{"type":"object"}},"required":["summary"]}`),
					ExampleInput: map[string]any{
						"summary": map[string]any{
							"summary": "Headline",
						},
						"recommendations":      []any{"Do X"},
						"requires_remediation": true,
					},
					Codec: tools.JSONCodec[any]{
						FromJSON: func(data []byte) (any, error) {
							// Force a decode failure that buildRetryHintFromDecodeError
							// can interpret.
							return nil, &json.SemanticError{JSONPointer: "/summary"}
						},
					},
				},
				Result: tools.TypeSpec{Name: "R"},
			},
		},
	}
	rt.toolsets["svc.ts"] = ToolsetRegistration{
		Name: "svc.ts",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			t.Fatalf("executor should not be called when pre-decode fails")
			return nil, nil
		},
		Specs: []tools.ToolSpec{
			rt.toolSpecs["svc.ts.tool"],
		},
	}

	raw := rawjson.Message([]byte(`{"summary":"wrong"}`))
	input := ToolInput{
		ToolsetName: "svc.ts",
		ToolName:    tools.Ident("svc.ts.tool"),
		Payload:     raw,
	}

	out, err := rt.ExecuteToolActivity(context.Background(), &input)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotEmpty(t, out.Error)
	require.NotNil(t, out.RetryHint)
	require.Equal(t, planner.RetryReasonMissingFields, out.RetryHint.Reason)
	require.Equal(t, []string{"summary"}, out.RetryHint.MissingFields)
	require.Nil(t, out.RetryHint.ExampleInput)
	require.Contains(t, out.RetryHint.Message, "summary:object,required")
}
