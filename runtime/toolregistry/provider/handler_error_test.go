package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	mockpulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/mocks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingHandler struct{ err error }

func (h *failingHandler) HandleToolCall(context.Context, toolregistry.ToolCallMessage) (toolregistry.ToolResultMessage, error) {
	return toolregistry.ToolResultMessage{}, h.err
}
func TestServeSanitizesHandlerErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "private error", err: errors.New("private backend address"), want: "tool execution failed"},
		{name: "wrapped safe remedy", err: fmt.Errorf("private wrapper: %w", loom.WithErrorRemedy(loom.PermanentError("backend", "private backend address"), &loom.ErrorRemedy{SafeMessage: "Try a different query."})), want: "Try a different query."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			events := make(chan *streaming.Event, 1)
			completed := make(chan toolregistry.ToolResultMessage, 1)
			sink := mockpulse.NewSink(t)
			sink.SetSubscribe(func() <-chan *streaming.Event { return events })
			sink.SetClose(func(context.Context) error { return nil })
			sink.SetAck(func(context.Context, *streaming.Event) error { return nil })
			stream := mockpulse.NewStream(t)
			stream.SetNewSink(func(context.Context, string, ...streamopts.Sink) (pulse.Sink, error) { return sink, nil })
			client := mockpulse.NewClient(t)
			client.SetStream(func(string, ...streamopts.Stream) (pulse.Stream, error) { return stream, nil })
			registration := successfulRegistration(func(result toolregistry.ToolResultMessage) error { completed <- result; return nil })
			done := make(chan error, 1)
			go func() {
				done <- Serve(ctx, client, "test.toolset", &failingHandler{err: tc.err}, registration, Options{
					ProviderID: testProviderID, Pong: func(context.Context, string, string, string) error { return nil },
				})
			}()
			events <- testToolCallEvent(t, "audit-private-error")
			select {
			case result := <-completed:
				require.NotNil(t, result.Error)
				assert.Equal(t, tc.want, result.Error.Message)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
		})
	}
}
