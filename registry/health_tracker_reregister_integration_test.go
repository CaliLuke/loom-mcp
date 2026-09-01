package registry

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom/pulse/pool"
	"github.com/CaliLuke/loom/pulse/rmap"
)

// TestPingsSurviveReregisterAndFailover verifies that calling StartPingLoop again
// (simulating a provider re-register) does not break distributed ticker failover.
//
// Regression: StartPingLoop used to stop and restart the local distributed ticker,
// which deletes the shared ticker-map entry and remotely stops other nodes'
// tickers. If the registering node then crashes, no node continues pinging.
func TestPingsSurviveReregisterAndFailover(t *testing.T) {
	rdb := getRedis(t)
	ctx := context.Background()

	healthMap, err := rmap.Join(ctx, "health-reregister-"+t.Name(), rdb)
	if err != nil {
		t.Fatalf("failed to create health map: %v", err)
	}
	defer healthMap.Close()

	registryMap, err := rmap.Join(ctx, "registry-reregister-"+t.Name(), rdb)
	if err != nil {
		t.Fatalf("failed to create registry map: %v", err)
	}
	defer registryMap.Close()

	poolName := "pool-" + t.Name()
	node1, err := pool.AddNode(ctx, poolName, rdb, testNodeOpts()...)
	if err != nil {
		t.Fatalf("failed to create node1: %v", err)
	}
	node2, err := pool.AddNode(ctx, poolName, rdb, testNodeOpts()...)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create node2: %v", err)
	}
	defer func() { _ = node2.Close(ctx) }()

	mockSM := &pingCountingStreamManager{
		messages: make(map[string]int),
		pingCh:   make(chan string, 100),
	}

	tracker2, err := NewHealthTracker(
		mockSM,
		healthMap,
		registryMap,
		node2,
		WithPingInterval(50*time.Millisecond),
		WithMissedPingThreshold(2),
	)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create tracker2: %v", err)
	}
	defer func() { _ = tracker2.Close() }()

	tracker1, err := NewHealthTracker(
		mockSM,
		healthMap,
		registryMap,
		node1,
		WithPingInterval(50*time.Millisecond),
		WithMissedPingThreshold(2),
	)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create tracker1: %v", err)
	}

	toolset := "reregister-toolset"
	registerCatalogToolset(t, ctx, registryMap, tracker1, toolset)

	// Wait for pings.
	for range 3 {
		select {
		case <-mockSM.pingCh:
		case <-time.After(10 * time.Second):
			_ = tracker1.Close()
			_ = node1.Close(ctx)
			t.Fatal("timeout waiting for pings before re-register")
		}
	}

	// Simulate provider re-register hitting the same gateway node.
	if err := tracker1.StartPingLoop(ctx, toolset); err != nil {
		_ = tracker1.Close()
		_ = node1.Close(ctx)
		t.Fatalf("failed to re-start ping loop: %v", err)
	}

	// Crash node1 without closing tracker1 (simulates process death).
	_ = node1.Close(ctx)

	// Drain buffered pings.
drain:
	for {
		select {
		case <-mockSM.pingCh:
		default:
			break drain
		}
	}

	pingCountBefore := mockSM.getPingCount(toolset)

	// Pings should continue from the remaining node.
	for range 3 {
		select {
		case <-mockSM.pingCh:
		case <-time.After(10 * time.Second):
			t.Fatalf(
				"timeout waiting for pings after failover (before=%d, current=%d)",
				pingCountBefore,
				mockSM.getPingCount(toolset),
			)
		}
	}
	if mockSM.getPingCount(toolset) <= pingCountBefore {
		t.Fatalf("expected ping count to increase after failover: before=%d after=%d", pingCountBefore, mockSM.getPingCount(toolset))
	}
}

