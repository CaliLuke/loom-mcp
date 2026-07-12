package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestMessageEventDataRoundTripsAndDetachesStructuredPayloads(t *testing.T) {
	structured := map[string]any{
		"items": []any{map[string]any{"name": "original"}},
		"bytes": []byte{1, 2},
	}
	labels := map[string]string{"source": "api"}
	userEvent := NewEvent(time.Unix(1, 0), UserMessageData{Message: "hello", Structured: structured}, labels)
	assistantEvent := NewEvent(time.Unix(2, 0), AssistantMessageData{Message: "answer", Structured: structured}, nil)

	structured["items"].([]any)[0].(map[string]any)["name"] = "mutated"
	structured["bytes"].([]byte)[0] = 9
	labels["source"] = "mutated"

	user, err := DecodeUserMessageData(userEvent)
	require.NoError(t, err)
	assistant, err := DecodeAssistantMessageData(assistantEvent)
	require.NoError(t, err)
	assert.Equal(t, "hello", user.Message)
	assert.Equal(t, "answer", assistant.Message)
	assert.Equal(t, "original", nestedName(user.Structured))
	assert.Equal(t, "original", nestedName(assistant.Structured))
	assert.Equal(t, byte(1), user.Structured.(map[string]any)["bytes"].([]byte)[0])
	assert.Equal(t, "api", userEvent.Labels["source"])

	user.Structured.(map[string]any)["items"].([]any)[0].(map[string]any)["name"] = "decoded mutation"
	stored := userEvent.Data.(map[string]any)[eventFieldStructured]
	assert.Equal(t, "original", nestedName(stored))
}

func TestMessageDataFromMapSupportsTypedFormsAndRejectsNilPointers(t *testing.T) {
	structured := map[string]any{"nested": map[string]any{"value": "original"}}
	userSource := UserMessageData{Message: "hello", Structured: structured}
	assistantSource := &AssistantMessageData{Message: "answer", Structured: structured}

	var user UserMessageData
	require.NoError(t, user.FromMap(userSource))
	var assistant AssistantMessageData
	require.NoError(t, assistant.FromMap(assistantSource))
	structured["nested"].(map[string]any)["value"] = "mutated"
	assert.Equal(t, "original", user.Structured.(map[string]any)["nested"].(map[string]any)["value"])
	assert.Equal(t, "original", assistant.Structured.(map[string]any)["nested"].(map[string]any)["value"])

	require.ErrorContains(t, user.FromMap((*UserMessageData)(nil)), "data is nil")
	require.ErrorContains(t, assistant.FromMap((*AssistantMessageData)(nil)), "data is nil")
}

