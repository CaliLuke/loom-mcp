package pulse

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"

	clientspulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
)

type (
	// EnvelopeDecoder converts raw payloads read from Pulse into runtime stream events.
	// Custom decoders can be provided to handle non-standard envelope formats.
	EnvelopeDecoder func([]byte) (stream.Event, error)

	// SubscriberOptions configures a Pulse-backed subscriber.
	SubscriberOptions struct {
		// Client is the Pulse client used to consume events. Required.
		Client clientspulse.Client
		// SinkName identifies the Pulse consumer group. Defaults to "loom_mcp_subscriber".
		SinkName string
		// Buffer specifies the event channel capacity. Defaults to 64.
		Buffer int
		// Decoder deserializes event payloads. Defaults to the built-in JSON decoder.
		Decoder EnvelopeDecoder
	}

	// Subscriber consumes Pulse streams and emits runtime stream events. It wraps
	// a Pulse sink (consumer group) and decodes incoming payloads into stream.Event
	// values.
	Subscriber struct {
		client  clientspulse.Client
		buffer  int
		name    string
		decode  EnvelopeDecoder
		dropped atomic.Uint64
	}

	// Delivery is one decoded Pulse event whose durable acknowledgement is
	// controlled by the consumer. A successful Ack removes the underlying Pulse
	// entry from the consumer group's pending list. Failed acknowledgements may
	// be retried; acknowledgements after the first success are no-ops.
	Delivery struct {
		event     stream.Event
		decodeErr error
		pulseID   string
		sink      clientspulse.Sink
		raw       *streaming.Event
		mu        sync.Mutex
		acked     bool
	}
	// decodedEvent implements stream.Event for Pulse-decoded envelopes.
	decodedEvent struct {
		t   stream.EventType
		run string
		s   string
		k   string
		b   jsontext.Value
	}
)

// Event returns the decoded runtime stream event.
func (d *Delivery) Event() stream.Event {
	return d.event
}

// DecodeError reports why the raw Pulse payload could not be decoded. Event
// returns nil when DecodeError is non-nil. The consumer may dead-letter
// RawPayload and then Ack, or omit Ack to leave the entry pending.
func (d *Delivery) DecodeError() error {
	return d.decodeErr
}

// RawPayload returns a detached copy of the underlying Pulse payload.
func (d *Delivery) RawPayload() []byte {
	return append([]byte(nil), d.raw.Payload...)
}

// PulseID returns the underlying Pulse stream entry ID.
func (d *Delivery) PulseID() string {
	return d.pulseID
}

// Ack acknowledges successful durable processing. It is safe to call Ack
// concurrently. A failed acknowledgement is not cached and may be retried.
func (d *Delivery) Ack(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.acked {
		return nil
	}
	if err := d.sink.Ack(ctx, d.raw); err != nil {
		return fmt.Errorf("pulse ack: %w", err)
	}
	d.acked = true
	return nil
}

func (e decodedEvent) Type() stream.EventType { return e.t }
func (e decodedEvent) RunID() string          { return e.run }
func (e decodedEvent) SessionID() string      { return e.s }
func (e decodedEvent) EventKey() string       { return e.k }
func (e decodedEvent) Payload() any           { return e.b }

// NewSubscriber constructs a Pulse-backed subscriber. The Client field in opts
// is required; SinkName, Buffer, and Decoder default to sensible values if not
// provided (see SubscriberOptions field documentation).
func NewSubscriber(opts SubscriberOptions) (*Subscriber, error) {
	if opts.Client == nil {
		return nil, errors.New("pulse client is required")
	}
	name := opts.SinkName
	if name == "" {
		name = "loom_mcp_subscriber"
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = 64
	}
	decoder := opts.Decoder
	if decoder == nil {
		decoder = decodeEnvelope
	}
	return &Subscriber{
		client: opts.Client,
		buffer: buffer,
		name:   name,
		decode: decoder,
	}, nil
}

// Subscribe opens a Pulse sink on the given stream ID and returns channels for
// events and errors. It spawns a goroutine that consumes from the sink, decodes
// payloads, and emits stream events. The returned cancel function stops
// consumption, closes the sink, and closes both channels.
//
// Errs channel contract: errs has a buffer of one and error delivery never
// blocks event consumption, so draining errs is optional. Non-terminal decode
// errors are offered best-effort and dropped when the buffer is full; drops
// are counted and exposed via DroppedErrors. Terminal errors (ack failures)
// evict any pending undelivered decode error so the terminal cause is always
// delivered, after which both channels are closed — a consumer that only
// ranges over events still observes termination.
//
// Usage:
//
//	events, errs, cancel, err := sub.Subscribe(ctx, "session/abc123")
//	defer cancel()
//	for evt := range events {
//	    // process event
//	}
func (s *Subscriber) Subscribe(
	ctx context.Context,
	streamID string,
	opts ...streamopts.Sink,
) (<-chan stream.Event, <-chan error, context.CancelFunc, error) {
	str, err := s.client.Stream(streamID)
	if err != nil {
		return nil, nil, nil, err
	}
	sink, err := str.NewSink(ctx, s.name, opts...)
	if err != nil {
		return nil, nil, nil, err
	}
	events := make(chan stream.Event, s.buffer)
	errs := make(chan error, 1)
	done := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	go s.consume(runCtx, sink, events, errs, done)
	cancelFunc := func() {
		cancel()
		<-done
		if err := sink.Close(context.Background()); err != nil {
			s.dropped.Add(1)
		}
	}
	return events, errs, cancelFunc, nil
}

