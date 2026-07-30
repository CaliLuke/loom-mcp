package executor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	aistream "github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorUsesOldestStartForResultStreamSink(t *testing.T) {
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
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
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
	}, pc, specs, WithResultEventKey(resultEventName))

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, tools.Ident("todos.update_todos"), res.Name)
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
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
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
	}, pc, specs, WithResultEventKey(resultEventName))

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, tools.Ident("todos.update_todos"), res.Name)
}

func TestExecutorReturnsResultWhenStreamDestroyFails(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-destroy-123"
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
		destroyErr:    assert.AnError,
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
				}),
			},
		},
	}
	pc := fakePulseClient{
		streamID: toolregistry.ResultStreamID(toolUseID),
		stream:   stream,
	}

	exec := New(fakeRegistryClient{
		toolUseID: toolUseID,
	}, pc, specs, WithResultEventKey(resultEventName))

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    "todos.update_todos",
		Payload: []byte(`{}`),
	})

	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, tools.Ident("todos.update_todos"), res.Name)
}

func TestExecutorCleansResultStreamWithDetachedContextWhenCanceled(t *testing.T) {
	t.Parallel()

	const (
		toolUseID = "tooluse-cancel-123"
		toolName  = tools.Ident("todos.update_todos")
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    toolName,
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}
	sink := &fakeSink{subscribe: make(chan *streaming.Event)}
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		sink:          sink,
	}
	pc := fakePulseClient{
		streamID: toolregistry.ResultStreamID(toolUseID),
		stream:   stream,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := New(fakeRegistryClient{
		toolUseID: toolUseID,
	}, pc, specs)

	res, err := exec.Execute(ctx, &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    toolName,
		Payload: []byte(`{}`),
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, res)
	assert.True(t, sink.closed())
	require.NoError(t, sink.closeContextErr())
	assert.True(t, stream.destroyed())
	assert.NoError(t, stream.destroyContextErr())
}

func TestExecutorBoundsResultStreamDestroyWhenSinkCreationFails(t *testing.T) {
	t.Parallel()

	const toolUseID = "tooluse-sink-failure"
	toolName := tools.Ident("todos.update_todos")
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		sinkErr:       assert.AnError,
		destroyWait:   250 * time.Millisecond,
	}
	exec := New(
		fakeRegistryClient{toolUseID: toolUseID},
		fakePulseClient{streamID: toolregistry.ResultStreamID(toolUseID), stream: stream},
		fakeSpecs{spec: &tools.ToolSpec{Name: toolName, Toolset: "todos.todos"}},
	)
	exec.cleanupTimeout = 20 * time.Millisecond

	start := time.Now()
	result, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{ToolCallID: "call"}, &planner.ToolRequest{Name: toolName, Payload: []byte(`{}`)})

	require.ErrorContains(t, err, "create sink")
	require.Nil(t, result)
	require.Less(t, time.Since(start), 150*time.Millisecond)
	require.ErrorIs(t, stream.destroyContextErr(), context.DeadlineExceeded)
}

func TestExecutorResultWaitTimesOutAndCleansResultStream(t *testing.T) {
	t.Parallel()

	const (
		toolUseID = "tooluse-timeout-123"
		toolName  = tools.Ident("todos.update_todos")
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    toolName,
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}
	sink := &fakeSink{subscribe: make(chan *streaming.Event)}
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		sink:          sink,
	}
	pc := fakePulseClient{
		streamID: toolregistry.ResultStreamID(toolUseID),
		stream:   stream,
	}
	exec := New(
		fakeRegistryClient{
			toolUseID: toolUseID,
		},
		pc,
		specs,
		WithResultWaitTimeout(10*time.Millisecond),
	)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    toolName,
		Payload: []byte(`{}`),
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, res)
	assert.True(t, sink.closed())
	require.NoError(t, sink.closeContextErr())
	assert.True(t, stream.destroyed())
	assert.NoError(t, stream.destroyContextErr())
}

