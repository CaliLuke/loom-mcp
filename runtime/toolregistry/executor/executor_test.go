package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	aistream "github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	testRegistrationTokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testRegistrationTokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestRegistryValidationRetryHintPreservesMissingFieldBehaviorAndBounds(t *testing.T) {
	t.Parallel()

	issues := make([]*tools.FieldIssue, 0, 64)
	for index := range 64 {
		issues = append(issues, &tools.FieldIssue{
			Field:      fmt.Sprintf("field_%02d_%s", index, strings.Repeat("界", 128)),
			Constraint: "missing_field",
		})
	}
	hint := retryHintFromRegistryError(&toolregistry.ToolError{
		Code:   toolregistry.ToolErrorCodeInvalidArguments,
		Issues: issues,
	}, "svc.registry")

	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonMissingFields, hint.Reason)
	require.NotEmpty(t, hint.ClarifyingQuestion)
	require.LessOrEqual(t, len(hint.MissingFields), 8)
	encoded, err := json.Marshal(hint)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 4096)
	require.Nil(t, hint.ExampleInput)
	require.Nil(t, hint.PriorInput)
}

func TestRegistryValidationRetryHintKeepsNonMissingIssuesInvalid(t *testing.T) {
	t.Parallel()

	hint := retryHintFromRegistryError(&toolregistry.ToolError{
		Code: toolregistry.ToolErrorCodeInvalidArguments,
		Issues: []*tools.FieldIssue{{
			Field:      "format",
			Constraint: "invalid_enum_value",
		}},
	}, "svc.registry")

	require.NotNil(t, hint)
	require.Equal(t, planner.RetryReasonInvalidArguments, hint.Reason)
	require.Equal(t, []string{"format"}, hint.MissingFields)
	require.Empty(t, hint.ClarifyingQuestion)
}

func TestExecutorUsesOldestStartForResultStreamReader(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-123"
		resultStreamID  = "result:" + toolUseID
		resultEventName = toolregistry.ResultEventKey
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}

	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
		},
	}
	pc := fakePulseClient{
		streamID: resultStreamID,
		stream:   stream,
	}

	exec := New(fakeRegistryClient{
		toolUseID: toolUseID,
	}, pc, specs)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	assert.NotNil(t, res)
	require.NotNil(t, res.ToolResult)
	assert.Equal(t, tools.Ident("todos.update_todos"), res.ToolResult.Name)
	assert.False(t, stream.destroyed)
}

func TestExecutorSequentialAndConcurrentWaitersReplayTerminalHistory(t *testing.T) {
	t.Parallel()

	const toolUseID = "replay-tool-use-id"
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{{
			ID:        "1-0",
			EventName: toolregistry.ResultEventKey,
			Payload: mustJSON(t, toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Result:            json.RawMessage(`{}`),
			}),
		}},
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)
	execute := func() (*agentsruntime.ToolExecutionResult, error) {
		return exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
			RunID:      "run",
			SessionID:  "session",
			ToolCallID: "call-1",
		}, &planner.ToolRequest{
			Name:    "todos.update_todos",
			Payload: []byte(`{}`),
		})
	}

	first, err := execute()
	require.NoError(t, err)
	require.NotNil(t, first)
	results := make(chan *agentsruntime.ToolExecutionResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			result, executeErr := execute()
			results <- result
			errs <- executeErr
		}()
	}
	for range 2 {
		require.NoError(t, <-errs)
		require.NotNil(t, <-results)
	}
	assert.Empty(t, stream.acked)
}

func TestExecutorRejectsMalformedTerminalHistoryImmediately(t *testing.T) {
	t.Parallel()

	const toolUseID = "malformed-tool-use-id"
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{{
			ID:        "1-0",
			EventName: toolregistry.ResultEventKey,
			Payload:   []byte(`{"registration_token":`),
		}},
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)

	_, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "session",
		ToolCallID: "call-1",
	}, &planner.ToolRequest{Name: "todos.update_todos", Payload: []byte(`{}`)})
	require.ErrorContains(t, err, "decode terminal tool result event 1-0")
}

