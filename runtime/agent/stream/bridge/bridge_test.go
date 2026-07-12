package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/stream"
)

func TestNewSubscriberAndRegister(t *testing.T) {
	_, err := NewSubscriber(nil)
	require.EqualError(t, err, "stream sink is required")
	_, err = Register(hooks.NewBus(), nil)
	require.EqualError(t, err, "stream sink is required")

	bus := hooks.NewBus()
	sink := &recordingSink{}
	subscription, err := Register(bus, sink)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(context.Background(), hooks.NewAssistantMessageEvent(
		"run-1",
		agent.Ident("assistant"),
		"session-1",
		"hello",
		nil,
	)))
	require.Len(t, sink.events, 1)
	assert.Equal(t, stream.EventAssistantReply, sink.events[0].Type())

	require.NoError(t, subscription.Close())
	require.NoError(t, bus.Publish(context.Background(), hooks.NewAssistantMessageEvent(
		"run-1",
		agent.Ident("assistant"),
		"session-1",
		"ignored",
		nil,
	)))
	assert.Len(t, sink.events, 1)

	wantErr := errors.New("registration failed")
	_, err = Register(rejectingBus{err: wantErr}, sink)
	require.ErrorIs(t, err, wantErr)
}

type recordingSink struct {
	events []stream.Event
}

func (s *recordingSink) Send(_ context.Context, event stream.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *recordingSink) Close(context.Context) error {
	return nil
}

type rejectingBus struct {
	err error
}

func (b rejectingBus) Publish(context.Context, hooks.Event) error {
	return nil
}

func (b rejectingBus) Register(hooks.Subscriber) (hooks.Subscription, error) {
	return nil, b.err
}
