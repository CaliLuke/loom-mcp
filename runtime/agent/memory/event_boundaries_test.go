package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Persisted input may come from JSON storage or an in-process typed store.
// Both forms must reject corruption without silently producing a valid event.
func TestEventDecodersRejectCorruptFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		base   map[string]any
		fields []string
		decode func(any) error
	}{
		{"user", map[string]any{}, []string{"message"}, new(UserMessageData).FromMap},
		{"assistant", map[string]any{}, []string{"message"}, new(AssistantMessageData).FromMap},
		{"call", map[string]any{"tool_call_id": "call", "tool_name": "docs.read"}, []string{"tool_call_id", "tool_name", "parent_tool_call_id", "queue", "payload", "expected_children_total"}, new(ToolCallData).FromMap},
		{"result", map[string]any{"tool_call_id": "call", "tool_name": "docs.read"}, []string{"tool_call_id", "tool_name", "parent_tool_call_id", "result_json", "server_data", "preview", "bounds", "duration", "telemetry", "retry_hint", "error_message", "artifacts"}, new(ToolResultData).FromMap},
		{"note", map[string]any{"note": "keep"}, []string{"note"}, new(PlannerNoteData).FromMap},
		{"thinking", map[string]any{}, []string{"text", "signature", "redacted", "content_index", "final"}, new(ThinkingData).FromMap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.decode(42))
			for _, field := range tc.fields {
				t.Run(field, func(t *testing.T) {
					data := make(map[string]any)
					for k, v := range tc.base {
						data[k] = v
					}
					data[field] = make(chan int)
					err := tc.decode(data)
					require.Error(t, err)
					assert.Contains(t, err.Error(), field)
				})
			}
		})
	}
}

func TestEventDecodersRejectTypedNil(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		decode func(any) error
	}{
		{"user", (*UserMessageData)(nil), new(UserMessageData).FromMap},
		{"assistant", (*AssistantMessageData)(nil), new(AssistantMessageData).FromMap},
		{"call", (*ToolCallData)(nil), new(ToolCallData).FromMap},
		{"result", (*ToolResultData)(nil), new(ToolResultData).FromMap},
		{"note", (*PlannerNoteData)(nil), new(PlannerNoteData).FromMap},
		{"thinking", (*ThinkingData)(nil), new(ThinkingData).FromMap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, tc.decode(tc.value), "nil")
		})
	}
}