func TestExecutorAsksRegistryToRetryTransientProviderOverload(t *testing.T) {
	t.Parallel()

	const toolUseID = "overload-tool-use-id"
	overloaded := toolregistry.NewToolResultRetryMessage(
		testRegistrationTokenA,
		toolUseID,
		toolregistry.ToolRetryReasonProviderOverloaded,
		toolregistry.ProviderOverloadRetryAfter,
	)
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: toolregistry.ResultEventKey,
				Payload:   mustJSON(t, overloaded),
			},
			{
				ID:        "2-0",
				EventName: toolregistry.ResultEventKey,
				Payload: mustJSON(t, toolregistry.NewToolResultMessage(
					testRegistrationTokenA,
					toolUseID,
					json.RawMessage(`{}`),
				)),
			},
		},
	}
	var calls atomic.Int64
	var retryToken string
	exec := New(
		fakeRegistryClient{
			toolUseID:          toolUseID,
			calls:              &calls,
			retryExpectedToken: &retryToken,
		},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)

	result, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "session",
		ToolCallID: "call-1",
	}, &planner.ToolRequest{Name: "todos.update_todos", Payload: []byte(`{}`)})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(2), calls.Load())
	assert.Equal(t, testRegistrationTokenA, retryToken)
}

func TestExecutorRejectsMalformedRetryControl(t *testing.T) {
	t.Parallel()

	const (
		toolUseID  = "malformed-retry-tool-use-id"
		toolCallID = "malformed-retry-tool-call-id"
	)
	validRetry := &toolregistry.ToolRetry{
		Reason:           toolregistry.ToolRetryReasonProviderOverloaded,
		RetryAfterMillis: toolregistry.ProviderOverloadRetryAfter.Milliseconds(),
	}
	terminalError := toolregistry.NewToolResultErrorMessage(
		testRegistrationTokenA,
		toolUseID,
		"execution_failed",
		"failed",
	).Error
	tests := []struct {
		name string
		msg  toolregistry.ToolResultMessage
		want string
	}{
		{
			name: "retry and result",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Result:            json.RawMessage(`{"ok":true}`),
				Retry:             validRetry,
			},
			want: "retry and result are both set",
		},
		{
			name: "retry and error",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Error:             terminalError,
				Retry:             validRetry,
			},
			want: "retry and error are both set",
		},
		{
			name: "retry and bounds",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Bounds:            &agent.Bounds{},
				Retry:             validRetry,
			},
			want: "retry and bounds are both set",
		},
		{
			name: "retry and server data",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				ServerData: []*toolregistry.ServerDataItem{{
					Kind: "test",
					Data: json.RawMessage(`{}`),
				}},
				Retry: validRetry,
			},
			want: "retry and server data are both set",
		},
		{
			name: "unknown reason",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Retry: &toolregistry.ToolRetry{
					Reason:           "try_again",
					RetryAfterMillis: toolregistry.ProviderOverloadRetryAfter.Milliseconds(),
				},
			},
			want: "unsupported tool retry reason",
		},
		{
			name: "missing delay",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Retry: &toolregistry.ToolRetry{
					Reason: toolregistry.ToolRetryReasonProviderOverloaded,
				},
			},
			want: "tool retry delay 0ms is outside",
		},
		{
			name: "excessive delay",
			msg: toolregistry.ToolResultMessage{
				RegistrationToken: testRegistrationTokenA,
				ToolUseID:         toolUseID,
				Retry: &toolregistry.ToolRetry{
					Reason: toolregistry.ToolRetryReasonProviderOverloaded,
					RetryAfterMillis: toolregistry.MaxProviderOverloadRetryAfter.Milliseconds() +
						1,
				},
			},
			want: "tool retry delay",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := executeRegistryResultMessage(
				t,
				toolUseID,
				toolCallID,
				test.msg,
				&tools.ToolSpec{
					Name:    "todos.update_todos",
					Toolset: "todos.todos",
					Result:  tools.TypeSpec{},
					Payload: tools.TypeSpec{},
				},
			)
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), test.want)
			assert.Contains(t, err.Error(), "tool_call_id="+toolCallID)
			assert.Contains(t, err.Error(), "tool_use_id="+toolUseID)
		})
	}
}

