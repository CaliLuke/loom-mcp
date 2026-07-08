package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pulse "github.com/CaliLuke/loom-mcp/features/stream/pulse/clients/pulse"
	mockpulse "github.com/CaliLuke/loom-mcp/features/stream/pulse/clients/pulse/mocks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingHandler struct {
	started  chan struct{}
	unblock  chan struct{}
	callSeen atomic.Bool
}

const pulseAddEventID = "0-0"

type recordingHandler struct {
	blockFirst bool
	started    chan struct{}
	unblock    chan struct{}
	seen       chan string
	once       sync.Once
}

func (h *recordingHandler) HandleToolCall(ctx context.Context, msg toolregistry.ToolCallMessage) (toolregistry.ToolResultMessage, error) {
	h.seen <- msg.ToolUseID
	if h.blockFirst {
		h.once.Do(func() {
			close(h.started)
			<-h.unblock
		})
	}
	return toolregistry.NewToolResultMessage(msg.ToolUseID, json.RawMessage(`{"ok":true}`)), nil
}

func (h *blockingHandler) HandleToolCall(ctx context.Context, msg toolregistry.ToolCallMessage) (toolregistry.ToolResultMessage, error) {
	if !h.callSeen.Swap(true) {
		close(h.started)
	}
	<-h.unblock
	return toolregistry.NewToolResultMessage(msg.ToolUseID, json.RawMessage(`{"ok":true}`)), nil
}

func TestDrainPending_PreservesCapacity(t *testing.T) {
	t.Parallel()

	work := make(chan workItem, 2)
	pending := make([]workItem, 0, 3)
	for i := 1; i <= 3; i++ {
		pending = append(pending, workItem{
			ev:         &streaming.Event{ID: fmt.Sprintf("%d-0", i)},
			msg:        toolregistry.ToolCallMessage{ToolUseID: fmt.Sprintf("tooluse_%d", i)},
			receivedAt: time.Now(),
		})
	}

	got := drainPending(context.Background(), work, pending, 0, time.Now(), telemetry.NewNoopLogger())
	require.Len(t, got, 1)
	require.Equal(t, cap(pending), cap(got))
	require.Equal(t, "tooluse_3", got[0].msg.ToolUseID)

	<-work
	got = drainPending(context.Background(), work, got, 0, time.Now(), telemetry.NewNoopLogger())
	require.Empty(t, got)
	require.Equal(t, cap(pending), cap(got))
}

func TestPendingItemExpired(t *testing.T) {
	t.Parallel()

	now := time.Now()
	grace := 100 * time.Millisecond
	oldAddTimeID := streamIDFromTime(now.Add(-time.Hour))

	tests := []struct {
		name     string
		item     workItem
		ackGrace time.Duration
		want     bool
	}{
		{
			name:     "zero ack grace never expires",
			item:     workItem{ev: &streaming.Event{ID: "1-0"}, receivedAt: now.Add(-time.Hour)},
			ackGrace: 0,
			want:     false,
		},
		{
			name:     "nil event never expires",
			item:     workItem{receivedAt: now.Add(-time.Hour)},
			ackGrace: grace,
			want:     false,
		},
		{
			name:     "zero receivedAt never expires",
			item:     workItem{ev: &streaming.Event{ID: oldAddTimeID}},
			ackGrace: grace,
			want:     false,
		},
		{
			name:     "old add-time with fresh receipt does not expire",
			item:     workItem{ev: &streaming.Event{ID: oldAddTimeID}, receivedAt: now},
			ackGrace: grace,
			want:     false,
		},
		{
			name:     "held locally past grace expires",
			item:     workItem{ev: &streaming.Event{ID: currentStreamID()}, receivedAt: now.Add(-2 * grace)},
			ackGrace: grace,
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, pendingItemExpired(tc.item, tc.ackGrace, now))
		})
	}
}

