package codex

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type scriptedSource struct {
	events [][]byte
	err    error
	closed int
}

func (s *scriptedSource) Next() ([]byte, error) {
	if len(s.events) == 0 {
		return nil, s.err
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *scriptedSource) Close() error {
	s.closed++
	return nil
}

func TestTerminalReconciliationDoesNotDuplicateFinalizedReasoning(t *testing.T) {
	client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		item := `{"id":"reason","type":"reasoning","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"thought"}]}`
		return sseResponse(http.StatusOK, sseEvents(
			`{"type":"codex.rate_limits","plan_type":"business","rate_limits":null}`,
			`{"type":"codex.response.metadata","headers":{"x-models-etag":"safe"}}`,
			`{"type":"responsesapi.websocket_timing","durations_ms":{"total":1}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning","encrypted_content":"draft"}}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":0,"summary_index":0,"delta":"thought"}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0,"text":"thought"}`,
			`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
			`{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[`+item+`]}}`,
		)), nil
	}))
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, response.Content, 2)
	redactedCount := 0
	for _, message := range response.Content {
		thinking, ok := message.Parts[0].(model.ThinkingPart)
		if ok && len(thinking.Redacted) > 0 {
			redactedCount++
			assert.Equal(t, []byte("opaque"), thinking.Redacted)
		}
	}
	assert.Equal(t, 1, redactedCount)
}

func TestTerminalReconciliationRejectsConflictingItems(t *testing.T) {
	client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, sseEvents(
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"item","output_index":0,"content_index":0,"delta":"safe"}`,
			`{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"item","type":"function_call","name":"tool","call_id":"call","arguments":"{}"}]}}`,
		)), nil
	}))
	stream, err := client.Stream(context.Background(), testRequest())
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	requireInvalidStreamError(t, err)
}

func TestAutomaticFallbackBoundary(t *testing.T) {
	cases := []struct {
		name   string
		events []string
	}{
		{name: "text", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"item","output_index":0,"content_index":0,"delta":"text"}`,
		}},
		{name: "thinking", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"item","output_index":0,"summary_index":0,"delta":"thought"}`,
		}},
		{name: "tool", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"item","output_index":0,"delta":"{"}`,
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			events := make([][]byte, 0, len(tt.events))
			for _, event := range tt.events {
				events = append(events, []byte(event))
			}
			fallbackCalls := 0
			stream := &codexStreamer{
				ctx:    context.Background(),
				source: &scriptedSource{events: events, err: &transportFailure{operation: "receive", cause: io.ErrUnexpectedEOF}},
				fallback: func() (eventSource, error) {
					fallbackCalls++
					return &scriptedSource{events: [][]byte{[]byte(emptyTerminalEvent())}}, nil
				},
				state: codexStreamState{codec: openaitoolname.New(0), modelID: "model", items: make(map[string]*itemState)},
			}
			_, err := stream.Recv()
			require.NoError(t, err)
			_, err = stream.Recv()
			require.Error(t, err)
			assert.Zero(t, fallbackCalls)
		})
	}
}

func TestAutomaticFallbackAfterProgressBeforeCanonicalOutput(t *testing.T) {
	initial := &scriptedSource{
		events: [][]byte{[]byte(`{"type":"response.created","response":{"id":"response","status":"in_progress"}}`)},
		err:    &transportFailure{operation: "receive", cause: io.ErrUnexpectedEOF},
	}
	fallbackCalls := 0
	stream := &codexStreamer{
		ctx:    context.Background(),
		source: initial,
		fallback: func() (eventSource, error) {
			fallbackCalls++
			return &scriptedSource{events: [][]byte{[]byte(emptyTerminalEvent())}, err: errors.New("not reached")}, nil
		},
		state: codexStreamState{codec: openaitoolname.New(0), modelID: "model", items: make(map[string]*itemState)},
	}
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, 1, fallbackCalls)
	assert.NotNil(t, stream.Response())
}

func TestStreamLatchesFatalErrorsAndDisablesFallback(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{name: "protocol", event: `{"type":"response.future"}`},
		{name: "provider", event: `{"type":"error","code":"rate_limit_exceeded","status":429}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fallbackCalls := 0
			stream := &codexStreamer{
				ctx: context.Background(),
				source: &scriptedSource{
					events: [][]byte{[]byte(tt.event), []byte(emptyTerminalEvent())},
					err:    &transportFailure{operation: "receive", cause: io.ErrUnexpectedEOF},
				},
				fallback: func() (eventSource, error) {
					fallbackCalls++
					return &scriptedSource{events: [][]byte{[]byte(emptyTerminalEvent())}}, nil
				},
				state: codexStreamState{codec: openaitoolname.New(0), modelID: "model", items: make(map[string]*itemState)},
			}
			_, first := stream.Recv()
			require.Error(t, first)
			_, second := stream.Recv()
			assert.Equal(t, first, second)
			assert.Zero(t, fallbackCalls)
		})
	}
}