func TestExecutorIgnoresLateResultFromReusedToolUseID(t *testing.T) {
	t.Parallel()

	const toolUseID = "reused-tool-use-id"
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: toolregistry.ResultEventKey,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenB,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
			{
				ID:        "2-0",
				EventName: toolregistry.ResultEventKey,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
		},
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)

	result, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "session",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, stream.acked)
}

func TestExecutorRejectsNoncanonicalRegistrationToken(t *testing.T) {
	t.Parallel()

	exec := New(
		fakeRegistryClient{
			toolUseID:         "tooluse-invalid-token",
			registrationToken: "ABC123",
		},
		fakePulseClient{},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)

	result, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "session",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ToolResult.Error)
	assert.Nil(t, result.ToolResult.RetryHint)
	assert.Contains(t, result.ToolResult.Error.Error(), toolregistry.ToolErrorCodeOutcomeUnknown)
}

func TestExecutorDerivesResultStreamIDFromToolUseID(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-derive-123"
		resultEventName = toolregistry.ResultEventKey
	)
	expectedResultStreamID := toolregistry.ResultStreamID(toolUseID)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}

	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
		},
	}
	pc := fakePulseClient{
		streamID: expectedResultStreamID,
		stream:   stream,
	}

	exec := New(fakeRegistryClient{
		toolUseID: toolUseID,
	}, pc, specs)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	assert.NotNil(t, res)
	require.NotNil(t, res.ToolResult)
	assert.Equal(t, tools.Ident("todos.update_todos"), res.ToolResult.Name)
}

func TestExecutorWaitsOnlyThroughExecutionDeadline(t *testing.T) {
	t.Parallel()

	const toolUseID = "tooluse-deadline"
	deadline := time.Now().Add(30 * time.Millisecond).UTC().Truncate(time.Millisecond)
	stream := &fakeStream{t: t, requiredStart: "0", keepOpen: true}
	exec := New(
		fakeRegistryClient{
			toolUseID:         toolUseID,
			executionDeadline: deadline,
		},
		fakePulseClient{
			streamID: toolregistry.ResultStreamID(toolUseID),
			stream:   stream,
		},
		fakeSpecs{spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		}},
	)

	result, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "session",
		ToolCallID: "call",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ToolResult.Error)
	assert.Nil(t, result.ToolResult.RetryHint)
	assert.Contains(t, result.ToolResult.Error.Error(), toolregistry.ToolErrorCodeOutcomeUnknown)
}

func TestExecutorEmitsRegistrySpan(t *testing.T) {
	tracer := &recordingTracer{}
	const (
		toolUseID       = "tooluse-genai-123"
		resultEventName = toolregistry.ResultEventKey
	)
	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
		},
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		specs,
		WithTracer(tracer),
	)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "sess",
		TurnID:     "turn",
		ToolCallID: "toolcall-1",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Len(t, tracer.spans, 1)
	assert.Equal(t, "toolregistry.execute", tracer.spans[0].name)
	attrs := attrsByKey(tracer.spans[0].attrs)
	assert.Equal(t, "todos.update_todos", attrs[attribute.Key("toolregistry.tool")].AsString())
	assert.Equal(t, "todos.todos", attrs[attribute.Key("toolregistry.toolset")].AsString())
	assert.Equal(t, "toolcall-1", attrs[attribute.Key("toolregistry.tool_call_id")].AsString())
	_, hasConsumerGroup := attrs[attribute.Key("toolregistry.sink")]
	assert.False(t, hasConsumerGroup)
}