// TestPingsSurviveUnregisterReregisterAndFailover verifies that a peer node
// re-joins the shared distributed ticker after a toolset is unregistered and
// then re-registered on another node.
//
// Regression: reconcileCatalogTickers only checked local ticker presence.
// Unregister deletes the shared ticker-map entry, which permanently stops the
// peer's local ticker instance; re-register recreates the shared entry, but
// the peer still held its dead ticker and never re-joined, so when the
// registering node later died no node kept pinging the toolset.
func TestPingsSurviveUnregisterReregisterAndFailover(t *testing.T) {
	rdb := getRedis(t)
	ctx := context.Background()

	healthMap, err := rmap.Join(ctx, "health-unreg-rereg-"+t.Name(), rdb)
	if err != nil {
		t.Fatalf("failed to create health map: %v", err)
	}
	defer healthMap.Close()

	registryMap, err := rmap.Join(ctx, "registry-unreg-rereg-"+t.Name(), rdb)
	if err != nil {
		t.Fatalf("failed to create registry map: %v", err)
	}
	defer registryMap.Close()

	poolName := "pool-" + t.Name()
	node1, err := pool.AddNode(ctx, poolName, rdb, testNodeOpts()...)
	if err != nil {
		t.Fatalf("failed to create node1: %v", err)
	}
	node2, err := pool.AddNode(ctx, poolName, rdb, testNodeOpts()...)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create node2: %v", err)
	}
	defer func() { _ = node2.Close(ctx) }()

	mockSM := &pingCountingStreamManager{
		messages: make(map[string]int),
		pingCh:   make(chan string, 100),
	}

	tracker2, err := NewHealthTracker(
		mockSM,
		healthMap,
		registryMap,
		node2,
		WithPingInterval(50*time.Millisecond),
		WithMissedPingThreshold(2),
	)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create tracker2: %v", err)
	}
	defer func() { _ = tracker2.Close() }()

	tracker1, err := NewHealthTracker(
		mockSM,
		healthMap,
		registryMap,
		node1,
		WithPingInterval(50*time.Millisecond),
		WithMissedPingThreshold(2),
	)
	if err != nil {
		_ = node1.Close(ctx)
		t.Fatalf("failed to create tracker1: %v", err)
	}

	toolset := "unreg-rereg-toolset"
	registerCatalogToolset(t, ctx, registryMap, tracker1, toolset)

	// Wait for pings so the shared ticker is provably live.
	for range 3 {
		select {
		case <-mockSM.pingCh:
		case <-time.After(10 * time.Second):
			_ = tracker1.Close()
			_ = node1.Close(ctx)
			t.Fatal("timeout waiting for pings before unregister")
		}
	}

	// Unregister on node1: Ticker.Stop deletes the shared ticker-map entry,
	// remotely stopping node2's local ticker instance.
	unregisterCatalogToolset(t, ctx, registryMap, tracker1, toolset)
	waitForTickerAbsent(t, tracker2, toolset)

	// Re-register on node1: rotates the catalog registration token and
	// recreates the shared ticker entry.
	registerCatalogToolset(t, ctx, registryMap, tracker1, toolset)

	newToken, err := newToolsetCatalog(registryMap).RegistrationToken(ctx, toolset)
	if err != nil {
		_ = tracker1.Close()
		_ = node1.Close(ctx)
		t.Fatalf("failed to resolve registration token after re-register: %v", err)
	}

	// Deterministically wait until node2 re-joined the new registration epoch.
	waitForTickerToken(t, tracker2, toolset, newToken)

	// Crash node1 without closing tracker1 (simulates process death).
	_ = node1.Close(ctx)

	// Drain buffered pings.
drain:
	for {
		select {
		case <-mockSM.pingCh:
		default:
			break drain
		}
	}

	pingCountBefore := mockSM.getPingCount(toolset)

	// Pings should continue from the remaining node.
	for range 3 {
		select {
		case <-mockSM.pingCh:
		case <-time.After(10 * time.Second):
			t.Fatalf(
				"timeout waiting for pings after unregister/re-register failover (before=%d, current=%d)",
				pingCountBefore,
				mockSM.getPingCount(toolset),
			)
		}
	}
	if mockSM.getPingCount(toolset) <= pingCountBefore {
		t.Fatalf("expected ping count to increase after failover: before=%d after=%d", pingCountBefore, mockSM.getPingCount(toolset))
	}
}

// waitForTickerAbsent waits until catalog propagation has detached the peer's
// local ticker. This establishes the unregister phase before the test starts a
// new registration epoch instead of relying on two asynchronous map events to
// be observed separately.
func waitForTickerAbsent(t *testing.T, tracker HealthTracker, toolset string) {
	t.Helper()

	ht, ok := tracker.(*healthTracker)
	if !ok {
		t.Fatalf("unexpected tracker type %T", tracker)
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		ht.mu.RLock()
		_, hasTicker := ht.tickers[toolset]
		_, hasToken := ht.tickerTokens[toolset]
		ht.mu.RUnlock()
		if !hasTicker && !hasToken {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("tracker did not detach ticker for %q (has_ticker=%v, has_token=%v)", toolset, hasTicker, hasToken)
		}
	}
}

// waitForTickerToken polls until the tracker holds a local ticker for the
// toolset that was created under the given catalog registration token.
func waitForTickerToken(t *testing.T, tracker HealthTracker, toolset string, token string) {
	t.Helper()

	ht, ok := tracker.(*healthTracker)
	if !ok {
		t.Fatalf("unexpected tracker type %T", tracker)
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		ht.mu.RLock()
		_, hasTicker := ht.tickers[toolset]
		gotToken := ht.tickerTokens[toolset]
		ht.mu.RUnlock()
		if hasTicker && gotToken == token {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("tracker did not re-join ticker for %q under token %q (has_ticker=%v, token=%q)", toolset, token, hasTicker, gotToken)
		}
	}
}
