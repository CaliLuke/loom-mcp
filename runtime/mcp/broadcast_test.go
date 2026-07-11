package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelBroadcasterSubscribeStopsWatcherOnSubscriptionClose(t *testing.T) {
	b := NewChannelBroadcaster(1, true)
	sub, err := b.Subscribe(context.Background())
	require.NoError(t, err)

	channelSub, ok := sub.(*channelSub)
	require.True(t, ok)

	require.NoError(t, sub.Close())
	requireClosed(t, channelSub.done)
}

func TestChannelBroadcasterSubscribeStopsWatcherOnBroadcasterClose(t *testing.T) {
	b := NewChannelBroadcaster(1, true)
	sub, err := b.Subscribe(context.Background())
	require.NoError(t, err)

	channelSub, ok := sub.(*channelSub)
	require.True(t, ok)

	require.NoError(t, b.Close())
	requireClosed(t, channelSub.done)
}

func TestChannelBroadcasterPublishSessionScopesEvents(t *testing.T) {
	b := NewChannelBroadcaster(1, true)
	scoped, ok := b.(SessionBroadcaster)
	require.True(t, ok)
	global, err := b.Subscribe(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, global.Close()) })

	one, err := scoped.SubscribeSession(context.Background(), "one")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, one.Close()) })

	two, err := scoped.SubscribeSession(context.Background(), "two")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, two.Close()) })

	scoped.PublishSession("one", "event")

	requireEvent(t, one.C(), "event")
	requireNoEvent(t, two.C())

	b.Publish("global")

	requireEvent(t, global.C(), "global")
	requireEvent(t, one.C(), "global")
	requireEvent(t, two.C(), "global")
}

func TestChannelBroadcasterCloseUnblocksPublishToSlowSubscriber(t *testing.T) {
	b := NewChannelBroadcaster(0, false)
	sub, err := b.Subscribe(context.Background())
	require.NoError(t, err)

	published := make(chan struct{})
	go func() {
		b.Publish("blocked")
		close(published)
	}()

	requireNoSignal(t, published)
	require.NoError(t, b.Close())
	requireClosed(t, published)
	requireClosedChannel(t, sub.C())
}

func TestChannelBroadcasterSubscriptionCloseUnblocksPublishToDepartedSubscriber(t *testing.T) {
	b := NewChannelBroadcaster(0, false)
	sub, err := b.Subscribe(context.Background())
	require.NoError(t, err)

	published := make(chan struct{})
	go func() {
		b.Publish("blocked")
		close(published)
	}()

	requireNoSignal(t, published)
	require.NoError(t, sub.Close())
	requireClosed(t, published)
	requireClosedChannel(t, sub.C())
	require.NoError(t, b.Close())
}

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel did not close")
	}
}

func requireClosedChannel(t *testing.T, ch <-chan any) {
	t.Helper()

	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel delivered a value instead of closing")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel did not close")
	}
}

func requireNoEvent(t *testing.T, ch <-chan any) {
	t.Helper()

	select {
	case ev := <-ch:
		t.Fatalf("received unexpected event %v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func requireEvent(t *testing.T, ch <-chan any, expected any) {
	t.Helper()

	select {
	case ev := <-ch:
		require.Equal(t, expected, ev)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("event was not delivered")
	}
}

func requireNoSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
		t.Fatal("received unexpected signal")
	case <-time.After(50 * time.Millisecond):
	}
}