func TestToolResultDataDeepCopyIsolation(t *testing.T) {
	total := 5
	nextCursor := "next"
	original := ToolResultData{
		ToolCallID: "call-1",
		ToolName:   tools.Ident("search.query"),
		ResultJSON: rawjson.Message(`{"ok":true}`),
		ServerData: rawjson.Message(`[{"kind":"card"}]`),
		Bounds:     &agent.Bounds{Total: &total, NextCursor: &nextCursor},
		Telemetry:  &telemetry.ToolTelemetry{Extra: map[string]any{"nested": map[string]any{"value": "original"}}},
		RetryHint: &RetryHintData{
			MissingFields: []string{"query"},
			ExampleInput:  map[string]any{"nested": []any{map[string]any{"value": "original"}}},
			PriorInput:    map[string]any{"query": "original"},
		},
		Artifacts: []artifact.Ref{{ID: "artifact-1", Metadata: map[string]string{"kind": "original"}}},
	}

	stored := original.ToMap()
	decoded, err := DecodeToolResultData(Event{Type: EventToolResult, Data: stored})
	require.NoError(t, err)

	*original.Bounds.Total = 9
	*original.Bounds.NextCursor = "mutated"
	original.Telemetry.Extra["nested"].(map[string]any)["value"] = "mutated"
	original.RetryHint.ExampleInput["nested"].([]any)[0].(map[string]any)["value"] = "mutated"
	original.RetryHint.PriorInput["query"] = "mutated"
	original.Artifacts[0].Metadata["kind"] = "mutated"
	original.ResultJSON[0] = '['

	assert.Equal(t, 5, *decoded.Bounds.Total)
	assert.Equal(t, "next", *decoded.Bounds.NextCursor)
	assert.Equal(t, "original", decoded.Telemetry.Extra["nested"].(map[string]any)["value"])
	assert.Equal(t, "original", decoded.RetryHint.ExampleInput["nested"].([]any)[0].(map[string]any)["value"])
	assert.Equal(t, "original", decoded.RetryHint.PriorInput["query"])
	assert.Equal(t, "original", decoded.Artifacts[0].Metadata["kind"])
	assert.JSONEq(t, `{"ok":true}`, string(decoded.ResultJSON))

	*decoded.Bounds.Total = 10
	decoded.Telemetry.Extra["nested"].(map[string]any)["value"] = "decoded mutation"
	redecoded, err := DecodeToolResultData(Event{Type: EventToolResult, Data: stored})
	require.NoError(t, err)
	assert.Equal(t, 5, *redecoded.Bounds.Total)
	assert.Equal(t, "original", redecoded.Telemetry.Extra["nested"].(map[string]any)["value"])
}

func TestDecodeEventDataRejectsWrongKindsAndMalformedShapes(t *testing.T) {
	wrongType := Event{Type: EventPlannerNote, Data: map[string]any{}}
	_, err := DecodeUserMessageData(wrongType)
	require.ErrorContains(t, err, "as user_message")
	_, err = DecodeAssistantMessageData(wrongType)
	require.ErrorContains(t, err, "as assistant_message")
	_, err = DecodeToolCallData(wrongType)
	require.ErrorContains(t, err, "as tool_call")
	_, err = DecodeToolResultData(wrongType)
	require.ErrorContains(t, err, "as tool_result")
	_, err = DecodeThinkingData(wrongType)
	require.ErrorContains(t, err, "as thinking")

	cases := []struct {
		name  string
		data  any
		field string
	}{
		{name: "nil_map", data: nil, field: "data is nil"},
		{name: "wrong_map_type", data: []any{}, field: "must be map[string]any"},
		{name: "missing_tool_call_id", data: map[string]any{eventFieldToolName: "svc.tool"}, field: `missing "tool_call_id"`},
		{name: "invalid_tool_name", data: map[string]any{eventFieldToolCallID: "call", eventFieldToolName: 1}, field: `field "tool_name" must be string`},
		{name: "invalid_json", data: map[string]any{eventFieldToolCallID: "call", eventFieldToolName: "svc.tool", eventFieldPayload: func() {}}, field: "encode tool_call field"},
		{name: "fractional_children", data: map[string]any{eventFieldToolCallID: "call", eventFieldToolName: "svc.tool", eventFieldExpectedChildrenTotal: 1.5}, field: "must be an integer"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var data ToolCallData
			err := data.FromMap(tt.data)
			require.ErrorContains(t, err, tt.field)
		})
	}
}

func TestThinkingDataMalformedOptionalFields(t *testing.T) {
	cases := []struct {
		name  string
		data  map[string]any
		field string
	}{
		{name: "text", data: map[string]any{eventFieldText: 1}, field: `field "text" must be string`},
		{name: "redacted", data: map[string]any{eventFieldRedacted: "not-base64"}, field: "must be base64"},
		{name: "index", data: map[string]any{eventFieldContentIndex: 1.5}, field: "must be an integer"},
		{name: "final", data: map[string]any{eventFieldFinal: "true"}, field: "must be bool"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var data ThinkingData
			err := data.FromMap(tt.data)
			require.ErrorContains(t, err, tt.field)
		})
	}
}

func nestedName(value any) string {
	return value.(map[string]any)["items"].([]any)[0].(map[string]any)["name"].(string)
}
