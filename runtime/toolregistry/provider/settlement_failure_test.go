package provider

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	pulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	mockpulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/mocks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeDoesNotAcknowledgeUnsettledCalls(t *testing.T) {
	for _, kind := range []string{"claim failure", "unknown claim", "publication failure"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			events := make(chan *streaming.Event, 1)
			events <- testToolCallEvent(t, "unsettled")
			var acks, completions atomic.Int64
			sink := mockpulse.NewSink(t)
			sink.SetSubscribe(func() <-chan *streaming.Event {
				return events
			})
			sink.SetAck(func(context.Context, *streaming.Event) error {
				acks.Add(1)
				return nil
			})
			sink.SetClose(func(context.Context) error {
				return nil
			})
			stream := mockpulse.NewStream(t)
			stream.SetNewSink(func(context.Context, string, ...streamopts.Sink) (pulse.Sink, error) {
				return sink, nil
			})
			client := mockpulse.NewClient(t)
			client.SetStream(func(string, ...streamopts.Stream) (pulse.Stream, error) {
				return stream, nil
			})
			registration := successfulRegistration(func(toolregistry.ToolResultMessage) error {
				completions.Add(1)
				return assert.AnError
			})
			switch kind {
			case "claim failure":
				registration.Claim = func(context.Context, string, string, string, string, string, string, string) (ClaimDisposition, error) {
					return "", assert.AnError
				}
			case "unknown claim":
				registration.Claim = func(context.Context, string, string, string, string, string, string, string) (ClaimDisposition, error) {
					return "future", nil
				}
			}
			handler := &recordingHandler{}
			err := Serve(ctx, client, "test.toolset", handler, registration, Options{ProviderID: testProviderID, SinkAckGracePeriod: time.Second, Pong: func(context.Context, string, string, string) error {
				return nil
			}})
			require.Error(t, err)
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			assert.Zero(t, acks.Load())
			if kind == "publication failure" {
				require.ErrorIs(t, err, assert.AnError)
				assert.EqualValues(t, 1, handler.calls.Load())
				assert.EqualValues(t, 1, completions.Load())
			} else {
				require.ErrorContains(t, err, "claim")
				assert.Zero(t, handler.calls.Load())
				assert.Zero(t, completions.Load())
			}
		})
	}
}

func TestServeReturnsMalformedMessageAcknowledgementFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	events := make(chan *streaming.Event, 1)
	events <- &streaming.Event{ID: "1-0", EventName: "call", Payload: []byte(`{`)}
	sink := mockpulse.NewSink(t)
	sink.SetSubscribe(func() <-chan *streaming.Event {
		return events
	})
	sink.SetAck(func(context.Context, *streaming.Event) error {
		return assert.AnError
	})
	sink.SetClose(func(context.Context) error {
		return nil
	})
	stream := mockpulse.NewStream(t)
	stream.SetNewSink(func(context.Context, string, ...streamopts.Sink) (pulse.Sink, error) {
		return sink, nil
	})
	client := mockpulse.NewClient(t)
	client.SetStream(func(string, ...streamopts.Stream) (pulse.Stream, error) {
		return stream, nil
	})
	handler := &recordingHandler{}
	err := Serve(ctx, client, "test.toolset", handler, successfulRegistration(), Options{ProviderID: testProviderID, Pong: func(context.Context, string, string, string) error {
		return nil
	}})
	require.ErrorIs(t, err, assert.AnError)
	require.ErrorContains(t, err, "ack malformed")
	assert.Zero(t, handler.calls.Load())
}