type captureSink struct {
	events []aistream.Event
}

type recordingTracer struct {
	spans []*recordingSpan
}

type recordingSpan struct {
	name  string
	attrs []attribute.KeyValue
}

func (s *captureSink) Send(ctx context.Context, event aistream.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *captureSink) Close(ctx context.Context) error {
	return nil
}

func TestExecutorForwardsOnlyExactAdmissionOutputDeltaForReusedToolUseID(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-123"
		resultStreamID  = "result:" + toolUseID
		resultEventName = toolregistry.ResultEventKey
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    "todos.update_todos",
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}

	malformedDelta := toolregistry.NewToolOutputDeltaMessage(
		"ABC123",
		toolUseID,
		"stdout",
		"malformed\n",
	)
	staleDelta := toolregistry.NewToolOutputDeltaMessage(
		testRegistrationTokenB,
		toolUseID,
		"stdout",
		"stale\n",
	)
	delta := toolregistry.NewToolOutputDeltaMessage(
		testRegistrationTokenA,
		toolUseID,
		"stdout",
		"hi\n",
	)
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: toolregistry.OutputDeltaEventKey,
				Payload:   mustJSON(t, malformedDelta),
			},
			{
				ID:        "2-0",
				EventName: toolregistry.OutputDeltaEventKey,
				Payload:   mustJSON(t, staleDelta),
			},
			{
				ID:        "3-0",
				EventName: toolregistry.OutputDeltaEventKey,
				Payload:   mustJSON(t, delta),
			},
			{
				ID:        "4-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
				}),
			},
		},
	}
	pc := fakePulseClient{
		streamID: resultStreamID,
		stream:   stream,
	}

	sink := &captureSink{}
	exec := New(
		fakeRegistryClient{
			toolUseID: toolUseID,
		},
		pc,
		specs,
		WithStreamSink(sink),
	)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "sess",
		ToolCallID: "toolcall-1",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Len(t, sink.events, 1)
	assert.Empty(t, stream.acked)
	ev, ok := sink.events[0].(aistream.ToolOutputDelta)
	require.True(t, ok)
	assert.Equal(t, aistream.EventToolOutputDelta, ev.Type())
	assert.Equal(t, "run", ev.RunID())
	assert.Equal(t, "sess", ev.SessionID())
	assert.Equal(t, "toolcall-1", ev.Data.ToolCallID)
	assert.Equal(t, "stdout", ev.Data.Stream)
	assert.Equal(t, "hi\n", ev.Data.Delta)
}

func TestExecutorRestoresBoundsFromRegistryMessage(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-123"
		resultStreamID  = "result:" + toolUseID
		resultEventName = toolregistry.ResultEventKey
	)
	nextCursor := "cursor-2"

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    "atlas.read.list_devices",
			Toolset: "atlas.read",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
			Bounds: &tools.BoundsSpec{
				Paging: &tools.PagingSpec{
					CursorField:     "cursor",
					NextCursorField: "next_cursor",
				},
			},
		},
	}

	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					RegistrationToken: testRegistrationTokenA,
					ToolUseID:         toolUseID,
					Result:            json.RawMessage(`{}`),
					Bounds: &agent.Bounds{
						Returned:       1,
						Truncated:      true,
						NextCursor:     &nextCursor,
						RefinementHint: "narrow by device",
					},
				}),
			},
		},
	}
	pc := fakePulseClient{
		streamID: resultStreamID,
		stream:   stream,
	}

	exec := New(
		fakeRegistryClient{
			toolUseID: toolUseID,
		},
		pc,
		specs,
	)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "atlas.read.list_devices",
		Payload: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.ToolResult)
	require.NotNil(t, res.ToolResult.Bounds)
	assert.Equal(t, 1, res.ToolResult.Bounds.Returned)
	assert.True(t, res.ToolResult.Bounds.Truncated)
	require.NotNil(t, res.ToolResult.Bounds.NextCursor)
	assert.Equal(t, nextCursor, *res.ToolResult.Bounds.NextCursor)
	assert.Equal(t, "narrow by device", res.ToolResult.Bounds.RefinementHint)
}