func TestStreamReconcilesOutputItemIDAndIndex(t *testing.T) {
	client := newEventsClient(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
		`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"hel"}`,
		`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"hello"}`,
		`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"msg","type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`,
	)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	var text strings.Builder
	for _, message := range response.Content {
		for _, part := range message.Parts {
			if value, ok := part.(model.TextPart); ok {
				text.WriteString(value.Text)
			}
		}
	}
	assert.Equal(t, "hello", text.String())
}

func TestStreamRejectsMalformedTerminalState(t *testing.T) {
	tests := []struct {
		name   string
		events []string
	}{
		{name: "missing response", events: []string{`{"type":"response.completed"}`}},
		{name: "missing response id", events: []string{`{"type":"response.completed","response":{"status":"completed"}}`}},
		{name: "conflicting status", events: []string{`{"type":"response.incomplete","response":{"id":"resp","status":"completed"}}`}},
		{name: "unfinished message", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"partial"}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed"}}`,
		}},
		{name: "truncated function arguments", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"tool","output_index":0,"delta":"{\"q\":"}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"tool","type":"function_call","name":"tool","call_id":"call"}]}}`,
		}},
		{name: "unknown keyed item", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"other","output_index":0,"content_index":0,"delta":"wrong"}`,
		}},
		{name: "event family mismatch", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.output_text.delta","item_id":"reason","output_index":0,"content_index":0,"delta":"wrong"}`,
		}},
		{name: "missing progress response", events: []string{`{"type":"response.created"}`}},
		{name: "conflicting progress status", events: []string{`{"type":"response.created","response":{"id":"resp","status":"completed"}}`}},
		{name: "conflicting response identity", events: []string{
			`{"type":"response.created","response":{"id":"first","status":"in_progress"}}`,
			`{"type":"response.completed","response":{"id":"second","status":"completed"}}`,
		}},
		{name: "missing output index", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","content_index":0,"delta":"wrong"}`,
		}},
		{name: "conflicting item identity", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"first","type":"message"}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"second","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"first","output_index":1,"content_index":0,"delta":"wrong"}`,
		}},
		{name: "terminal item identity mismatch", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"first","type":"message"}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"second","type":"message"}}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"second","type":"message"},{"id":"first","type":"message"}]}}`,
		}},
		{name: "missing content index", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"delta":"wrong"}`,
		}},
		{name: "negative content index", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":-1,"delta":"wrong"}`,
		}},
		{name: "missing summary index", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":0,"delta":"wrong"}`,
		}},
		{name: "negative summary index", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":0,"summary_index":-1,"delta":"wrong"}`,
		}},
		{name: "missing message part payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.content_part.added","item_id":"msg","output_index":0,"content_index":0}`,
		}},
		{name: "missing reasoning part payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_part.added","item_id":"reason","output_index":0,"summary_index":0}`,
		}},
		{name: "negative output index", events: []string{
			`{"type":"response.output_item.added","output_index":-1,"item":{"id":"msg","type":"message"}}`,
		}},
		{name: "text delta after done", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"done"}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"late"}`,
		}},
		{name: "reasoning delta after done", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0,"text":"done"}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":0,"summary_index":0,"delta":"late"}`,
		}},
		{name: "tool delta after done", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.done","item_id":"tool","output_index":0,"arguments":"{}"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"tool","output_index":0,"delta":"late"}`,
		}},
		{name: "message part added after done", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"done"}`,
			`{"type":"response.content_part.added","item_id":"msg","output_index":0,"content_index":0,"part":{"type":"output_text"}}`,
		}},
		{name: "reasoning part added after done", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0,"text":"done"}`,
			`{"type":"response.reasoning_summary_part.added","item_id":"reason","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`,
		}},
		{name: "event after completed output item", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"late"}`,
		}},
		{name: "done event after completed output item", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"late"}`,
		}},
		{name: "reasoning done after completed output item", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0,"text":"late"}`,
		}},
		{name: "repeated output completion adds content", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg","type":"message","content":[{"type":"output_text","text":"late"}]}}`,
		}},
		{name: "missing terminal reasoning summary type", events: []string{
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"reason","type":"reasoning","summary":[{"text":"thought"}]}]}}`,
		}},
		{name: "unsupported terminal reasoning content type", events: []string{
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"reason","type":"reasoning","content":[{"type":"output_text","text":"thought"}]}]}}`,
		}},
		{name: "missing output text done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0}`,
		}},
		{name: "missing refusal done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.refusal.done","item_id":"msg","output_index":0,"content_index":0}`,
		}},
		{name: "missing reasoning done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0}`,
		}},
		{name: "missing function arguments done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.done","item_id":"tool","output_index":0}`,
		}},
		{name: "terminal reasoning changes completed encrypted content", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"reason","type":"reasoning","encrypted_content":"first"}}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"reason","type":"reasoning","encrypted_content":"second"}]}}`,
		}},
		{name: "missing reasoning text done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_text.done","item_id":"reason","output_index":0,"content_index":0}`,
		}},
		{name: "missing message part done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.content_part.done","item_id":"msg","output_index":0,"content_index":0,"part":{"type":"output_text"}}`,
		}},
		{name: "missing reasoning part done payload", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_part.done","item_id":"reason","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`,
		}},
		{name: "empty text completion conflicts with deltas", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"text"}`,
			`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":""}`,
		}},
		{name: "empty refusal completion conflicts with deltas", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.refusal.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"no"}`,
			`{"type":"response.refusal.done","item_id":"msg","output_index":0,"content_index":0,"refusal":""}`,
		}},
		{name: "empty reasoning completion conflicts with deltas", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reason","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":0,"summary_index":0,"delta":"thought"}`,
			`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":0,"summary_index":0,"text":""}`,
		}},
		{name: "empty function completion conflicts with deltas", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"tool","output_index":0,"delta":"{}"}`,
			`{"type":"response.function_call_arguments.done","item_id":"tool","output_index":0,"arguments":""}`,
		}},
		{name: "terminal message omits content", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"text"}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"msg","type":"message"}]}}`,
		}},
		{name: "terminal function omits arguments", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool","type":"function_call","name":"tool","call_id":"call"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"tool","output_index":0,"delta":"{}"}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"tool","type":"function_call","name":"tool","call_id":"call"}]}}`,
		}},
		{name: "terminal reasoning omits payload", events: []string{
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"reason","type":"reasoning"}]}}`,
		}},
		{name: "failed status conflict", events: []string{`{"type":"response.failed","response":{"id":"resp","status":"completed","error":{"code":"server_error"}}}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newEventsClient(t, tt.events...)
			stream, err := client.Stream(context.Background(), testRequest())
			require.NoError(t, err)
			for err == nil {
				_, err = stream.Recv()
			}
			requireInvalidStreamError(t, err)
		})
	}
}