func TestServe_RespondsToPingWhileToolCallInFlight(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	toolsetStreamID := toolregistry.ToolsetStreamID(toolset)

	eventsCh := make(chan *streaming.Event, 10)

	// Toolset stream + sink.
	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, _ *streaming.Event) error { return nil })
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	// Result stream capture.
	var adds atomic.Int64
	resultStream := mockpulse.NewStream(t)
	resultStream.SetAdd(func(_ context.Context, _ string, _ []byte) (string, error) {
		adds.Add(1)
		return pulseAddEventID, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		switch name {
		case toolsetStreamID:
			return toolsetStream, nil
		default:
			// Result streams.
			return resultStream, nil
		}
	})

	h := &blockingHandler{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}

	pongs := make(chan string, 10)

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, client, toolset, h, Options{
			Pong: func(_ context.Context, pingID string) error {
				pongs <- pingID
				return nil
			},
		})
	}()

	// Send a tool call first, then wait until the handler is running (blocked).
	call := toolregistry.NewToolCallMessage(
		"tooluse_1",
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":1}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	callPayload, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	eventsCh <- &streaming.Event{ID: "1-0", EventName: "call", Payload: callPayload}

	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	// Now send a ping and assert Pong is handled promptly while the tool call is still blocked.
	ping := toolregistry.NewPingMessage("ping_1")
	pingPayload, err := json.Marshal(ping)
	if err != nil {
		t.Fatalf("marshal ping: %v", err)
	}
	eventsCh <- &streaming.Event{ID: "2-0", EventName: "ping", Payload: pingPayload}

	select {
	case got := <-pongs:
		if got != "ping_1" {
			t.Fatalf("unexpected ping id: %q", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected pong while tool call is in flight")
	}

	// Let the tool call complete (publish result), then stop the server.
	close(h.unblock)
	deadline := time.After(2 * time.Second)
	for adds.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 1 result publish, got %d", adds.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
		// The server should stop on context cancellation.
	}

	// The provider should have published exactly one result.
	if adds.Load() != 1 {
		t.Fatalf("expected 1 result publish, got %d", adds.Load())
	}
}

func TestServe_RespondsToPingWhenQueueIsFull(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	toolsetStreamID := toolregistry.ToolsetStreamID(toolset)

	eventsCh := make(chan *streaming.Event, 10)

	// Toolset stream + sink.
	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, _ *streaming.Event) error { return nil })
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	// Result stream capture.
	var adds atomic.Int64
	resultStream := mockpulse.NewStream(t)
	resultStream.SetAdd(func(_ context.Context, _ string, _ []byte) (string, error) {
		adds.Add(1)
		return pulseAddEventID, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		switch name {
		case toolsetStreamID:
			return toolsetStream, nil
		default:
			// Result streams.
			return resultStream, nil
		}
	})

	h := &blockingHandler{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}

	pongs := make(chan string, 10)

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, client, toolset, h, Options{
			MaxConcurrentToolCalls: 1,
			MaxQueuedToolCalls:     0,
			Pong: func(_ context.Context, pingID string) error {
				pongs <- pingID
				return nil
			},
		})
	}()

	// Send one tool call that will start and block.
	call1 := toolregistry.NewToolCallMessage(
		"tooluse_1",
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":1}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	call1Payload, err := json.Marshal(call1)
	if err != nil {
		t.Fatalf("marshal call1: %v", err)
	}
	eventsCh <- &streaming.Event{ID: "1-0", EventName: "call", Payload: call1Payload}

	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	// Send a second tool call while the first is blocked. With a single worker
	// and a tiny queue, the provider must not block pings while it buffers.
	call2 := toolregistry.NewToolCallMessage(
		"tooluse_2",
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":2}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	call2Payload, err := json.Marshal(call2)
	if err != nil {
		t.Fatalf("marshal call2: %v", err)
	}
	eventsCh <- &streaming.Event{ID: "2-0", EventName: "call", Payload: call2Payload}

	ping := toolregistry.NewPingMessage("ping_1")
	pingPayload, err := json.Marshal(ping)
	if err != nil {
		t.Fatalf("marshal ping: %v", err)
	}
	eventsCh <- &streaming.Event{ID: "3-0", EventName: "ping", Payload: pingPayload}

	select {
	case got := <-pongs:
		if got != "ping_1" {
			t.Fatalf("unexpected ping id: %q", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected pong while queue is full")
	}

	// Let the tool call complete (publish result), then stop the server.
	close(h.unblock)
	deadline := time.After(2 * time.Second)
	for adds.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 1 result publish, got %d", adds.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
		// The server should stop on context cancellation.
	}
}

func TestServe_DrainsPendingWhenStreamQuiet(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	harness := newProviderHarness(t)
	handler := &recordingHandler{
		blockFirst: true,
		started:    make(chan struct{}),
		unblock:    make(chan struct{}),
		seen:       make(chan string, 8),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, harness.client, toolset, handler, Options{
			MaxConcurrentToolCalls: 1,
			MaxQueuedToolCalls:     1,
			Pong:                   func(_ context.Context, _ string) error { return nil },
		})
	}()

	harness.events <- makeToolCallEvent(t, currentStreamID(), "tooluse_1")
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	harness.events <- makeToolCallEvent(t, currentStreamID(), "tooluse_2")
	harness.events <- makeToolCallEvent(t, currentStreamID(), "tooluse_3")
	close(handler.unblock)

	seen := waitForToolUses(t, handler.seen, 3)
	require.Contains(t, seen, "tooluse_1")
	require.Contains(t, seen, "tooluse_2")
	require.Contains(t, seen, "tooluse_3")

	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
	}
}