type fakeRegistryClient struct {
	toolUseID          string
	registrationToken  string
	executionDeadline  time.Time
	resultStreamTTL    time.Duration
	err                error
	calls              *atomic.Int64
	retryExpectedToken *string
}

func (c fakeRegistryClient) CallTool(
	context.Context,
	string,
	tools.Ident,
	[]byte,
	toolregistry.ToolCallMeta,
) (toolregistry.ToolCallRef, error) {
	if c.calls != nil {
		c.calls.Add(1)
	}
	if c.err != nil {
		return toolregistry.ToolCallRef{}, c.err
	}
	registrationToken := c.registrationToken
	if registrationToken == "" {
		registrationToken = testRegistrationTokenA
	}
	executionDeadline := c.executionDeadline
	if executionDeadline.IsZero() {
		executionDeadline = testResultStreamExpiration(toolregistry.MaxToolCallWait)
	}
	resultStreamTTL := c.resultStreamTTL
	if resultStreamTTL == 0 {
		resultStreamTTL = toolregistry.DefaultResultStreamTTL
	}
	return toolregistry.ToolCallRef{
		ToolUseID:             c.toolUseID,
		RegistrationToken:     registrationToken,
		ExecutionDeadline:     executionDeadline,
		ResultStreamExpiresAt: testResultStreamExpiration(resultStreamTTL),
	}, nil
}

func (c fakeRegistryClient) RetryTool(
	_ context.Context,
	_ string,
	_ tools.Ident,
	_ []byte,
	_ toolregistry.ToolCallMeta,
	expectedRegistrationToken string,
) (toolregistry.ToolCallRef, error) {
	if c.calls != nil {
		c.calls.Add(1)
	}
	if c.retryExpectedToken != nil {
		*c.retryExpectedToken = expectedRegistrationToken
	}
	if c.err != nil {
		return toolregistry.ToolCallRef{}, c.err
	}
	resultStreamTTL := c.resultStreamTTL
	if resultStreamTTL == 0 {
		resultStreamTTL = toolregistry.DefaultResultStreamTTL
	}
	executionDeadline := c.executionDeadline
	if executionDeadline.IsZero() {
		executionDeadline = testResultStreamExpiration(toolregistry.MaxToolCallWait)
	}
	return toolregistry.ToolCallRef{
		ToolUseID:             c.toolUseID,
		RegistrationToken:     expectedRegistrationToken,
		ExecutionDeadline:     executionDeadline,
		ResultStreamExpiresAt: testResultStreamExpiration(resultStreamTTL),
	}, nil
}

func testResultStreamExpiration(retention time.Duration) time.Time {
	return time.Now().UTC().Truncate(time.Minute).Add(retention)
}

type fakeSpecs struct {
	spec *tools.ToolSpec
}

func (s fakeSpecs) Spec(name tools.Ident) (*tools.ToolSpec, bool) {
	if s.spec == nil {
		return nil, false
	}
	if s.spec.Name != name {
		return nil, false
	}
	return s.spec, true
}

type fakePulseClient struct {
	streamID string
	stream   pulse.Stream
}

func (c fakePulseClient) Stream(name string, opts ...streamopts.Stream) (pulse.Stream, error) {
	if name != c.streamID {
		return nil, assert.AnError
	}
	parsed := streamopts.ParseStreamOptions(opts...)
	assert.Equal(c.stream.(*fakeStream).t, toolregistry.ResultStreamMaxLen, parsed.MaxLen)
	assert.Zero(c.stream.(*fakeStream).t, parsed.TTL, "the registry owns the absolute result-stream expiration")
	return c.stream, nil
}