func TestExecutorCleansResultStreamWhenResultAckFails(t *testing.T) {
	t.Parallel()

	const (
		toolUseID       = "tooluse-ack-fails-123"
		toolName        = tools.Ident("todos.update_todos")
		resultEventName = toolregistry.ResultEventKey
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    toolName,
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}
	sink := &fakeSink{
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
				}),
			},
		},
		ackErr: assert.AnError,
	}
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		sink:          sink,
	}
	pc := fakePulseClient{
		streamID: toolregistry.ResultStreamID(toolUseID),
		stream:   stream,
	}
	exec := New(
		fakeRegistryClient{
			toolUseID: toolUseID,
		},
		pc,
		specs,
		WithResultEventKey(resultEventName),
	)

	res, err := exec.Execute(context.Background(), &agentsruntime.ToolCallMeta{
		RunID:     "run",
		SessionID: "sess",
	}, &planner.ToolRequest{
		Name:    toolName,
		Payload: []byte(`{}`),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ack tool result message")
	assert.Nil(t, res)
	assert.True(t, sink.closed())
	require.NoError(t, sink.closeContextErr())
	assert.True(t, stream.destroyed())
	assert.NoError(t, stream.destroyContextErr())
}

func TestExecutorCleansResultStreamWhenSinkCreateFails(t *testing.T) {
	t.Parallel()

	const (
		toolUseID = "tooluse-sink-fails-123"
		toolName  = tools.Ident("todos.update_todos")
	)

	specs := fakeSpecs{
		spec: &tools.ToolSpec{
			Name:    toolName,
			Toolset: "todos.todos",
			Result:  tools.TypeSpec{},
			Payload: tools.TypeSpec{},
		},
	}
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		sinkErr:       assert.AnError,
	}
	pc := fakePulseClient{
		streamID: toolregistry.ResultStreamID(toolUseID),
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
		Name:    toolName,
		Payload: []byte(`{}`),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create sink")
	assert.Nil(t, res)
	assert.True(t, stream.destroyed())
	require.NoError(t, stream.destroyContextErr())
}

type captureSink struct {
	events []aistream.Event
}

func (s *captureSink) Send(ctx context.Context, event aistream.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *captureSink) Close(ctx context.Context) error {
	return nil
}

func TestExecutorForwardsOutputDelta(t *testing.T) {
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

	delta := toolregistry.NewToolOutputDeltaMessage(toolUseID, "stdout", "hi\n")
	stream := &fakeStream{
		t:             t,
		requiredStart: "0",
		events: []*streaming.Event{
			{
				ID:        "1-0",
				EventName: toolregistry.OutputDeltaEventKey,
				Payload:   mustJSON(t, delta),
			},
			{
				ID:        "2-0",
				EventName: resultEventName,
				Payload: mustJSON(t, toolregistry.ToolResultMessage{
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
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
		WithResultEventKey(resultEventName),
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
					ToolUseID: toolUseID,
					Result:    json.RawMessage(`{}`),
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
		WithResultEventKey(resultEventName),
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
	require.NotNil(t, res.Bounds)
	assert.Equal(t, 1, res.Bounds.Returned)
	assert.True(t, res.Bounds.Truncated)
	require.NotNil(t, res.Bounds.NextCursor)
	assert.Equal(t, nextCursor, *res.Bounds.NextCursor)
	assert.Equal(t, "narrow by device", res.Bounds.RefinementHint)
}

type fakeRegistryClient struct {
	toolUseID string
}

func (c fakeRegistryClient) CallTool(ctx context.Context, toolset string, tool tools.Ident, payload []byte, meta toolregistry.ToolCallMeta) (string, error) {
	return c.toolUseID, nil
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

func (c fakePulseClient) Stream(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
	if name != c.streamID {
		return nil, assert.AnError
	}
	return c.stream, nil
}

func (c fakePulseClient) Close(ctx context.Context) error {
	return nil
}

type fakeStream struct {
	t             *testing.T
	requiredStart string
	destroyErr    error
	destroyWait   time.Duration
	sinkErr       error
	events        []*streaming.Event
	sink          *fakeSink
	mu            sync.Mutex
	destroyCalled bool
	destroyCtxErr error
}

func (s *fakeStream) Add(ctx context.Context, event string, payload []byte) (string, error) {
	return "", assert.AnError
}

func (s *fakeStream) NewSink(ctx context.Context, name string, opts ...streamopts.Sink) (pulse.Sink, error) {
	o := streamopts.ParseSinkOptions(opts...)
	assert.Equal(s.t, s.requiredStart, o.LastEventID)
	if s.sinkErr != nil {
		return nil, s.sinkErr
	}
	if s.sink != nil {
		return s.sink, nil
	}
	return &fakeSink{events: s.events}, nil
}

func (s *fakeStream) Destroy(ctx context.Context) error {
	if s.destroyWait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(s.destroyWait):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyCalled = true
	s.destroyCtxErr = ctx.Err()
	return s.destroyErr
}

func (s *fakeStream) destroyed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destroyCalled
}

func (s *fakeStream) destroyContextErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destroyCtxErr
}

type fakeSink struct {
	events      []*streaming.Event
	subscribe   <-chan *streaming.Event
	ackErr      error
	mu          sync.Mutex
	closeCalled bool
	closeCtxErr error
}

func (s *fakeSink) Subscribe() <-chan *streaming.Event {
	if s.subscribe != nil {
		return s.subscribe
	}
	ch := make(chan *streaming.Event, len(s.events))
	for _, ev := range s.events {
		ch <- ev
	}
	close(ch)
	return ch
}

func (s *fakeSink) Ack(ctx context.Context, ev *streaming.Event) error {
	if s.ackErr != nil {
		return s.ackErr
	}
	return nil
}

func (s *fakeSink) Close(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalled = true
	s.closeCtxErr = ctx.Err()
}

func (s *fakeSink) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalled
}

func (s *fakeSink) closeContextErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCtxErr
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