func TestServe_ShedsPendingHeldPastAckGraceWithoutAck(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	harness := newProviderHarness(t)
	handler := &recordingHandler{
		blockFirst: true,
		started:    make(chan struct{}),
		unblock:    make(chan struct{}),
		seen:       make(chan string, 8),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, harness.client, toolset, handler, Options{
			SinkAckGracePeriod:     20 * time.Millisecond,
			MaxConcurrentToolCalls: 1,
			MaxQueuedToolCalls:     1,
			Pong:                   func(_ context.Context, _ string) error { return nil },
		})
	}()

	harness.events <- makeToolCallEvent(t, "1-0", "tooluse_1")
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	// With one blocked worker and a work queue of one, the third call lands
	// in the pending overflow buffer and is held there past the ack grace.
	harness.events <- makeToolCallEvent(t, "2-0", "tooluse_2")
	harness.events <- makeToolCallEvent(t, "3-0", "tooluse_3")

	// Hold the worker blocked well past the grace period so the pending item
	// is shed. It must be dropped WITHOUT acking: acking would permanently
	// discard a never-executed call, while the un-acked event stays on the
	// sink for redelivery.
	select {
	case got := <-harness.acked:
		t.Fatalf("no event should be acked while the worker is blocked, got %s", got)
	case <-time.After(150 * time.Millisecond):
	}

	close(handler.unblock)
	seen := waitForToolUses(t, handler.seen, 2)
	require.Contains(t, seen, "tooluse_1")
	require.Contains(t, seen, "tooluse_2")
	require.NotContains(t, seen, "tooluse_3")

	select {
	case got := <-handler.seen:
		t.Fatalf("shed pending item executed unexpectedly: %s", got)
	case <-time.After(100 * time.Millisecond):
	}

	acked := waitForAcks(t, harness.acked, 2)
	require.Contains(t, acked, "1-0")
	require.Contains(t, acked, "2-0")

	select {
	case got := <-harness.acked:
		t.Fatalf("shed pending event must not be acked, got ack for %s", got)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
	}
}

func TestServe_ExecutesFirstDeliveryBacklogOlderThanAckGrace(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	harness := newProviderHarness(t)
	handler := &recordingHandler{
		blockFirst: true,
		started:    make(chan struct{}),
		unblock:    make(chan struct{}),
		seen:       make(chan string, 8),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, harness.client, toolset, handler, Options{
			SinkAckGracePeriod:     2 * time.Second,
			MaxConcurrentToolCalls: 1,
			MaxQueuedToolCalls:     1,
			Pong:                   func(_ context.Context, _ string) error { return nil },
		})
	}()

	harness.events <- makeToolCallEvent(t, "1-0", "tooluse_1")
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	// The backlog event's add-time is far older than the ack grace period,
	// but this is its FIRST delivery: it must be buffered and executed, not
	// dropped or acked. Staleness is measured from local receipt, mirroring
	// Pulse's PEL idle clock, so add-time age is irrelevant.
	staleID := streamIDFromTime(time.Now().Add(-time.Minute))
	harness.events <- makeToolCallEvent(t, "2-0", "tooluse_2")
	harness.events <- makeToolCallEvent(t, staleID, "tooluse_3")

	select {
	case got := <-harness.acked:
		t.Fatalf("backlog event must not be acked before execution, got ack for %s", got)
	case <-time.After(150 * time.Millisecond):
	}

	close(handler.unblock)
	seen := waitForToolUses(t, handler.seen, 3)
	require.Contains(t, seen, "tooluse_1")
	require.Contains(t, seen, "tooluse_2")
	require.Contains(t, seen, "tooluse_3")

	acked := waitForAcks(t, harness.acked, 3)
	require.Contains(t, acked, staleID)

	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
	}
}

