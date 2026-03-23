// Package provider implements the provider-side Pulse subscription loop for
// registry-routed tool execution. Providers receive tool calls from a toolset
// stream and publish results to per-call result streams.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	pulseclients "github.com/CaliLuke/loom-mcp/features/stream/pulse/clients/pulse"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/toolregistry"
	"goa.design/pulse/streaming"
	streamopts "goa.design/pulse/streaming/options"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type (
	// Handler executes tool calls received from a toolset stream.
	// Implementations are responsible for decoding/encoding tool payload/result
	// using the compiled tool codecs for their toolset.
	Handler interface {
		HandleToolCall(ctx context.Context, msg toolregistry.ToolCallMessage) (toolregistry.ToolResultMessage, error)
	}

	// Options configure the provider loop.
	Options struct {
		// SinkName identifies the Pulse sink used for subscribing.
		// When empty, defaults to "provider".
		SinkName string

		// ResultEventType is the Pulse entry type used for publishing results.
		// When empty, defaults to toolregistry.ResultEventKey.
		ResultEventType string

		// SinkAckGracePeriod configures the Pulse sink acknowledgement grace
		// period. When non-zero, Serve passes it to the sink.
		//
		// This value must be identical across all providers using the same sink
		// name for a given toolset stream.
		//
		// Important: If a tool call can take longer than the sink ack grace
		// period and the provider only Ack's after publishing the tool result,
		// Pulse may reclaim and re-deliver the call while it is still in flight.
		// Deployments should set this high enough to cover worst-case tool
		// execution time.
		SinkAckGracePeriod time.Duration

		// Pong acknowledges health pings emitted by the registry gateway.
		// Providers must supply this to participate in health tracking.
		Pong func(ctx context.Context, pingID string) error

		// PongTimeout bounds how long Serve will wait for the Pong callback to
		// return when handling a ping message.
		//
		// Contract:
		//   - Ping messages exist solely for toolset health tracking; they are not part
		//     of tool execution correctness.
		//   - Pong failures must never crash the provider loop. If the registry is
		//     temporarily unreachable, the toolset should be marked unhealthy by the
		//     registry, and the provider should continue draining the stream so it can
		//     recover without a restart loop.
		//
		// When 0, Serve defaults to a short value suitable for transient outages.
		PongTimeout time.Duration

		// MaxConcurrentToolCalls caps the number of tool calls executed
		// concurrently by this provider (worker pool size).
		//
		// Serve drains the toolset stream in a dedicated loop and enqueues tool
		// calls for workers; it does not execute tool calls inline. This option
		// exists to bound provider-side resource usage (CPU, memory, upstream
		// concurrency) and to avoid overload amplification.
		//
		// When 0, Serve defaults to a small, safe value.
		MaxConcurrentToolCalls int

		// MaxQueuedToolCalls bounds how many tool calls may be buffered for worker
		// execution. When 0, defaults to a value derived from MaxConcurrentToolCalls.
		//
		// The provider subscription loop never blocks on tool execution. Instead,
		// it enqueues calls and continues draining the toolset stream so it can
		// respond to health pings.
		MaxQueuedToolCalls int

		// Logger is used for provider internal logging. When nil, defaults to a noop logger.
		Logger telemetry.Logger

		// Tracer is used for provider spans. When nil, defaults to a noop tracer.
		Tracer telemetry.Tracer
	}

	// pulseOutputDeltaPublisher publishes best-effort tool output fragments to the
	// tool call's per-call result stream (`result:<tool_use_id>`).
	//
	// Contract:
	//   - This is a UX-only signal: consumers may drop deltas without affecting
	//     correctness.
	//   - The canonical tool output remains the final ToolResultMessage published
	//     under the result event key.
	pulseOutputDeltaPublisher struct {
		stream    pulseclients.Stream
		toolUseID string
	}

	workItem struct {
		ev  *streaming.Event
		msg toolregistry.ToolCallMessage
	}

	providerConfig struct {
		sinkName        string
		resultEventType string
		logger          telemetry.Logger
		tracer          telemetry.Tracer
		pongTimeout     time.Duration
		maxConcurrent   int
		maxQueued       int
	}
)

