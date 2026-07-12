package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/streaming"
)

type specLookupFunc func(tools.Ident) (*tools.ToolSpec, bool)

func (f specLookupFunc) Spec(name tools.Ident) (*tools.ToolSpec, bool) {
	return f(name)
}

func TestPrepareExecutionRejectsInvalidDependenciesAndSpecs(t *testing.T) {
	toolName := tools.Ident("todos.update")
	validSpec := &tools.ToolSpec{Name: toolName, Toolset: "todos"}
	validCall := &planner.ToolRequest{Name: toolName}
	validMeta := &agentsruntime.ToolCallMeta{ToolCallID: "call-1"}
	cases := []struct {
		name    string
		exec    *Executor
		call    *planner.ToolRequest
		meta    *agentsruntime.ToolCallMeta
		message string
	}{
		{name: "nil_request", exec: New(fakeRegistryClient{}, fakePulseClient{}, fakeSpecs{spec: validSpec}), meta: validMeta, message: "tool request is nil"},
		{name: "nil_meta", exec: New(fakeRegistryClient{}, fakePulseClient{}, fakeSpecs{spec: validSpec}), call: validCall, message: "tool call meta is nil"},
		{name: "nil_registry", exec: New(nil, fakePulseClient{}, fakeSpecs{spec: validSpec}), call: validCall, meta: validMeta, message: "registry client is nil"},
		{name: "nil_pulse", exec: New(fakeRegistryClient{}, nil, fakeSpecs{spec: validSpec}), call: validCall, meta: validMeta, message: "pulse client is nil"},
		{name: "nil_specs", exec: New(fakeRegistryClient{}, fakePulseClient{}, nil), call: validCall, meta: validMeta, message: "tool specs lookup is nil"},
		{name: "unknown_tool", exec: New(fakeRegistryClient{}, fakePulseClient{}, fakeSpecs{}), call: validCall, meta: validMeta, message: `unknown tool "todos.update"`},
		{name: "nil_spec", exec: New(fakeRegistryClient{}, fakePulseClient{}, specLookupFunc(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, true })), call: validCall, meta: validMeta, message: `tool "todos.update" has nil spec`},
		{name: "missing_toolset", exec: New(fakeRegistryClient{}, fakePulseClient{}, fakeSpecs{spec: &tools.ToolSpec{Name: toolName}}), call: validCall, meta: validMeta, message: "missing toolset routing id"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, result := tt.exec.prepareExecution(tt.call, tt.meta)
			require.NotNil(t, result)
			require.NotNil(t, result.Error)
			assert.Contains(t, result.Error.Error(), tt.message)
		})
	}
}

func TestHandleResultStreamEventRejectsNilEvent(t *testing.T) {
	exec := New(nil, nil, nil)
	done, result, err := exec.handleResultStreamEvent(
		context.Background(), telemetry.NewNoopTracer().Span(context.Background()), &fakeSink{}, &tools.ToolSpec{},
		&agentsruntime.ToolCallMeta{}, &planner.ToolRequest{}, "tool-use", "result:tool-use", nil,
	)

	assert.True(t, done)
	assert.Nil(t, result)
	assert.EqualError(t, err, "tool result stream delivered nil event")
}

func TestDecodeToolResultBuildsDetachedRetryHint(t *testing.T) {
	toolName := tools.Ident("search.query")
	example := map[string]any{
		"query":   "example",
		"filters": map[string]any{"types": []any{"docs", map[string]any{"kind": "spec"}}},
	}
	spec := &tools.ToolSpec{Name: toolName, Payload: tools.TypeSpec{ExampleInput: example}}
	msg := toolregistry.ToolResultMessage{Error: &toolregistry.ToolError{
		Code:    errorCodeInvalidArguments,
		Message: "missing inputs",
		Issues: []*tools.FieldIssue{
			nil,
			{Field: fieldRequestedSignals, Constraint: issueConstraintMissing},
			{Field: fieldQuery, Constraint: issueConstraintMissing},
			{Field: fieldQuery, Constraint: issueConstraintMissing},
			{Field: "", Constraint: issueConstraintMissing},
		},
	}}

	result := New(nil, nil, nil).decodeToolResult(spec, &planner.ToolRequest{Name: toolName}, "call-1", msg)

	require.NotNil(t, result.Error)
	require.NotNil(t, result.RetryHint)
	assert.Equal(t, planner.RetryReasonMissingFields, result.RetryHint.Reason)
	assert.Equal(t, []string{"query", "requested_signals"}, result.RetryHint.MissingFields)
	assert.Contains(t, result.RetryHint.ClarifyingQuestion, "either `query`")
	assert.Equal(t, example, result.RetryHint.ExampleInput)

	result.RetryHint.ExampleInput["query"] = "mutated"
	filters := result.RetryHint.ExampleInput["filters"].(map[string]any)
	types := filters["types"].([]any)
	types[1].(map[string]any)["kind"] = "mutated"
	assert.Equal(t, "example", example["query"])
	originalTypes := example["filters"].(map[string]any)["types"].([]any)
	assert.Equal(t, "spec", originalTypes[1].(map[string]any)["kind"])
}