func TestTypedEventCopiesOwnMutableData(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		t.Run(fmt.Sprint(pointer), func(t *testing.T) {
			user := UserMessageData{Message: "question", Structured: map[string]any{"items": []any{"original"}}}
			assistant := AssistantMessageData{Message: "answer", Structured: map[string]any{"items": []any{"original"}}}
			call := ToolCallData{ToolCallID: "call", ToolName: "docs.read", PayloadJSON: rawjson.Message(`{"id":1}`)}
			result := ToolResultData{ToolCallID: "call", ToolName: "docs.read", ResultJSON: rawjson.Message(`{"id":1}`), ServerData: rawjson.Message(`{"private":true}`), Bounds: &agent.Bounds{Returned: 1}, Telemetry: &telemetry.ToolTelemetry{Extra: map[string]any{"key": "original"}}, RetryHint: &RetryHintData{MissingFields: []string{"original"}}, Artifacts: []artifact.Ref{{Metadata: map[string]string{"key": "original"}}}}
			note := PlannerNoteData{Note: "keep"}
			thinking := ThinkingData{Text: "text", Redacted: []byte("opaque"), Signature: "signature", ContentIndex: 2, Final: true}
			var u UserMessageData
			var a AssistantMessageData
			var c ToolCallData
			var r ToolResultData
			var n PlannerNoteData
			var th ThinkingData
			values := []any{user, assistant, call, result, note, thinking}
			if pointer {
				values = []any{&user, &assistant, &call, &result, &note, &thinking}
			}
			require.NoError(t, u.FromMap(values[0]))
			require.NoError(t, a.FromMap(values[1]))
			require.NoError(t, c.FromMap(values[2]))
			require.NoError(t, r.FromMap(values[3]))
			require.NoError(t, n.FromMap(values[4]))
			require.NoError(t, th.FromMap(values[5]))
			assert.Equal(t, user, u)
			assert.Equal(t, assistant, a)
			assert.Equal(t, call, c)
			assert.Equal(t, result, r)
			assert.Equal(t, note, n)
			assert.Equal(t, thinking, th)
			u.Structured.(map[string]any)["items"].([]any)[0] = "changed"
			a.Structured.(map[string]any)["items"].([]any)[0] = "changed"
			c.PayloadJSON[0] = '!'
			r.ResultJSON[0] = '!'
			r.ServerData[0] = '!'
			r.Bounds.Returned = 9
			r.Telemetry.Extra["key"] = "changed"
			r.RetryHint.MissingFields[0] = "changed"
			r.Artifacts[0].Metadata["key"] = "changed"
			th.Redacted[0] = '!'
			assert.Equal(t, "original", user.Structured.(map[string]any)["items"].([]any)[0])
			assert.Equal(t, "original", assistant.Structured.(map[string]any)["items"].([]any)[0])
			assert.JSONEq(t, `{"id":1}`, string(call.PayloadJSON))
			assert.JSONEq(t, `{"id":1}`, string(result.ResultJSON))
			assert.JSONEq(t, `{"private":true}`, string(result.ServerData))
			assert.Equal(t, 1, result.Bounds.Returned)
			assert.Equal(t, "original", result.Telemetry.Extra["key"])
			assert.Equal(t, []string{"original"}, result.RetryHint.MissingFields)
			assert.Equal(t, "original", result.Artifacts[0].Metadata["key"])
			assert.Equal(t, []byte("opaque"), thinking.Redacted)
		})
	}
}

func TestEventStorageRepresentations(t *testing.T) {
	for _, value := range []any{int(2), int8(2), int16(2), int32(2), int64(2), float64(2)} {
		t.Run(fmt.Sprintf("integer_%T", value), func(t *testing.T) {
			var data ThinkingData
			require.NoError(t, data.FromMap(map[string]any{"content_index": value}))
			assert.Equal(t, 2, data.ContentIndex)
		})
	}
	for _, value := range []any{time.Duration(2), int(2), int32(2), int64(2), float64(2)} {
		t.Run(fmt.Sprintf("duration_%T", value), func(t *testing.T) {
			var data ToolResultData
			require.NoError(t, data.FromMap(map[string]any{"tool_call_id": "call", "tool_name": "docs.read", "duration": value}))
			assert.Equal(t, time.Duration(2), data.Duration)
		})
	}
	for _, value := range []any{agent.Bounds{Returned: 2}, map[string]any{"Returned": 2}} {
		var data ToolResultData
		require.NoError(t, data.FromMap(map[string]any{"tool_call_id": "call", "tool_name": "docs.read", "bounds": value, "telemetry": telemetry.ToolTelemetry{TokensUsed: 3}, "retry_hint": RetryHintData{Message: "retry"}, "artifacts": []any{map[string]any{"metadata": map[string]any{"key": "value"}}}}))
		assert.Equal(t, 2, data.Bounds.Returned)
		assert.Equal(t, 3, data.Telemetry.TokensUsed)
		assert.Equal(t, "retry", data.RetryHint.Message)
		require.Len(t, data.Artifacts, 1)
		assert.Equal(t, "value", data.Artifacts[0].Metadata["key"])
	}
	for _, value := range []any{"", []byte{}, []byte("opaque"), "b3BhcXVl"} {
		var data ThinkingData
		require.NoError(t, data.FromMap(map[string]any{"redacted": value}))
		assert.Contains(t, []string{"", "opaque"}, string(data.Redacted))
	}
}
