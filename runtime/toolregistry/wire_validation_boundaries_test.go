package toolregistry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallWireValidationRejectsInvalidLifetimeAndIdentity(t *testing.T) {
	token := strings.Repeat("a", 64)
	tests := []struct {
		name   string
		mutate func(*ToolCallMessage)
		want   string
	}{
		{"missing ping", func(m *ToolCallMessage) {
			m.Type = MessageTypePing
		}, "ping ID"},
		{"missing use", func(m *ToolCallMessage) {
			m.ToolUseID = ""
		}, "tool use"},
		{"missing deadline", func(m *ToolCallMessage) {
			m.ExecutionDeadlineUnixMilli = 0
		}, "execution deadline"},
		{"missing expiration", func(m *ToolCallMessage) {
			m.ResultStreamExpiresAtUnixMilli = 0
		}, "result stream expiration"},
		{"reversed lifetime", func(m *ToolCallMessage) {
			m.ExecutionDeadlineUnixMilli = m.ResultStreamExpiresAtUnixMilli
		}, "precede"},
		{"missing metadata", func(m *ToolCallMessage) {
			m.Meta = nil
		}, "metadata"},
		{"missing run", func(m *ToolCallMessage) {
			m.Meta.RunID = ""
		}, "metadata"},
		{"missing session", func(m *ToolCallMessage) {
			m.Meta.SessionID = ""
		}, "metadata"},
		{"missing call", func(m *ToolCallMessage) {
			m.Meta.ToolCallID = ""
		}, "metadata"},
		{"oversized optional identity", func(m *ToolCallMessage) {
			m.Meta.ParentToolCallID = strings.Repeat("x", MaxToolCallMetaIDLength+1)
		}, "exceeds"},
		{"embedded NUL", func(m *ToolCallMessage) {
			m.Meta.TurnID = "turn\x00"
		}, "NUL"},
		{"unknown message", func(m *ToolCallMessage) {
			m.Type = "future"
		}, "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewToolCallMessage(token, "use", time.UnixMilli(1000), time.UnixMilli(2000), "read", json.RawMessage(`{}`), &ToolCallMeta{RunID: "run", SessionID: "session", ToolCallID: "call"})
			require.NoError(t, ValidateToolCallMessage(m))
			tt.mutate(&m)
			assert.ErrorContains(t, ValidateToolCallMessage(m), tt.want)
		})
	}
}

func TestDecodeServerDataPreservesTypedEnvelopeAndRejectsCorruption(t *testing.T) {
	items, err := DecodeServerData([]byte(`[{"kind":"card","audience":"ui","data":{"title":"Done"}}]`))
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "card", items[0].Kind)
	assert.Equal(t, "ui", items[0].Audience)
	assert.JSONEq(t, `{"title":"Done"}`, string(items[0].Data))
	items, err = DecodeServerData(nil)
	require.NoError(t, err)
	assert.Nil(t, items)
	items, err = DecodeServerData([]byte(`{`))
	require.ErrorContains(t, err, "decode server data")
	assert.Nil(t, items)
}

func TestResultWireValidationRejectsIncompleteErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*ToolResultMessage)
		want   string
	}{
		{"code", func(m *ToolResultMessage) {
			m.Error.Code = ""
		}, "error code"},
		{"message", func(m *ToolResultMessage) {
			m.Error.Message = ""
		}, "error message"},
		{"server data", func(m *ToolResultMessage) {
			m.ServerData = []*ServerDataItem{{Kind: "card"}}
		}, "error and server data"},
		{"invalid result JSON", func(m *ToolResultMessage) {
			m.Result = json.RawMessage(`{`)
		}, "encode tool result"},
		{"missing use", func(m *ToolResultMessage) {
			m.ToolUseID = ""
		}, "tool use"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := NewToolResultErrorMessage(strings.Repeat("a", 64), "use", "failed", "Failed safely")
			require.NoError(t, ValidateToolResultMessage(m))
			tt.mutate(&m)
			assert.ErrorContains(t, ValidateToolResultMessage(m), tt.want)
		})
	}
}