func TestServe_DoesNotExitOnPongFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	toolsetStreamID := toolregistry.ToolsetStreamID(toolset)

	eventsCh := make(chan *streaming.Event, 10)

	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, _ *streaming.Event) error { return nil })
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	var adds atomic.Int64
	resultStream := mockpulse.NewStream(t)
	resultStream.SetAdd(func(_ context.Context, _ string, _ []byte) (string, error) {
		adds.Add(1)
		return pulseAddEventID, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		switch name {
		case toolsetStreamID:
			return toolsetStream, nil
		default:
			return resultStream, nil
		}
	})

	h := &blockingHandler{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}

	var attempts atomic.Int64
	pongs := make(chan string, 10)

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, client, toolset, h, Options{
			PongTimeout: 50 * time.Millisecond,
			Pong: func(_ context.Context, pingID string) error {
				pongs <- pingID
				if attempts.Add(1) == 1 {
					return errors.New("pong failed")
				}
				return nil
			},
		})
	}()

	// Send a ping that will fail Pong. Serve must not exit.
	ping1 := toolregistry.NewPingMessage("ping_1")
	ping1Payload, err := json.Marshal(ping1)
	require.NoError(t, err)
	eventsCh <- &streaming.Event{ID: "1-0", EventName: "ping", Payload: ping1Payload}

	select {
	case got := <-pongs:
		require.Equal(t, "ping_1", got)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected pong attempt for ping_1")
	}
	select {
	case err := <-errc:
		t.Fatalf("Serve exited unexpectedly: %v", err)
	default:
	}

	// Send a second ping which should succeed.
	ping2 := toolregistry.NewPingMessage("ping_2")
	ping2Payload, err := json.Marshal(ping2)
	require.NoError(t, err)
	eventsCh <- &streaming.Event{ID: "2-0", EventName: "ping", Payload: ping2Payload}

	select {
	case got := <-pongs:
		require.Equal(t, "ping_2", got)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected pong attempt for ping_2")
	}

	// Send a tool call to prove the provider still executes calls after a failed Pong.
	call := toolregistry.NewToolCallMessage(
		"tooluse_1",
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":1}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	callPayload, err := json.Marshal(call)
	require.NoError(t, err)
	eventsCh <- &streaming.Event{ID: "3-0", EventName: "call", Payload: callPayload}

	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	close(h.unblock)

	deadline := time.After(2 * time.Second)
	for adds.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected at least 1 result publish")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
	}
}

func TestServe_ReturnsWhenSubscriptionCloses(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	harness := newProviderHarness(t)
	handler := &recordingHandler{
		seen: make(chan string, 1),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, harness.client, toolset, handler, Options{
			Pong: func(_ context.Context, _ string) error { return nil },
		})
	}()

	close(harness.events)
	err := waitForServeReturn(t, errc)
	require.EqualError(t, err, "toolset stream subscription closed")
}

func TestServe_ReturnsOnAckFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	toolsetStreamID := toolregistry.ToolsetStreamID(toolset)
	eventsCh := make(chan *streaming.Event, 1)
	ackErr := errors.New("ack failed")

	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, _ *streaming.Event) error { return ackErr })
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		require.Equal(t, toolsetStreamID, name)
		return toolsetStream, nil
	})

	handler := &recordingHandler{
		seen: make(chan string, 1),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, client, toolset, handler, Options{
			Pong: func(_ context.Context, _ string) error { return nil },
		})
	}()

	ping := toolregistry.NewPingMessage("ping_1")
	pingPayload, err := json.Marshal(ping)
	require.NoError(t, err)
	eventsCh <- &streaming.Event{ID: "1-0", EventName: "ping", Payload: pingPayload}

	err = waitForServeReturn(t, errc)
	require.ErrorIs(t, err, ackErr)
	require.ErrorContains(t, err, "ack ping toolset event")
}

type outputDeltaHandler struct {
	errc chan error
}

func (h *outputDeltaHandler) HandleToolCall(ctx context.Context, msg toolregistry.ToolCallMessage) (toolregistry.ToolResultMessage, error) {
	pub, ok := toolregistry.OutputDeltaPublisherFromContext(ctx)
	if !ok {
		select {
		case h.errc <- errors.New("missing output delta publisher in context"):
		default:
		}
		return toolregistry.NewToolResultMessage(msg.ToolUseID, json.RawMessage(`{"ok":true}`)), nil
	}
	if err := pub.PublishToolOutputDelta(ctx, "stdout", "hello\n"); err != nil {
		select {
		case h.errc <- err:
		default:
		}
	}
	return toolregistry.NewToolResultMessage(msg.ToolUseID, json.RawMessage(`{"ok":true}`)), nil
}