// SubscribeManual opens a Pulse sink and returns deliveries that remain in
// the consumer group's pending list until the consumer calls Delivery.Ack.
// This is the durable-consumer API: acknowledge only after the downstream
// transaction or side effect has committed. Unlike Subscribe, malformed
// payloads become deliveries with a DecodeError and are also reported on errs.
// The consumer may dead-letter RawPayload and Ack, or leave the entry pending
// for Pulse redelivery or an operator-owned reclamation policy.
//
// The returned cancel function stops consumption, waits for the consumer
// goroutine, closes the sink, and closes both channels.
func (s *Subscriber) SubscribeManual(
	ctx context.Context,
	streamID string,
	opts ...streamopts.Sink,
) (<-chan *Delivery, <-chan error, context.CancelFunc, error) {
	str, err := s.client.Stream(streamID)
	if err != nil {
		return nil, nil, nil, err
	}
	sink, err := str.NewSink(ctx, s.name, opts...)
	if err != nil {
		return nil, nil, nil, err
	}
	deliveries := make(chan *Delivery, s.buffer)
	errs := make(chan error, 1)
	done := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	go s.consumeManual(runCtx, sink, deliveries, errs, done)
	cancelFunc := func() {
		cancel()
		<-done
		if err := sink.Close(context.Background()); err != nil {
			s.dropped.Add(1)
		}
	}
	return deliveries, errs, cancelFunc, nil
}

// DroppedErrors reports how many errors were dropped because the errs channel
// buffer was full at delivery time. The count aggregates across all
// subscriptions opened from this Subscriber and only ever grows.
func (s *Subscriber) DroppedErrors() uint64 {
	return s.dropped.Load()
}

// consume reads events from the Pulse sink channel, decodes them, and emits them
// on the out channel. It acks each event after successful emission, and also
// acks undecodable poison messages so one bad payload cannot halt the stream.
// Closes both channels when ctx is canceled or when the sink channel closes.
// Ack failures are terminal because the sink cannot safely advance. Error
// delivery never blocks, so consumers that only drain the out channel keep
// receiving events even when errs is never read.
func (s *Subscriber) consume(ctx context.Context, sink clientspulse.Sink, out chan<- stream.Event, errs chan error, done chan<- struct{}) {
	defer close(done)
	defer close(out)
	defer close(errs)
	ch := sink.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			decoded, err := s.decode(evt.Payload)
			if err != nil {
				if ackErr := sink.Ack(ctx, evt); ackErr != nil {
					s.reportTerminalError(errs, fmt.Errorf("pulse ack: %w", ackErr))
					return
				}
				s.reportDecodeError(errs, fmt.Errorf("pulse decode payload: %w", err))
				continue
			}
			select {
			case out <- decoded:
			case <-ctx.Done():
				return
			}
			if ackErr := sink.Ack(ctx, evt); ackErr != nil {
				s.reportTerminalError(errs, fmt.Errorf("pulse ack: %w", ackErr))
				return
			}
		}
	}
}

func (s *Subscriber) consumeManual(ctx context.Context, sink clientspulse.Sink, out chan<- *Delivery, errs chan error, done chan<- struct{}) {
	defer close(done)
	defer close(out)
	defer close(errs)
	ch := sink.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			decoded, decodeErr := s.decode(evt.Payload)
			if decodeErr != nil {
				decodeErr = fmt.Errorf("pulse decode payload: %w", decodeErr)
				s.reportDecodeError(errs, decodeErr)
			}
			delivery := &Delivery{
				event:     decoded,
				decodeErr: decodeErr,
				pulseID:   evt.ID,
				sink:      sink,
				raw:       evt,
			}
			select {
			case out <- delivery:
			case <-ctx.Done():
				return
			}
		}
	}
}

// reportDecodeError offers a non-terminal decode error on errs without ever
// blocking consumption. When the buffer is full the error is dropped and
// counted so droppage stays observable via DroppedErrors.
func (s *Subscriber) reportDecodeError(errs chan<- error, err error) {
	select {
	case errs <- err:
	default:
		s.dropped.Add(1)
	}
}

// reportTerminalError delivers a terminal error on errs without blocking. If
// the buffer is full it evicts the pending undelivered error (counting it as
// dropped) so the terminal cause always reaches the consumer. The consume
// goroutine is the sole sender, so after eviction the send cannot block.
func (s *Subscriber) reportTerminalError(errs chan error, err error) {
	select {
	case errs <- err:
		return
	default:
	}
	select {
	case <-errs:
		s.dropped.Add(1)
	default:
	}
	errs <- err
}

// decodeEnvelope deserializes the default JSON envelope format and extracts the
// runtime stream event. Returns an error if the payload is malformed.
func decodeEnvelope(payload []byte) (stream.Event, error) {
	var env struct {
		Type      string         `json:"type"`
		EventKey  string         `json:"event_key"`
		RunID     string         `json:"run_id"`
		SessionID string         `json:"session_id"`
		Timestamp time.Time      `json:"timestamp"`
		Payload   jsontext.Value `json:"payload"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	return decodedEvent{
		t:   stream.EventType(env.Type),
		run: env.RunID,
		s:   env.SessionID,
		k:   env.EventKey,
		b:   env.Payload,
	}, nil
}