func (p *pulseOutputDeltaPublisher) PublishToolOutputDelta(ctx context.Context, stream string, delta string) error {
	msg := toolregistry.NewToolOutputDeltaMessage(p.toolUseID, stream, delta)
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal tool output delta: %w", err)
	}
	_, err = p.stream.Add(ctx, toolregistry.OutputDeltaEventKey, payload)
	if err != nil {
		return fmt.Errorf("publish tool output delta: %w", err)
	}
	return nil
}

// Serve subscribes to the toolset request stream and dispatches tool call
// messages to handler. It publishes tool results to per-call result streams.
func Serve(ctx context.Context, pulse pulseclients.Client, toolset string, handler Handler, opts Options) error {
	cfg, err := providerConfigFromOptions(pulse, toolset, handler, opts)
	if err != nil {
		return err
	}
	streamID := toolregistry.ToolsetStreamID(toolset)
	stream, err := pulse.Stream(streamID)
	if err != nil {
		return fmt.Errorf("open toolset stream %q: %w", streamID, err)
	}
	var sinkOpts []streamopts.Sink
	if opts.SinkAckGracePeriod > 0 {
		sinkOpts = append(sinkOpts, streamopts.WithSinkAckGracePeriod(opts.SinkAckGracePeriod))
	}
	sink, err := stream.NewSink(ctx, cfg.sinkName, sinkOpts...)
	if err != nil {
		return fmt.Errorf("create sink %q for toolset stream %q: %w", cfg.sinkName, streamID, err)
	}
	defer sink.Close(ctx)

	cfg.logger.Debug(
		ctx,
		"tool-registry provider subscribed",
		"component", "tool-registry-provider",
		"toolset", toolset,
		"stream_id", streamID,
		"sink", cfg.sinkName,
	)

	events := sink.Subscribe()
	var (
		cancelCtx, cancel = context.WithCancel(ctx)
		wg                sync.WaitGroup
		errc              = make(chan error, 1)
	)
	defer cancel()

	work := make(chan workItem, cfg.maxQueued)
	acks := make(chan *streaming.Event, cfg.maxQueued+1024)

	signalErr := func(err error) {
		select {
		case errc <- err:
			cancel()
		default:
		}
	}

	ackWG := startAckLoop(cancelCtx, &sync.WaitGroup{}, sink, acks, signalErr)
	startWorkers(cancelCtx, &wg, cfg.maxConcurrent, workerDeps{
		pulse:           pulse,
		handler:         handler,
		logger:          cfg.logger,
		tracer:          cfg.tracer,
		toolset:         toolset,
		streamID:        streamID,
		resultEventType: cfg.resultEventType,
		work:            work,
		acks:            acks,
		signalErr:       signalErr,
	})

	pending := make([]workItem, 0, cfg.maxQueued)

	for {
		select {
		case <-cancelCtx.Done():
			wg.Wait()
			ackWG.Wait()
			return cancelCtx.Err()
		case err := <-errc:
			wg.Wait()
			ackWG.Wait()
			return err
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("toolset stream subscription closed")
			}
			pending = drainPending(work, pending)
			if _, err := handleSubscribedEvent(cancelCtx, sink, ev, pendingDeps{
				opts:     opts,
				cfg:      cfg,
				logger:   cfg.logger,
				toolset:  toolset,
				streamID: streamID,
				work:     work,
				pending:  &pending,
			}); err != nil {
				return err
			}
		}
	}
}

type workerDeps struct {
	pulse           pulseclients.Client
	handler         Handler
	logger          telemetry.Logger
	tracer          telemetry.Tracer
	toolset         string
	streamID        string
	resultEventType string
	work            <-chan workItem
	acks            chan<- *streaming.Event
	signalErr       func(error)
}

type pendingDeps struct {
	opts     Options
	cfg      providerConfig
	logger   telemetry.Logger
	toolset  string
	streamID string
	work     chan<- workItem
	pending  *[]workItem
}