func TestServe_PublishesOutputDeltaToResultStream(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolset = "test.toolset"
	toolsetStreamID := toolregistry.ToolsetStreamID(toolset)

	eventsCh := make(chan *streaming.Event, 10)

	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, _ *streaming.Event) error { return nil })
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	addEvents := make(chan string, 8)
	resultStream := mockpulse.NewStream(t)
	resultStream.SetAdd(func(_ context.Context, event string, _ []byte) (string, error) {
		addEvents <- event
		return pulseAddEventID, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		switch name {
		case toolsetStreamID:
			return toolsetStream, nil
		default:
			return resultStream, nil
		}
	})

	handlerErrs := make(chan error, 1)
	h := &outputDeltaHandler{errc: handlerErrs}

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, client, toolset, h, Options{
			Pong: func(_ context.Context, _ string) error { return nil },
		})
	}()

	call := toolregistry.NewToolCallMessage(
		"tooluse_1",
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":1}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	callPayload, err := json.Marshal(call)
	require.NoError(t, err)
	eventsCh <- &streaming.Event{ID: "1-0", EventName: "call", Payload: callPayload}

	seen := map[string]int{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case ev := <-addEvents:
			seen[ev] += 1
		case <-deadline:
			t.Fatalf("timed out waiting for result stream events, saw=%v", seen)
		}
	}

	select {
	case err := <-handlerErrs:
		if err != nil {
			t.Fatalf("handler delta publish failed: %v", err)
		}
	default:
	}
	if seen[toolregistry.OutputDeltaEventKey] < 1 {
		t.Fatalf("expected at least 1 %q event, saw=%v", toolregistry.OutputDeltaEventKey, seen)
	}
	if seen[toolregistry.ResultEventKey] < 1 {
		t.Fatalf("expected at least 1 %q event, saw=%v", toolregistry.ResultEventKey, seen)
	}

	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	case <-errc:
	}
}

type providerHarness struct {
	events chan *streaming.Event
	acked  chan string
	client pulse.Client
	adds   *atomic.Int64
}

// providerTestToolset is the single toolset name the provider harness serves;
// tests asserting on names should use their own local constant equal to it.
const providerTestToolset = "test.toolset"

func newProviderHarness(t *testing.T) providerHarness {
	t.Helper()

	toolsetStreamID := toolregistry.ToolsetStreamID(providerTestToolset)
	eventsCh := make(chan *streaming.Event, 10)
	acked := make(chan string, 10)

	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event { return eventsCh })
	sink.SetAck(func(_ context.Context, ev *streaming.Event) error {
		acked <- ev.ID
		return nil
	})
	sink.SetClose(func(_ context.Context) {})

	toolsetStream := mockpulse.NewStream(t)
	toolsetStream.SetNewSink(func(_ context.Context, _ string, _ ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})

	adds := &atomic.Int64{}
	resultStream := mockpulse.NewStream(t)
	resultStream.SetAdd(func(_ context.Context, _ string, _ []byte) (string, error) {
		adds.Add(1)
		return pulseAddEventID, nil
	})

	client := mockpulse.NewClient(t)
	client.SetStream(func(name string, _ ...streamopts.Stream) (pulse.Stream, error) {
		switch name {
		case toolsetStreamID:
			return toolsetStream, nil
		default:
			return resultStream, nil
		}
	})

	return providerHarness{
		events: eventsCh,
		acked:  acked,
		client: client,
		adds:   adds,
	}
}

func makeToolCallEvent(t *testing.T, eventID string, toolUseID string) *streaming.Event {
	t.Helper()

	call := toolregistry.NewToolCallMessage(
		toolUseID,
		tools.Ident("toolset.tool"),
		json.RawMessage(`{"x":1}`),
		&toolregistry.ToolCallMeta{RunID: "r1", SessionID: "s1"},
	)
	payload, err := json.Marshal(call)
	require.NoError(t, err)
	return &streaming.Event{ID: eventID, EventName: "call", Payload: payload}
}

func waitForToolUses(t *testing.T, seen <-chan string, count int) map[string]bool {
	t.Helper()

	got := make(map[string]bool, count)
	deadline := time.After(2 * time.Second)
	for len(got) < count {
		select {
		case toolUseID := <-seen:
			got[toolUseID] = true
		case <-deadline:
			t.Fatalf("timed out waiting for %d tool calls, saw=%v", count, got)
		}
	}
	return got
}

func waitForAcks(t *testing.T, acked <-chan string, count int) map[string]bool {
	t.Helper()

	got := make(map[string]bool, count)
	deadline := time.After(2 * time.Second)
	for len(got) < count {
		select {
		case id := <-acked:
			got[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for %d acks, saw=%v", count, got)
		}
	}
	return got
}

func waitForServeReturn(t *testing.T, errc <-chan error) error {
	t.Helper()

	select {
	case err := <-errc:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	}
	return nil
}

func currentStreamID() string {
	return streamIDFromTime(time.Now())
}

func streamIDFromTime(t time.Time) string {
	return fmt.Sprintf("%d-0", t.UnixMilli())
}
