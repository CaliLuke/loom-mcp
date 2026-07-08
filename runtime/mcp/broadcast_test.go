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

	one, err := scoped.SubscribeSession(context.Background(), "one")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, one.Close()) })

	two, err := scoped.SubscribeSession(context.Background(), "two")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, two.Close()) })

	scoped.PublishSession("one", "event")

	require.Equal(t, "event", <-one.C())
	requireNoEvent(t, two.C())

	b.Publish("global")

	requireNoEvent(t, one.C())
	requireNoEvent(t, two.C())
}

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
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