func (c fakePulseClient) Close(ctx context.Context) error {
	return nil
}

type fakeStream struct {
	t             *testing.T
	requiredStart string
	events        []*streaming.Event
	acked         []*streaming.Event
	destroyed     bool
	keepOpen      bool
}

func (s *fakeStream) Add(ctx context.Context, event string, payload []byte) (string, error) {
	return "", assert.AnError
}

func (s *fakeStream) Snapshot(context.Context) ([]pulse.SnapshotEvent, error) {
	return nil, nil
}

func (s *fakeStream) Open(context.Context) error {
	s.t.Helper()
	s.t.Error("executor must not establish registry-owned result streams")
	return assert.AnError
}

func (s *fakeStream) NewSink(ctx context.Context, name string, opts ...streamopts.Sink) (pulse.Sink, error) {
	o := streamopts.ParseSinkOptions(opts...)
	assert.Equal(s.t, s.requiredStart, o.LastEventID)
	return &fakeSink{events: s.events, acked: &s.acked}, nil
}

func (s *fakeStream) NewReader(ctx context.Context, opts ...streamopts.Reader) (pulse.Reader, error) {
	o := streamopts.ParseReaderOptions(opts...)
	assert.Equal(s.t, s.requiredStart, o.LastEventID)
	return &fakeReader{events: s.events, keepOpen: s.keepOpen}, nil
}

func (s *fakeStream) EnsureGroup(context.Context, string) error { return nil }

func (s *fakeStream) Destroy(ctx context.Context) error {
	s.destroyed = true
	return nil
}

type fakeSink struct {
	events []*streaming.Event
	acked  *[]*streaming.Event
}

type fakeReader struct {
	events   []*streaming.Event
	keepOpen bool
}

func (r *fakeReader) Subscribe() <-chan *streaming.Event {
	ch := make(chan *streaming.Event, len(r.events))
	for _, event := range r.events {
		ch <- event
	}
	if !r.keepOpen {
		close(ch)
	}
	return ch
}

func (r *fakeReader) Close() {}

func (s *fakeSink) Subscribe() <-chan *streaming.Event {
	ch := make(chan *streaming.Event, len(s.events))
	for _, ev := range s.events {
		ch <- ev
	}
	close(ch)
	return ch
}

func (s *fakeSink) Ack(ctx context.Context, ev *streaming.Event) error {
	*s.acked = append(*s.acked, ev)
	return nil
}

func (s *fakeSink) Close(ctx context.Context) error { return nil }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func executeRegistryResultMessage(
	t *testing.T,
	toolUseID string,
	toolCallID string,
	msg toolregistry.ToolResultMessage,
	spec *tools.ToolSpec,
) (*agentsruntime.ToolExecutionResult, error) {
	t.Helper()

	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: toolregistry.ResultEventKey,
				Payload:   mustJSON(t, msg),
			},
		},
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: spec},
	)
	return exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:      "run",
		SessionID:  "sess",
		ToolCallID: toolCallID,
	}, &planner.ToolRequest{
		Name:    spec.Name,
		Payload: []byte(`{}`),
	})
}

func (t *recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, telemetry.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	span := &recordingSpan{
		name:  name,
		attrs: cfg.Attributes(),
	}
	t.spans = append(t.spans, span)
	return ctx, span
}

func (t *recordingTracer) Span(context.Context) telemetry.Span {
	if len(t.spans) == 0 {
		return &recordingSpan{}
	}
	return t.spans[len(t.spans)-1]
}

func (s *recordingSpan) End(...trace.SpanEndOption) {}

func (s *recordingSpan) AddEvent(string, ...any) {}

func (s *recordingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *recordingSpan) SetStatus(codes.Code, string) {}

func (s *recordingSpan) RecordError(error, ...trace.EventOption) {}

func attrsByKey(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	out := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[attr.Key] = attr.Value
	}
	return out
}
