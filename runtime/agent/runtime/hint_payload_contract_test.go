package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	rthints "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingHintSink struct{ closed bool }

func TestHintPayloadRepresentationsPreserveTypedHintsAndRejectMalformedJSON(t *testing.T) {
	id := tools.Ident("runtime.hints.contract.representations")
	rthints.RegisterCallHint(id, mustTemplate(t, id, "Resolution={{.Resolution}}"))
	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{id: newTypedHintSpec(id)}, logger: telemetry.NoopLogger{}}
	for _, tc := range []struct {
		name    string
		payload rawjson.Message
		want    string
	}{
		{"raw", rawjson.Message(`{"resolution":"hourly"}`), "Resolution=hourly"},
		{"empty", nil, "Resolution="},
		{"malformed", rawjson.Message("{"), ""},
		{"wrong type", rawjson.Message(`{"resolution":true}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &hintRecordingStreamSink{}
			decorated := newHintingSink(rt, sink)
			data := stream.ToolStartPayload{ToolCallID: "call", ToolName: string(id), Payload: tc.payload}
			require.NoError(t, decorated.Send(t.Context(), stream.ToolStart{Base: stream.NewBase(stream.EventToolStart, "run", "session", data), Data: data}))
			require.Len(t, sink.events, 1)
			event := sink.events[0].(stream.ToolStart)
			assert.Equal(t, tc.want, event.Data.DisplayHint)
			assert.Equal(t, "run", event.RunID())
			assert.Equal(t, "session", event.SessionID())
			assert.Equal(t, tc.payload, event.Data.Payload)
		})
	}
}

func TestHintSinkPropagatesDeliveryAndCloseFailures(t *testing.T) {
	sink := &failingHintSink{}
	decorated := newHintingSink(New(), sink)
	require.ErrorIs(t, decorated.Send(t.Context(), stream.ToolStart{}), assert.AnError)
	require.ErrorIs(t, decorated.Close(t.Context()), assert.AnError)
	assert.True(t, sink.closed)
}

func (s *failingHintSink) Send(context.Context, stream.Event) error {
	return assert.AnError
}

func (s *failingHintSink) Close(context.Context) error {
	s.closed = true
	return assert.AnError
}