func providerConfigFromOptions(pulse pulseclients.Client, toolset string, handler Handler, opts Options) (providerConfig, error) {
	if pulse == nil {
		return providerConfig{}, fmt.Errorf("pulse client is required")
	}
	if toolset == "" {
		return providerConfig{}, fmt.Errorf("toolset is required")
	}
	if handler == nil {
		return providerConfig{}, fmt.Errorf("handler is required")
	}
	if opts.Pong == nil {
		return providerConfig{}, fmt.Errorf("pong handler is required")
	}
	cfg := providerConfig{
		sinkName:        opts.SinkName,
		resultEventType: opts.ResultEventType,
		logger:          opts.Logger,
		tracer:          opts.Tracer,
		pongTimeout:     opts.PongTimeout,
		maxConcurrent:   opts.MaxConcurrentToolCalls,
		maxQueued:       opts.MaxQueuedToolCalls,
	}
	if cfg.sinkName == "" {
		cfg.sinkName = "provider"
	}
	if cfg.resultEventType == "" {
		cfg.resultEventType = toolregistry.ResultEventKey
	}
	if cfg.logger == nil {
		cfg.logger = telemetry.NewNoopLogger()
	}
	if cfg.tracer == nil {
		cfg.tracer = telemetry.NewNoopTracer()
	}
	if cfg.pongTimeout <= 0 {
		cfg.pongTimeout = 2 * time.Second
	}
	if cfg.maxConcurrent <= 0 {
		cfg.maxConcurrent = 4
	}
	if cfg.maxQueued <= 0 {
		cfg.maxQueued = cfg.maxConcurrent * 64
	}
	return cfg, nil
}

func startAckLoop(ctx context.Context, wg *sync.WaitGroup, sink pulseclients.Sink, acks <-chan *streaming.Event, signalErr func(error)) *sync.WaitGroup {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-acks:
				if ev == nil {
					continue
				}
				if err := sink.Ack(ctx, ev); err != nil {
					signalErr(fmt.Errorf("ack toolset event: %w", err))
					return
				}
			}
		}
	}()
	return wg
}

func startWorkers(ctx context.Context, wg *sync.WaitGroup, count int, deps workerDeps) {
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-deps.work:
					if err := handleWorkItem(ctx, item, deps); err != nil {
						deps.signalErr(err)
						return
					}
				}
			}
		}()
	}
}

func handleWorkItem(ctx context.Context, item workItem, deps workerDeps) error {
	callCtx := toolregistry.ExtractTraceContext(ctx, item.msg.TraceParent, item.msg.TraceState, item.msg.Baggage)
	callCtx, span := deps.tracer.Start(
		callCtx,
		"toolregistry.handle",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "pulse"),
			attribute.String("messaging.destination.name", deps.streamID),
			attribute.String("messaging.operation", "process"),
			attribute.String("messaging.message.id", item.ev.ID),
			attribute.String("toolregistry.toolset", deps.toolset),
			attribute.String("toolregistry.tool_use_id", item.msg.ToolUseID),
			attribute.String("toolregistry.tool", item.msg.Tool.String()),
			attribute.String("toolregistry.stream_id", deps.streamID),
			attribute.String("toolregistry.event_id", item.ev.ID),
		),
	)
	defer span.End()

	resultStreamID := toolregistry.ResultStreamID(item.msg.ToolUseID)
	resultStream, err := deps.pulse.Stream(resultStreamID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "open result stream")
		return fmt.Errorf("open result stream %q: %w", resultStreamID, err)
	}

	callCtx = toolregistry.WithOutputDeltaPublisher(callCtx, &pulseOutputDeltaPublisher{
		stream:    resultStream,
		toolUseID: item.msg.ToolUseID,
	})
	res := executeToolHandler(callCtx, item, deps, span)
	return publishToolResult(callCtx, item, res, resultStream, resultStreamID, deps, span)
}

func executeToolHandler(ctx context.Context, item workItem, deps workerDeps, span telemetry.Span) toolregistry.ToolResultMessage {
	res, err := deps.handler.HandleToolCall(ctx, item.msg)
	if err == nil {
		return res
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "handle tool call")
	deps.logger.Error(
		ctx,
		"tool call handler failed",
		"component", "tool-registry-provider",
		"toolset", deps.toolset,
		"tool_use_id", item.msg.ToolUseID,
		"tool", item.msg.Tool,
		"err", err,
	)
	return toolregistry.NewToolResultErrorMessage(item.msg.ToolUseID, "execution_failed", err.Error())
}