func TestNormalizeUsageRejectsOverflow(t *testing.T) {
	for _, total := range []int{0, 1} {
		_, err := normalizeUsage(wireUsage{InputTokens: math.MaxInt, OutputTokens: 1, TotalTokens: total}, "model", model.ModelClassDefault)
		require.ErrorContains(t, err, "token usage total overflows")
	}
}

func TestStreamAcceptsDuplicateDoneMarkers(t *testing.T) {
	item := `{"id":"msg","type":"message","content":[{"type":"output_text","text":"done"}]}`
	client := newEventsClient(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
		`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"done"}`,
		`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"done"}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[`+item+`]}}`,
	)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	var text strings.Builder
	for _, message := range response.Content {
		for _, part := range message.Parts {
			if value, ok := part.(model.TextPart); ok {
				text.WriteString(value.Text)
			}
		}
	}
	assert.Equal(t, "done", text.String())
}

func TestStreamPreservesRefusalAndMultipleMessageParts(t *testing.T) {
	client := newEventsClient(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
		`{"type":"response.refusal.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"can"}`,
		`{"type":"response.refusal.done","item_id":"msg","output_index":0,"content_index":0,"refusal":"cannot"}`,
		`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":1,"delta":"ans"}`,
		`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":1,"text":"answer"}`,
		`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"msg","type":"message","content":[{"type":"refusal","refusal":"cannot"},{"type":"output_text","text":"answer"}]}]}}`,
	)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	var text strings.Builder
	for _, message := range response.Content {
		for _, part := range message.Parts {
			if value, ok := part.(model.TextPart); ok {
				text.WriteString(value.Text)
			}
		}
	}
	assert.Equal(t, "cannotanswer", text.String())
}

