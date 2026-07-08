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

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel did not close")
	}
}