func publishToolResult(
	ctx context.Context,
	item workItem,
	res toolregistry.ToolResultMessage,
	resultStream pulseclients.Stream,
	resultStreamID string,
	deps workerDeps,
	span telemetry.Span,
) error {
	payload, err := json.Marshal(res)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal tool result")
		return fmt.Errorf("marshal tool result: %w", err)
	}
	if _, err := resultStream.Add(ctx, deps.resultEventType, payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish tool result")
		deps.logger.Error(
			ctx,
			"publish tool result failed",
			"component", "tool-registry-provider",
			"toolset", deps.toolset,
			"tool_use_id", item.msg.ToolUseID,
			"tool", item.msg.Tool,
			"result_stream_id", resultStreamID,
			"err", err,
		)
		return fmt.Errorf("publish tool result to %q: %w", resultStreamID, err)
	}
	span.AddEvent("toolregistry.tool_result_published", "toolregistry.result_stream_id", resultStreamID)
	select {
	case deps.acks <- item.ev:
		return nil
	case <-ctx.Done():
		return nil
	default:
		return fmt.Errorf("ack queue full")
	}
}

func drainPending(work chan<- workItem, pending []workItem) []workItem {
	for len(pending) > 0 {
		select {
		case work <- pending[0]:
			pending = pending[1:]
		default:
			return pending
		}
	}
	return pending
}

func handleSubscribedEvent(ctx context.Context, sink pulseclients.Sink, ev *streaming.Event, deps pendingDeps) (bool, error) {
	msg, done, err := decodeToolsetEvent(ctx, sink, ev, deps.logger, deps.toolset, deps.streamID)
	if err != nil || done {
		return done, err
	}
	done, err = handleControlMessage(ctx, sink, ev, msg, deps)
	if err != nil || done {
		return done, err
	}
	if msg.ToolUseID == "" {
		return true, ackEvent(ctx, sink, ev, "ack tool call missing tool_use_id")
	}
	queueWorkItem(ctx, workItem{ev: ev, msg: msg}, deps)
	return false, nil
}

func decodeToolsetEvent(
	ctx context.Context,
	sink pulseclients.Sink,
	ev *streaming.Event,
	logger telemetry.Logger,
	toolset string,
	streamID string,
) (toolregistry.ToolCallMessage, bool, error) {
	var msg toolregistry.ToolCallMessage
	if err := json.Unmarshal(ev.Payload, &msg); err != nil {
		logger.Error(
			ctx,
			"unmarshal toolset message failed",
			"component", "tool-registry-provider",
			"toolset", toolset,
			"stream_id", streamID,
			"event_id", ev.ID,
			"event_name", ev.EventName,
			"err", err,
		)
		return toolregistry.ToolCallMessage{}, true, ackEvent(ctx, sink, ev, "ack malformed toolset event")
	}
	return msg, false, nil
}

func handleControlMessage(ctx context.Context, sink pulseclients.Sink, ev *streaming.Event, msg toolregistry.ToolCallMessage, deps pendingDeps) (bool, error) {
	switch msg.Type {
	case toolregistry.MessageTypePing:
		if msg.PingID != "" {
			pongCtx, pongCancel := context.WithTimeout(ctx, deps.cfg.pongTimeout)
			err := deps.opts.Pong(pongCtx, msg.PingID)
			pongCancel()
			if err != nil {
				deps.logger.Error(
					ctx,
					"pong failed",
					"component", "tool-registry-provider",
					"toolset", deps.toolset,
					"stream_id", deps.streamID,
					"event_id", ev.ID,
					"ping_id", msg.PingID,
					"err", err,
				)
			}
		}
		return true, ackEvent(ctx, sink, ev, "ack ping toolset event")
	case toolregistry.MessageTypeCall:
		return false, nil
	default:
		return true, ackEvent(ctx, sink, ev, "ack unknown toolset event")
	}
}

func queueWorkItem(ctx context.Context, item workItem, deps pendingDeps) {
	select {
	case deps.work <- item:
	default:
		if len(*deps.pending) < cap(*deps.pending) {
			*deps.pending = append(*deps.pending, item)
			return
		}
		deps.logger.Error(
			ctx,
			"tool call queue full; leaving message unacked for later delivery",
			"component", "tool-registry-provider",
			"toolset", deps.toolset,
			"tool_use_id", item.msg.ToolUseID,
			"tool", item.msg.Tool,
			"stream_id", deps.streamID,
			"event_id", item.ev.ID,
			"max_concurrent", deps.cfg.maxConcurrent,
			"max_queued", deps.cfg.maxQueued,
		)
	case <-ctx.Done():
	}
}

func ackEvent(ctx context.Context, sink pulseclients.Sink, ev *streaming.Event, msg string) error {
	if err := sink.Ack(ctx, ev); err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}