func TestStreamUsesResponseGlobalThinkingIndexes(t *testing.T) {
	client := newEventsClient(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"first","type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"first","output_index":0,"summary_index":0,"delta":"summary"}`,
		`{"type":"response.reasoning_summary_text.done","item_id":"first","output_index":0,"summary_index":0,"text":"summary"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"second","type":"reasoning"}}`,
		`{"type":"response.reasoning_text.delta","item_id":"second","output_index":1,"content_index":0,"delta":"detail"}`,
		`{"type":"response.reasoning_text.done","item_id":"second","output_index":1,"content_index":0,"text":"detail"}`,
		`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"first","type":"reasoning","summary":[{"type":"summary_text","text":"summary"}]},{"id":"second","type":"reasoning","content":[{"type":"reasoning_text","text":"detail"}]}]}}`,
	)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	thinking := make(map[int]string)
	for _, message := range response.Content {
		for _, part := range message.Parts {
			if value, ok := part.(model.ThinkingPart); ok {
				thinking[value.Index] = value.Text
			}
		}
	}
	assert.Equal(t, map[int]string{0: "summary", 1: "detail"}, thinking)
}

func TestStreamAcceptsResponseDoneAndNormalizesTopLevelErrors(t *testing.T) {
	client := newEventsClient(t, `{"type":"response.done","response":{"id":"resp","status":"completed"}}`)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	assert.Equal(t, "completed", response.StopReason)

	client = newEventsClient(t, `{"type":"error","code":"rate_limit_exceeded","status":429,"request_id":"request-1","message":"ignored provider detail"}`)
	_, err = client.Complete(context.Background(), testRequest())
	require.ErrorIs(t, err, model.ErrRateLimited)
	providerErr, ok := model.AsProviderError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusTooManyRequests, providerErr.HTTPStatus())
	assert.Equal(t, model.ProviderErrorKindRateLimited, providerErr.Kind())
	assert.Equal(t, "rate_limit_exceeded", providerErr.Code())
	assert.Equal(t, "request-1", providerErr.RequestID())
	assert.NotContains(t, err.Error(), "ignored provider detail")

	client = newEventsClient(t, `{"type":"response.failed","response":{"id":"resp","status":"failed","error":{"code":"server_error","status":500,"request_id":"request-2"}}}`)
	_, err = client.Complete(context.Background(), testRequest())
	providerErr, ok = model.AsProviderError(err)
	require.True(t, ok)
	assert.Equal(t, model.ProviderErrorKindUnavailable, providerErr.Kind())
	assert.Equal(t, http.StatusInternalServerError, providerErr.HTTPStatus())
	assert.True(t, providerErr.Retryable())

	client = newEventsClient(t, `{"type":"error","code":"future_error"}`)
	_, err = client.Complete(context.Background(), testRequest())
	providerErr, ok = model.AsProviderError(err)
	require.True(t, ok)
	assert.Equal(t, model.ProviderErrorKindUnknown, providerErr.Kind())
	assert.Zero(t, providerErr.HTTPStatus())
}

func TestStreamRejectsUnknownEventWithoutFallback(t *testing.T) {
	fallbackCalls := 0
	stream := &codexStreamer{
		ctx:    context.Background(),
		source: &scriptedSource{events: [][]byte{[]byte(`{"type":"response.future_event"}`)}},
		fallback: func() (eventSource, error) {
			fallbackCalls++
			return &scriptedSource{}, nil
		},
		state: codexStreamState{codec: openaitoolname.New(0), modelID: "model", items: make(map[string]*itemState)},
	}
	_, err := stream.Recv()
	requireInvalidStreamError(t, err)
	assert.Zero(t, fallbackCalls)
}

func newEventsClient(t *testing.T, events ...string) *Client {
	t.Helper()
	return newEventsClientForTimeout(t, defaultIdleTimeout, events...)
}
