package mcp

import (
	"context"
	"sync"
)

// Broadcaster provides a minimal, concurrency-safe publish/subscribe abstraction
// used by generated MCP adapters to stream server-initiated events (for example
// status notifications and resource updates). Implementations must allow
// Subscribe, Publish and Close to be called from multiple goroutines.
//
// The event payload type is intentionally untyped (any) so generated packages can
// publish values of their locally-generated result types without creating cyclic
// dependencies across packages.
//
// Close terminates the broadcaster and all active subscriptions. After Close,
// future Subscribe calls succeed with a closed Subscription and Publish becomes
// a no-op.
type Broadcaster interface {
	// Subscribe registers a new subscriber and returns a Subscription. The caller
	// must Close the subscription when done.
	Subscribe(ctx context.Context) (Subscription, error)
	// Publish delivers an event to all current subscribers.
	Publish(ev any)
	// Close closes the broadcaster and all current subscriptions.
	Close() error
}

// SessionBroadcaster scopes subscriptions and publishes to an MCP session.
type SessionBroadcaster interface {
	// SubscribeSession registers a new subscriber for one session.
	SubscribeSession(ctx context.Context, sessionID string) (Subscription, error)
	// PublishSession delivers an event to exactly one subscriber for one session.
	PublishSession(sessionID string, ev any)
}

// Subscription represents a live registration with a Broadcaster.
//
// The channel returned by C delivers events in publish order. The channel is
// closed when either Close is called on the subscription or the owning
// Broadcaster is closed. Close is idempotent and safe to call multiple times.
type Subscription interface {
	// C returns a receive-only channel for events.
	C() <-chan any
	// Close unregisters the subscription.
	Close() error
}

type channelBroadcaster struct {
	mu          sync.RWMutex
	subs        map[*channelSub]struct{}
	sessionSubs map[string]map[*channelSub]struct{}
	buf         int
	drop        bool
	closed      bool
}

// NewChannelBroadcaster constructs an in-memory Broadcaster backed by buffered
// channels. The returned implementation is safe for concurrent use.
//
// Buffering and back-pressure:
//   - buf controls the size of each subscriber channel buffer.
//   - When drop is true, Publish does not block; if a subscriber channel is full
//     the event is dropped for that subscriber.
//   - When drop is false, Publish blocks until each subscriber has available
//     buffer space, applying back-pressure to publishers.
func NewChannelBroadcaster(buf int, drop bool) Broadcaster {
	return &channelBroadcaster{
		subs:        make(map[*channelSub]struct{}),
		sessionSubs: make(map[string]map[*channelSub]struct{}),
		buf:         buf,
		drop:        drop,
	}
}

func (b *channelBroadcaster) Subscribe(ctx context.Context) (Subscription, error) {
	return b.SubscribeSession(ctx, "")
}

func (b *channelBroadcaster) SubscribeSession(ctx context.Context, sessionID string) (Subscription, error) {
	sub := &channelSub{
		ch:        make(chan any, b.buf),
		parent:    b,
		done:      make(chan struct{}),
		sessionID: sessionID,
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(sub.ch)
		sub.closeDone()
		return sub, nil
	}
	if sessionID != "" {
		sessionSubs := b.sessionSubs[sessionID]
		if sessionSubs == nil {
			sessionSubs = make(map[*channelSub]struct{})
			b.sessionSubs[sessionID] = sessionSubs
		}
		sessionSubs[sub] = struct{}{}
	} else {
		b.subs[sub] = struct{}{}
	}
	b.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			_ = sub.Close()
		case <-sub.done:
		}
	}()
	return sub, nil
}

func (b *channelBroadcaster) Publish(ev any) {
	subs := b.snapshotAllSubscribers()
	b.publish(subs, ev)
}

func (b *channelBroadcaster) PublishSession(sessionID string, ev any) {
	subs := b.snapshotSubscribers(sessionID)
	if len(subs) == 0 {
		return
	}
	b.publish(subs[:1], ev)
}

func (b *channelBroadcaster) snapshotSubscribers(sessionID string) []*channelSub {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil
	}
	var source map[*channelSub]struct{}
	if sessionID == "" {
		source = b.subs
	} else {
		source = b.sessionSubs[sessionID]
	}
	subs := make([]*channelSub, 0, len(source))
	for sub := range source {
		subs = append(subs, sub)
	}
	return subs
}

func (b *channelBroadcaster) snapshotAllSubscribers() []*channelSub {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil
	}
	subCount := len(b.subs)
	for _, sessionSubs := range b.sessionSubs {
		subCount += len(sessionSubs)
	}
	subs := make([]*channelSub, 0, subCount)
	for sub := range b.subs {
		subs = append(subs, sub)
	}
	for _, sessionSubs := range b.sessionSubs {
		for sub := range sessionSubs {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (b *channelBroadcaster) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := make([]*channelSub, 0, len(b.subs))
	for sub := range b.subs {
		subs = append(subs, sub)
		delete(b.subs, sub)
	}
	for _, sessionSubs := range b.sessionSubs {
		for sub := range sessionSubs {
			subs = append(subs, sub)
		}
	}
	clear(b.sessionSubs)
	b.mu.Unlock()
	closeSubscriptions(subs)
	return nil
}

func (b *channelBroadcaster) publish(subs []*channelSub, ev any) {
	for _, sub := range subs {
		sub.publish(ev, b.drop)
	}
}

type channelSub struct {
	ch        chan any
	parent    *channelBroadcaster
	done      chan struct{}
	sessionID string
	once      sync.Once
	sendMu    sync.Mutex
}

func (s *channelSub) C() <-chan any { return s.ch }

func (s *channelSub) Close() error {
	if s == nil || s.parent == nil || s.ch == nil {
		return nil
	}
	removed := false
	s.parent.mu.Lock()
	if s.sessionID == "" {
		if _, ok := s.parent.subs[s]; ok {
			delete(s.parent.subs, s)
			removed = true
		}
	} else if _, ok := s.parent.sessionSubs[s.sessionID][s]; ok {
		delete(s.parent.sessionSubs[s.sessionID], s)
		if len(s.parent.sessionSubs[s.sessionID]) == 0 {
			delete(s.parent.sessionSubs, s.sessionID)
		}
		removed = true
	}
	s.parent.mu.Unlock()
	if removed {
		closeSubscriptions([]*channelSub{s})
	}
	return nil
}

func (s *channelSub) publish(ev any, drop bool) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	select {
	case <-s.done:
		return
	default:
	}
	if drop {
		select {
		case s.ch <- ev:
		case <-s.done:
		default:
		}
		return
	}
	select {
	case s.ch <- ev:
	case <-s.done:
	}
}

func (s *channelSub) closeDone() {
	s.once.Do(func() {
		close(s.done)
	})
}

func closeSubscriptions(subs []*channelSub) {
	for _, sub := range subs {
		sub.closeDone()
		sub.sendMu.Lock()
		close(sub.ch)
		sub.sendMu.Unlock()
	}
}