func TestRetryHintsCoverCodesAndIssueShapes(t *testing.T) {
	toolName := tools.Ident("search.query")
	cases := []struct {
		code   string
		reason planner.RetryReason
	}{
		{code: "invalid_input", reason: planner.RetryReasonInvalidArguments},
		{code: errorCodeInvalidArguments, reason: planner.RetryReasonInvalidArguments},
		{code: "timeout", reason: planner.RetryReasonTimeout},
		{code: "internal"},
	}
	for _, tt := range cases {
		t.Run(tt.code, func(t *testing.T) {
			hint := retryHintFromToolErrorCode(toolName, tt.code)
			if tt.reason == "" {
				assert.Nil(t, hint)
				return
			}
			require.NotNil(t, hint)
			assert.Equal(t, tt.reason, hint.Reason)
			assert.Equal(t, toolName, hint.Tool)
		})
	}

	hint := buildRetryHintFromIssues(toolName, nil, []*tools.FieldIssue{
		{Field: "limit", Constraint: "invalid_range"},
		{Field: "query", Constraint: "invalid_length"},
		{Field: "limit", Constraint: "invalid_range"},
	})
	require.NotNil(t, hint)
	assert.Equal(t, planner.RetryReasonInvalidArguments, hint.Reason)
	assert.Empty(t, hint.MissingFields)
	assert.Contains(t, hint.ClarifyingQuestion, "limit, query")
	assert.Nil(t, buildRetryHintFromIssues(toolName, nil, nil))
	assert.Nil(t, buildRetryHintFromIssues(toolName, nil, []*tools.FieldIssue{nil, {}}))
}

func TestDecodeToolResultDecodesAndDetachesWireMetadata(t *testing.T) {
	total := 9
	next := "cursor-2"
	bounds := &agent.Bounds{Returned: 2, Total: &total, NextCursor: &next}
	serverData := []*toolregistry.ServerDataItem{{Kind: "card", Audience: "timeline", Data: json.RawMessage(`{"title":"original"}`)}, nil}
	spec := &tools.ToolSpec{Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
	msg := toolregistry.ToolResultMessage{Result: json.RawMessage(`{"ok":true}`), Bounds: bounds, ServerData: serverData}

	result := New(nil, nil, nil).decodeToolResult(spec, &planner.ToolRequest{Name: "todos.update"}, "call-1", msg)

	require.Nil(t, result.Error)
	assert.Equal(t, map[string]any{"ok": true}, result.Result)
	require.NotNil(t, result.Bounds)
	assert.Equal(t, 9, *result.Bounds.Total)
	assert.Equal(t, "cursor-2", *result.Bounds.NextCursor)
	assert.JSONEq(t, `[{"kind":"card","audience":"timeline","data":{"title":"original"}}]`, string(result.ServerData))

	*bounds.Total = 10
	*bounds.NextCursor = "mutated"
	serverData[0].Data[0] = '['
	assert.Equal(t, 9, *result.Bounds.Total)
	assert.Equal(t, "cursor-2", *result.Bounds.NextCursor)
	assert.JSONEq(t, `[{"kind":"card","audience":"timeline","data":{"title":"original"}}]`, string(result.ServerData))
}

func TestDecodeToolResultSurfacesCodecFailure(t *testing.T) {
	codecErr := errors.New("decode result")
	spec := &tools.ToolSpec{Result: tools.TypeSpec{Codec: tools.JSONCodec[any]{
		FromJSON: func([]byte) (any, error) {
			return nil, codecErr
		},
	}}}

	result := New(nil, nil, nil).decodeToolResult(spec, &planner.ToolRequest{Name: "todos.update"}, "call-1", toolregistry.ToolResultMessage{Result: json.RawMessage(`{}`)})

	require.NotNil(t, result.Error)
	assert.Equal(t, codecErr.Error(), result.Error.Error())
}

func TestHandleResultStreamEventAcknowledgesIgnorableEvents(t *testing.T) {
	exec := New(nil, nil, nil)
	sink := &countingSink{}
	span := telemetry.NewNoopTracer().Span(context.Background())
	meta := &agentsruntime.ToolCallMeta{}
	call := &planner.ToolRequest{}
	events := []*streaming.Event{
		{EventName: "other"},
		{EventName: toolregistry.OutputDeltaEventKey, Payload: []byte(`not-json`)},
		{EventName: toolregistry.OutputDeltaEventKey, Payload: mustJSON(t, toolregistry.NewToolOutputDeltaMessage("other-use", "stdout", "ignored"))},
		{EventName: toolregistry.ResultEventKey, Payload: []byte(`not-json`)},
		{EventName: toolregistry.ResultEventKey, Payload: mustJSON(t, toolregistry.ToolResultMessage{ToolUseID: "other-use"})},
	}

	for _, event := range events {
		done, result, err := exec.handleResultStreamEvent(context.Background(), span, sink, &tools.ToolSpec{}, meta, call, "tool-use", "result:tool-use", event)
		require.NoError(t, err)
		assert.False(t, done)
		assert.Nil(t, result)
	}
	assert.Equal(t, len(events), sink.acks)
}

type countingSink struct {
	acks int
}

func (s *countingSink) Subscribe() <-chan *streaming.Event {
	return nil
}

func (s *countingSink) Ack(context.Context, *streaming.Event) error {
	s.acks++
	return nil
}

func (s *countingSink) Close(context.Context) {}
